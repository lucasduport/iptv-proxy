package discord

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
)

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

	// Build a map: show title -> seasons -> episodes, based on results
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
			// Accept both true series and episodic entries from M3U that look like SxxEyy
			show := vr.SeriesTitle
			season := vr.Season
			episode := vr.Episode
			epTitle := vr.EpisodeTitle
			// Infer from title when type/fields are missing
			if show == "" || season == 0 || episode == 0 {
				if inferred, name, sn, ep, epT := inferSeriesFromTitle(vr.Title); inferred {
					show = name
					season = sn
					episode = ep
					if epTitle == "" { epTitle = epT }
				}
			}
			if show == "" || season == 0 || episode == 0 { continue }
			// Fill inferred metadata but keep original StreamType (download path may depend on it)
			vr.SeriesTitle = show
			vr.Season = season
			vr.Episode = episode
			if vr.EpisodeTitle == "" { vr.EpisodeTitle = epTitle }
			if _, ok := showMap[show]; !ok { showMap[show] = map[int][]struct{ idx int; item types.VODResult }{}; showOrder = append(showOrder, show) }
			showMap[show][season] = append(showMap[show][season], struct{ idx int; item types.VODResult }{idx: idx, item: vr})
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

// handleCachedList shows current cached items with time until expiry
func (b *Bot) handleCachedList(s *discordgo.Session, m *discordgo.MessageCreate) {
	ok, resp, err := b.makeAPIRequest("GET", "/cache/list", nil)
	if err != nil || !ok {
		b.fail(m.ChannelID, "❌ Cache List Failed", "Couldn't fetch cached items.")
		return
	}
	arr, _ := resp.([]interface{})
	if len(arr) == 0 {
		b.info(m.ChannelID, "💾 Cached Items", "No active cached items.")
		return
	}
	const per = 10
	pages := (len(arr)+per-1)/per
	for p := 0; p < pages; p++ {
		start := p*per
		end := start+per
		if end > len(arr) { end = len(arr) }
		lines := make([]string, 0, end-start)
		for _, it := range arr[start:end] {
			mapp, _ := it.(map[string]interface{})
			typ := getString(mapp, "type")
			title := strings.TrimSpace(getString(mapp, "title"))
			if typ == "series" {
				st := getString(mapp, "series_title")
				if strings.TrimSpace(st) != "" { title = st }
				if title == "" { title = "Series" }
				season := int(getInt64(mapp, "season"))
				episode := int(getInt64(mapp, "episode"))
				if season > 0 || episode > 0 {
					title = fmt.Sprintf("%s S%02dE%02d", title, season, episode)
				}
			} else {
				if title == "" { title = "Unknown title" }
			}
			by := strings.TrimSpace(getString(mapp, "requested_by"))
			leftSecs := int(getInt64(mapp, "time_left_seconds"))
			// Humanize left: prioritize days, else hours
			left := "expired"
			if leftSecs > 0 {
				days := leftSecs / 86400
				if days >= 1 {
					if days == 1 { left = "1 day" } else { left = fmt.Sprintf("%d days", days) }
				} else {
					hours := (leftSecs + 3599) / 3600 // round up
					if hours <= 1 { left = "1 hour" } else { left = fmt.Sprintf("%d hours", hours) }
				}
			}
			// Build line: Title [— by user] — expires in X
			line := fmt.Sprintf("• %s", title)
			if by != "" { line += fmt.Sprintf(" — by %s", by) }
			line += fmt.Sprintf(" — expires in %s", left)
			lines = append(lines, line)
		}
		desc := strings.Join(lines, "\n")
		if pages > 1 { desc += fmt.Sprintf("\n\nPage %d/%d", p+1, pages) }
		b.info(m.ChannelID, "💾 Cached Items", desc)
	}
}

// inferSeriesFromTitle tries to parse titles like:
//   "Game of Thrones (MULTI) FHD S07 E05"
//   "Game of Thrones S08E01 — Winterfell"
//   "The Office (US) 1080p S3E12"
// Returns (true, seriesTitle, season, episode, episodeTitle) on success.
func inferSeriesFromTitle(title string) (bool, string, int, int, string) {
	t := strings.TrimSpace(title)
	if t == "" { return false, "", 0, 0, "" }
	// Variant 1: ... S07 E05 ... (optional separator and ep title at end)
	re1 := regexp.MustCompile(`(?i)^(.*?)\s*(?:\([^)]*\)\s*)?(?:FHD|HD|UHD|4K|1080p|720p|MULTI)?\s*S(\d{1,2})\s*[EEx×](\d{1,2})(?:\s*[-–—:]\s*(.*))?$`)
	if m := re1.FindStringSubmatch(t); m != nil {
		name := cleanSeriesName(m[1])
		sn := atoiSafe(m[2])
		ep := atoiSafe(m[3])
		epTitle := strings.TrimSpace(m[4])
		return true, name, sn, ep, epTitle
	}
	// Variant 2: allow compact S01E02
	re2 := regexp.MustCompile(`(?i)^(.*?)\s*S(\d{1,2})\s*[EEx×](\d{1,2})(?:\s*[-–—:]\s*(.*))?`)
	if m := re2.FindStringSubmatch(t); m != nil {
		name := cleanSeriesName(m[1])
		sn := atoiSafe(m[2])
		ep := atoiSafe(m[3])
		epTitle := strings.TrimSpace(m[4])
		return true, name, sn, ep, epTitle
	}
	return false, "", 0, 0, ""
}

func cleanSeriesName(s string) string {
	s = strings.TrimSpace(s)
	// Remove trailing quality tokens if any
	s = regexp.MustCompile(`(?i)\b(FHD|HD|UHD|4K|1080p|720p|MULTI)\b`).ReplaceAllString(s, "")
	// Remove stray separators
	s = strings.Trim(s, "-—–:|• ")
	// Collapse spaces
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func atoiSafe(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' { continue }
		n = n*10 + int(ch-'0')
	}
	return n
}