package discord

import (
    "fmt"
    "strconv"
    "strings"

    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/utils"
)

// handleInteractionCreate processes all component interactions (dropdowns, buttons).
func (b *Bot) handleInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
    if i.Type != discordgo.InteractionMessageComponent { return }

    msgID := i.Message.ID
    customID := i.MessageComponentData().CustomID
    switch customID {
    case "vod_prev":
        b.selectLock.RLock(); ctx, ok := b.pendingVODSelect[msgID]; b.selectLock.RUnlock(); if !ok { return }
        if !b.isSameUser(ctx.UserID, i) { return }
        ctx.Page--; if ctx.Page < 0 { ctx.Page = 0 }
        if err := b.updateVODInteractiveMessage(s, msgID, ctx); err != nil { utils.WarnLog("Discord: failed to update VOD message (prev): %v", err) }
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
    case "vod_next":
        b.selectLock.RLock(); ctx, ok := b.pendingVODSelect[msgID]; b.selectLock.RUnlock(); if !ok { return }
        if !b.isSameUser(ctx.UserID, i) { return }
        ctx.Page++
        if err := b.updateVODInteractiveMessage(s, msgID, ctx); err != nil { utils.WarnLog("Discord: failed to update VOD message (next): %v", err) }
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
    default:
        // Single select component
        if customID != "vod_select" { return }
        b.selectLock.RLock(); ctx, ok := b.pendingVODSelect[msgID]; b.selectLock.RUnlock(); if !ok { return }
        if !b.isSameUser(ctx.UserID, i) { return }
        data := i.MessageComponentData(); if len(data.Values) == 0 { return }
        idx, err := strconv.Atoi(data.Values[0]); if err != nil || idx < 0 || idx >= len(ctx.Results) { return }
        selected := ctx.Results[idx]
        if strings.HasPrefix(ctx.Query, "cache:") {
            days := 1
            if p := strings.LastIndex(ctx.Query, "for "); p != -1 {
                var n int
                fmt.Sscanf(ctx.Query[p:], "for %dd", &n)
                if n > 0 { days = n }
            }
            _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: fmt.Sprintf("Caching: %s (days=%d)", selected.Title, days)}})
            go b.startVODCacheFromSelection(s, ctx.Channel, ctx.UserID, selected, days)
        } else {
            _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: fmt.Sprintf("Starting download for: %s", selected.Title)}})
            go b.startVODDownloadFromSelection(s, ctx.Channel, ctx.UserID, selected)
        }
    }
}
