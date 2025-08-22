
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
    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/utils"
)

// hasAdminRole checks if a user has the configured admin role in a guild.
func (b *Bot) hasAdminRole(s *discordgo.Session, guildID, userID string) bool {
    if b.adminRoleID == "" {
        return false
    }
    member, err := s.GuildMember(guildID, userID)
    if err != nil {
        utils.ErrorLog("Failed to get member info: %v", err)
        return false
    }
    for _, roleID := range member.Roles {
        if roleID == b.adminRoleID {
            return true
        }
    }
    return false
}
