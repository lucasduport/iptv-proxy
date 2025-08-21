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

package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lucasduport/stream-share/pkg/types"
	"github.com/lucasduport/stream-share/pkg/utils"
	_ "github.com/lib/pq" // PostgreSQL driver
)

// DBManager handles database operations
type DBManager struct {
	db          *sql.DB
	initialized bool
}

// NewDBManager creates a new database manager
func NewDBManager(_ string) (*DBManager, error) {
	utils.InfoLog("Initializing PostgreSQL database connection")

	host := utils.GetEnvOrDefault("DB_HOST", "localhost")
	port := utils.GetEnvOrDefault("DB_PORT", "5432")
	dbName := utils.GetEnvOrDefault("DB_NAME", "iptvproxy")
	user := utils.GetEnvOrDefault("DB_USER", "postgres")
	password := utils.GetEnvOrDefault("DB_PASSWORD", "")

	connStr := fmt.Sprintf(
		"host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		host, port, dbName, user, password,
	)

	utils.DebugLog("Connecting to PostgreSQL: host=%s port=%s dbname=%s user=%s", host, port, dbName, user)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL database: %w", err)
	}

	if err := db.Ping(); err != nil {
		utils.ErrorLog("Failed to connect to database: %v", err)
		return nil, fmt.Errorf("database connection test failed: %w", err)
	}
	utils.InfoLog("Database connection successful")

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	manager := &DBManager{db: db}
	if err := manager.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	manager.initialized = true
	return manager, nil
}

// initSchema creates database tables if they don't exist
func (m *DBManager) initSchema() error {
	utils.InfoLog("Initializing database schema")

	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS discord_ldap_mapping (
			discord_id TEXT PRIMARY KEY,
			discord_name TEXT NOT NULL,
			ldap_username TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_active TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		utils.ErrorLog("Failed to create discord_ldap_mapping table: %v", err)
		return fmt.Errorf("failed to create discord_ldap_mapping table: %w", err)
	}

	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS stream_history (
			id SERIAL PRIMARY KEY,
			username TEXT NOT NULL,
			discord_id TEXT,
			stream_id TEXT NOT NULL,
			stream_type TEXT NOT NULL,
			stream_title TEXT,
			start_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			end_time TIMESTAMP,
			ip_address TEXT,
			user_agent TEXT
		)
	`)
	if err != nil {
		utils.ErrorLog("Failed to create stream_history table: %v", err)
		return fmt.Errorf("failed to create stream_history table: %w", err)
	}

	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS temporary_links (
			token TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			url TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			stream_id TEXT,
			title TEXT
		)
	`)
	if err != nil {
		utils.ErrorLog("Failed to create temporary_links table: %v", err)
		return fmt.Errorf("failed to create temporary_links table: %w", err)
	}

	utils.InfoLog("Database schema initialized successfully")

	var count int
	err = m.db.QueryRow(`SELECT count(*)
		FROM information_schema.tables 
		WHERE table_name IN ('discord_ldap_mapping', 'stream_history', 'temporary_links')`).Scan(&count)
	if err != nil {
		utils.WarnLog("Failed to verify tables were created: %v", err)
	} else {
		utils.InfoLog("Database verification: %d of 3 required tables exist", count)
	}
	return nil
}

// IsInitialized returns whether the database is initialized
func (m *DBManager) IsInitialized() bool {
	return m != nil && m.initialized && m.db != nil
}

// Close closes the database connection
func (m *DBManager) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	utils.InfoLog("Closing database connection")
	return m.db.Close()
}

// LinkDiscordToLDAP maps a Discord user ID to an LDAP username
func (m *DBManager) LinkDiscordToLDAP(discordID, discordName, ldapUsername string) error {
	utils.DebugLog("Database: Linking Discord ID %s (%s) to LDAP user %s", discordID, discordName, ldapUsername)
	if m == nil || m.db == nil {
		return fmt.Errorf("database not initialized")
	}

	stmt := `
		INSERT INTO discord_ldap_mapping (discord_id, discord_name, ldap_username) 
		VALUES ($1, $2, $3)
		ON CONFLICT(discord_id) DO UPDATE SET 
		  discord_name = EXCLUDED.discord_name,
		  ldap_username = EXCLUDED.ldap_username,
		  last_active = CURRENT_TIMESTAMP
	`
	_, err := m.db.Exec(stmt, discordID, discordName, ldapUsername)
	if err != nil {
		utils.ErrorLog("Database error linking Discord to LDAP: %v", err)
		return err
	}
	utils.InfoLog("Successfully linked Discord ID %s to LDAP user %s", discordID, ldapUsername)
	return nil
}

// GetLDAPUserByDiscordID retrieves the LDAP username for a Discord ID
func (m *DBManager) GetLDAPUserByDiscordID(discordID string) (string, error) {
	utils.DebugLog("Database: Getting LDAP user for Discord ID %s", discordID)
	if m == nil || m.db == nil {
		return "", fmt.Errorf("database not initialized")
	}

	var ldapUsername string
	err := m.db.QueryRow(`
		SELECT ldap_username FROM discord_ldap_mapping 
		WHERE discord_id = $1
	`, discordID).Scan(&ldapUsername)

	if err == sql.ErrNoRows {
		utils.DebugLog("No LDAP user found for Discord ID %s", discordID)
		return "", fmt.Errorf("no LDAP user linked to Discord ID %s", discordID)
	}
	if err != nil {
		utils.ErrorLog("Database error getting LDAP user: %v", err)
		return "", err
	}
	utils.DebugLog("Found LDAP user %s for Discord ID %s", ldapUsername, discordID)
	return ldapUsername, nil
}

