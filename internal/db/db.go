package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=cache_size(-32000)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(134217728)&_pragma=foreign_keys(ON)&_pragma=secure_delete(ON)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(4) // SQLite WAL concurrent reads + busy_timeout write serialization
	conn.SetMaxIdleConns(4)
	d := &DB{sql: conn}
	if err := d.migrate(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if path != "" && path != ":memory:" && !strings.HasPrefix(path, "file::memory:") {
		_ = os.Chmod(path, 0o600)
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'viewer',
			oidc_subject TEXT NOT NULL DEFAULT '',
			oidc_issuer TEXT NOT NULL DEFAULT '',
			totp_secret_enc TEXT NOT NULL DEFAULT '',
			totp_enabled INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_users_oidc ON users(oidc_issuer, oidc_subject);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			mfa_pending INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS webauthn_credentials (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			label TEXT NOT NULL DEFAULT 'Passkey',
			credential_json TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			scopes_json TEXT NOT NULL DEFAULT '["read"]',
			allowed_servers_json TEXT NOT NULL DEFAULT '[]',
			allowed_libraries_json TEXT NOT NULL DEFAULT '[]',
			last_used_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS servers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'plex',
			url TEXT NOT NULL,
			token_enc TEXT NOT NULL DEFAULT '',
			ssl_verify INTEGER NOT NULL DEFAULT 1,
			env_managed INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			config_json TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS items (
			server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			item_key TEXT NOT NULL,
			rating_key TEXT NOT NULL,
			library TEXT NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			grandparent_title TEXT NOT NULL DEFAULT '',
			parent_title TEXT NOT NULL DEFAULT '',
			parent_index INTEGER NOT NULL DEFAULT 0,
			item_index INTEGER NOT NULL DEFAULT 0,
			bytes INTEGER NOT NULL DEFAULT 0,
			parts_json TEXT NOT NULL DEFAULT '[]',
			missing_json TEXT NOT NULL DEFAULT '[]',
			miss_loudness INTEGER NOT NULL DEFAULT 0,
			miss_voice INTEGER NOT NULL DEFAULT 0,
			miss_chapters INTEGER NOT NULL DEFAULT 0,
			miss_intro INTEGER NOT NULL DEFAULT 0,
			miss_credits INTEGER NOT NULL DEFAULT 0,
			miss_ads INTEGER NOT NULL DEFAULT 0,
			miss_previews INTEGER NOT NULL DEFAULT 0,
			miss_deep_analysis INTEGER NOT NULL DEFAULT 0,
			miss_sonic INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			priority_pos INTEGER NOT NULL DEFAULT 0,
			skipped INTEGER NOT NULL DEFAULT 0,
			next_retry_at INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			added_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (server_id, item_key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_items_server_status ON items(server_id, status, library);`,
		`CREATE INDEX IF NOT EXISTS idx_items_server_prio ON items(server_id, priority_pos);`,
		`CREATE INDEX IF NOT EXISTS idx_items_server_next ON items(server_id, skipped, status, next_retry_at, priority_pos);`,
		`CREATE INDEX IF NOT EXISTS idx_items_server_group ON items(server_id, library, grandparent_title, parent_title);`,
		`CREATE TABLE IF NOT EXISTS reservations (
			server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			part_key TEXT NOT NULL,
			bytes INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			PRIMARY KEY (server_id, part_key)
		);`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id TEXT NOT NULL DEFAULT '',
			ts INTEGER NOT NULL,
			level TEXT NOT NULL,
			category TEXT NOT NULL,
			message TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_server_id ON events(server_id, id DESC);`,
		`CREATE TABLE IF NOT EXISTS library_prefs (
			server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			library TEXT NOT NULL,
			disabled_caps_json TEXT NOT NULL DEFAULT '[]',
			PRIMARY KEY (server_id, library)
		);`,
		`CREATE TABLE IF NOT EXISTS user_mfa (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL DEFAULT 'Authenticator',
			secret_enc TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS scan_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			item_key TEXT NOT NULL,
			title TEXT NOT NULL,
			library TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_scan_history_server ON scan_history(server_id, id DESC);`,
	}
	for _, s := range stmts {
		if _, err := d.sql.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Idempotent column additions for existing installations
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN password_login_disabled INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN passkey_require_mfa INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN allowed_servers_json TEXT NOT NULL DEFAULT '[]'`)
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN can_create_api_keys INTEGER NOT NULL DEFAULT 1`)
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN require_mfa INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN require_passkey INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE sessions ADD COLUMN ip_address TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE api_keys ADD COLUMN allowed_servers_json TEXT NOT NULL DEFAULT '[]'`)
	_, _ = d.sql.Exec(`ALTER TABLE api_keys ADD COLUMN allowed_libraries_json TEXT NOT NULL DEFAULT '[]'`)
	_, _ = d.sql.Exec(`ALTER TABLE api_keys ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE items ADD COLUMN collection TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE items ADD COLUMN is_dvr INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE items ADD COLUMN queued_by TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE items ADD COLUMN queued_reason TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE items ADD COLUMN blocklisted_by TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE items ADD COLUMN blocklisted_at INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE scan_history ADD COLUMN added_caps_json TEXT NOT NULL DEFAULT '[]'`)
	_, _ = d.sql.Exec(`ALTER TABLE scan_history ADD COLUMN missing_caps_json TEXT NOT NULL DEFAULT '[]'`)
	_, _ = d.sql.Exec(`ALTER TABLE scan_history ADD COLUMN bytes INTEGER NOT NULL DEFAULT 0`)
	_, _ = d.sql.Exec(`ALTER TABLE scan_history ADD COLUMN queued_by TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE scan_history ADD COLUMN queued_reason TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE events ADD COLUMN username TEXT NOT NULL DEFAULT ''`)
	_, _ = d.sql.Exec(`ALTER TABLE library_prefs ADD COLUMN cap_modes_json TEXT NOT NULL DEFAULT '{}'`)
	_, _ = d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`)
	_, _ = d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_passkeys_user ON webauthn_credentials(user_id)`)
	_, _ = d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id)`)
	_, _ = d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_items_server_skipped ON items(server_id, skipped, updated_at DESC)`)
	return nil
}

type scannerRow interface {
	Scan(dest ...any) error
}

func appendInClause(clauses *[]string, args *[]any, col string, values []string) {
	if len(values) == 0 {
		return
	}
	phs := make([]string, len(values))
	for i, v := range values {
		phs[i] = "?"
		*args = append(*args, strings.TrimSpace(v))
	}
	*clauses = append(*clauses, col+" IN ("+strings.Join(phs, ",")+")")
}

func clampPagination(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 250 {
		pageSize = 50
	}
	return page, pageSize
}

func calcTotalPages(total, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 1
	}
	return (total + pageSize - 1) / pageSize
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
