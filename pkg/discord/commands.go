package discord

import (
    "fmt"
    "strings"

    "github.com/bwmarrin/discordgo"
)

// messageCreate routes prefixed commands to their handlers.
func (b *Bot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
    if m.Author != nil && m.Author.Bot { return }

    content := strings.TrimSpace(m.Content)
    if m.GuildID != "" && content == "" {
        // Missing MESSAGE CONTENT INTENT
        return
    }
    if !strings.HasPrefix(content, b.prefix) { return }

    parts := strings.Fields(content[len(b.prefix):])
    if len(parts) == 0 { return }

    command := strings.ToLower(parts[0])
    args := parts[1:]

    switch command {
    case "link":
        b.handleLink(s, m, args)
    case "movie":
        b.handleMovie(s, m, args)
    case "show":
        b.handleShow(s, m, args)
    case "status":
        b.handleStatus(s, m, args)
    case "cache":
        b.handleCache(s, m, args)
    case "cached":
        b.handleCachedList(s, m)
    case "disconnect":
        b.handleDisconnect(s, m, args)
    case "timeout":
        b.handleTimeout(s, m, args)
    case "help":
        b.handleHelp(s, m)
    default:
        b.handleHelp(s, m)
    }
}

// handleHelp shows a concise help message.
func (b *Bot) handleHelp(s *discordgo.Session, m *discordgo.MessageCreate) {
    var cmd strings.Builder
    cmd.WriteString("**User Commands**\n")
    cmd.WriteString("• `!link <ldap_username>` — Link your Discord account.\n")
    cmd.WriteString("• `!movie <title>` — Search movies; use the dropdown to pick.\n")
    cmd.WriteString("• `!show <series>` — Pick a show, then season and episode easily.\n")
    cmd.WriteString("• `!cache <title> <days>` — Cache a movie or episode on the server for up to 14 days.\n")
    cmd.WriteString("• `!cached` — List cached items and when they expire.\n")
    cmd.WriteString("• `!status` — Show active streams and users.\n")
    cmd.WriteString("• `!help` — Show this help.\n\n")

    if b.hasAdminRole(s, m.GuildID, m.Author.ID) {
        cmd.WriteString("**Admin Commands**\n")
        cmd.WriteString("• `!disconnect <username>` — Forcibly disconnect a user.\n")
        cmd.WriteString("• `!timeout <username> <minutes>` — Temporarily block a user.\n")
    }

    b.info(m.ChannelID, "🤖 IPTV Proxy Bot — Help", cmd.String())
}

// handleStatus displays consolidated proxy status.
func (b *Bot) handleStatus(s *discordgo.Session, m *discordgo.MessageCreate, _ []string) {
    ok, data, err := b.makeAPIRequest("GET", "/status", nil)
    if err != nil || !ok { b.fail(m.ChannelID, "❌ Status Failed", fmt.Sprintf("Failed to get status: %v", err)); return }
    mp, _ := data.(map[string]interface{})
    streams := 0
    if v, ok := mp["streams_count"].(float64); ok { streams = int(v) }
    users := 0
    if v, ok := mp["users_count_active"].(float64); ok { users = int(v) }
    text := ""
    if sstr, ok := mp["text"].(string); ok { text = strings.TrimSpace(sstr) }
    desc := fmt.Sprintf("Active Streams: **%d**\nActive Users: **%d**", streams, users)
    if text != "" { desc += "\n\n" + text } else if streams == 0 { desc += "\n\nNo active streams." }
    b.info(m.ChannelID, "📊 IPTV Proxy Status", desc)
}
