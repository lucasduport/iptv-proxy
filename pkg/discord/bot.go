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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
)

// Bot represents the Discord bot
type Bot struct {
	session          *discordgo.Session
	token            string
	prefix           string
	adminRoleID      string
	apiURL           string
	apiKey           string
	client           *http.Client
	pendingVODLinks  map[string]map[int]types.VODResult // Discord user ID -> choice index -> VOD result
	linkTokens       map[string]string                  // Token -> Discord user ID
	pendingVODLock   sync.RWMutex
	linkTokensLock   sync.RWMutex
	cleanupInterval  time.Duration
	// Add: track pending VOD choices per sent message (for reaction selection)
	pendingVODByMsg  map[string]*vodPendingContext // messageID -> context
	pendingMsgLock   sync.RWMutex
}

// Context for reaction-based VOD selection
type vodPendingContext struct {
	UserID    string
	ChannelID string
	Choices   map[int]types.VODResult // 1..10 (0 represents 10)
}

// NewBot creates a new Discord bot
func NewBot(token, prefix, adminRoleID, apiURL, apiKey string) (*Bot, error) {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	bot := &Bot{
		session:         dg,
		token:           token,
		prefix:          prefix,
		adminRoleID:     adminRoleID,
		apiURL:          strings.TrimSuffix(apiURL, "/"),
		apiKey:          apiKey,
		pendingVODLinks: make(map[string]map[int]types.VODResult),
		linkTokens:      make(map[string]string),
		cleanupInterval: 30 * time.Minute,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		// Added maps/locks for reaction-based selection
		pendingVODByMsg: make(map[string]*vodPendingContext),
	}

	// Register handlers
	dg.AddHandler(bot.messageCreate)
	// Handle reactions for selection
	dg.AddHandler(bot.messageReactionAdd)
	dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		// Polished ready log
		if s != nil && s.State != nil && s.State.User != nil {
			utils.InfoLog("Discord ready: %s#%s (%s) | Prefix: %s",
				s.State.User.Username, s.State.User.Discriminator, s.State.User.ID, bot.prefix)
		} else {
			utils.InfoLog("Discord ready: session state not populated yet | Prefix: %s", bot.prefix)
		}
		utils.WarnLog("Ensure 'MESSAGE CONTENT INTENT' is enabled in the Developer Portal.")
	})

	// Intents: add reactions handling
	dg.Identify.Intents = discordgo.IntentGuilds |
		discordgo.IntentGuildMessages |
		discordgo.IntentDirectMessages |
		discordgo.IntentMessageContent |
		discordgo.IntentGuildMessageReactions |
		discordgo.IntentDirectMessageReactions

	// Start cleanup routine
	go bot.cleanupRoutine()

	return bot, nil
}

// Start starts the Discord bot
func (b *Bot) Start() error {
	utils.InfoLog("Starting Discord bot with intents: Guilds, GuildMessages, DirectMessages, MessageContent, Reactions")
	return b.session.Open()
}

// Stop stops the Discord bot
func (b *Bot) Stop() {
	utils.InfoLog("Stopping Discord bot")
	b.session.Close()
}

// cleanupRoutine periodically cleans up expired data
func (b *Bot) cleanupRoutine() {
	ticker := time.NewTicker(b.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		b.cleanupExpiredVODLinks()
		b.cleanupExpiredLinkTokens()
	}
}

// cleanupExpiredVODLinks removes VOD search results that have been pending for too long
func (b *Bot) cleanupExpiredVODLinks() {
	b.pendingVODLock.Lock()
	defer b.pendingVODLock.Unlock()

	// For simplicity, just clear all pending VOD links
	// In a real implementation, you'd check timestamps
	b.pendingVODLinks = make(map[string]map[int]types.VODResult)
}

