package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"path"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
)

// internalAPIKey is used to secure the internal API
var internalAPIKey string

func init() {
	// Generate a random API key at startup or use from environment
	envKey := os.Getenv("INTERNAL_API_KEY")
	if envKey != "" {
		internalAPIKey = envKey
		utils.InfoLog("Using API key from environment")
	} else {
		internalAPIKey = uuid.New().String()
		utils.InfoLog("Generated new internal API key: %s", internalAPIKey)
	}
}

// GetAPIKey returns the current internal API key
func GetAPIKey() string {
	return internalAPIKey
}

// setupInternalAPI configures internal API routes for bot communication
func (c *Config) setupInternalAPI(r *gin.Engine) {
	utils.InfoLog("Setting up internal API endpoints")

	// Check if database is initialized
	if c.db == nil {
		utils.ErrorLog("CRITICAL ERROR: Database not initialized when setting up API")
	}

	// Check if session manager is initialized
	if c.sessionManager == nil {
		utils.ErrorLog("CRITICAL ERROR: Session manager not initialized when setting up API")
	}

	api := r.Group("/api/internal")
	api.Use(c.apiKeyAuth())

	// Add recovery middleware to prevent panics from taking down the server
	api.Use(gin.Recovery())
	api.Use(func(ctx *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				utils.ErrorLog("API PANIC RECOVERED: %v\nStack trace: %s", err, debug.Stack())
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, types.APIResponse{
					Success: false,
					Error:   fmt.Sprintf("Internal server error: %v", err),
				})
			}
		}()
		ctx.Next()
	})

	// User management endpoints
	api.GET("/users", c.getAllUsers)
	api.GET("/users/:username", c.getUserInfo)
	api.POST("/users/disconnect/:username", c.disconnectUser)
	api.POST("/users/timeout/:username", c.timeoutUser)

	// Stream management endpoints
	api.GET("/streams", c.getAllStreams)
	api.GET("/streams/:streamid", c.getStreamInfo)

	// Discord integration endpoints
	api.POST("/discord/link", c.linkDiscordUser)
	api.GET("/discord/:discordid/ldap", c.getLDAPFromDiscord)

	// VOD search and download endpoints
	api.POST("/vod/search", c.searchVOD)
	api.POST("/vod/download", c.createVODDownload)
	api.GET("/vod/status/:requestid", c.getVODRequestStatus)

	// Debug endpoint to verify API is working
	api.GET("/ping", func(ctx *gin.Context) {
		utils.DebugLog("API ping received")
		ctx.JSON(http.StatusOK, types.APIResponse{
			Success: true,
			Message: "API is running",
			Data: map[string]interface{}{
				"time":          time.Now().String(),
				"db_connected":  c.db != nil,
				"session_mgr":   c.sessionManager != nil,
				"discord_ready": c.discordBot != nil,
			},
		})
	})

	utils.InfoLog("Internal API routes configured successfully")
}

// apiKeyAuth middleware validates the internal API key
func (c *Config) apiKeyAuth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		key := ctx.GetHeader("X-API-Key")
		utils.DebugLog("API Key auth check - received key: %s...", maskString(key))

		if key != internalAPIKey {
			utils.WarnLog("API authentication failed - invalid key: %s", maskString(key))
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, types.APIResponse{
				Success: false,
				Error:   "Invalid API key",
			})
			return
		}
		utils.DebugLog("API authentication successful for endpoint: %s", ctx.Request.URL.Path)
		ctx.Next()
	}
}

// getAllUsers returns information about all active users
func (c *Config) getAllUsers(ctx *gin.Context) {
	utils.DebugLog("API: Getting all users")

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in getAllUsers")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	sessions := c.sessionManager.GetAllSessions()
	utils.DebugLog("API: Found %d active user sessions", len(sessions))

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data:    sessions,
	})
}

