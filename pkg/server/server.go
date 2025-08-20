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
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
	"strings" // FIX: required by endpointAntiColision and replaceURL

	"github.com/gin-contrib/cors"
	"github.com/jamesnetherton/m3u"
	"github.com/lucasduport/stream-share/pkg/config"
	"github.com/lucasduport/stream-share/pkg/database"
	"github.com/lucasduport/stream-share/pkg/discord"
	"github.com/lucasduport/stream-share/pkg/session"
	"github.com/lucasduport/stream-share/pkg/utils"
	uuid "github.com/satori/go.uuid"

	"github.com/gin-gonic/gin"
)

var defaultProxyfiedM3UPath = filepath.Join(os.TempDir(), uuid.NewV4().String()+".stream-share.m3u")
var endpointAntiColision = strings.Split(uuid.NewV4().String(), "-")[0]

// Config represent the server configuration
type Config struct {
	*config.ProxyConfig

	// M3U service part
	playlist *m3u.Playlist
	// this variable is set only for m3u proxy endpoints
	track *m3u.Track
	// path to the proxyfied m3u file
	proxyfiedM3UPath string

	endpointAntiColision string

	// New components
	sessionManager *session.SessionManager
	db             *database.DBManager
	discordBot     *discord.Bot
}

// NewServer initializes a new server configuration with all necessary components
func NewServer(config *config.ProxyConfig) (*Config, error) {
	var p m3u.Playlist

	// Parse the M3U playlist from the remote URL if provided
	if config.RemoteURL.String() != "" {
		var err error
		p, err = m3u.Parse(config.RemoteURL.String())
		if err != nil {
			return nil, utils.PrintErrorAndReturn(err)
		}
		utils.InfoLog("Successfully parsed M3U playlist from %s", config.RemoteURL.String())
	}

	// Use custom ID for endpoint if provided, otherwise use a generated one
	customID := endpointAntiColision
	if trimmedCustomId := strings.Trim(config.CustomId, "/"); trimmedCustomId != "" {
		customID = trimmedCustomId
		utils.InfoLog("Using custom endpoint ID: %s", customID)
	}

	// Initialize debug logging from environment variable
	utils.Config.DebugLoggingEnabled = os.Getenv("DEBUG_LOGGING") == "true"

	// Create server configuration
	serverConfig := &Config{
		config,
		&p,
		nil,
		defaultProxyfiedM3UPath,
		customID,
		nil,
		nil,
		nil,
	}

	// Force PostgreSQL initialization (sqlite removed)
	utils.InfoLog("Bootstrap: Forcing PostgreSQL database initialization")
	db, err := database.NewDBManager("") // path unused for postgres
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	serverConfig.db = db
	serverConfig.sessionManager = session.NewSessionManager(db)
	utils.InfoLog("Session manager initialized with database connection")

	// After session manager init
	if serverConfig.sessionManager == nil {
		utils.ErrorLog("Bootstrap: sessionManager is NIL - multiplexing will NOT be used")
	} else {
		utils.InfoLog("Bootstrap: sessionManager initialized OK")
	}

	// Configure session parameters from environment variables
	if serverConfig.sessionManager != nil {
		if v := os.Getenv("SESSION_TIMEOUT_MINUTES"); v != "" {
			if mins, err := strconv.Atoi(v); err == nil && mins > 0 {
				serverConfig.sessionManager.SetSessionTimeout(time.Duration(mins) * time.Minute)
				utils.InfoLog("Session timeout set to %d minutes", mins)
			} else {
				utils.WarnLog("Invalid SESSION_TIMEOUT_MINUTES: %s", v)
			}
		}
		if v := os.Getenv("STREAM_TIMEOUT_MINUTES"); v != "" {
			if mins, err := strconv.Atoi(v); err == nil && mins > 0 {
				serverConfig.sessionManager.SetStreamTimeout(time.Duration(mins) * time.Minute)
				utils.InfoLog("Stream timeout set to %d minutes", mins)
			} else {
				utils.WarnLog("Invalid STREAM_TIMEOUT_MINUTES: %s", v)
			}
		}
		if v := os.Getenv("TEMP_LINK_HOURS"); v != "" {
			if hours, err := strconv.Atoi(v); err == nil && hours > 0 {
				serverConfig.sessionManager.SetTempLinkTimeout(time.Duration(hours) * time.Hour)
				utils.InfoLog("Temporary link timeout set to %d hours", hours)
			} else {
				utils.WarnLog("Invalid TEMP_LINK_HOURS: %s", v)
			}
		}
	}

	// Initialize Discord bot if token is provided
	discordToken := os.Getenv("DISCORD_BOT_TOKEN")
	if discordToken != "" {
		utils.InfoLog("Initializing Discord bot")
		discordPrefix := os.Getenv("DISCORD_BOT_PREFIX")
		if discordPrefix == "" {
			discordPrefix = "!"
		}
		if strings.HasPrefix(discordPrefix, "/") {
			utils.WarnLog("Discord prefix is '%s'. Slash prefixes may not trigger classic handlers. Prefer '!'", discordPrefix)
		} else {
			utils.InfoLog("Discord bot command prefix: '%s'", discordPrefix)
		}
		discordAdminRole := os.Getenv("DISCORD_ADMIN_ROLE_ID")

		// Get API URL from config, defaulting to localhost
		apiURL := os.Getenv("DISCORD_API_URL")
		if apiURL == "" {
			protocol := "http"
			if config.HTTPS {
				protocol = "https"
			}
			apiURL = fmt.Sprintf("%s://%s:%d", protocol, config.HostConfig.Hostname, config.HostConfig.Port)
		}
		utils.InfoLog("Discord API URL used by bot: %s", apiURL)
		utils.InfoLog("Reminder: Ensure 'MESSAGE CONTENT INTENT' is enabled in Discord Developer Portal for this bot.")

		bot, err := discord.NewBot(discordToken, discordPrefix, discordAdminRole, apiURL, GetAPIKey())
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Discord bot: %w", err)
		}

		serverConfig.discordBot = bot
		utils.InfoLog("Discord bot initialized with prefix %s", discordPrefix)
	} else {
		utils.InfoLog("Bootstrap: DISCORD_BOT_TOKEN not set - Discord bot is DISABLED")
	}

	return serverConfig, nil
}

