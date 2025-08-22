package discord

import (
    "fmt"
    "strconv"
    "strings"
    "time"

    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/types"
    "github.com/lucasduport/stream-share/pkg/utils"
)

// handleMovie handles the !movie command (search movies only)
func (b *Bot) handleMovie(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
    query := strings.TrimSpace(strings.Join(args, " "))
    if query == "" {
        b.info(m.ChannelID, "🎬 Movie Search",
            "Usage: `!movie <title>`\n\nExample: `!movie The Matrix`")
        return
    }

    // Send a loading embed while processing the search
    loading, _ := s.ChannelMessageSendEmbed(m.ChannelID, &discordgo.MessageEmbed{
        Title:       "🔎 Searching…",
        Description: fmt.Sprintf("Looking for movies matching `%s`", query),
        Color:       colorInfo,
        Timestamp:   time.Now().UTC().Format(time.RFC3339),
    })

    // Resolve LDAP user for this Discord user
    success, respData, err := b.makeAPIRequest("GET", "/discord/"+m.Author.ID+"/ldap", nil)
    if err != nil || !success {
    // Update the loading message to a warning and return
    _ = editEmbed(s, loading, colorWarn, "🔗 Linking Required",
            "Your Discord account is not linked to an IPTV user.\n\nPlease link it first:\n`!link <ldap_username>`")
        return
    }

    data, ok := respData.(map[string]interface{})
    if !ok {
        _ = editEmbed(s, loading, colorError, "❌ Unexpected Response",
            "Failed to process the server response. Please try again later.")
        return
    }
    ldapUser, ok := data["ldap_user"].(string)
    if !ok || ldapUser == "" {
        _ = editEmbed(s, loading, colorWarn, "🔗 Linking Required",
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
        if err != nil { msg += fmt.Sprintf("\n\nError: `%s`", err.Error()) }
        _ = editEmbed(s, loading, colorError, "❌ Search Failed", msg)
        return
    }

    data, ok = respData.(map[string]interface{})
    if !ok {
        _ = editEmbed(s, loading, colorError, "❌ Unexpected Response",
            "Failed to process the search results. Please try again later.")
        return
    }

    resultsData, ok := data["results"].([]interface{})
    if !ok || len(resultsData) == 0 {
        _ = editEmbed(s, loading, colorInfo, "🔎 No Results",
            fmt.Sprintf("No results found for `%s`.\nTry a different title or spelling.", query))
        return
    }

    // Convert and filter to movies only
    var results []types.VODResult
    for _, result := range resultsData {
        if resultMap, ok := result.(map[string]interface{}); ok {
            vr := types.VODResult{
                ID:         getString(resultMap, "ID"),
                Title:      getString(resultMap, "Title"),
                Category:   getString(resultMap, "Category"),
                Duration:   getString(resultMap, "Duration"),
                Year:       getString(resultMap, "Year"),
                Rating:     getString(resultMap, "Rating"),
                StreamID:   getString(resultMap, "StreamID"),
                Size:       getString(resultMap, "Size"),
                StreamType: getString(resultMap, "StreamType"),
            }
            if strings.ToLower(vr.StreamType) == "movie" || vr.StreamType == "" {
                results = append(results, vr)
            }
        }
    }
    if len(results) == 0 {
        b.info(m.ChannelID, "🎬 No Movies Found",
            fmt.Sprintf("No movies found for `%s`. Try refining your query.", query))
        return
    }

    // Create a single interactive message with dropdown + Prev/Next buttons
    ctx := &vodSelectContext{
        UserID:  m.Author.ID,
        Channel: m.ChannelID,
        Query:   query,
        Results: results,
        Page:    0,
        PerPage: 25,
        Created: time.Now(),
    }

    // Replace loading message with the interactive components
    total := len(ctx.Results)
    pages := (total + ctx.PerPage - 1) / ctx.PerPage
    if pages == 0 { pages = 1 }
    if ctx.Page < 0 { ctx.Page = 0 }
    if ctx.Page >= pages { ctx.Page = pages - 1 }

    start := ctx.Page * ctx.PerPage
    end := start + ctx.PerPage
    if end > total { end = total }

    options := make([]discordgo.SelectMenuOption, 0, end-start)
    for i := start; i < end; i++ {
        r := ctx.Results[i]
        label := r.Title
        if r.Year != "" { label = fmt.Sprintf("%s (%s)", label, r.Year) }
        if len([]rune(label)) > 100 { label = string([]rune(label)[:97]) + "..." }
        value := strconv.Itoa(i)
        descParts := []string{}
        if r.Category != "" { descParts = append(descParts, r.Category) }
        if r.Size != "" { descParts = append(descParts, r.Size) }
        if r.Rating != "" { descParts = append(descParts, "⭐ "+r.Rating) }
        optDesc := strings.Join(descParts, "  •  ")
        if len([]rune(optDesc)) > 100 { optDesc = string([]rune(optDesc)[:97]) + "..." }
        options = append(options, discordgo.SelectMenuOption{Label: label, Value: value, Description: optDesc})
    }
    one := 1
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.SelectMenu{CustomID: "vod_select", Placeholder: "Pick a title…", MinValues: &one, MaxValues: 1, Options: options},
        }},
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Prev", CustomID: "vod_prev", Disabled: ctx.Page == 0},
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: "vod_next", Disabled: ctx.Page >= pages-1},
        }},
    }
    desc := fmt.Sprintf("Query: `%s` — %d result(s) — Page %d/%d\nUse the dropdown to choose.", ctx.Query, total, ctx.Page+1, pages)
    embed := &discordgo.MessageEmbed{Title: "🎬 VOD Search Results", Description: desc, Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    embeds := []*discordgo.MessageEmbed{embed}
    if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: loading.ID, Channel: m.ChannelID, Embeds: &embeds, Components: &components}); err != nil {
        // Fallback: send a new message
        msg, err2 := b.renderVODInteractiveMessage(s, ctx)
        if err2 != nil {
            utils.ErrorLog("Discord: failed to send interactive VOD message: %v", err2)
            _ = editEmbed(s, loading, colorError, "❌ Search Failed", "Couldn't render results. Please try again.")
            return
        }
        b.selectLock.Lock(); b.pendingVODSelect[msg.ID] = ctx; b.selectLock.Unlock()
    } else {
        b.selectLock.Lock(); b.pendingVODSelect[loading.ID] = ctx; b.selectLock.Unlock()
    }
}

