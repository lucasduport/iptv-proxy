package discord

import (
    "net/http"
    "sync"
    "time"

    "github.com/bwmarrin/discordgo"
    "github.com/lucasduport/stream-share/pkg/types"
)

// Bot represents the Discord bot and its stateful maps for interactive flows.
type Bot struct {
    session         *discordgo.Session
    token           string
    prefix          string
    adminRoleID     string
    apiURL          string
    apiKey          string
    client          *http.Client

    cleanupInterval time.Duration

    // Component-based selection contexts
    pendingVODSelect map[string]*vodSelectContext // messageID -> selection context
    selectLock       sync.RWMutex
}


// Context for component-based VOD selection (dropdown + buttons)
type vodSelectContext struct {
    UserID  string
    Channel string
    Query   string
    Results []types.VODResult
    Page    int
    PerPage int
    Created time.Time
}
