/*
 * stream-share is a project to efficiently share the use of an IPTV service.
 * Copyright (C) 2025  Lucas Duport
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package types

import (
	"sync"
	"time"
)

// UserSession represents an active user session
type UserSession struct {
	Username    string    // LDAP/local username
	DiscordID   string    // Linked Discord ID (if available)
	DiscordName string    // Discord username for display
	StreamID    string    // Current stream ID
	StreamType  string    // "live", "vod", "series"
	StartTime   time.Time // Session start time
	LastActive  time.Time // Last activity time
	IPAddress   string    // User's IP address
	UserAgent   string    // User's device/agent
}

// StreamSession represents a shared stream with multiple viewers
type StreamSession struct {
	StreamID      string               // Stream identifier
	StreamType    string               // Type of content (live, vod, series)
	StreamTitle   string               // Content title
	ProxyURL      string               // Internal proxy URL
	UpstreamURL   string               // Upstream provider URL
	StartTime     time.Time            // When the stream started
	LastRequested time.Time            // Last time any user requested this stream
	Viewers       map[string]time.Time // Map of usernames to their last activity time
	Active        bool                 // Whether the stream is currently active
	lock          sync.RWMutex         // Lock for concurrent access
}

// AddViewer adds a viewer to the stream session
func (s *StreamSession) AddViewer(username string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	
	s.Viewers[username] = time.Now()
	s.LastRequested = time.Now()
}

// RemoveViewer removes a viewer from the stream session
func (s *StreamSession) RemoveViewer(username string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	
	delete(s.Viewers, username)
	
	// Return whether the stream still has viewers
	return len(s.Viewers) > 0
}

// GetViewers returns a copy of the current viewers map
func (s *StreamSession) GetViewers() map[string]time.Time {
	s.lock.RLock()
	defer s.lock.RUnlock()
	
	// Create a copy of the viewers map to avoid race conditions
	viewers := make(map[string]time.Time, len(s.Viewers))
	for k, v := range s.Viewers {
		viewers[k] = v
	}
	
	return viewers
}

// VODRequest represents a VOD search request and response
type VODRequest struct {
	Username  string
	Query     string
	Results   []VODResult
	CreatedAt time.Time
	ExpiresAt time.Time
	Token     string  // Unique token for this request
}

// VODResult represents a single VOD search result
type VODResult struct {
	ID       string
	Title    string
	Category string
	Duration string
	Year     string
	Rating   string
	StreamID string // The stream ID needed to retrieve this content
}

// TemporaryLink represents a generated temporary download link
type TemporaryLink struct {
	Token     string
	Username  string
	URL       string
	ExpiresAt time.Time
	StreamID  string
	Title     string
}

// APIResponse is a standardized API response structure
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}
