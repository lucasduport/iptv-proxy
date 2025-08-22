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

    // Legacy reaction flow (kept for backward compatibility)
    pendingVODLinks map[string]map[int]types.VODResult // Discord user ID -> choice index -> VOD result
    linkTokens      map[string]string                  // Token -> Discord user ID
    pendingVODLock  sync.RWMutex
    linkTokensLock  sync.RWMutex

    cleanupInterval time.Duration

    // Reaction selection context per message
    pendingVODByMsg map[string]*vodPendingContext // messageID -> context
    pendingMsgLock  sync.RWMutex

    // Component-based selection contexts
    pendingVODSelect map[string]*vodSelectContext // messageID -> selection context
    selectLock       sync.RWMutex

    // Show selection flow
    showFlows map[string]*showHierarchy
    showState map[string]*showState
    showLock  sync.RWMutex
}

// Context for reaction-based VOD selection
type vodPendingContext struct {
    UserID    string
    ChannelID string
    Choices   map[int]types.VODResult // 1..10 (0 represents 10)
}

// Hierarchy for shows: show -> seasons -> episodes
type showHierarchy struct {
    Order []string
    Data  map[string]map[int][]types.VODResult
}

// Current user selection for a show flow
type showState struct {
    UserID         string
    SelectedShow   string
    SelectedSeason int
    EpisodePage    int
    PerPage        int
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
