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
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
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
	Choices   map[int]types.VODResult // 1..10 (0️⃣ represents 10)
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
		if s != nil && s.State != nil && s.State.User != nil {
			utils.InfoLog("Discord ready: logged in as %s#%s (ID: %s), prefix='%s'",
				s.State.User.Username, s.State.User.Discriminator, s.State.User.ID, bot.prefix)
		} else {
			utils.InfoLog("Discord ready: session state not populated yet, prefix='%s'", bot.prefix)
		}
		utils.InfoLog("Reminder: Ensure 'MESSAGE CONTENT INTENT' is enabled in the Discord Developer Portal")
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
	utils.InfoLog("Starting Discord bot with intents: Guilds, GuildMessages, DirectMessages, MessageContent")
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
		// Changed usage (now expects LDAP username)
		s.ChannelMessageSend(m.ChannelID, "Usage: !link <ldap_username>") // nolint: errcheck
		return
	}

	ldapUser := strings.TrimSpace(args[0])
	if ldapUser == "" {
		s.ChannelMessageSend(m.ChannelID, "Usage: !link <ldap_username>") // nolint: errcheck
		return
	}

	// Send link request to API (token is optional on server side)
	linkData := map[string]interface{}{
		"discord_id":   m.Author.ID,
		"discord_name": m.Author.Username,
		"ldap_user":    ldapUser,
	}

	success, resp, err := b.makeAPIRequest("POST", "/discord/link", linkData)
	if err != nil || !success {
		errMsg := "Failed to link your account"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		s.ChannelMessageSend(m.ChannelID, errMsg) // nolint: errcheck
		return
	}

	// Extract the LDAP username from response if available
	var confirmed string
	if data, ok := resp.(map[string]interface{}); ok {
		if u, exists := data["ldap_user"]; exists {
			confirmed = fmt.Sprintf("%v", u)
		}
	}
	if confirmed == "" {
		confirmed = ldapUser
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Successfully linked your Discord account to user %s", confirmed)) // nolint: errcheck
}