// Serve the stream-share api
func (c *Config) Serve() error {
	utils.InfoLog("[stream-share] Server is starting...")

	if c.db != nil && c.db.IsInitialized() {
		utils.InfoLog("Bootstrap: Database is initialized and connected")
	} else if c.db != nil {
		utils.WarnLog("Bootstrap: Database manager present but not initialized")
	} else {
		utils.WarnLog("Bootstrap: Database is DISABLED (no persistence)")
	}

	if c.sessionManager == nil {
		utils.ErrorLog("Bootstrap: sessionManager is NIL inside Serve()")
	} else {
		utils.InfoLog("Bootstrap: sessionManager ready (timeouts: session=%v, stream=%v, tempLink=%v)",
			// not exported; we just acknowledge presence
			time.Minute, time.Minute, time.Hour)
	}

	if err := c.playlistInitialization(); err != nil {
		utils.ErrorLog("Playlist initialization failed: %v", err)
		return err
	}

	// Start Discord bot if configured
	if c.discordBot != nil {
		utils.InfoLog("Starting Discord bot...")
		if err := c.discordBot.Start(); err != nil {
			return fmt.Errorf("failed to start Discord bot: %w", err)
		}
		defer c.discordBot.Stop()
	}

	router := gin.Default()
	router.Use(cors.Default())
	utils.InfoLog("Setting up routes and internal API...")

	// Setup API routes for Discord bot and other internal tools
	c.setupInternalAPI(router)

	// Setup regular routes
	group := router.Group("/")
	c.routes(group)

	// Add direct streaming routes with proxy credentials
	c.addProxyCredentialRoutes(router)

	// Add temporary link download route
	router.GET("/download/:token", c.handleTemporaryLink)

	// Add a message to indicate the server is ready
	utils.InfoLog("[stream-share] Server is ready and listening on :%d", c.HostConfig.Port)
	return router.Run(fmt.Sprintf(":%d", c.HostConfig.Port))
}

