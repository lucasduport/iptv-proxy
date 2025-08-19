package server

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
)

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

	// Status summary for Discord and dashboards
	api.GET("/status", c.statusSummary)

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