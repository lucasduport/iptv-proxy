package session

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lucasduport/iptv-proxy/pkg/database"
	"github.com/lucasduport/iptv-proxy/pkg/types"
	"github.com/lucasduport/iptv-proxy/pkg/utils"
)

// SessionManager handles user sessions and stream multiplexing
type SessionManager struct {
	userSessions     map[string]*types.UserSession     // username -> session
	streamSessions   map[string]*types.StreamSession   // streamID -> session
	streamBuffers    map[string]*StreamBuffer          // streamID -> buffer
	db               *database.DBManager
	tempLinks        map[string]*types.TemporaryLink   // token -> temp link
	userLock         sync.RWMutex
	streamLock       sync.RWMutex
	tempLinkLock     sync.RWMutex
	cleanupInterval  time.Duration
	sessionTimeout   time.Duration
	streamTimeout    time.Duration
	tempLinkTimeout  time.Duration
	httpClient       *http.Client
}

// StreamBuffer handles buffering and distribution of stream data
type StreamBuffer struct {
	streamID     string
	upstreamURL  string
	active       bool
	clients      map[string]chan []byte
	stopChan     chan struct{}
	clientsLock  sync.RWMutex
}

// NewSessionManager creates a new session manager
func NewSessionManager(db *database.DBManager) *SessionManager {
	manager := &SessionManager{
		userSessions:    make(map[string]*types.UserSession),
		streamSessions:  make(map[string]*types.StreamSession),
		streamBuffers:   make(map[string]*StreamBuffer),
		tempLinks:       make(map[string]*types.TemporaryLink),
		db:              db,
		cleanupInterval: 5 * time.Minute,
		sessionTimeout:  30 * time.Minute,
		streamTimeout:   2 * time.Minute,  // Time after which an unused stream is closed
		tempLinkTimeout: 24 * time.Hour,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}

	// Start cleanup routines
	go manager.cleanupRoutine()

	return manager
}

// cleanupRoutine periodically removes expired sessions and links
func (sm *SessionManager) cleanupRoutine() {
	ticker := time.NewTicker(sm.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		sm.cleanupExpiredSessions()
		sm.cleanupUnusedStreams()
		
		// Also clean up expired temporary links in the database
		if sm.db != nil {
			if count, err := sm.db.CleanupExpiredLinks(); err != nil {
				utils.ErrorLog("Failed to clean expired links: %v", err)
			} else if count > 0 {
				utils.InfoLog("Cleaned %d expired temporary links", count)
			}
		}
	}
}

// cleanupExpiredSessions removes inactive user sessions
func (sm *SessionManager) cleanupExpiredSessions() {
	threshold := time.Now().Add(-sm.sessionTimeout)
	
	sm.userLock.Lock()
	defer sm.userLock.Unlock()
	
	for username, session := range sm.userSessions {
		if session.LastActive.Before(threshold) {
			utils.InfoLog("Session expired for user %s (inactive since %v)",
				username, session.LastActive)
				
			// If user was watching a stream, remove from viewers
			if session.StreamID != "" {
				sm.streamLock.Lock()
				if streamSession, exists := sm.streamSessions[session.StreamID]; exists {
					if !streamSession.RemoveViewer(username) && streamSession.Active {
						// No more viewers, stop the stream
						sm.stopStream(session.StreamID)
					}
				}
				sm.streamLock.Unlock()
			}
			
			delete(sm.userSessions, username)
		}
	}
}

// cleanupUnusedStreams stops streams that have no viewers
func (sm *SessionManager) cleanupUnusedStreams() {
	threshold := time.Now().Add(-sm.streamTimeout)
	
	sm.streamLock.Lock()
	defer sm.streamLock.Unlock()
	
	for streamID, session := range sm.streamSessions {
		if session.LastRequested.Before(threshold) && session.Active {
			utils.InfoLog("Stream %s has been inactive for %v, stopping",
				streamID, sm.streamTimeout)
			sm.stopStream(streamID)
		}
	}
}

