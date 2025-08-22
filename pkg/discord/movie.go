package discord

import (
    "fmt"
    "strconv"
    "time"

    "github.com/bwmarrin/discordgo"
)

// Renders or updates the interactive VOD selection message.
func (b *Bot) renderVODInteractiveMessage(s *discordgo.Session, ctx *vodSelectContext) (*discordgo.Message, error) {
    total := len(ctx.Results)
    if ctx.PerPage <= 0 { ctx.PerPage = 100 }
    pages := (total + ctx.PerPage - 1) / ctx.PerPage
    if pages == 0 { pages = 1 }
    if ctx.Page < 0 { ctx.Page = 0 }
    if ctx.Page >= pages { ctx.Page = pages - 1 }

    start := ctx.Page * ctx.PerPage
    end := start + ctx.PerPage
    if end > total { end = total }

    // Single select (25 options max) + optional Prev/Next
    one := 1
    options := make([]discordgo.SelectMenuOption, 0, end-start)
    for i := start; i < end; i++ {
        r := ctx.Results[i]
        label := r.Title
        if r.Year != "" { label = fmt.Sprintf("%s (%s)", label, r.Year) }
        if len([]rune(label)) > 100 { label = string([]rune(label)[:97]) + "..." }
        value := strconv.Itoa(i)
        options = append(options, discordgo.SelectMenuOption{Label: label, Value: value})
    }
    placeholder := "Pick a title…"
    if pages > 1 { placeholder = fmt.Sprintf("Pick a title… (%d/%d)", ctx.Page+1, pages) }
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{ discordgo.SelectMenu{CustomID: "vod_select", Placeholder: placeholder, MinValues: &one, MaxValues: 1, Options: options} }},
    }
    if pages > 1 {
        components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Prev", CustomID: "vod_prev", Disabled: ctx.Page == 0},
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: "vod_next", Disabled: ctx.Page >= pages-1},
        }})
    }

    desc := fmt.Sprintf("Query: `%s` — %d result(s)%s\nUse the dropdown to choose.", ctx.Query, total, func() string { if pages>1 { return fmt.Sprintf(" — Page %d/%d", ctx.Page+1, pages) }; return "" }())
    embed := &discordgo.MessageEmbed{Title: "🎬 VOD Search Results", Description: desc, Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    msg, err := s.ChannelMessageSendComplex(ctx.Channel, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}, Components: components})
    if err != nil { return nil, err }
    return msg, nil
}

// Updates an existing message with a new page
func (b *Bot) updateVODInteractiveMessage(s *discordgo.Session, messageID string, ctx *vodSelectContext) error {
    total := len(ctx.Results)
    if ctx.PerPage <= 0 { ctx.PerPage = 100 }
    pages := (total + ctx.PerPage - 1) / ctx.PerPage
    if pages == 0 { pages = 1 }
    if ctx.Page < 0 { ctx.Page = 0 }
    if ctx.Page >= pages { ctx.Page = pages - 1 }

    start := ctx.Page * ctx.PerPage
    end := start + ctx.PerPage
    if end > total { end = total }

    // Single select (25 options max) + optional Prev/Next
    one := 1
    options := make([]discordgo.SelectMenuOption, 0, end-start)
    for i := start; i < end; i++ {
        r := ctx.Results[i]
        label := r.Title
        if r.Year != "" { label = fmt.Sprintf("%s (%s)", label, r.Year) }
        if len([]rune(label)) > 100 { label = string([]rune(label)[:97]) + "..." }
        value := strconv.Itoa(i)
        options = append(options, discordgo.SelectMenuOption{Label: label, Value: value})
    }
    placeholder := "Pick a title…"
    if pages > 1 { placeholder = fmt.Sprintf("Pick a title… (%d/%d)", ctx.Page+1, pages) }
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{ discordgo.SelectMenu{CustomID: "vod_select", Placeholder: placeholder, MinValues: &one, MaxValues: 1, Options: options} }},
    }
    if pages > 1 {
        components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Prev", CustomID: "vod_prev", Disabled: ctx.Page == 0},
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: "vod_next", Disabled: ctx.Page >= pages-1},
        }})
    }

    desc := fmt.Sprintf("Query: `%s` — %d result(s)%s\nUse the dropdown to choose.", ctx.Query, total, func() string { if pages>1 { return fmt.Sprintf(" — Page %d/%d", ctx.Page+1, pages) }; return "" }())
    embed := &discordgo.MessageEmbed{Title: "🎬 VOD Search Results", Description: desc, Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    embeds := []*discordgo.MessageEmbed{embed}
    _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: messageID, Channel: ctx.Channel, Embeds: &embeds, Components: &components})
    return err
}