// getUserInfo returns information about a specific user
func (c *Config) getUserInfo(ctx *gin.Context) {
	username := ctx.Param("username")
	utils.DebugLog("API: Getting info for user: %s", username)

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in getUserInfo")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	session := c.sessionManager.GetUserSession(username)

	if session == nil {
		utils.DebugLog("API: User not found: %s", username)
		ctx.JSON(http.StatusNotFound, types.APIResponse{
			Success: false,
			Error:   "User not found",
		})
		return
	}

	utils.DebugLog("API: Found user session for %s, streaming: %s", username, session.StreamID)
	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data:    session,
	})
}

// disconnectUser forcibly disconnects a user from all streams
func (c *Config) disconnectUser(ctx *gin.Context) {
	username := ctx.Param("username")
	utils.DebugLog("API: Disconnecting user: %s", username)

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in disconnectUser")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	c.sessionManager.DisconnectUser(username)
	utils.InfoLog("User %s forcibly disconnected via API", username)

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Message: fmt.Sprintf("User %s disconnected", username),
	})
}

// timeoutUser temporarily blocks a user for a specified duration
func (c *Config) timeoutUser(ctx *gin.Context) {
	username := ctx.Param("username")
	utils.DebugLog("API: Timeout request for user: %s", username)

	var req struct {
		Minutes int `json:"minutes"`
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ErrorLog("API: Invalid timeout request: %v", err)
		ctx.JSON(http.StatusBadRequest, types.APIResponse{
			Success: false,
			Error:   "Invalid request: " + err.Error(),
		})
		return
	}

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in timeoutUser")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	// Disconnect the user
	c.sessionManager.DisconnectUser(username)
	utils.InfoLog("User %s timed out for %d minutes", username, req.Minutes)

	// TODO: Implement actual timeout mechanism
	// For now we'll just disconnect

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Message: fmt.Sprintf("User %s timed out for %d minutes", username, req.Minutes),
	})
}

// getAllStreams returns information about all active streams
func (c *Config) getAllStreams(ctx *gin.Context) {
	utils.DebugLog("API: Getting all active streams")

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in getAllStreams")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	streams := c.sessionManager.GetAllStreams()
	utils.DebugLog("API: Found %d active streams", len(streams))

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data:    streams,
	})
}

// getStreamInfo returns information about a specific stream
func (c *Config) getStreamInfo(ctx *gin.Context) {
	streamID := ctx.Param("streamid")
	utils.DebugLog("API: Getting stream info for: %s", streamID)

	if c.sessionManager == nil {
		utils.ErrorLog("Session manager is nil in getStreamInfo")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Session manager not initialized",
		})
		return
	}

	stream, exists := c.sessionManager.GetStreamInfo(streamID)

	if !exists || !stream.Active {
		utils.DebugLog("API: Stream not found or inactive: %s", streamID)
		ctx.JSON(http.StatusNotFound, types.APIResponse{
			Success: false,
			Error:   "Stream not found or inactive",
		})
		return
	}

	utils.DebugLog("API: Found active stream %s with %d viewers",
		streamID, len(stream.GetViewers()))

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data:    stream,
	})
}

// linkDiscordUser links a Discord user ID to an LDAP username
func (c *Config) linkDiscordUser(ctx *gin.Context) {
	utils.DebugLog("API: Request to link Discord user to LDAP")

	var req struct {
		DiscordID   string `json:"discord_id"`
		DiscordName string `json:"discord_name"`
		LDAPUser    string `json:"ldap_user"`
		Token       string `json:"token"`  // Optional validation token
	}

	if err := ctx.ShouldBindJSON(&req); err != nil {
		utils.ErrorLog("API: Invalid Discord link request: %v", err)
		ctx.JSON(http.StatusBadRequest, types.APIResponse{
			Success: false,
			Error:   "Invalid request: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Linking Discord ID %s (%s) to LDAP user %s",
		req.DiscordID, req.DiscordName, req.LDAPUser)

	if c.db == nil {
		utils.ErrorLog("Database is nil in linkDiscordUser")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Database not initialized",
		})
		return
	}

	if err := c.db.LinkDiscordToLDAP(req.DiscordID, req.DiscordName, req.LDAPUser); err != nil {
		utils.ErrorLog("API: Failed to link Discord to LDAP: %v", err)
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Failed to link accounts: " + err.Error(),
		})
		return
	}

	utils.InfoLog("Successfully linked Discord ID %s (%s) to LDAP user %s",
		req.DiscordID, req.DiscordName, req.LDAPUser)

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Message: "Discord account linked successfully",
		Data: map[string]string{
			"discord_id":   req.DiscordID,
			"discord_name": req.DiscordName,
			"ldap_user":    req.LDAPUser,
		},
	})
}