// RegisterUser creates or updates a user session
func (sm *SessionManager) RegisterUser(username, ip, userAgent string) *types.UserSession {
	sm.userLock.Lock()
	defer sm.userLock.Unlock()
	
	now := time.Now()
	
	// Check if user already has a session
	if session, exists := sm.userSessions[username]; exists {
		session.LastActive = now
		session.IPAddress = ip
		session.UserAgent = userAgent
		return session
	}
	
	// Create new session
	session := &types.UserSession{
		Username:   username,
		StartTime:  now,
		LastActive: now,
		IPAddress:  ip,
		UserAgent:  userAgent,
	}
	
	sm.userSessions[username] = session
	
	// Try to get Discord info if available
	if sm.db != nil {
		discordID, discordName, err := sm.db.GetDiscordByLDAPUser(username)
		if err == nil {
			session.DiscordID = discordID
			session.DiscordName = discordName
			utils.DebugLog("Linked Discord account %s to user %s", discordName, username)
		}
	}
	
	utils.InfoLog("New session registered for user %s from %s", username, ip)
	return session
}

// GetUserSession retrieves a user session if it exists
func (sm *SessionManager) GetUserSession(username string) *types.UserSession {
	sm.userLock.RLock()
	defer sm.userLock.RUnlock()
	
	session, exists := sm.userSessions[username]
	if !exists {
		return nil
	}
	
	// Update last active time
	session.LastActive = time.Now()
	return session
}

// RequestStream handles a new stream request and implements connection multiplexing
func (sm *SessionManager) RequestStream(username, streamID, streamType, streamTitle string, 
	upstreamURL *url.URL) (*StreamBuffer, error) {
	
	// Get user session, creating if necessary
	var userSession *types.UserSession
	sm.userLock.Lock()
	if session, exists := sm.userSessions[username]; exists {
		userSession = session
	} else {
		userSession = &types.UserSession{
			Username:   username,
			StartTime:  time.Now(),
			LastActive: time.Now(),
		}
		sm.userSessions[username] = userSession
	}
	
	// Update user session with stream info
	prevStreamID := userSession.StreamID
	userSession.StreamID = streamID
	userSession.StreamType = streamType
	userSession.LastActive = time.Now()
	sm.userLock.Unlock()
	
	// Handle case where user switches streams
	if prevStreamID != "" && prevStreamID != streamID {
		sm.streamLock.Lock()
		if prevStream, exists := sm.streamSessions[prevStreamID]; exists {
			if !prevStream.RemoveViewer(username) && prevStream.Active {
				// If no more viewers, stop the previous stream
				sm.stopStream(prevStreamID)
			}
		}
		sm.streamLock.Unlock()
	}
	
	// Check if this stream is already active
	sm.streamLock.Lock()
	defer sm.streamLock.Unlock()
	
	var streamBuffer *StreamBuffer
	
	// If this stream already exists, add the user as a viewer
	if existingBuffer, exists := sm.streamBuffers[streamID]; exists && existingBuffer.active {
		utils.InfoLog("User %s joined existing stream %s", username, streamID)
		
		// Add user to existing stream session
		if streamSession, exists := sm.streamSessions[streamID]; exists {
			streamSession.AddViewer(username)
		}
		
		// Add user as a client to the buffer
		clientChan := make(chan []byte, 10)
		existingBuffer.clientsLock.Lock()
		existingBuffer.clients[username] = clientChan
		existingBuffer.clientsLock.Unlock()
		
		return existingBuffer, nil
	}
	
	// Create a new stream session
	streamSession := &types.StreamSession{
		StreamID:      streamID,
		StreamType:    streamType,
		StreamTitle:   streamTitle,
		UpstreamURL:   upstreamURL.String(),
		StartTime:     time.Now(),
		LastRequested: time.Now(),
		Viewers:       make(map[string]time.Time),
		Active:        true,
	}
	streamSession.AddViewer(username)
	sm.streamSessions[streamID] = streamSession
	
	// Create a new stream buffer
	streamBuffer = &StreamBuffer{
		streamID:    streamID,
		upstreamURL: upstreamURL.String(),
		active:      true,
		clients:     make(map[string]chan []byte),
		stopChan:    make(chan struct{}),
	}
	
	// Add the requesting user as the first client
	clientChan := make(chan []byte, 10)
	streamBuffer.clients[username] = clientChan
	
	sm.streamBuffers[streamID] = streamBuffer
	
	// Start the stream goroutine
	go sm.streamToClients(streamBuffer, upstreamURL)
	
	// Record in database
	if sm.db != nil {
		_, err := sm.db.AddStreamHistory(
			username, streamID, streamType, streamTitle, 
			userSession.IPAddress, userSession.UserAgent,
		)
		if err != nil {
			utils.ErrorLog("Failed to record stream history: %v", err)
		}
	}
	
	utils.InfoLog("Started new stream %s for user %s", streamID, username)
	return streamBuffer, nil
}