// cleanupExpiredLinkTokens removes expired link tokens
func (b *Bot) cleanupExpiredLinkTokens() {
	b.linkTokensLock.Lock()
	defer b.linkTokensLock.Unlock()

	// For simplicity, just clear all link tokens
	// In a real implementation, you'd check timestamps
	b.linkTokens = make(map[string]string)
}

// messageCreate is the handler for new messages
func (b *Bot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from bots (including self)
	if m.Author != nil && m.Author.Bot {
		return
	}

	// Basic diagnostics
	utils.DebugLog("Discord messageCreate: guild=%s channel=%s author=%s content_len=%d",
		m.GuildID, m.ChannelID, m.Author.ID, len(m.Content))

	// If content is empty in guild messages, it's almost certainly missing Message Content Intent
	if m.GuildID != "" && strings.TrimSpace(m.Content) == "" {
		utils.WarnLog("Discord message content is empty for guild message; check 'MESSAGE CONTENT INTENT' is enabled")
		return
	}

	// Check if the message starts with the command prefix
	content := strings.TrimSpace(m.Content)
	if !strings.HasPrefix(content, b.prefix) {
		return
	}

	// Extract the command and arguments
	parts := strings.Fields(content[len(b.prefix):])
	if len(parts) == 0 {
		return
	}

	command := strings.ToLower(parts[0])
	args := parts[1:]

	utils.DebugLog("Discord command received: %s args=%v", command, args)

	// Command handlers
	switch command {
	case "link":
		b.handleLink(s, m, args)
	case "vod":
		b.handleVOD(s, m, args)
	case "status":
		b.handleStatus(s, m, args)
	case "disconnect":
		b.handleDisconnect(s, m, args)
	case "timeout":
		b.handleTimeout(s, m, args)
	case "help":
		b.handleHelp(s, m)
	default:
		utils.DebugLog("Discord: unknown command '%s'", command)
	}
}

// handleLink handles the !link command to link Discord account to LDAP
func (b *Bot) handleLink(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	if len(args) != 1 {
		b.info(m.ChannelID, "🔗 Link Your Account",
			"Usage: `!link <ldap_username>`\n\nThis links your Discord account to your IPTV account.")
		return
	}

	ldapUser := strings.TrimSpace(args[0])
	if ldapUser == "" {
		b.info(m.ChannelID, "🔗 Link Your Account",
			"Usage: `!link <ldap_username>`\n\nThis links your Discord account to your IPTV account.")
		return
	}

	linkData := map[string]interface{}{
		"discord_id":   m.Author.ID,
		"discord_name": m.Author.Username,
		"ldap_user":    ldapUser,
	}

	success, resp, err := b.makeAPIRequest("POST", "/discord/link", linkData)
	if err != nil || !success {
		msg := "We couldn't link your account right now."
		if err != nil {
			msg += fmt.Sprintf("\n\nError: `%s`", err.Error())
		}
		b.fail(m.ChannelID, "❌ Link Failed", msg)
		return
	}

	var confirmed string
	if data, ok := resp.(map[string]interface{}); ok {
		if u, exists := data["ldap_user"]; exists {
			confirmed = fmt.Sprintf("%v", u)
		}
	}
	if confirmed == "" {
		confirmed = ldapUser
	}

	b.success(
		m.ChannelID,
		"✅ Linked Successfully",
		fmt.Sprintf("Your Discord account is now linked to `%s`.\n\nYou're all set to use other commands.", confirmed),
	)
}