// getLDAPFromDiscord gets the LDAP username for a Discord ID
func (c *Config) getLDAPFromDiscord(ctx *gin.Context) {
	discordID := ctx.Param("discordid")
	utils.DebugLog("API: Getting LDAP user for Discord ID: %s", discordID)

	if c.db == nil {
		utils.ErrorLog("Database is nil in getLDAPFromDiscord")
		ctx.JSON(http.StatusInternalServerError, types.APIResponse{
			Success: false,
			Error:   "Database not initialized",
		})
		return
	}

	ldapUser, err := c.db.GetLDAPUserByDiscordID(discordID)
	if err != nil {
		utils.DebugLog("API: Discord user not linked: %v", err)
		ctx.JSON(http.StatusNotFound, types.APIResponse{
			Success: false,
			Error:   "Discord user not linked: " + err.Error(),
		})
		return
	}

	utils.DebugLog("API: Found LDAP user %s for Discord ID %s", ldapUser, discordID)
	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data: map[string]string{
			"ldap_user": ldapUser,
		},
	})
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

	// Here we would query the Xtream API for VOD content matching the query
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

	// Create a new VOD request with a unique token
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

	utils.DebugLog("API: Creating download for user %s, stream %s, title %s",
		req.Username, req.StreamID, req.Title)

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
	vodURL := fmt.Sprintf("%s/movie/%s/%s/%s",
		c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, req.StreamID)

	utils.DebugLog("API: VOD URL created: %s", maskURL(vodURL))

	// Generate a temporary download token
	token, err := c.sessionManager.GenerateTemporaryLink(
		req.Username, req.StreamID, req.Title, vodURL)
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

	downloadURL := fmt.Sprintf("%s://%s:%d/download/%s",
		protocol, c.HostConfig.Hostname, c.AdvertisedPort, token)

	utils.InfoLog("Created VOD download link for user %s, title: %s, token: %s",
		req.Username, req.Title, token)

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
	// This is just a placeholder for now

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"status":   "completed",
			"progress": 100,
		},
	})
}

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

// ---- M3U cache & parsing helpers ----

var vodM3UMu sync.Mutex

func (c *Config) ensureVODM3UCache() (string, error) {
	vodM3UMu.Lock()
	defer vodM3UMu.Unlock()

	// Cache directory preference: CACHE_FOLDER env or temp dir
	cacheDir := os.Getenv("CACHE_FOLDER")
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), ".iptv-proxy")
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

	utils.InfoLog("Refreshing VOD M3U from Xtream: %s", maskURL(getURL))

	req, err := http.NewRequest("GET", getURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "iptv-proxy/1.0")

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
				// crude extraction
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

// Helper function to mask sensitive parts of strings for logging
func maskString(s string) string {
	if len(s) <= 8 {
		if len(s) <= 0 {
			return "[empty]"
		}
		return s[:1] + "******"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// Helper function to mask sensitive parts of URLs for logging
func maskURL(urlStr string) string {
	parts := strings.Split(urlStr, "/")
	if len(parts) >= 7 {
		// For URLs like http://host/path/user/pass/id
		parts[5] = maskString(parts[5]) // Password
		parts[4] = maskString(parts[4]) // Username
	}
	return strings.Join(parts, "/")
}