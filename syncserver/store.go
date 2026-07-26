package syncserver

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SyncEntry represents a synced word entry (shared between PC and mobile)
type SyncEntry struct {
	ID        string `json:"id"`
	Word      string `json:"word"`
	Result    string `json:"result"`    // JSON string of merged ECDICT+LLM result
	CreatedAt int64  `json:"createdAt"` // Unix timestamp (seconds)
	UpdatedAt int64  `json:"updatedAt"` // Unix timestamp (seconds)
	Deleted   bool   `json:"deleted"`   // Soft delete flag
}

// SyncPushRequest is the request body for pushing entries from client
type SyncPushRequest struct {
	Entries []SyncEntry `json:"entries"`
}

// SyncPullResponse is the response for pulling entries
type SyncPullResponse struct {
	Entries   []SyncEntry `json:"entries"`
	ServerNow int64       `json:"serverNow"` // Server timestamp for client reference
}

// SyncStatusResponse shows sync status for a user
type SyncStatusResponse struct {
	Token     string `json:"token"`
	WordCount int    `json:"wordCount"` // Non-deleted word count
	LastSync  int64  `json:"lastSync"`  // Last sync timestamp
	CreatedAt int64  `json:"createdAt"` // Account creation time
}

// AuthSession represents a pending QR-code login session
type AuthSession struct {
	Scene     string `json:"scene"`     // Random 8-char scene parameter
	Status    string `json:"status"`    // pending | scanned | expired
	Token     string `json:"token"`     // Bound token (set when scanned)
	OpenID    string `json:"openid"`    // Bound openid (set when scanned)
	CreatedAt int64  `json:"createdAt"` // Unix timestamp
	ExpiresAt int64  `json:"expiresAt"` // Unix timestamp when session expires
}

// Store manages the SQLite database for sync data
type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewStore creates a new Store with the given database path
func NewStore(dbPath string) (*Store, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %v", err)
	}

	// SQLite pragmas for better performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("Warning: set WAL mode failed: %v", err)
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		log.Printf("Warning: set synchronous failed: %v", err)
	}
	if _, err := db.Exec("PRAGMA cache_size=-16384"); err != nil { // 16MB cache
		log.Printf("Warning: set cache_size failed: %v", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		log.Printf("Warning: set busy_timeout failed: %v", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库迁移失败: %v", err)
	}

	return s, nil
}

// migrate creates the database tables
func (s *Store) migrate() error {
	// Users table
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			token      TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
			last_sync  INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("创建 users 表失败: %v", err)
	}

	// Sync entries table
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sync_entries (
			id         TEXT NOT NULL,
			token      TEXT NOT NULL,
			word       TEXT NOT NULL,
			result     TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			deleted    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (id, token)
		)
	`); err != nil {
		return fmt.Errorf("创建 sync_entries 表失败: %v", err)
	}

	// Index for fast pull queries
	if _, err := s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_sync_entries_token_updated
		ON sync_entries(token, updated_at)
	`); err != nil {
		log.Printf("Warning: create index failed: %v", err)
	}

	// Index for word lookup within a user
	if _, err := s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_sync_entries_token_word
		ON sync_entries(token, word)
	`); err != nil {
		log.Printf("Warning: create index failed: %v", err)
	}

	// Auth sessions table (for QR code login flow)
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_sessions (
			scene      TEXT PRIMARY KEY,
			status     TEXT NOT NULL DEFAULT 'pending',
			token      TEXT NOT NULL DEFAULT '',
			openid     TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
			expires_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("创建 auth_sessions 表失败: %v", err)
	}

	// Add openid column to users table if not exists (migration for existing DBs)
	s.migrateAddColumn("users", "openid", "TEXT NOT NULL DEFAULT ''")

	// Unique index on openid for user lookup
	if _, err := s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_users_openid
		ON users(openid) WHERE openid != ''
	`); err != nil {
		log.Printf("Warning: create openid index failed: %v", err)
	}

	// Clean up expired auth sessions
	s.cleanExpiredAuthSessions()

	return nil
}

// migrateAddColumn adds a column to a table if it doesn't already exist.
func (s *Store) migrateAddColumn(table, column, definition string) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
		table, column,
	).Scan(&count)
	if err == nil && count == 0 {
		_, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
		if err != nil {
			log.Printf("Warning: add column %s.%s failed: %v", table, column, err)
		} else {
			log.Printf("Migration: added column %s.%s", table, column)
		}
	}
}

// Close closes the database
func (s *Store) Close() error {
	return s.db.Close()
}

// GenerateToken creates a new random token (32 bytes = 64 hex chars)
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成token失败: %v", err)
	}
	return hex.EncodeToString(b), nil
}

// CreateUser creates a new user with a generated token
func (s *Store) CreateUser() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, err := GenerateToken()
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()
	_, err = s.db.Exec(
		"INSERT INTO users (token, created_at, last_sync) VALUES (?, ?, ?)",
		token, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("创建用户失败: %v", err)
	}

	log.Printf("新用户创建成功, token前8位: %s...", token[:8])
	return token, nil
}

