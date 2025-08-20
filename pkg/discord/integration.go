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

package discord

import (
	"os"

	"github.com/lucasduport/stream-share/pkg/utils"
)

// Integration manages Discord integration components (bot only)
type Integration struct {
	Bot         *Bot
	Enabled     bool
	initialized bool
}

// NewIntegration creates a new Discord integration (bot only)
func NewIntegration() (*Integration, error) {
	utils.InfoLog("Initializing Discord integration")

	enabled := os.Getenv("DISCORD_BOT_ENABLED") == "true"
	if !enabled {
		utils.InfoLog("Discord integration disabled by configuration")
		return &Integration{Enabled: false}, nil
	}

	integration := &Integration{Enabled: true}

	// Initialize bot
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		utils.WarnLog("Discord bot token not provided - bot functionality disabled")
	} else {
		prefix := os.Getenv("DISCORD_BOT_PREFIX")
		if prefix == "" {
			prefix = "!"
		}
		adminRole := os.Getenv("DISCORD_ADMIN_ROLE_ID")
		apiURL := os.Getenv("DISCORD_API_URL")
		apiKey := os.Getenv("INTERNAL_API_KEY")
		if apiKey == "" {
			utils.ErrorLog("INTERNAL_API_KEY not set, Discord bot will not be able to communicate with API")
		}
		bot, err := NewBot(token, prefix, adminRole, apiURL, apiKey)
		if err != nil {
			utils.ErrorLog("Failed to initialize Discord bot: %v", err)
			return nil, err
		}
		integration.Bot = bot
		utils.InfoLog("Discord bot initialized with prefix '%s'", prefix)
	}

	integration.initialized = true
	return integration, nil
}

// Start starts the Discord integration components
func (i *Integration) Start() error {
	if !i.Enabled || !i.initialized {
		return nil
	}
	utils.InfoLog("Starting Discord integration")
	if i.Bot != nil {
		utils.InfoLog("Starting Discord bot")
		if err := i.Bot.Start(); err != nil {
			utils.ErrorLog("Failed to start Discord bot: %v", err)
			return err
		}
	}
	return nil
}

// Stop stops the Discord integration components
func (i *Integration) Stop() {
	if !i.Enabled || !i.initialized {
		return
	}
	utils.InfoLog("Stopping Discord integration")
	if i.Bot != nil {
		utils.InfoLog("Stopping Discord bot")
		i.Bot.Stop()
	}
}