// streamToClients fetches the stream from upstream and distributes to all clients
func (sm *SessionManager) streamToClients(buffer *StreamBuffer, upstreamURL *url.URL) {
	utils.DebugLog("Starting stream from %s", upstreamURL.String())
	
	req, err := http.NewRequest("GET", upstreamURL.String(), nil)
	if err != nil {
		utils.ErrorLog("Failed to create request: %v", err)
		return
	}
	
	// Set common headers for the request
	req.Header.Set("User-Agent", "IPTV-Proxy")
	
	resp, err := sm.httpClient.Do(req)
	if err != nil {
		utils.ErrorLog("Failed to connect to upstream: %v", err)
		sm.stopStream(buffer.streamID)
		return
	}
	defer resp.Body.Close()
	
	// Check if response is successful
	if resp.StatusCode != http.StatusOK {
		utils.ErrorLog("Upstream returned status %d for stream %s", 
			resp.StatusCode, buffer.streamID)
		sm.stopStream(buffer.streamID)
		return
	}
	
	// Stream data to all clients
	buffer.active = true
	
	// Use a larger buffer for better performance
	const bufferSize = 64 * 1024  // 64KB buffer
	dataBuffer := make([]byte, bufferSize)
	
	for {
		select {
		case <-buffer.stopChan:
			utils.DebugLog("Stream %s stopped", buffer.streamID)
			return
		default:
			// Read from upstream
			n, err := resp.Body.Read(dataBuffer)
			if err != nil {
				if err != io.EOF {
					utils.ErrorLog("Error reading from upstream: %v", err)
				}
				sm.stopStream(buffer.streamID)
				return
			}
			
			if n > 0 {
				// Copy the data to avoid race conditions when sending to multiple clients
				dataCopy := make([]byte, n)
				copy(dataCopy, dataBuffer[:n])
				
				// Send to all connected clients
				buffer.clientsLock.RLock()
				for username, clientChan := range buffer.clients {
					// Non-blocking send, skip if client can't keep up
					select {
					case clientChan <- dataCopy:
						// Successfully sent
					default:
						utils.DebugLog("Client %s buffer full, skipping chunk", username)
					}
				}
				buffer.clientsLock.RUnlock()
			}
		}
	}
}

// GetClientChannel retrieves the data channel for a specific client
func (sm *SessionManager) GetClientChannel(streamID, username string) (chan []byte, bool) {
	sm.streamLock.RLock()
	defer sm.streamLock.RUnlock()
	
	buffer, exists := sm.streamBuffers[streamID]
	if !exists || !buffer.active {
		return nil, false
	}
	
	buffer.clientsLock.RLock()
	defer buffer.clientsLock.RUnlock()
	
	channel, exists := buffer.clients[username]
	return channel, exists
}

// RemoveClient removes a client from a stream
func (sm *SessionManager) RemoveClient(streamID, username string) {
	sm.streamLock.Lock()
	defer sm.streamLock.Unlock()
	
	// Update user session
	sm.userLock.Lock()
	if userSession, exists := sm.userSessions[username]; exists && userSession.StreamID == streamID {
		userSession.StreamID = ""
		userSession.StreamType = ""
	}
	sm.userLock.Unlock()
	
	// Remove from stream buffer
	buffer, exists := sm.streamBuffers[streamID]
	if !exists {
		return
	}
	
	buffer.clientsLock.Lock()
	if ch, found := buffer.clients[username]; found {
		close(ch)
		delete(buffer.clients, username)
	}
	buffer.clientsLock.Unlock()
	
	// Remove from stream session
	streamSession, exists := sm.streamSessions[streamID]
	if !exists {
		return
	}
	
	// If this was the last viewer, stop the stream
	if !streamSession.RemoveViewer(username) && buffer.active {
		sm.stopStream(streamID)
	}
	
	utils.InfoLog("User %s removed from stream %s", username, streamID)
}

// stopStream stops an active stream
func (sm *SessionManager) stopStream(streamID string) {
	utils.InfoLog("Stopping stream %s", streamID)
	
	// Get the buffer
	buffer, exists := sm.streamBuffers[streamID]
	if !exists || !buffer.active {
		return
	}
	
	// Signal the streaming goroutine to stop
	close(buffer.stopChan)
	buffer.active = false
	
	// Close all client channels
	buffer.clientsLock.Lock()
	for username, ch := range buffer.clients {
		close(ch)
		
		// Also update the user session
		sm.userLock.Lock()
		if userSession, exists := sm.userSessions[username]; exists && userSession.StreamID == streamID {
			userSession.StreamID = ""
			userSession.StreamType = ""
		}
		sm.userLock.Unlock()
	}
	buffer.clients = make(map[string]chan []byte)
	buffer.clientsLock.Unlock()
	
	// Update the stream session
	if streamSession, exists := sm.streamSessions[streamID]; exists {
		streamSession.Active = false
	}
	
	utils.InfoLog("Stream %s stopped and all clients disconnected", streamID)
}

