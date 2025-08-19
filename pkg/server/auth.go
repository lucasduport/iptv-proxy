package server

import (
	"os"

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

// apiKeyAuth middleware validates the internal API key
func (c *Config) apiKeyAuth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		key := ctx.GetHeader("X-API-Key")
		utils.DebugLog("API Key auth check - received key: %s...", utils.MaskString(key))

		if key != internalAPIKey {
			utils.WarnLog("API authentication failed - invalid key: %s", utils.MaskString(key))
			ctx.AbortWithStatusJSON(401, types.APIResponse{
				Success: false,
				Error:   "Invalid API key",
			})
			return
		}
		utils.DebugLog("API authentication successful for endpoint: %s", ctx.Request.URL.Path)
		ctx.Next()
	}
}