// handleVOD handles the !vod command to search for VOD content
func (b *Bot) handleVOD(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		b.info(m.ChannelID, "🎬 VOD Search",
			"Usage: `!vod <search query>`\n\nExample: `!vod the matrix`")
		return
	}

	// Resolve LDAP user for this Discord user
	success, respData, err := b.makeAPIRequest("GET", "/discord/"+m.Author.ID+"/ldap", nil)
	if err != nil || !success {
		b.warn(m.ChannelID, "🔗 Linking Required",
			"Your Discord account is not linked to an IPTV user.\n\nPlease link it first:\n`!link <ldap_username>`")
		return
	}

	data, ok := respData.(map[string]interface{})
	if !ok {
		b.fail(m.ChannelID, "❌ Unexpected Response",
			"Failed to process the server response. Please try again later.")
		return
	}
	ldapUser, ok := data["ldap_user"].(string)
	if !ok || ldapUser == "" {
		b.warn(m.ChannelID, "🔗 Linking Required",
			"Your Discord account is not linked to an IPTV user.\n\nPlease link it first:\n`!link <ldap_username>`")
		return
	}

	// Search request
	searchData := map[string]string{
		"username": ldapUser,
		"query":    query,
	}
	success, respData, err = b.makeAPIRequest("POST", "/vod/search", searchData)
	if err != nil || !success {
		msg := "We couldn't complete your search."
		if err != nil {
			msg += fmt.Sprintf("\n\nError: `%s`", err.Error())
		}
		b.fail(m.ChannelID, "❌ Search Failed", msg)
		return
	}

	data, ok = respData.(map[string]interface{})
	if !ok {
		b.fail(m.ChannelID, "❌ Unexpected Response",
			"Failed to process the search results. Please try again later.")
		return
	}

	resultsData, ok := data["results"].([]interface{})
	if !ok || len(resultsData) == 0 {
		b.info(m.ChannelID, "🔎 No Results",
			fmt.Sprintf("No results found for `%s`.\nTry a different title or spelling.", query))
		return
	}

	// Convert and render up to 10 results
	var results []types.VODResult
	for _, result := range resultsData {
		if len(results) >= 10 {
			break
		}
		if resultMap, ok := result.(map[string]interface{}); ok {
			results = append(results, types.VODResult{
				ID:       getString(resultMap, "ID"),
				Title:    getString(resultMap, "Title"),
				Category: getString(resultMap, "Category"),
				Duration: getString(resultMap, "Duration"),
				Year:     getString(resultMap, "Year"),
				Rating:   getString(resultMap, "Rating"),
				StreamID: getString(resultMap, "StreamID"),
			})
		}
	}

	// Build a polished results embed
	var list strings.Builder
	emojis := []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "0️⃣"}
	for i, r := range results {
		num := emojis[i]
		list.WriteString(fmt.Sprintf("%s  •  **%s** (%s) — %s", num, r.Title, r.Year, r.Category))
		if r.Rating != "" {
			list.WriteString(fmt.Sprintf("  |  ⭐ %s", r.Rating))
		}
		list.WriteString("\n")
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🎬 VOD Search Results",
		Description: fmt.Sprintf("Query: `%s`\n\nReact below to pick a number and start the download.", query),
		Color:       colorInfo,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Results",
				Value:  list.String(),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	msg, err := s.ChannelMessageSendEmbed(m.ChannelID, embed)
	if err != nil {
		utils.ErrorLog("Discord: failed to send VOD results embed: %v", err)
		return
	}

	// Map choices 1..N (10th is 0)
	choiceMap := make(map[int]types.VODResult, len(results))
	for i, r := range results {
		choiceMap[i+1] = r
	}

	b.pendingMsgLock.Lock()
	b.pendingVODByMsg[msg.ID] = &vodPendingContext{
		UserID:    m.Author.ID,
		ChannelID: m.ChannelID,
		Choices:   choiceMap,
	}
	b.pendingMsgLock.Unlock()

	limit := len(results)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		if err := s.MessageReactionAdd(msg.ChannelID, msg.ID, emojis[i]); err != nil {
			utils.WarnLog("Discord: failed to add reaction %s: %v", emojis[i], err)
		}
	}
}