// Renders or updates the interactive VOD selection message.
func (b *Bot) renderVODInteractiveMessage(s *discordgo.Session, ctx *vodSelectContext) (*discordgo.Message, error) {
    total := len(ctx.Results)
    pages := (total + ctx.PerPage - 1) / ctx.PerPage
    if pages == 0 { pages = 1 }
    if ctx.Page < 0 { ctx.Page = 0 }
    if ctx.Page >= pages { ctx.Page = pages - 1 }

    start := ctx.Page * ctx.PerPage
    end := start + ctx.PerPage
    if end > total { end = total }

    desc := fmt.Sprintf("Query: `%s` — %d result(s) — Page %d/%d\nUse the dropdown to choose.", ctx.Query, total, ctx.Page+1, pages)

    options := make([]discordgo.SelectMenuOption, 0, end-start)
    for i := start; i < end; i++ {
        r := ctx.Results[i]
        label := r.Title
        if r.Year != "" {
            label = fmt.Sprintf("%s (%s)", label, r.Year)
        }
        if len([]rune(label)) > 100 {
            labelRunes := []rune(label)
            label = string(labelRunes[:97]) + "..."
        }
        value := strconv.Itoa(i)
        descParts := []string{}
        if r.Category != "" { descParts = append(descParts, r.Category) }
        if r.Size != "" { descParts = append(descParts, r.Size) }
        if r.Rating != "" { descParts = append(descParts, "⭐ "+r.Rating) }
        optDesc := strings.Join(descParts, "  •  ")
        if len([]rune(optDesc)) > 100 {
            optRunes := []rune(optDesc)
            optDesc = string(optRunes[:97]) + "..."
        }
        options = append(options, discordgo.SelectMenuOption{Label: label, Value: value, Description: optDesc})
    }

    one := 1
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.SelectMenu{CustomID: "vod_select", Placeholder: "Pick a title…", MinValues: &one, MaxValues: 1, Options: options},
        }},
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Prev", CustomID: "vod_prev", Disabled: ctx.Page == 0},
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: "vod_next", Disabled: ctx.Page >= pages-1},
        }},
    }

    embed := &discordgo.MessageEmbed{Title: "🎬 VOD Search Results", Description: desc, Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    msg, err := s.ChannelMessageSendComplex(ctx.Channel, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}, Components: components})
    if err != nil {
        return nil, err
    }
    return msg, nil
}

// Updates an existing message with a new page
func (b *Bot) updateVODInteractiveMessage(s *discordgo.Session, messageID string, ctx *vodSelectContext) error {
    total := len(ctx.Results)
    pages := (total + ctx.PerPage - 1) / ctx.PerPage
    if pages == 0 { pages = 1 }
    if ctx.Page < 0 { ctx.Page = 0 }
    if ctx.Page >= pages { ctx.Page = pages - 1 }

    start := ctx.Page * ctx.PerPage
    end := start + ctx.PerPage
    if end > total { end = total }

    options := make([]discordgo.SelectMenuOption, 0, end-start)
    for i := start; i < end; i++ {
        r := ctx.Results[i]
        label := r.Title
        if r.Year != "" { label = fmt.Sprintf("%s (%s)", label, r.Year) }
        if len([]rune(label)) > 100 { label = string([]rune(label)[:97]) + "..." }
        value := strconv.Itoa(i)
        descParts := []string{}
        if r.Category != "" { descParts = append(descParts, r.Category) }
        if r.Size != "" { descParts = append(descParts, r.Size) }
        if r.Rating != "" { descParts = append(descParts, "⭐ "+r.Rating) }
        optDesc := strings.Join(descParts, "  •  ")
        if len([]rune(optDesc)) > 100 { optDesc = string([]rune(optDesc)[:97]) + "..." }
        options = append(options, discordgo.SelectMenuOption{Label: label, Value: value, Description: optDesc})
    }

    one := 1
    components := []discordgo.MessageComponent{
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.SelectMenu{CustomID: "vod_select", Placeholder: "Pick a title…", MinValues: &one, MaxValues: 1, Options: options},
        }},
        discordgo.ActionsRow{Components: []discordgo.MessageComponent{
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Prev", CustomID: "vod_prev", Disabled: ctx.Page == 0},
            discordgo.Button{Style: discordgo.SecondaryButton, Label: "Next", CustomID: "vod_next", Disabled: ctx.Page >= pages-1},
        }},
    }

    desc := fmt.Sprintf("Query: `%s` — %d result(s) — Page %d/%d\nUse the dropdown to choose.", ctx.Query, total, ctx.Page+1, pages)
    embed := &discordgo.MessageEmbed{Title: "🎬 VOD Search Results", Description: desc, Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    embeds := []*discordgo.MessageEmbed{embed}
    _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: messageID, Channel: ctx.Channel, Embeds: &embeds, Components: &components})
    return err
}
