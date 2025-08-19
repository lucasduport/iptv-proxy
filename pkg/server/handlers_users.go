package server

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
)

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

	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Message: fmt.Sprintf("User %s timed out for %d minutes", username, req.Minutes),
	})
}