// Reaction handler: user picks a result by reacting with a digit
func (b *Bot) messageReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	// Ignore bot reactions
	if r.UserID == s.State.User.ID {
		return
	}

	// Lookup pending context by message ID
	b.pendingMsgLock.RLock()
	ctx, ok := b.pendingVODByMsg[r.MessageID]
	b.pendingMsgLock.RUnlock()
	if !ok {
		return
	}

	// Only accept reactions from the requesting user
	if ctx.UserID != r.UserID {
		return
	}

	// Map emoji to selection index (1..10 where 10 is 0)
	emojiToIndex := map[string]int{
		"1️⃣": 1, "2️⃣": 2, "3️⃣": 3, "4️⃣": 4, "5️⃣": 5,
		"6️⃣": 6, "7️⃣": 7, "8️⃣": 8, "9️⃣": 9, "0️⃣": 10,
	}
	selection, ok := emojiToIndex[r.Emoji.Name]
	if !ok {
		return
	}

	// Resolve selected VOD
	selected, exists := ctx.Choices[selection]
	if !exists || selected.StreamID == "" {
		utils.WarnLog("Discord: invalid selection via reaction: %d", selection)
		return
	}

	// Consume this pending context (prevent duplicate processing)
	b.pendingMsgLock.Lock()
	delete(b.pendingVODByMsg, r.MessageID)
	b.pendingMsgLock.Unlock()

	// Proceed with download creation
	b.startVODDownloadFromSelection(s, ctx.ChannelID, r.UserID, selected)
}

// Starts VOD download for the given selection and informs the user
func (b *Bot) startVODDownloadFromSelection(s *discordgo.Session, channelID, userID string, selectedVOD types.VODResult) {
	// Get LDAP username for this Discord user
	success, respData, err := b.makeAPIRequest("GET", "/discord/"+userID+"/ldap", nil)
	if err != nil || !success {
		b.fail(channelID, "❌ Download Failed", "Failed to retrieve your user information. Please try again later.")
		return
	}

	data, ok := respData.(map[string]interface{})
	if !ok {
		b.fail(channelID, "❌ Download Failed", "Failed to process server response.")
		return
	}
	ldapUser, ok := data["ldap_user"].(string)
	if !ok || ldapUser == "" {
		b.warn(channelID, "🔗 Linking Required", "Your Discord account is not linked to an IPTV user.\n\nPlease link it first:\n`!link <ldap_username>`")
		return
	}

	// Send download request to API
	downloadData := map[string]string{
		"username":  ldapUser,
		"stream_id": selectedVOD.StreamID,
		"title":     selectedVOD.Title,
	}
	success, respData, err = b.makeAPIRequest("POST", "/vod/download", downloadData)
	if err != nil || !success {
		errMsg := "Failed to create download"
		if err != nil {
			errMsg += ": " + err.Error()
		} else if respData != nil {
			if errData, ok := respData.(map[string]interface{}); ok {
				if errStr, ok := errData["Error"].(string); ok {
					errMsg += ": " + errStr
				}
			}
		}
		b.fail(channelID, "❌ Download Failed", errMsg)
		return
	}

	// Process download response
	data, ok = respData.(map[string]interface{})
	if !ok {
		b.fail(channelID, "❌ Download Failed", "Failed to process download response.")
		return
	}
	downloadURL, ok := data["download_url"].(string)
	if !ok || downloadURL == "" {
		b.fail(channelID, "❌ Download Failed", "Failed to get download URL.")
		return
	}

	// Format expiration time if available
	var expirationInfo string
	if expiry, ok := data["expires_at"].(string); ok && strings.TrimSpace(expiry) != "" {
		expirationInfo = fmt.Sprintf("\nThis link will expire after %s", expiry)
	}

	// Send polished success embed
	desc := fmt.Sprintf("**%s**\n\nYour download is ready.", selectedVOD.Title)
	if expirationInfo != "" {
		desc += "\n" + expirationInfo
	}
	fields := []*discordgo.MessageEmbedField{
		{
			Name:   "Download Link",
			Value:  fmt.Sprintf("[Click here to download](%s)", downloadURL),
			Inline: false,
		},
	}
	b.success(channelID, "✅ Download Ready", desc, fields...)
}