// handleVOD handles the !vod command to search for VOD content
func (b *Bot) handleVOD(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Remove "reply with number" flow; use reactions instead

	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		s.ChannelMessageSend(m.ChannelID, "Usage: !vod <search query>") // nolint: errcheck
		return
	}

	// First, get the LDAP username for this Discord user
	success, respData, err := b.makeAPIRequest("GET", "/discord/"+m.Author.ID+"/ldap", nil)
	if err != nil || !success {
		s.ChannelMessageSend(m.ChannelID, "Your Discord account is not linked to an LDAP user. Please use !link <ldap_username> first.") // nolint: errcheck
		return
	}

	// Extract LDAP username
	data, ok := respData.(map[string]interface{})
	if !ok {
		s.ChannelMessageSend(m.ChannelID, "Failed to process server response.") // nolint: errcheck
		return
	}
	ldapUser, ok := data["ldap_user"].(string)
	if !ok || ldapUser == "" {
		s.ChannelMessageSend(m.ChannelID, "Your Discord account is not linked to an LDAP user. Please use !link <ldap_username> first.") // nolint: errcheck
		return
	}

	// Send search request to API
	searchData := map[string]string{
		"username": ldapUser,
		"query":    query,
	}
	success, respData, err = b.makeAPIRequest("POST", "/vod/search", searchData)
	if err != nil || !success {
		errMsg := "Failed to search for VOD content"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		s.ChannelMessageSend(m.ChannelID, errMsg) // nolint: errcheck
		return
	}

	// Process search results
	data, ok = respData.(map[string]interface{})
	if !ok {
		s.ChannelMessageSend(m.ChannelID, "Failed to process search results.") // nolint: errcheck
		return
	}

	resultsData, ok := data["results"].([]interface{})
	if !ok || len(resultsData) == 0 {
		s.ChannelMessageSend(m.ChannelID, "No results found for your search.") // nolint: errcheck
		return
	}

	// Convert results and limit to 10 choices
	var results []types.VODResult
	for _, result := range resultsData {
		if len(results) >= 10 {
			break
		}
		resultMap, ok := result.(map[string]interface{})
		if !ok {
			continue
		}
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

	// Build display message
	var sb strings.Builder
	sb.WriteString("Search results:\n")
	for i, result := range results {
		num := i + 1
		displayNum := fmt.Sprintf("%d", num)
		if num == 10 {
			displayNum = "0"
		}
		sb.WriteString(fmt.Sprintf("%s) %s (%s) - %s | Rating: %s\n",
			displayNum, result.Title, result.Year, result.Category, result.Rating))
	}
	sb.WriteString("\nAdd a reaction on this message using the corresponding number (0-9).")

	// Send message and add reactions 1-9 and 0 (for 10th)
	msg, err := s.ChannelMessageSend(m.ChannelID, sb.String())
	if err != nil {
		utils.ErrorLog("Discord: failed to send VOD results: %v", err)
		return
	}

	// Map choices 1..N (10th is 0)
	choiceMap := make(map[int]types.VODResult, len(results))
	for i, r := range results {
		choiceMap[i+1] = r
	}

	// Store pending context keyed by message ID
	b.pendingMsgLock.Lock()
	b.pendingVODByMsg[msg.ID] = &vodPendingContext{
		UserID:    m.Author.ID,
		ChannelID: m.ChannelID,
		Choices:   choiceMap,
	}
	b.pendingMsgLock.Unlock()

	// React with emojis corresponding to available options
	emojis := []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "0️⃣"}
	limit := len(results)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		emoji := emojis[i]
		// FIX: MessageReactionAdd returns only error
		if err := s.MessageReactionAdd(msg.ChannelID, msg.ID, emoji); err != nil {
			utils.WarnLog("Discord: failed to add reaction %s: %v", emoji, err)
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
		s.ChannelMessageSend(channelID, "Failed to retrieve your user information. Please try again later.") // nolint: errcheck
		return
	}

	data, ok := respData.(map[string]interface{})
	if !ok {
		s.ChannelMessageSend(channelID, "Failed to process server response.") // nolint: errcheck
		return
	}
	ldapUser, ok := data["ldap_user"].(string)
	if !ok || ldapUser == "" {
		s.ChannelMessageSend(channelID, "Your Discord account is not linked to an LDAP user. Please use !link <ldap_username> first.") // nolint: errcheck
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
		s.ChannelMessageSend(channelID, errMsg) // nolint: errcheck
		return
	}

	// Process download response
	data, ok = respData.(map[string]interface{})
	if !ok {
		s.ChannelMessageSend(channelID, "Failed to process download response.") // nolint: errcheck
		return
	}
	downloadURL, ok := data["download_url"].(string)
	if !ok || downloadURL == "" {
		s.ChannelMessageSend(channelID, "Failed to get download URL.") // nolint: errcheck
		return
	}

	// Format expiration time if available
	var expirationInfo string
	if expiry, ok := data["expires_at"].(string); ok {
		expirationInfo = fmt.Sprintf("\n\nThis link will expire after %s", expiry)
	}

	// Send download link to user
	s.ChannelMessageSend(channelID, fmt.Sprintf(
		"Your download for %s is ready!\n\nDownload link: %s%s",
		selectedVOD.Title, downloadURL, expirationInfo)) // nolint: errcheck
}

