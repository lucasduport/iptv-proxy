/*
 * stream-share is a project to efficiently share the use of an IPTV service.
 * Copyright (C) 2025  Lucas Duport
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package server

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
	"strings"
	"context"
	"io"
	"path"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
)

// Optional timeout-aware session manager interface (non-breaking)
type timeoutAware interface {
	// Returns (true, until) when user is timed out; (false, zeroTime) otherwise.
	IsUserTimedOut(username string) (bool, time.Time)
}

// searchVOD searches for VOD content matching the query
func (c *Config) searchVOD(ctx *gin.Context) {
	utils.DebugLog("API: VOD search request received")

	var req struct {
		Username string `json:"username"`
		Query    string `json:"query"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ErrorLog("API: Invalid VOD search request: %v", err)
		ctx.JSON(http.StatusBadRequest, types.APIResponse{
			Success: false,
			Error:   "Invalid request: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Searching VOD for user %s, query: %s", req.Username, req.Query)

	// Enforce timeout if supported by session manager
	if c.sessionManager != nil {
		if sm, ok := interface{}(c.sessionManager).(timeoutAware); ok {
			if timedOut, until := sm.IsUserTimedOut(req.Username); timedOut {
				utils.WarnLog("API: VOD search blocked for timed-out user %s (until %s)", req.Username, until.Format(time.RFC3339))
				ctx.JSON(http.StatusForbidden, types.APIResponse{
					Success: false,
					Error:   fmt.Sprintf("User '%s' is currently timed out until %s", req.Username, until.Format(time.RFC3339)),
				})
				return
			}
		}
	}

	results, err := c.searchXtreamVOD(req.Query)
	if err != nil {
		utils.ErrorLog("API: VOD search failed: %v", err)
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Failed to search VOD: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Found %d VOD results for query: %s", len(results), req.Query)

	token := uuid.New().String()
	vodRequest := &types.VODRequest{
		Username:  req.Username,
		Query:     req.Query,
		Results:   results,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(30 * time.Minute),
		Token:     token,
	}

	// TODO: Store the VOD request in the database

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"request_token": token,
			"results":       results,
			"expires_at":    vodRequest.ExpiresAt,
		},
	})
}

// createVODDownload creates a temporary download link for VOD content
func (c *Config) createVODDownload(ctx *gin.Context) {
	utils.DebugLog("API: VOD download request received")

	var req struct {
		Username string `json:"username"`
		StreamID string `json:"stream_id"`
		Title    string `json:"title"`
		Type     string `json:"type"` // movie or series
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ErrorLog("API: Invalid VOD download request: %v", err)
		ctx.JSON(http.StatusBadRequest, types.APIResponse{
			Success: false,
			Error:   "Invalid request: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Creating download for user %s, stream %s, title %s", req.Username, req.StreamID, req.Title)

	// Enforce timeout if supported by session manager
	if c.sessionManager != nil {
		if sm, ok := interface{}(c.sessionManager).(timeoutAware); ok {
			if timedOut, until := sm.IsUserTimedOut(req.Username); timedOut {
				utils.WarnLog("API: VOD download blocked for timed-out user %s (until %s)", req.Username, until.Format(time.RFC3339))
				ctx.JSON(http.StatusForbidden, types.APIResponse{
					Success: false,
					Error:   fmt.Sprintf("User '%s' is currently timed out until %s", req.Username, until.Format(time.RFC3339)),
				})
				return
			}
		}
	}

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in createVODDownload")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	// Check if the user is currently streaming something
	userSession := c.sessionManager.GetUserSession(req.Username)
	if userSession != nil && userSession.StreamID != "" && userSession.StreamType == "live" {
		utils.WarnLog("User %s tried to download while streaming %s", req.Username, userSession.StreamID)
		ctx.JSON(http.StatusConflict, types.APIResponse{
			Success: false,
			Error:   "User is currently watching a live stream. Please stop streaming first.",
		})
		return
	}

	// Generate a download URL for the VOD content, preserving the original extension from M3U
	basePath := "movie"
	if strings.ToLower(req.Type) == "series" {
		basePath = "series"
	}
	finalID := req.StreamID
	if path.Ext(finalID) == "" {
		// Try to resolve extension from cached M3U (movie/series), then fall back
		if ext := c.findVODExtensionInCache(basePath, finalID); ext != "" {
			utils.DebugLog("VOD extension resolved from cache: %s%s", finalID, ext)
			finalID = finalID + ext
		} else if basePath == "series" { 
			// Some providers predominantly use .mkv for series
			utils.DebugLog("VOD extension not found in cache for series id=%s; defaulting to .mkv", finalID)
			finalID = finalID + ".mkv"
		}
	}
	vodURL := fmt.Sprintf("%s/%s/%s/%s/%s", c.XtreamBaseURL, basePath, c.XtreamUser, c.XtreamPassword, finalID)
	utils.DebugLog("API: VOD URL created: %s", utils.MaskURL(vodURL))

	// Generate a temporary download token
	token, err := c.sessionManager.GenerateTemporaryLink(req.Username, req.StreamID, req.Title, vodURL)
	if err != nil {
		utils.ErrorLog("API: Failed to generate temporary link: %v", err)
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Failed to generate download link: " + err.Error(),
		})
		return
	}

	// Create a proxied download URL
	protocol := "http"
	if c.ProxyConfig.HTTPS {
		protocol = "https"
	}
	downloadURL := fmt.Sprintf("%s://%s/download/%s", protocol, c.HostConfig.Hostname, token)

	utils.InfoLog("Created VOD download link for user %s, title: %s, token: %s", req.Username, req.Title, token)

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"download_url": downloadURL,
			"token":        token,
			"expires_at":   time.Now().Add(24 * time.Hour),
		},
	})
}

// pickVODExtension tries a small set of common extensions and returns the first that appears valid for the upstream.
// It performs quick HEAD requests with a short timeout. Falls back to .mp4 if none are conclusive.
func (c *Config) pickVODExtension(ctx *gin.Context, basePath, streamID string) string {
	// Allow override via env
	order := []string{".mp4", ".ts", ".mkv", ""}
	if v := strings.TrimSpace(utils.GetEnvOrDefault("VOD_EXT_ORDER", "")); v != "" {
		// comma-separated, keep only known values to avoid surprises
		parts := strings.Split(v, ",")
		tmp := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == ".mp4" || p == ".mkv" || p == ".ts" || p == "" { tmp = append(tmp, p) }
		}
		if len(tmp) > 0 { order = tmp }
	}
	client := &http.Client{ Timeout: 5 * time.Second }
	for _, ext := range order {
		url := fmt.Sprintf("%s/%s/%s/%s/%s%s", c.XtreamBaseURL, basePath, c.XtreamUser, c.XtreamPassword, streamID, ext)
		req, _ := http.NewRequestWithContext(context.Background(), "HEAD", url, nil)
		req.Header.Set("User-Agent", utils.GetIPTVUserAgent())
		resp, err := client.Do(req)
		if err != nil { 
			utils.DebugLog("VOD probe (HEAD) failed for %s: %v", utils.MaskURL(url), err)
			continue 
		}
		resp.Body.Close()
		// Accept 2xx and 206
		if (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusPartialContent {
			utils.DebugLog("VOD probe (HEAD) ok %d for %s", resp.StatusCode, utils.MaskURL(url))
			return ext
		}
		// Some providers return non-standard 461 or block HEAD; try GET range fallback
		if resp.StatusCode == 461 || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
			utils.DebugLog("VOD probe (HEAD) status %d for %s, trying GET range fallback", resp.StatusCode, utils.MaskURL(url))
			getReq, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)
			getReq.Header.Set("User-Agent", utils.GetIPTVUserAgent())
			getReq.Header.Set("Range", "bytes=0-0")
			if getResp, getErr := client.Do(getReq); getErr == nil {
				io.Copy(io.Discard, getResp.Body)
				getResp.Body.Close()
				if (getResp.StatusCode >= 200 && getResp.StatusCode < 300) || getResp.StatusCode == http.StatusPartialContent {
					utils.DebugLog("VOD probe (GET range) ok %d for %s", getResp.StatusCode, utils.MaskURL(url))
					return ext
				}
				utils.DebugLog("VOD probe (GET range) status %d for %s", getResp.StatusCode, utils.MaskURL(url))
			} else {
				utils.DebugLog("VOD probe (GET range) error for %s: %v", utils.MaskURL(url), getErr)
			}
		} else {
			utils.DebugLog("VOD probe (HEAD) status %d for %s", resp.StatusCode, utils.MaskURL(url))
		}
	}
	return ".mp4"
}

// getVODRequestStatus gets the status of a VOD download request
func (c *Config) getVODRequestStatus(ctx *gin.Context) {
	requestID := ctx.Param("requestid")
	utils.DebugLog("API: Getting VOD request status for ID: %s", requestID)

	// TODO: Implement actual status checking from database
	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"status":   "completed",
			"progress": 100,
		},
	})
}

// findVODExtensionInCache tries to locate the original extension for a given stream ID
// by scanning the cached VOD M3U or series entries. Returns empty string if unknown.
func (c *Config) findVODExtensionInCache(basePath, streamID string) string {
	// First scan the cached VOD M3U for both movies and series
	if m3uPath, err := c.ensureVODM3UCache(); err == nil {
		if ext := findExtInM3U(m3uPath, basePath, streamID); ext != "" {
			return ext
		}
	}
	// Fallback: proxified main M3U if available
	c.ensureChannelIndex()
	if strings.TrimSpace(c.proxyfiedM3UPath) != "" {
		if ext := findExtInM3U(c.proxyfiedM3UPath, basePath, streamID); ext != "" {
			return ext
		}
	}
	return ""
}

// findExtInM3U scans a given M3U file for an entry path containing basePath and having
// the last segment starting with streamID plus an extension.
func findExtInM3U(filePath, basePath, streamID string) string {
	f, err := os.Open(filePath)
	if err != nil { return "" }
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") { continue }
		if !(strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://")) { continue }
		// Quick path filter by basePath
		if !strings.Contains(line, "/"+basePath+"/") { continue }
		u, err := url.Parse(line)
		if err != nil { continue }
		last := path.Base(u.Path)
		if strings.HasPrefix(last, streamID+".") {
			return path.Ext(last)
		}
	}
	return ""
}
