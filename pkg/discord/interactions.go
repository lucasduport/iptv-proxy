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
    case "vod_select":
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
    case "show_pick":
        userID := b.interactionUserID(i); if userID == "" { return }
        data := i.MessageComponentData(); if len(data.Values) == 0 { return }
        show := data.Values[0]
        b.showLock.Lock(); b.showState[msgID] = &showState{UserID: userID, SelectedShow: show, SelectedSeason: 0, EpisodePage: 0, PerPage: 25}; b.showLock.Unlock()
        if err := b.renderSeasonPicker(s, i.ChannelID, msgID, userID, show); err != nil { utils.WarnLog("Discord: failed to render season picker: %v", err) }
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
    case "season_pick":
        userID := b.interactionUserID(i); if userID == "" { return }
        data := i.MessageComponentData(); if len(data.Values) == 0 { return }
        season, _ := strconv.Atoi(data.Values[0])
        b.showLock.Lock(); st := b.showState[msgID]; if st == nil { st = &showState{UserID: userID, PerPage: 25}; b.showState[msgID] = st }
        if st.UserID != userID { b.showLock.Unlock(); return }
        st.SelectedSeason = season; st.EpisodePage = 0; show := st.SelectedShow; b.showLock.Unlock()
        if err := b.renderEpisodePicker(s, i.ChannelID, msgID, userID, show, season, 0, 25); err != nil { utils.WarnLog("Discord: failed to render episode picker: %v", err) }
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
    case "ep_prev", "ep_next":
        userID := b.interactionUserID(i); if userID == "" { return }
        b.showLock.Lock(); st := b.showState[msgID]; b.showLock.Unlock(); if st == nil || st.UserID != userID { return }
        delta := -1; if customID == "ep_next" { delta = 1 }
        b.showLock.Lock(); st.EpisodePage += delta; page := st.EpisodePage; season := st.SelectedSeason; show := st.SelectedShow; b.showLock.Unlock()
        if err := b.renderEpisodePicker(s, i.ChannelID, msgID, userID, show, season, page, 25); err != nil { utils.WarnLog("Discord: failed to update episode page: %v", err) }
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
    case "episode_pick":
        userID := b.interactionUserID(i); if userID == "" { return }
        b.showLock.RLock(); st := b.showState[msgID]; flow := b.showFlows[msgID]; b.showLock.RUnlock(); if st == nil || flow == nil || st.UserID != userID { return }
        data := i.MessageComponentData(); if len(data.Values) == 0 { return }
        idxWithinPage, _ := strconv.Atoi(data.Values[0])
        page := st.EpisodePage; start := page * 25
        episodes := flow.Data[st.SelectedShow][st.SelectedSeason]
        if start+idxWithinPage < 0 || start+idxWithinPage >= len(episodes) { return }
        selected := episodes[start+idxWithinPage]
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: fmt.Sprintf("Starting download for: %s S%02dE%02d — %s", st.SelectedShow, st.SelectedSeason, selected.Episode, selected.EpisodeTitle)}})
        go b.startVODDownloadFromSelection(s, i.ChannelID, userID, selected)
    }
}