// GetDiscordByLDAPUser retrieves Discord info for an LDAP username
func (m *DBManager) GetDiscordByLDAPUser(ldapUsername string) (string, string, error) {
	utils.DebugLog("Database: Getting Discord info for LDAP user %s", ldapUsername)
	if m == nil || m.db == nil {
		return "", "", fmt.Errorf("database not initialized")
	}

	var discordID, discordName string
	err := m.db.QueryRow(`
		SELECT discord_id, discord_name FROM discord_ldap_mapping 
		WHERE ldap_username = $1
	`, ldapUsername).Scan(&discordID, &discordName)

	if err == sql.ErrNoRows {
		utils.DebugLog("No Discord account linked to LDAP user %s", ldapUsername)
		return "", "", fmt.Errorf("no Discord account linked to LDAP user %s", ldapUsername)
	}
	if err != nil {
		utils.ErrorLog("Database error getting Discord info: %v", err)
		return "", "", err
	}
	utils.DebugLog("Found Discord ID %s (%s) for LDAP user %s", discordID, discordName, ldapUsername)
	return discordID, discordName, nil
}

// AddStreamHistory records a new stream session
func (m *DBManager) AddStreamHistory(username, streamID, streamType, streamTitle, ipAddress, userAgent string) (int64, error) {
	utils.DebugLog("Database: Recording stream history - user: %s, stream: %s, type: %s", username, streamID, streamType)
	if m == nil || m.db == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	var discordID string
	_ = m.db.QueryRow(`SELECT discord_id FROM discord_ldap_mapping WHERE ldap_username = $1`, username).Scan(&discordID)

	var id int64
	err := m.db.QueryRow(`
		INSERT INTO stream_history 
		  (username, discord_id, stream_id, stream_type, stream_title, ip_address, user_agent) 
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, username, discordID, streamID, streamType, streamTitle, ipAddress, userAgent).Scan(&id)
	if err != nil {
		utils.ErrorLog("Database error adding stream history: %v", err)
		return 0, err
	}
	return id, nil
}

// CloseStreamHistory marks a stream session as ended
func (m *DBManager) CloseStreamHistory(historyID int64) error {
	utils.DebugLog("Database: Closing stream history record %d", historyID)
	if m == nil || m.db == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := m.db.Exec(`UPDATE stream_history SET end_time = CURRENT_TIMESTAMP WHERE id = $1`, historyID)
	if err != nil {
		utils.ErrorLog("Database error closing stream history: %v", err)
		return err
	}
	return nil
}

// CreateTemporaryLink generates a new temporary download link
func (m *DBManager) CreateTemporaryLink(token, username, url, streamID, title string, expirationTime time.Time) error {
	utils.DebugLog("Database: Creating temporary link - token: %s, user: %s, expires: %v", token, username, expirationTime)
	if m == nil || m.db == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := m.db.Exec(`
		INSERT INTO temporary_links (token, username, url, expires_at, stream_id, title) 
		VALUES ($1, $2, $3, $4, $5, $6)
	`, token, username, url, expirationTime, streamID, title)
	if err != nil {
		utils.ErrorLog("Database error creating temporary link: %v", err)
		return err
	}
	return nil
}

// GetTemporaryLink retrieves a temporary link by token
func (m *DBManager) GetTemporaryLink(token string) (*types.TemporaryLink, error) {
	utils.DebugLog("Database: Getting temporary link for token %s", token)
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	link := &types.TemporaryLink{}
	err := m.db.QueryRow(`
		SELECT token, username, url, expires_at, stream_id, title
		FROM temporary_links 
		WHERE token = $1 AND expires_at > CURRENT_TIMESTAMP
	`, token).Scan(&link.Token, &link.Username, &link.URL, &link.ExpiresAt, &link.StreamID, &link.Title)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("temporary link not found or expired")
	}
	if err != nil {
		utils.ErrorLog("Database error getting temporary link: %v", err)
		return nil, err
	}
	return link, nil
}

// CleanupExpiredLinks removes expired temporary links
func (m *DBManager) CleanupExpiredLinks() (int64, error) {
	utils.DebugLog("Database: Cleaning up expired temporary links")
	if m == nil || m.db == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	result, err := m.db.Exec(`DELETE FROM temporary_links WHERE expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		utils.ErrorLog("Database error cleaning up expired links: %v", err)
		return 0, err
	}
	rows, _ := result.RowsAffected()
	if rows > 0 {
		utils.InfoLog("Cleaned up %d expired temporary links", rows)
	}
	return rows, nil
}

// GetStreamHistoryStats gets statistics about stream usage
func (m *DBManager) GetStreamHistoryStats() (map[string]interface{}, error) {
	utils.DebugLog("Database: Getting stream history statistics")
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	stats := make(map[string]interface{})
	var totalStreams int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM stream_history").Scan(&totalStreams); err != nil {
		utils.ErrorLog("Database error counting streams: %v", err)
		return nil, err
	}
	stats["total_streams"] = totalStreams

	var activeUsers int
	if err := m.db.QueryRow(`
		SELECT COUNT(DISTINCT username) FROM stream_history WHERE start_time > $1
	`, time.Now().Add(-24*time.Hour)).Scan(&activeUsers); err != nil {
		utils.ErrorLog("Database error counting active users: %v", err)
		return nil, err
	}
	stats["active_users_24h"] = activeUsers

	return stats, nil
}