package discord

import (
    "fmt"
    "strings"
    "time"

    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/types"
    "github.com/lucasduport/stream-share/pkg/utils"
)

// (movie-related types and functions moved to movie.go)

// handleShow handles the !show command. It provides a hierarchical flow:
// 1) Search shows (series titles only)
// 2) Pick a show -> list seasons
// 3) Pick a season -> list sorted episodes
// 4) Pick an episode -> start download
func (b *Bot) handleShow(s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		b.info(m.ChannelID, "📺 Show Search",
			"Usage: `!show <series>`\n\nExample: `!show Game of Thrones`")
		return
	}

    // Show a loading embed while searching
    loading, _ := s.ChannelMessageSendEmbed(m.ChannelID, &discordgo.MessageEmbed{
        Title:       "🔎 Searching…",
        Description: fmt.Sprintf("Looking for shows matching `%s`", query),
        Color:       colorInfo,
        Timestamp:   time.Now().UTC().Format(time.RFC3339),
    })

	// Resolve LDAP user for this Discord user
	success, respData, err := b.makeAPIRequest("GET", "/discord/"+m.Author.ID+"/ldap", nil)
    if err != nil || !success {
        _ = editEmbed(s, loading, colorWarn, "🔗 Linking Required",
            "Your Discord account is not linked to an IPTV user.\n\nPlease link it first:\n`!link <ldap_username>`")
        return
    }
	data, ok := respData.(map[string]interface{})
    if !ok { _ = editEmbed(s, loading, colorError, "❌ Unexpected Response", "Failed to process the server response."); return }
	ldapUser, ok := data["ldap_user"].(string)
    if !ok || ldapUser == "" { _ = editEmbed(s, loading, colorWarn, "🔗 Linking Required", "Link your account with `!link <ldap_username>`. "); return }

	// Use existing search API to fetch series results (it returns flattened episodes too)
	searchData := map[string]string{"username": ldapUser, "query": query}
    success, respData, err = b.makeAPIRequest("POST", "/vod/search", searchData)
    if err != nil || !success { _ = editEmbed(s, loading, colorError, "❌ Search Failed", "Couldn't complete your search."); return }
	data, ok = respData.(map[string]interface{})
    if !ok { _ = editEmbed(s, loading, colorError, "❌ Unexpected Response", "Failed to process the search results."); return }
	resultsData, ok := data["results"].([]interface{})
    if !ok || len(resultsData) == 0 { _ = editEmbed(s, loading, colorInfo, "🔎 No Results", fmt.Sprintf("No shows found for `%s`.", query)); return }

	// Build a map: show title -> seasons -> episodes, based on series results
	showMap := map[string]map[int][]struct{ idx int; item types.VODResult }{}
	// collect distinct shows preserving natural order
	showOrder := []string{}
	for idx, r := range resultsData {
		if rm, ok := r.(map[string]interface{}); ok {
			vr := types.VODResult{
				ID:          getString(rm, "ID"),
				Title:       getString(rm, "Title"),
				Category:    getString(rm, "Category"),
				Duration:    getString(rm, "Duration"),
				Year:        getString(rm, "Year"),
				Rating:      getString(rm, "Rating"),
				StreamID:    getString(rm, "StreamID"),
				Size:        getString(rm, "Size"),
				StreamType:  getString(rm, "StreamType"),
				SeriesTitle: getString(rm, "SeriesTitle"),
			}
			// parse season/episode if provided as numbers
			if v, ok := rm["Season"]; ok {
				switch t := v.(type) { case float64: vr.Season = int(t) }
			}
			if v, ok := rm["Episode"]; ok {
				switch t := v.(type) { case float64: vr.Episode = int(t) }
			}
			if strings.ToLower(vr.StreamType) != "series" { continue }
			show := vr.SeriesTitle
			if show == "" {
				// try to extract series title from Title like "Name S01E02 — ..."
				show = strings.TrimSpace(strings.Split(vr.Title, " S")[0])
			}
			if show == "" { continue }
			if _, ok := showMap[show]; !ok { showMap[show] = map[int][]struct{ idx int; item types.VODResult }{}; showOrder = append(showOrder, show) }
			showMap[show][vr.Season] = append(showMap[show][vr.Season], struct{ idx int; item types.VODResult }{idx: idx, item: vr})
		}
	}
	if len(showOrder) == 0 { b.info(m.ChannelID, "📺 No Shows Found", fmt.Sprintf("No shows found for `%s`.", query)); return }

	// Create a select of shows (first 25)
	opts := make([]discordgo.SelectMenuOption, 0, len(showOrder))
	max := len(showOrder)
	if max > 25 { max = 25 }
	for i := 0; i < max; i++ {
		show := showOrder[i]
		opts = append(opts, discordgo.SelectMenuOption{Label: show, Value: show})
	}
	one := 1
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: "show_pick", Placeholder: "Pick a show…", MinValues: &one, MaxValues: 1, Options: opts},
		}},
	}
    embed := &discordgo.MessageEmbed{Title: "📺 Pick a Show", Description: fmt.Sprintf("Query: `%s` — %d match(es)", query, len(showOrder)), Color: colorInfo, Timestamp: time.Now().UTC().Format(time.RFC3339)}
    // Replace loading message with show picker
    embeds := []*discordgo.MessageEmbed{embed}
    if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: loading.ID, Channel: m.ChannelID, Embeds: &embeds, Components: &components}); err != nil {
        // Fallback to sending a new message
        msg, err2 := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}, Components: components})
        if err2 != nil { utils.ErrorLog("Discord: failed to send show picker: %v", err2); return }
        b.selectLock.Lock()
        b.pendingVODSelect[msg.ID] = &vodSelectContext{UserID: m.Author.ID, Channel: m.ChannelID, Query: "show:"+query, Results: nil, Page: 0, PerPage: 25, Created: time.Now()}
        b.selectLock.Unlock()
        b.setShowHierarchy(msg.ID, showMap, showOrder)
    } else {
        // Attach context to the same message ID
        b.selectLock.Lock()
        b.pendingVODSelect[loading.ID] = &vodSelectContext{UserID: m.Author.ID, Channel: m.ChannelID, Query: "show:"+query, Results: nil, Page: 0, PerPage: 25, Created: time.Now()}
        b.selectLock.Unlock()
        b.setShowHierarchy(loading.ID, showMap, showOrder)
    }
}