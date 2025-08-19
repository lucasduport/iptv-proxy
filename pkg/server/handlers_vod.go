package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
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

	// Generate a download URL for the VOD content
	vodURL := fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, req.StreamID)
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
	downloadURL := fmt.Sprintf("%s://%s:%d/download/%s", protocol, c.HostConfig.Hostname, c.AdvertisedPort, token)

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