// GenerateTemporaryLink creates a temporary download link
func (sm *SessionManager) GenerateTemporaryLink(username, streamID, title, rawURL string) (string, error) {
	token := uuid.New().String()
	expiresAt := time.Now().Add(sm.tempLinkTimeout)
	
	tempLink := &types.TemporaryLink{
		Token:     token,
		Username:  username,
		URL:       rawURL,
		ExpiresAt: expiresAt,
		StreamID:  streamID,
		Title:     title,
	}
	
	// Store in memory
	sm.tempLinkLock.Lock()
	sm.tempLinks[token] = tempLink
	sm.tempLinkLock.Unlock()
	
	// Store in database if available
	if sm.db != nil {
		if err := sm.db.CreateTemporaryLink(token, username, rawURL, streamID, title, expiresAt); err != nil {
			utils.ErrorLog("Failed to store temporary link in database: %v", err)
		}
	}
	
	utils.InfoLog("Generated temporary link for user %s, expires at %v", username, expiresAt)
	return token, nil
}

// GetTemporaryLink retrieves a temporary link by token
func (sm *SessionManager) GetTemporaryLink(token string) (*types.TemporaryLink, error) {
	// First check in memory
	sm.tempLinkLock.RLock()
	tempLink, exists := sm.tempLinks[token]
	sm.tempLinkLock.RUnlock()
	
	if exists && time.Now().Before(tempLink.ExpiresAt) {
		return tempLink, nil
	}
	
	// If not in memory or expired, try the database
	if sm.db != nil {
		return sm.db.GetTemporaryLink(token)
	}
	
	return nil, fmt.Errorf("temporary link not found or expired")
}

// GetAllSessions returns all current user sessions
func (sm *SessionManager) GetAllSessions() []*types.UserSession {
	sm.userLock.RLock()
	defer sm.userLock.RUnlock()
	
	sessions := make([]*types.UserSession, 0, len(sm.userSessions))
	for _, session := range sm.userSessions {
		sessions = append(sessions, session)
	}
	
	return sessions
}

// GetAllStreams returns all active stream sessions
func (sm *SessionManager) GetAllStreams() []*types.StreamSession {
	sm.streamLock.RLock()
	defer sm.streamLock.RUnlock()
	
	streams := make([]*types.StreamSession, 0, len(sm.streamSessions))
	for _, stream := range sm.streamSessions {
		if stream.Active {
			streams = append(streams, stream)
		}
	}
	
	return streams
}

// DisconnectUser forcibly disconnects all streams for a user
func (sm *SessionManager) DisconnectUser(username string) {
	sm.userLock.Lock()
	userSession, exists := sm.userSessions[username]
	if !exists {
		sm.userLock.Unlock()
		return
	}
	
	streamID := userSession.StreamID
	userSession.StreamID = ""
	userSession.StreamType = ""
	sm.userLock.Unlock()
	
	// If user was watching a stream, remove them
	if streamID != "" {
		sm.RemoveClient(streamID, username)
	}
	
	utils.InfoLog("User %s forcibly disconnected", username)
}

// GetStreamInfo gets information about a specific stream
func (sm *SessionManager) GetStreamInfo(streamID string) (*types.StreamSession, bool) {
	sm.streamLock.RLock()
	defer sm.streamLock.RUnlock()
	
	session, exists := sm.streamSessions[streamID]
	return session, exists
}

// SetSessionTimeout sets the user session timeout duration
func (sm *SessionManager) SetSessionTimeout(timeout time.Duration) {
	sm.sessionTimeout = timeout
}

// SetStreamTimeout sets the unused stream timeout duration
func (sm *SessionManager) SetStreamTimeout(timeout time.Duration) {
	sm.streamTimeout = timeout
}

// SetTempLinkTimeout sets the temporary link expiration duration
func (sm *SessionManager) SetTempLinkTimeout(timeout time.Duration) {
	sm.tempLinkTimeout = timeout
}
