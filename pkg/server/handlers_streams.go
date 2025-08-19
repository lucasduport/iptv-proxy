package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
)

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

	utils.DebugLog("API: Found active stream %s with %d viewers", streamID, len(stream.GetViewers()))
	ctx.JSON(http.StatusOK, types.APIResponse{
		Success: true,
		Data:    stream,
	})
}
