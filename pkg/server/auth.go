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
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
)

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

func GetAPIKey() string {
	return internalAPIKey
}

// apiKeyAuth middleware validates the internal API key
func (c *Config) apiKeyAuth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		key := ctx.GetHeader("X-API-Key")
		utils.DebugLog("API Key auth check - received key: %s...", utils.MaskString(key))

		if key != internalAPIKey {
			utils.DebugLog("API authentication failed - invalid key: %s", utils.MaskString(key))
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
