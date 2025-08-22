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
    "fmt"

    "github.com/bwmarrin/discordgo"
)

// handleDisconnect forcibly disconnects a user (admin only).
func (b *Bot) handleDisconnect(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
    if len(args) != 1 { b.info(m.ChannelID, "🔌 Disconnect User", "Usage: `!disconnect <username>`"); return }
    username := args[0]
    ok, _, err := b.makeAPIRequest("POST", "/users/disconnect/"+username, nil)
    if err != nil || !ok { b.fail(m.ChannelID, "❌ Disconnect Failed", fmt.Sprintf("We couldn't disconnect this user.\n\nError: `%v`", err)); return }
    b.success(m.ChannelID, "✅ User Disconnected", fmt.Sprintf("User **%s** has been disconnected.", username))
}

// handleTimeout temporarily blocks a user (admin only).
func (b *Bot) handleTimeout(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
    if len(args) != 2 { b.info(m.ChannelID, "⏳ Timeout User", "Usage: `!timeout <username> <minutes>`"); return }
    username := args[0]
    minutes := 0
    fmt.Sscanf(args[1], "%d", &minutes)
    if minutes <= 0 { b.warn(m.ChannelID, "⏳ Invalid Timeout", "Timeout minutes must be a positive number."); return }
    ok, _, err := b.makeAPIRequest("POST", "/users/timeout/"+username, map[string]int{"minutes": minutes})
    if err != nil || !ok { b.fail(m.ChannelID, "❌ Timeout Failed", fmt.Sprintf("We couldn't set a timeout for this user.\n\nError: `%v`", err)); return }
    b.success(m.ChannelID, "✅ Timeout Applied", fmt.Sprintf("User **%s** has been timed out for **%d** minutes.", username, minutes))
}