// handleStatus shows the current streams and users
func (b *Bot) handleStatus(s *discordgo.Session, m *discordgo.MessageCreate, _ []string) {
	// Call the consolidated status endpoint
	success, respData, err := b.makeAPIRequest("GET", "/status", nil)
	if err != nil || !success {
		msg := "Failed to get status"
		if err != nil {
			msg += ": " + err.Error()
		}
		b.fail(m.ChannelID, "❌ Status Failed", msg)
		return
	}

	data, ok := respData.(map[string]interface{})
	if !ok {
		b.fail(m.ChannelID, "❌ Unexpected Response", "Failed to process status from server.")
		return
	}

	// Helpers for safe extraction
	intValue := func(key string) int {
		if v, ok := data[key]; ok {
			switch t := v.(type) {
			case float64:
				return int(t)
			case int:
				return t
			}
		}
		return 0
	}
	strValue := func(key string) string {
		if v, ok := data[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	streamsCount := intValue("streams_count")
	activeUsersCount := intValue("users_count_active")
	text := strings.TrimSpace(strValue("text"))

	desc := fmt.Sprintf("Active Streams: **%d**\nActive Users: **%d**", streamsCount, activeUsersCount)
	if text != "" {
		desc += fmt.Sprintf("\n\n%s", text)
	} else if streamsCount == 0 {
		desc += "\n\nNo active streams."
	}

	b.info(m.ChannelID, "📊 IPTV Proxy Status", desc)
}

// handleDisconnect forcibly disconnects a user
func (b *Bot) handleDisconnect(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if user has admin permissions
	if !b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		b.fail(m.ChannelID, "❌ Permission Denied", "You don't have permission to use this command.")
		return
	}

	if len(args) != 1 {
		b.info(m.ChannelID, "🔌 Disconnect User", "Usage: `!disconnect <username>`")
		return
	}

	username := args[0]
	success, _, err := b.makeAPIRequest("POST", "/users/disconnect/"+username, nil)
	if err != nil || !success {
		msg := "We couldn't disconnect this user."
		if err != nil {
			msg += fmt.Sprintf("\n\nError: `%s`", err.Error())
		}
		b.fail(m.ChannelID, "❌ Disconnect Failed", msg)
		return
	}

	b.success(m.ChannelID, "✅ User Disconnected",
		fmt.Sprintf("User **%s** has been disconnected.", username))
}

// handleTimeout temporarily blocks a user
func (b *Bot) handleTimeout(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if user has admin permissions
	if !b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		b.fail(m.ChannelID, "❌ Permission Denied", "You don't have permission to use this command.")
		return
	}

	if len(args) != 2 {
		b.info(m.ChannelID, "⏳ Timeout User", "Usage: `!timeout <username> <minutes>`")
		return
	}

	username := args[0]
	minutes := 0
	fmt.Sscanf(args[1], "%d", &minutes)
	if minutes <= 0 {
		b.warn(m.ChannelID, "⏳ Invalid Timeout",
			"Timeout minutes must be a positive number.")
		return
	}

	timeoutData := map[string]int{"minutes": minutes}
	success, _, err := b.makeAPIRequest("POST", "/users/timeout/"+username, timeoutData)
	if err != nil || !success {
		msg := "We couldn't set a timeout for this user."
		if err != nil {
			msg += fmt.Sprintf("\n\nError: `%s`", err.Error())
		}
		b.fail(m.ChannelID, "❌ Timeout Failed", msg)
		return
	}

	b.success(m.ChannelID, "✅ Timeout Applied",
		fmt.Sprintf("User **%s** has been timed out for **%d** minutes.", username, minutes))
}

