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
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
)

var vodM3UMu sync.Mutex

// searchXtreamVOD is a helper function to search for VOD content using a cached M3U file
func (c *Config) searchXtreamVOD(query string) ([]types.VODResult, error) {
	utils.DebugLog("Searching VOD with query: %s", query)

	// Validate Xtream configuration
	if c.XtreamBaseURL == "" || c.XtreamUser.String() == "" || c.XtreamPassword.String() == "" {
		utils.ErrorLog("Xtream configuration is incomplete")
		return nil, fmt.Errorf("xtream configuration is incomplete")
	}

	// Ensure local cached M3U exists and is fresh
	m3uPath, err := c.ensureVODM3UCache()
	if err != nil {
		return nil, fmt.Errorf("failed to prepare VOD M3U cache: %w", err)
	}

	// Search inside the local M3U file for movie entries
	results, err := searchVODInM3UFile(m3uPath, query)
	if err != nil {
		return nil, fmt.Errorf("failed to search VOD in M3U: %w", err)
	}

	utils.DebugLog("VOD search returned %d results for query: %s", len(results), query)
	return results, nil
}

func (c *Config) ensureVODM3UCache() (string, error) {
	vodM3UMu.Lock()
	defer vodM3UMu.Unlock()

	// Cache directory preference: CACHE_FOLDER env or temp dir
	cacheDir := os.Getenv("CACHE_FOLDER")
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), ".stream-share")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	cacheFile := filepath.Join(cacheDir, "vod_cache.m3u")

	// Check freshness vs. configured M3U cache expiration (hours)
	expHours := c.M3UCacheExpiration
	info, err := os.Stat(cacheFile)
	if err == nil {
		age := time.Since(info.ModTime())
		if age.Hours() < float64(expHours) {
			utils.DebugLog("Using cached VOD M3U: %s (age: %v)", cacheFile, age)
			return cacheFile, nil
		}
	}

	// Fetch latest M3U (m3u_plus includes movie entries)
	getURL := fmt.Sprintf("%s/get.php?username=%s&password=%s&type=m3u_plus&output=m3u8",
		c.XtreamBaseURL, c.XtreamUser.String(), c.XtreamPassword.String())

	utils.InfoLog("Refreshing VOD M3U from Xtream: %s", utils.MaskURL(getURL))

	req, err := http.NewRequest("GET", getURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", utils.GetIPTVUserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("backend returned %d for M3U request", resp.StatusCode)
	}

	f, err := os.Create(cacheFile)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	utils.InfoLog("Stored VOD M3U to %s", cacheFile)
	return cacheFile, nil
}

func searchVODInM3UFile(m3uPath string, query string) ([]types.VODResult, error) {
	f, err := os.Open(m3uPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	q := strings.ToLower(strings.TrimSpace(query))
	sc := bufio.NewScanner(f)
	lastEXTINF := ""
	results := make([]types.VODResult, 0, 50)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			lastEXTINF = line
			continue
		}
		// URL lines
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			u, err := url.Parse(line)
			if err != nil {
				continue
			}
			// Only consider movie entries
			if !strings.Contains(u.Path, "/movie/") {
				continue
			}

			// Title: take from EXTINF after the comma
			title := ""
			category := ""
			if lastEXTINF != "" {
				if idx := strings.LastIndex(lastEXTINF, ","); idx != -1 && idx+1 < len(lastEXTINF) {
					title = strings.TrimSpace(lastEXTINF[idx+1:])
				}
				// Extract group-title="..."
				attrs := lastEXTINF
				if i := strings.Index(attrs, " "); i != -1 {
					attrs = attrs[i+1:]
				}
				const key = `group-title="`
				if pos := strings.Index(attrs, key); pos != -1 {
					start := pos + len(key)
					if end := strings.Index(attrs[start:], `"`); end != -1 {
						category = attrs[start : start+end]
					}
				}
			}
			if title == "" {
				title = path.Base(u.Path)
			}

			// Filter by query if provided
			if q != "" && !strings.Contains(strings.ToLower(title), q) {
				continue
			}

			// StreamID is the last path segment
			streamID := path.Base(u.Path)

			results = append(results, types.VODResult{
				ID:       streamID,
				Title:    title,
				Category: category,
				Duration: "",
				Year:     "",
				Rating:   "",
				StreamID: streamID,
			})

			// Reset lastEXTINF after pairing with URL
			lastEXTINF = ""
			if len(results) >= 50 {
				break
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
