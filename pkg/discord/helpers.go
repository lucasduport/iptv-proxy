package discord

import (
	"fmt"
	"strconv"
	"strings"

    "github.com/bwmarrin/discordgo"
	"github.com/lucasduport/stream-share/pkg/utils"
)

// getString safely extracts string from a map[string]interface{}
func getString(m map[string]interface{}, key string) string {
    if val, ok := m[key].(string); ok {
        return val
    }
    return ""
}

// isSameUser verifies the interaction comes from the expected user.
func (b *Bot) isSameUser(expected string, i *discordgo.InteractionCreate) bool {
    if i.Member != nil && i.Member.User != nil {
        return i.Member.User.ID == expected
    }
    if i.User != nil {
        return i.User.ID == expected
    }
    return false
}

// interactionUserID extracts user ID from an interaction.
func (b *Bot) interactionUserID(i *discordgo.InteractionCreate) string {
    if i.Member != nil && i.Member.User != nil { return i.Member.User.ID }
    if i.User != nil { return i.User.ID }
    return ""
}


func getInt64(m map[string]interface{}, k string) int64 {
    if v, ok := m[k]; ok {
        switch t := v.(type) {
        case float64:
            return int64(t)
        case int64:
            return t
        case int:
            return int64(t)
        case string:
            if n, err := strconv.ParseInt(t, 10, 64); err == nil { return n }
        }
    }
    return 0
}

// renderBar returns a textual progress bar and bytes summary
func renderBar(done, total int64) string {
    // 20 char bar
    const width = 20
    var pct int
    if total > 0 { pct = int((done*100)/total) } else { pct = 0 }
    if pct > 100 { pct = 100 }
    filled := (pct * width) / 100
    if filled > width { filled = width }
    bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
    var size string
    if total > 0 {
        size = fmt.Sprintf("%s/%s", utils.HumanBytes(done), utils.HumanBytes(total))
    } else if done > 0 {
        size = fmt.Sprintf("%s", utils.HumanBytes(done))
    } else {
        size = "starting…"
    }
    return fmt.Sprintf("`[%s]` %d%% — %s", bar, pct, size)
}