// ValidateToken checks if a token belongs to a valid user
func (s *Store) ValidateToken(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM users WHERE token = ?",
		token,
	).Scan(&count)
	return err == nil && count > 0
}

// GetUserStatus returns the sync status for a user
func (s *Store) GetUserStatus(token string) (*SyncStatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var createdAt, lastSync int64
	err := s.db.QueryRow(
		"SELECT created_at, last_sync FROM users WHERE token = ?",
		token,
	).Scan(&createdAt, &lastSync)
	if err != nil {
		return nil, fmt.Errorf("用户不存在")
	}

	var wordCount int
	err = s.db.QueryRow(
		"SELECT COUNT(*) FROM sync_entries WHERE token = ? AND deleted = 0",
		token,
	).Scan(&wordCount)
	if err != nil {
		wordCount = 0
	}

	return &SyncStatusResponse{
		Token:     token,
		WordCount: wordCount,
		LastSync:  lastSync,
		CreatedAt: createdAt,
	}, nil
}

// PushEntries upserts entries for a user. Uses "last write wins" based on updated_at.
func (s *Store) PushEntries(token string, entries []SyncEntry) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Inline token validation (can't call ValidateToken which needs RLock)
	var tokenCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE token = ?", token).Scan(&tokenCount); err != nil || tokenCount == 0 {
		return 0, fmt.Errorf("无效的token")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	upserted := 0

	for _, entry := range entries {
		// Set timestamps if not provided
		if entry.CreatedAt == 0 {
			entry.CreatedAt = now
		}
		if entry.UpdatedAt == 0 {
			entry.UpdatedAt = now
		}

		// Check if entry exists and compare timestamps (last write wins)
		var existingUpdatedAt int64
		var existingDeleted int
		err := tx.QueryRow(
			"SELECT updated_at, deleted FROM sync_entries WHERE id = ? AND token = ?",
			entry.ID, token,
		).Scan(&existingUpdatedAt, &existingDeleted)

		if err == sql.ErrNoRows {
			// New entry - insert
			deleted := 0
			if entry.Deleted {
				deleted = 1
			}
			_, err := tx.Exec(
				`INSERT INTO sync_entries (id, token, word, result, created_at, updated_at, deleted)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				entry.ID, token, entry.Word, entry.Result,
				entry.CreatedAt, entry.UpdatedAt, deleted,
			)
			if err != nil {
				log.Printf("Warning: insert entry %s failed: %v", entry.ID, err)
				continue
			}
			upserted++
		} else if err == nil {
			// Existing entry - only update if incoming is newer
			if entry.UpdatedAt > existingUpdatedAt {
				deleted := 0
				if entry.Deleted {
					deleted = 1
				}
				_, err := tx.Exec(
					`UPDATE sync_entries
					 SET word = ?, result = ?, updated_at = ?, deleted = ?
					 WHERE id = ? AND token = ?`,
					entry.Word, entry.Result, entry.UpdatedAt, deleted,
					entry.ID, token,
				)
				if err != nil {
					log.Printf("Warning: update entry %s failed: %v", entry.ID, err)
					continue
				}
				upserted++
			}
			// If existing is newer, skip (server wins)
		}
	}

	// Update user's last_sync time
	_, err = tx.Exec(
		"UPDATE users SET last_sync = ? WHERE token = ?",
		now, token,
	)
	if err != nil {
		log.Printf("Warning: update last_sync failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交事务失败: %v", err)
	}

	return upserted, nil
}

// PullEntries returns entries updated after the given timestamp.
// If since is 0, returns all non-deleted entries.
func (s *Store) PullEntries(token string, since int64) ([]SyncEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Inline token validation
	var tokenCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE token = ?", token).Scan(&tokenCount); err != nil || tokenCount == 0 {
		return nil, fmt.Errorf("无效的token")
	}

	var rows *sql.Rows
	var err error

	if since > 0 {
		rows, err = s.db.Query(
			`SELECT id, word, result, created_at, updated_at, deleted
			 FROM sync_entries
			 WHERE token = ? AND updated_at > ?
			 ORDER BY updated_at ASC`,
			token, since,
		)
	} else {
		// Initial sync: return all non-deleted entries
		rows, err = s.db.Query(
			`SELECT id, word, result, created_at, updated_at, deleted
			 FROM sync_entries
			 WHERE token = ? AND deleted = 0
			 ORDER BY updated_at ASC`,
			token,
		)
	}

	if err != nil {
		return nil, fmt.Errorf("查询失败: %v", err)
	}
	defer rows.Close()

	var entries []SyncEntry
	for rows.Next() {
		var e SyncEntry
		var deleted int
		err := rows.Scan(&e.ID, &e.Word, &e.Result, &e.CreatedAt, &e.UpdatedAt, &deleted)
		if err != nil {
			log.Printf("Warning: scan entry failed: %v", err)
			continue
		}
		e.Deleted = deleted == 1
		entries = append(entries, e)
	}

	return entries, nil
}

// DeleteEntry soft-deletes an entry by ID
func (s *Store) DeleteEntry(token, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	_, err := s.db.Exec(
		"UPDATE sync_entries SET deleted = 1, updated_at = ? WHERE id = ? AND token = ?",
		now, id, token,
	)
	return err
}

// CleanDeleted permanently removes soft-deleted entries older than the given duration
func (s *Store) CleanDeleted(olderThan time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()
	result, err := s.db.Exec(
		"DELETE FROM sync_entries WHERE deleted = 1 AND updated_at < ?",
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// ============================================================
// Auth Session methods (QR code login flow)
// ============================================================

// GenerateScene creates a random 8-character scene string (uppercase + digits)
func GenerateScene() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成scene失败: %v", err)
	}
	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return string(b), nil
}

// CreateAuthSession creates a new auth session with a random scene value.
// Returns the scene string. The session expires after ttl.
func (s *Store) CreateAuthSession(ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	scene, err := GenerateScene()
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()
	expiresAt := now + int64(ttl.Seconds())

	_, err = s.db.Exec(
		"INSERT INTO auth_sessions (scene, status, token, openid, created_at, expires_at) VALUES (?, 'pending', '', '', ?, ?)",
		scene, now, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("创建auth session失败: %v", err)
	}

	log.Printf("Auth session created: scene=%s, expires_in=%ds", scene, int(ttl.Seconds()))
	return scene, nil
}

// GetAuthSession returns the auth session by scene.
// Returns nil if not found or expired.
func (s *Store) GetAuthSession(scene string) (*AuthSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sess AuthSession
	err := s.db.QueryRow(
		"SELECT scene, status, token, openid, created_at, expires_at FROM auth_sessions WHERE scene = ?",
		scene,
	).Scan(&sess.Scene, &sess.Status, &sess.Token, &sess.OpenID, &sess.CreatedAt, &sess.ExpiresAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query auth session failed: %v", err)
	}

	// Check expiration
	if time.Now().Unix() > sess.ExpiresAt {
		sess.Status = "expired"
	}

	return &sess, nil
}

// CompleteAuthSession marks an auth session as scanned and binds it to a user token.
func (s *Store) CompleteAuthSession(scene, token, openid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify session exists and is still pending
	var status string
	var expiresAt int64
	err := s.db.QueryRow(
		"SELECT status, expires_at FROM auth_sessions WHERE scene = ?",
		scene,
	).Scan(&status, &expiresAt)

	if err == sql.ErrNoRows {
		return fmt.Errorf("session not found")
	}
	if err != nil {
		return fmt.Errorf("query session failed: %v", err)
	}
	if time.Now().Unix() > expiresAt {
		return fmt.Errorf("session expired")
	}

	_, err = s.db.Exec(
		"UPDATE auth_sessions SET status = 'scanned', token = ?, openid = ? WHERE scene = ?",
		token, openid, scene,
	)
	if err != nil {
		return fmt.Errorf("update auth session failed: %v", err)
	}

	log.Printf("Auth session completed: scene=%s, token=%s..., openid=%s...", scene, token[:8], openid[:min(8, len(openid))])
	return nil
}

// FindOrCreateUserByOpenID finds an existing user by openid, or creates a new one.
// Returns the user's token.
func (s *Store) FindOrCreateUserByOpenID(openid string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Try to find existing user by openid
	var token string
	err := s.db.QueryRow(
		"SELECT token FROM users WHERE openid = ?",
		openid,
	).Scan(&token)

	if err == nil {
		// Existing user found
		log.Printf("Existing user found for openid=%s..., token=%s...", openid[:min(8, len(openid))], token[:8])
		return token, nil
	}

	if err != sql.ErrNoRows {
		return "", fmt.Errorf("query user by openid failed: %v", err)
	}

	// Create new user
	newToken, err := GenerateToken()
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()
	_, err = s.db.Exec(
		"INSERT INTO users (token, openid, created_at, last_sync) VALUES (?, ?, ?, ?)",
		newToken, openid, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("create user for openid failed: %v", err)
	}

	log.Printf("New user created for openid=%s..., token=%s...", openid[:min(8, len(openid))], newToken[:8])
	return newToken, nil
}

// cleanExpiredAuthSessions removes expired auth sessions from the database.
func (s *Store) cleanExpiredAuthSessions() {
	now := time.Now().Unix()
	result, err := s.db.Exec(
		"DELETE FROM auth_sessions WHERE expires_at < ?",
		now,	)
	if err != nil {
		log.Printf("Warning: clean expired auth sessions failed: %v", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("Cleaned %d expired auth sessions", affected)
	}
}