// handleStatus shows the current streams and users
func (b *Bot) handleStatus(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if user has admin permissions
	if !b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	// Get all active streams
	success, respData, err := b.makeAPIRequest("GET", "/streams", nil)
	if err != nil || !success {
		errMsg := "Failed to get stream status"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		s.ChannelMessageSend(m.ChannelID, errMsg)
		return
	}

	// Parse streams data
	var streams []interface{}
	if data, ok := respData.(map[string]interface{}); ok {
		if streamsData, ok := data["data"].([]interface{}); ok {
			streams = streamsData
		}
	}

	// Get all active users
	success, respData, err = b.makeAPIRequest("GET", "/users", nil)
	if err != nil || !success {
		errMsg := "Failed to get user status"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		s.ChannelMessageSend(m.ChannelID, errMsg)
		return
	}

	// Parse users data
	var users []interface{}
	if data, ok := respData.(map[string]interface{}); ok {
		if usersData, ok := data["data"].([]interface{}); ok {
			users = usersData
		}
	}

	// Format status message
	var sb strings.Builder
	sb.WriteString("**IPTV Proxy Status**\n\n")

	// Format streams info
	sb.WriteString(fmt.Sprintf("**Active Streams:** %d\n\n", len(streams)))
	for i, stream := range streams {
		streamMap, ok := stream.(map[string]interface{})
		if !ok {
			continue
		}

		streamID := getString(streamMap, "StreamID")
		streamTitle := getString(streamMap, "StreamTitle")
		streamType := getString(streamMap, "StreamType")

		// Format viewers
		var viewerCount int
		var viewersList string
		if viewers, ok := streamMap["Viewers"].(map[string]interface{}); ok {
			viewerCount = len(viewers)
			if viewerCount > 0 {
				var viewerNames []string
				for viewer := range viewers {
					viewerNames = append(viewerNames, viewer)
				}
				viewersList = strings.Join(viewerNames, ", ")
			}
		}

		sb.WriteString(fmt.Sprintf("%d. **%s** (%s type: %s)\n", i+1, streamTitle, streamType, streamID))
		sb.WriteString(fmt.Sprintf("   Viewers (%d): %s\n\n", viewerCount, viewersList))
	}

	// Format users info
	sb.WriteString(fmt.Sprintf("\n**Active Users:** %d\n\n", len(users)))
	for i, user := range users {
		userMap, ok := user.(map[string]interface{})
		if !ok {
			continue
		}

		username := getString(userMap, "Username")
		streamID := getString(userMap, "StreamID")

		var streamInfo string
		if streamID != "" {
			streamInfo = fmt.Sprintf(" - Watching: %s", streamID)
		} else {
			streamInfo = " - Not streaming"
		}

		sb.WriteString(fmt.Sprintf("%d. **%s**%s\n", i+1, username, streamInfo))
	}

	// Send status message, splitting if needed
	statusMessage := sb.String()

	// Discord has a message length limit of 2000 chars
	if len(statusMessage) > 1900 {
		// Split into multiple messages
		for i := 0; i < len(statusMessage); i += 1900 {
			end := i + 1900
			if end > len(statusMessage) {
				end = len(statusMessage)
			}
			s.ChannelMessageSend(m.ChannelID, statusMessage[i:end])
		}
	} else {
		s.ChannelMessageSend(m.ChannelID, statusMessage)
	}
}

// handleDisconnect forcibly disconnects a user
func (b *Bot) handleDisconnect(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if user has admin permissions
	if !b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	if len(args) != 1 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !disconnect <username>")
		return
	}

	username := args[0]

	// Send disconnect request to API
	success, _, err := b.makeAPIRequest("POST", "/users/disconnect/"+username, nil)
	if err != nil || !success {
		errMsg := "Failed to disconnect user"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		s.ChannelMessageSend(m.ChannelID, errMsg)
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("User **%s** has been disconnected.", username))
}

// handleTimeout temporarily blocks a user
func (b *Bot) handleTimeout(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	// Check if user has admin permissions
	if !b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		s.ChannelMessageSend(m.ChannelID, "You don't have permission to use this command.")
		return
	}

	if len(args) != 2 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !timeout <username> <minutes>")
		return
	}

	username := args[0]
	minutes := 0
	fmt.Sscanf(args[1], "%d", &minutes)

	if minutes <= 0 {
		s.ChannelMessageSend(m.ChannelID, "Timeout minutes must be a positive number.")
		return
	}

	// Send timeout request to API
	timeoutData := map[string]int{
		"minutes": minutes,
	}

	success, _, err := b.makeAPIRequest("POST", "/users/timeout/"+username, timeoutData)
	if err != nil || !success {
		errMsg := "Failed to timeout user"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		s.ChannelMessageSend(m.ChannelID, errMsg)
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("User **%s** has been timed out for %d minutes.", username, minutes))
}

// handleHelp shows the help message
func (b *Bot) handleHelp(s *discordgo.Session, m *discordgo.MessageCreate) {
	var sb strings.Builder
	sb.WriteString("**IPTV Proxy Bot Commands**\n\n")

	// Regular user commands
	sb.WriteString("**User Commands:**\n")
	sb.WriteString("`!link <ldap_username>` - Link your Discord account to your IPTV account\n")
	sb.WriteString("`!vod <search query>` - Search for VOD; react 0-9 on the results message to choose\n")
	sb.WriteString("`!help` - Show this help message\n\n")

	// Admin commands if user has admin role
	if b.hasAdminRole(s, m.GuildID, m.Author.ID) {
		sb.WriteString("**Admin Commands:**\n")
		sb.WriteString("`!status` - Show active streams and users\n")
		sb.WriteString("`!disconnect <username>` - Forcibly disconnect a user\n")
		sb.WriteString("`!timeout <username> <minutes>` - Temporarily block a user\n")
	}

	if _, err := s.ChannelMessageSend(m.ChannelID, sb.String()); err != nil {
		utils.ErrorLog("Discord: failed to send help message: %v", err)
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