// Add direct streaming routes with proxy credentials
func (c *Config) addProxyCredentialRoutes(router *gin.Engine) {
	utils.InfoLog("[stream-share] Setting up direct stream routes with proxy credentials")

	// Root level (generic)
	router.GET("/:username/:password/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		id := ctx.Param("id")
		utils.DebugLog("Direct stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
		rpURL, err := url.Parse(fmt.Sprintf("%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
		if err != nil {
			utils.ErrorLog("Failed to parse upstream URL: %v", err)
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.multiplexedStream(ctx, rpURL)
	})

	// Live
	router.GET("/live/:username/:password/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		id := ctx.Param("id")
		utils.DebugLog("Direct live stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
		rpURL, err := url.Parse(fmt.Sprintf("%s/live/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
		if err != nil {
			utils.ErrorLog("Failed to parse upstream URL: %v", err)
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.multiplexedStream(ctx, rpURL)
	})

	// Movie
	router.GET("/movie/:username/:password/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		id := ctx.Param("id")
		utils.DebugLog("Direct movie stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
		rpURL, err := url.Parse(fmt.Sprintf("%s/movie/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
		if err != nil {
			utils.ErrorLog("Failed to parse upstream URL: %v", err)
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.multiplexedStream(ctx, rpURL)
	})

	// Series
	router.GET("/series/:username/:password/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		id := ctx.Param("id")
		utils.DebugLog("Direct series stream request with proxy credentials: username=%s, id=%s", ctx.Param("username"), id)
		rpURL, err := url.Parse(fmt.Sprintf("%s/series/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, id))
		if err != nil {
			utils.ErrorLog("Failed to parse upstream URL: %v", err)
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.multiplexedStream(ctx, rpURL)
	})

	// Timeshift
	router.GET("/timeshift/:username/:password/:duration/:start/:id", c.authWithPathCredentials(), func(ctx *gin.Context) {
		duration := ctx.Param("duration")
		start := ctx.Param("start")
		id := ctx.Param("id")
		utils.DebugLog("Timeshift request with proxy credentials: duration=%s, start=%s, id=%s", duration, start, id)
		rpURL, err := url.Parse(fmt.Sprintf("%s/timeshift/%s/%s/%s/%s/%s", c.XtreamBaseURL, c.XtreamUser, c.XtreamPassword, duration, start, id))
		if err != nil {
			utils.ErrorLog("Failed to parse upstream URL: %v", err)
			ctx.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.multiplexedStream(ctx, rpURL)
	})

	utils.InfoLog("[stream-share] Routes initialized with direct stream URL support")
}

// Authentication middleware that checks credentials from URL path parameters
// and manages user sessions for multiplexing
func (c *Config) authWithPathCredentials() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		username := ctx.Param("username")
		password := ctx.Param("password")
		ip := ctx.ClientIP()
		userAgent := ctx.Request.UserAgent()

		utils.DebugLog("Path credentials auth check: username=%s, IP=%s", username, ip)

		// If LDAP is enabled, authenticate against LDAP
		if c.ProxyConfig.LDAPEnabled {
			ok := ldapAuthenticate(
				c.ProxyConfig.LDAPServer,
				c.ProxyConfig.LDAPBaseDN,
				c.ProxyConfig.LDAPBindDN,
				c.ProxyConfig.LDAPBindPassword,
				c.ProxyConfig.LDAPUserAttribute,
				c.ProxyConfig.LDAPGroupAttribute,
				c.ProxyConfig.LDAPRequiredGroup,
				username,
				password,
			)
			if !ok {
				utils.DebugLog("LDAP authentication failed for user in path: %s", username)
				ctx.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			utils.DebugLog("LDAP authentication succeeded for user in path: %s", username)
		} else if c.ProxyConfig.User.String() != username || c.ProxyConfig.Password.String() != password {
			utils.DebugLog("Local authentication failed for user in path: %s", username)
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		// Register or update the user session and set username in context for later logs
		if c.sessionManager == nil {
			utils.ErrorLog("authWithPathCredentials: sessionManager is NIL - cannot register user session")
		} else {
			c.sessionManager.RegisterUser(username, ip, userAgent)
			utils.InfoLog("authWithPathCredentials: session registered for user=%s ip=%s", username, ip)
		}
		ctx.Set("username", username)

		ctx.Next()
	}
}

// handleTemporaryLink processes temporary link downloads
func (c *Config) handleTemporaryLink(ctx *gin.Context) {
	token := ctx.Param("token")

	// Get the temporary link from session manager
	tempLink, err := c.sessionManager.GetTemporaryLink(token)
	if err != nil {
		utils.DebugLog("Temporary link not found: %v", err)
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	// Parse the target URL
	targetURL, err := url.Parse(tempLink.URL)
	if err != nil {
		utils.ErrorLog("Invalid URL in temporary link: %v", err)
		ctx.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// Add appropriate headers for download
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.mp4"`, tempLink.Title))

	// Stream the content to the client
	c.multiplexedStream(ctx, targetURL)
}

// multiplexedStream handles streaming with connection multiplexing
func (c *Config) multiplexedStream(ctx *gin.Context, targetURL *url.URL) {
	username := ctx.GetString("username")
	if username == "" {
		// Try to get from path parameters
		username = ctx.Param("username")
	}

	// If username is still empty, use a temporary random ID
	if username == "" {
		username = fmt.Sprintf("temp-%s", uuid.NewV4().String())
	}

	// Extract stream ID and type
	streamID := path.Base(targetURL.Path)
	streamType := "unknown"
	p := targetURL.Path
	if strings.Contains(p, "/movie/") {
		streamType = "movie"
	} else if strings.Contains(p, "/series/") {
		streamType = "series"
	} else if strings.Contains(p, "/live/") {
		streamType = "live"
	} else if strings.Contains(p, "/timeshift/") {
		streamType = "timeshift"
	}

	// Title from query parameter or fallback to stream ID
	streamTitle := targetURL.Query().Get("title")
	if streamTitle == "" {
		streamTitle = streamID
	}

	utils.DebugLog("Multiplexed stream request: user=%s, id=%s, type=%s, title=%s, upstream=%s",
		username, streamID, streamType, streamTitle, targetURL.String())

	if c.sessionManager == nil {
		utils.ErrorLog("Multiplex: sessionManager is NIL, falling back to direct streaming")
		c.stream(ctx, targetURL)
		return
	}

	// Request the stream through the session manager for multiplexing
	buffer, err := c.sessionManager.RequestStream(username, streamID, streamType, streamTitle, targetURL)
	if err != nil {
		utils.ErrorLog("Multiplex: RequestStream failed for user=%s streamID=%s err=%v", username, streamID, err)
		ctx.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if buffer == nil {
		utils.WarnLog("Multiplex: buffer returned is NIL for streamID=%s (user=%s)", streamID, username)
	}

	// Get the channel for this client
	dataChan, exists := c.sessionManager.GetClientChannel(streamID, username)
	if !exists {
		utils.ErrorLog("Failed to get client channel for user=%s, streamID=%s", username, streamID)
		ctx.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// Set appropriate headers based on content type
	// ctx.Header("Content-Type", "video/mp4") // Default, may be overridden
	// Replace with smarter content-type and no-buffering headers
	contentType := "application/octet-stream"
	ext := strings.ToLower(path.Ext(targetURL.Path))
	if strings.Contains(p, "/live/") || ext == ".ts" {
		contentType = "video/mp2t"
	} else if ext == ".m3u8" {
		contentType = "application/vnd.apple.mpegurl"
	} else if ext == ".mp4" {
		contentType = "video/mp4"
	}
	ctx.Header("Content-Type", contentType)
	// Disable intermediary buffering and keep connection alive
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Pragma", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.Header("X-Accel-Buffering", "no")

	// Stream data to the client
	utils.InfoLog("Starting multiplexed stream for user %s (stream %s)", username, streamID)

	ctx.Stream(func(w io.Writer) bool {
		// Wait for data from channel
		data, ok := <-dataChan
		if !ok {
			// Channel closed, end streaming
			utils.DebugLog("Stream channel closed for user %s (stream %s)", username, streamID)
			return false
		}

		// Write data to client
		if _, err := w.Write(data); err != nil {
			// Client disconnected
			utils.DebugLog("Client write error for user %s (stream %s): %v", username, streamID, err)
			c.sessionManager.RemoveClient(streamID, username)
			return false
		}

		// Force immediate delivery to client to avoid periodic buffering
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		return true
	})

	// Clean up after streaming is done
	utils.InfoLog("Stream ended for user %s (stream %s)", username, streamID)
	c.sessionManager.RemoveClient(streamID, username)
}

func (c *Config) playlistInitialization() error {
	if len(c.playlist.Tracks) == 0 {
		return nil
	}

	f, err := os.Create(c.proxyfiedM3UPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return c.marshallInto(f, false)
}

// MarshallInto a *bufio.Writer a Playlist.
func (c *Config) marshallInto(into *os.File, xtream bool) error {
	filteredTrack := make([]m3u.Track, 0, len(c.playlist.Tracks))

	ret := 0
	into.WriteString("#EXTM3U\n") // nolint: errcheck
	for i, track := range c.playlist.Tracks {
		var buffer bytes.Buffer

		buffer.WriteString("#EXTINF:")                       // nolint: errcheck
		buffer.WriteString(fmt.Sprintf("%d ", track.Length)) // nolint: errcheck
		for i := range track.Tags {
			if i == len(track.Tags)-1 {
				buffer.WriteString(fmt.Sprintf("%s=%q", track.Tags[i].Name, track.Tags[i].Value)) // nolint: errcheck
				continue
			}
			buffer.WriteString(fmt.Sprintf("%s=%q ", track.Tags[i].Name, track.Tags[i].Value)) // nolint: errcheck
		}

		uri, err := c.replaceURL(track.URI, i-ret, xtream)
		if err != nil {
			ret++
			log.Printf("ERROR: track: %s: %s", track.Name, err)
			continue
		}

		into.WriteString(fmt.Sprintf("%s, %s\n%s\n", buffer.String(), track.Name, uri)) // nolint: errcheck

		filteredTrack = append(filteredTrack, track)
	}
	c.playlist.Tracks = filteredTrack

	return into.Sync()
}

// ReplaceURL replace original playlist url by proxy url
func (c *Config) replaceURL(uri string, trackIndex int, xtream bool) (string, error) {
	oriURL, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	protocol := "http"
	if c.HTTPS {
		protocol = "https"
	}

	customEnd := strings.Trim(c.CustomEndpoint, "/")
	if customEnd != "" {
		customEnd = fmt.Sprintf("/%s", customEnd)
	}

	uriPath := oriURL.EscapedPath()
	if xtream {
		// Xtream get.php mode: replace provider creds with local creds in path
		uriPath = strings.ReplaceAll(uriPath, c.XtreamUser.PathEscape(), c.User.PathEscape())
		uriPath = strings.ReplaceAll(uriPath, c.XtreamPassword.PathEscape(), c.Password.PathEscape())
	} else {
		// M3U proxified path
		uriPath = path.Join(
			"/",
			c.endpointAntiColision,
			c.User.PathEscape(),
			c.Password.PathEscape(),
			fmt.Sprintf("%d", trackIndex),
			path.Base(uriPath),
		)
	}

	basicAuth := oriURL.User.String()
	if basicAuth != "" {
		basicAuth += "@"
	}

	newURI := fmt.Sprintf(
		"%s://%s%s:%d%s%s",
		protocol,
		basicAuth,
		c.HostConfig.Hostname,
		c.AdvertisedPort,
		customEnd,
		uriPath,
	)

	newURL, err := url.Parse(newURI)
	if err != nil {
		return "", err
	}

	return newURL.String(), nil
}