// handleHelp shows the help message
func (b *Bot) handleHelp(s *discordgo.Session, m *discordgo.MessageCreate) {
	var cmd strings.Builder
	cmd.WriteString("**User Commands**\n")
	cmd.WriteString("• `!link <ldap_username>` — Link your Discord account.\n")
	cmd.WriteString("• `!vod <query>` — Search VOD; react 0–9 to choose.\n")
	cmd.WriteString("• `!status` — Show active streams and users.\n")
	cmd.WriteString("• `!help` — Show this help.\n\n")

	if b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		cmd.WriteString("**Admin Commands**\n")
		// status is available to all users now; do not list it here
		cmd.WriteString("• `!disconnect <username>` — Forcibly disconnect a user.\n")
		cmd.WriteString("• `!timeout <username> <minutes>` — Temporarily block a user.\n")
	}

	b.info(m.ChannelID, "🤖 IPTV Proxy Bot — Help", cmd.String())
}

// Embed styles and helpers
const (
	colorInfo    = 0x5BC0DE // teal-ish
	colorSuccess = 0x28A745 // green
	colorWarn    = 0xFFC107 // amber
	colorError   = 0xDC3545 // red
)

func (b *Bot) sendEmbed(channelID string, color int, title, description string, fields ...*discordgo.MessageEmbedField) error {
	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	if len(fields) > 0 {
		embed.Fields = make([]*discordgo.MessageEmbedField, 0, len(fields))
		for _, f := range fields {
			if f != nil {
				embed.Fields = append(embed.Fields, f)
			}
		}
	}
	_, err := b.session.ChannelMessageSendEmbed(channelID, embed)
	return err
}

func (b *Bot) info(channelID, title, desc string, fields ...*discordgo.MessageEmbedField) {
	if err := b.sendEmbed(channelID, colorInfo, title, desc, fields...); err != nil {
		utils.ErrorLog("Discord: failed to send info embed: %v", err)
	}
}
func (b *Bot) success(channelID, title, desc string, fields ...*discordgo.MessageEmbedField) {
	if err := b.sendEmbed(channelID, colorSuccess, title, desc, fields...); err != nil {
		utils.ErrorLog("Discord: failed to send success embed: %v", err)
	}
}
func (b *Bot) warn(channelID, title, desc string, fields ...*discordgo.MessageEmbedField) {
	if err := b.sendEmbed(channelID, colorWarn, title, desc, fields...); err != nil {
		utils.ErrorLog("Discord: failed to send warning embed: %v", err)
	}
}
func (b *Bot) fail(channelID, title, desc string, fields ...*discordgo.MessageEmbedField) {
	if err := b.sendEmbed(channelID, colorError, title, desc, fields...); err != nil {
		utils.ErrorLog("Discord: failed to send error embed: %v", err)
	}
}

// makeAPIRequest makes a request to the internal API
func (b *Bot) makeAPIRequest(method, endpoint string, body interface{}) (bool, interface{}, error) {
	url := b.apiURL + "/api/internal" + endpoint

	var reqBody []byte
	var err error

	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return false, nil, err
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return false, nil, err
	}

	// Add headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", b.apiKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()

	// Read and parse response
	var apiResp types.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return false, nil, err
	}

	// Check for success
	if !apiResp.Success {
		return false, apiResp.Data, fmt.Errorf(apiResp.Error)
	}

	return true, apiResp.Data, nil
}

// hasAdminRole checks if a user has the admin role
func (b *Bot) hasAdminRole(s *discordgo.Session, guildID, userID string) bool {
	// If no admin role is specified, assume no admin permissions
	if b.adminRoleID == "" {
		return false
	}

	// Get member info
	member, err := s.GuildMember(guildID, userID)
	if err != nil {
		utils.ErrorLog("Failed to get member info: %v", err)
		return false
	}

	// Check roles
	for _, roleID := range member.Roles {
		if roleID == b.adminRoleID {
			return true
		}
	}

	return false
}

// Helper function to safely get string values from maps
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}