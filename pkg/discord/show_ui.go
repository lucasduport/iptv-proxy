package discord

import (
    "fmt"
    "sort"
    "strconv"
    "strings"
    "time"

    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/types"
)

// setShowHierarchy stores the show -> seasons -> episodes mapping for a message.
func (b *Bot) setShowHierarchy(messageID string, raw map[string]map[int][]struct{ idx int; item types.VODResult }, order []string) {
    data := make(map[string]map[int][]types.VODResult, len(raw))
    for show, seasons := range raw {
        data[show] = make(map[int][]types.VODResult, len(seasons))
        for season, eps := range seasons {
            arr := make([]types.VODResult, 0, len(eps))
            for _, e := range eps { arr = append(arr, e.item) }
            sort.SliceStable(arr, func(i, j int) bool {
                if arr[i].Episode == arr[j].Episode { return arr[i].Title < arr[j].Title }
                return arr[i].Episode < arr[j].Episode
            })
            data[show][season] = arr
        }
    }
    b.showLock.Lock(); b.showFlows[messageID] = &showHierarchy{Order: order, Data: data}; b.showLock.Unlock()
}

// renderSeasonPicker updates the message with a season dropdown for a selected show.
func (b *Bot) renderSeasonPicker(s *discordgo.Session, channelID, messageID, userID, show string) error {
    b.showLock.RLock(); flow := b.showFlows[messageID]; b.showLock.RUnlock(); if flow == nil { return fmt.Errorf("no flow") }
    seasons := make([]int, 0, len(flow.Data[show]))
    for sn := range flow.Data[show] { seasons = append(seasons, sn) }
    sort.Ints(seasons)
    opts := make([]discordgo.SelectMenuOption, 0, len(seasons))
    for _, sn := range seasons { opts = append(opts, discordgo.SelectMenuOption{Label: fmt.Sprintf("Season %d", sn), Value: strconv.Itoa(sn)}) }
    one := 1
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{ discordgo.SelectMenu{CustomID: "season_pick", Placeholder: "Pick a season…", MinValues: &one, MaxValues: 1, Options: opts} }},
    }
    embed := &discordgo.MessageEmbed{Title: "📺 Pick a Season", Description: fmt.Sprintf("Show: **%s**", show), Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: messageID, Channel: channelID, Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &components})
    return err
}

// renderEpisodePicker updates the message with an episode dropdown and prev/next buttons.
func (b *Bot) renderEpisodePicker(s *discordgo.Session, channelID, messageID, userID, show string, season int, page, perPage int) error {
    b.showLock.RLock(); flow := b.showFlows[messageID]; b.showLock.RUnlock(); if flow == nil { return fmt.Errorf("no flow") }
    episodes := flow.Data[show][season]
    total := len(episodes)
    if perPage <= 0 { perPage = 25 }
    pages := (total + perPage - 1) / perPage; if pages == 0 { pages = 1 }
    if page < 0 { page = 0 }
    if page >= pages { page = pages - 1 }
    start := page * perPage
    end := start + perPage; if end > total { end = total }

    opts := make([]discordgo.SelectMenuOption, 0, end-start)
    for i := start; i < end; i++ {
        ep := episodes[i]
        epName := ep.EpisodeTitle
        if strings.TrimSpace(epName) == "" { epName = ep.Title }
        label := fmt.Sprintf("S%02dE%02d — %s", season, ep.Episode, epName)
        if len([]rune(label)) > 100 { label = string([]rune(label)[:97]) + "..." }
        opts = append(opts, discordgo.SelectMenuOption{Label: label, Value: strconv.Itoa(i)})
    }

    one := 1
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{ discordgo.SelectMenu{CustomID: "episode_pick", Placeholder: "Pick an episode…", MinValues: &one, MaxValues: 1, Options: opts} }},
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{ discordgo.Button{Style: discordgo.SecondaryButton, Label: "Prev", CustomID: "ep_prev", Disabled: page == 0}, discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: "ep_next", Disabled: page >= pages-1} }},
    }
    embed := &discordgo.MessageEmbed{Title: "📺 Pick an Episode", Description: fmt.Sprintf("Show: **%s** — Season %d — Page %d/%d", show, season, page+1, pages), Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: messageID, Channel: channelID, Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &components})
    return err
}
