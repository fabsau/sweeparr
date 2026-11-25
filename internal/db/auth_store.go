package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// --- Users ---

type User struct {
	ID                    string   `json:"id"`
	Username              string   `json:"username"`
	DisplayName           string   `json:"display_name"`
	Email                 string   `json:"email"`
	PasswordHash          string   `json:"-"`
	Role                  string   `json:"role"`
	AllowedServers        []string `json:"allowed_servers"`
	OIDCSubject           string   `json:"oidc_subject,omitempty"`
	OIDCIssuer            string   `json:"oidc_issuer,omitempty"`
	TOTPSecretEnc         string   `json:"-"`
	TOTPEnabled           bool     `json:"totp_enabled"`
	PasswordLoginDisabled bool     `json:"password_login_disabled"`
	PasskeyRequireMFA     bool     `json:"passkey_require_mfa"`
	CanCreateAPIKeys      bool     `json:"can_create_api_keys"`
	RequireMFA            bool     `json:"require_mfa"`
	RequirePasskey        bool     `json:"require_passkey"`
	MustChangePassword    bool     `json:"must_change_password"`
	MFACount              int      `json:"mfa_count"`
	PasskeyCount          int      `json:"passkey_count"`
	SessionCount          int      `json:"session_count"`
	CreatedAt             int64    `json:"created_at"`
}

const userSelectCols = `u.id, u.username, u.display_name, u.email, u.password_hash, u.role, u.allowed_servers_json, u.oidc_subject, u.oidc_issuer, u.totp_secret_enc, u.totp_enabled, u.password_login_disabled, u.passkey_require_mfa, u.can_create_api_keys, u.require_mfa, u.require_passkey, u.must_change_password, u.created_at,
	(SELECT COUNT(*) FROM user_mfa m WHERE m.user_id = u.id) AS mfa_count,
	(SELECT COUNT(*) FROM webauthn_credentials w WHERE w.user_id = u.id) AS passkey_count,
	(SELECT COUNT(*) FROM sessions s WHERE s.user_id = u.id AND s.expires_at > CAST(strftime('%s','now') AS INTEGER)) AS session_count`

func (d *DB) CountUsers() (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) CreateUser(u User) error {
	if u.AllowedServers == nil {
		u.AllowedServers = []string{}
	}
	srvJSON, _ := json.Marshal(u.AllowedServers)
	_, err := d.sql.Exec(
		`INSERT INTO users(id, username, display_name, email, password_hash, role, allowed_servers_json, oidc_subject, oidc_issuer, totp_secret_enc, totp_enabled, password_login_disabled, passkey_require_mfa, can_create_api_keys, require_mfa, require_passkey, must_change_password, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.DisplayName, u.Email, u.PasswordHash, u.Role, string(srvJSON), u.OIDCSubject, u.OIDCIssuer, u.TOTPSecretEnc, boolInt(u.TOTPEnabled), boolInt(u.PasswordLoginDisabled), boolInt(u.PasskeyRequireMFA), boolInt(u.CanCreateAPIKeys), boolInt(u.RequireMFA), boolInt(u.RequirePasskey), boolInt(u.MustChangePassword), u.CreatedAt,
	)
	return err
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	return d.scanUser(d.sql.QueryRow(`SELECT `+userSelectCols+` FROM users u WHERE LOWER(u.username)=LOWER(?)`, username))
}

func (d *DB) GetUserByID(id string) (*User, error) {
	return d.scanUser(d.sql.QueryRow(`SELECT `+userSelectCols+` FROM users u WHERE u.id=?`, id))
}

func (d *DB) GetUserByOIDC(issuer, subject string) (*User, error) {
	return d.scanUser(d.sql.QueryRow(`SELECT `+userSelectCols+` FROM users u WHERE u.oidc_issuer=? AND u.oidc_subject=?`, issuer, subject))
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.sql.Query(`SELECT ` + userSelectCols + ` FROM users u ORDER BY u.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := d.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (d *DB) UpdateUserRole(id, role string) error {
	_, err := d.sql.Exec(`UPDATE users SET role=? WHERE id=?`, role, id)
	return err
}

func (d *DB) UpdateUserSecurityPolicy(id string, canCreateAPIKeys, requireMFA, requirePasskey bool) error {
	_, err := d.sql.Exec(`UPDATE users SET can_create_api_keys=?, require_mfa=?, require_passkey=? WHERE id=?`,
		boolInt(canCreateAPIKeys), boolInt(requireMFA), boolInt(requirePasskey), id)
	return err
}

func (d *DB) UpdateUserAllowedServers(id string, servers []string) error {
	if servers == nil {
		servers = []string{}
	}
	b, _ := json.Marshal(servers)
	_, err := d.sql.Exec(`UPDATE users SET allowed_servers_json=? WHERE id=?`, string(b), id)
	return err
}

func (d *DB) DeleteUser(id string) error {
	_, err := d.sql.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (d *DB) UpdateUserPassword(id, hash string) error {
	_, err := d.sql.Exec(`UPDATE users SET password_hash=?, must_change_password=0 WHERE id=?`, hash, id)
	return err
}

// AdminSetUserPassword sets a new password, re-enables password login, and optionally forces a change at next sign-in.
func (d *DB) AdminSetUserPassword(id, hash string, mustChange bool) error {
	_, err := d.sql.Exec(`UPDATE users SET password_hash=?, password_login_disabled=0, must_change_password=? WHERE id=?`, hash, boolInt(mustChange), id)
	return err
}

// ResetUserMFA removes all TOTP authenticators for a user.
func (d *DB) ResetUserMFA(id string) error {
	if _, err := d.sql.Exec(`DELETE FROM user_mfa WHERE user_id=?`, id); err != nil {
		return err
	}
	_, err := d.sql.Exec(`UPDATE users SET totp_enabled=0, totp_secret_enc='', passkey_require_mfa=0 WHERE id=?`, id)
	return err
}

// ResetUserPasskeys removes all passkeys and restores password sign-in so the user cannot be locked out.
func (d *DB) ResetUserPasskeys(id string) error {
	if _, err := d.sql.Exec(`DELETE FROM webauthn_credentials WHERE user_id=?`, id); err != nil {
		return err
	}
	_, err := d.sql.Exec(`UPDATE users SET password_login_disabled=0, passkey_require_mfa=0 WHERE id=?`, id)
	return err
}

func (d *DB) SetPasswordLoginDisabled(id string, disabled bool) error {
	_, err := d.sql.Exec(`UPDATE users SET password_login_disabled=? WHERE id=?`, boolInt(disabled), id)
	return err
}

func (d *DB) SetPasskeyRequireMFA(id string, req bool) error {
	_, err := d.sql.Exec(`UPDATE users SET passkey_require_mfa=? WHERE id=?`, boolInt(req), id)
	return err
}

func (d *DB) SetUserTOTP(id, secretEnc string, enabled bool) error {
	_, err := d.sql.Exec(`UPDATE users SET totp_secret_enc=?, totp_enabled=? WHERE id=?`, secretEnc, boolInt(enabled), id)
	return err
}

func (d *DB) scanUser(row scannerRow) (*User, error) {
	var u User
	var totpInt, pwDisInt, pkMfaInt, canKeyInt, reqMfaInt, reqPkInt, mustPwInt int
	var allowedRaw string
	err := row.Scan(
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash, &u.Role, &allowedRaw,
		&u.OIDCSubject, &u.OIDCIssuer, &u.TOTPSecretEnc, &totpInt, &pwDisInt, &pkMfaInt,
		&canKeyInt, &reqMfaInt, &reqPkInt, &mustPwInt, &u.CreatedAt,
		&u.MFACount, &u.PasskeyCount, &u.SessionCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(allowedRaw), &u.AllowedServers)
	if u.AllowedServers == nil {
		u.AllowedServers = []string{}
	}
	u.TOTPEnabled = totpInt != 0
	u.PasswordLoginDisabled = pwDisInt != 0
	u.PasskeyRequireMFA = pkMfaInt != 0
	u.CanCreateAPIKeys = canKeyInt != 0
	u.RequireMFA = reqMfaInt != 0
	u.RequirePasskey = reqPkInt != 0
	u.MustChangePassword = mustPwInt != 0
	return &u, nil
}

// --- Sessions ---

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	IPAddress  string
	MFAPending bool
	ExpiresAt  int64
	CreatedAt  int64
}

func (d *DB) CreateSession(s Session) error {
	_, err := d.sql.Exec(
		`INSERT INTO sessions(id, user_id, token_hash, ip_address, mfa_pending, expires_at, created_at) VALUES(?,?,?,?,?,?,?)`,
		s.ID, s.UserID, s.TokenHash, s.IPAddress, boolInt(s.MFAPending), s.ExpiresAt, s.CreatedAt,
	)
	return err
}

func (d *DB) GetSessionByTokenHash(hash string) (*Session, error) {
	var s Session
	var mfaInt int
	err := d.sql.QueryRow(`SELECT id, user_id, token_hash, ip_address, mfa_pending, expires_at, created_at FROM sessions WHERE token_hash=?`, hash).
		Scan(&s.ID, &s.UserID, &s.TokenHash, &s.IPAddress, &mfaInt, &s.ExpiresAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.MFAPending = mfaInt != 0
	return &s, nil
}

func (d *DB) CompleteSessionMFA(id string) error {
	_, err := d.sql.Exec(`UPDATE sessions SET mfa_pending=0 WHERE id=?`, id)
	return err
}

func (d *DB) DeleteSessionByTokenHash(hash string) error {
	_, err := d.sql.Exec(`DELETE FROM sessions WHERE token_hash=?`, hash)
	return err
}

func (d *DB) DeleteUserSessions(userID string) error {
	_, err := d.sql.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

func (d *DB) PruneExpiredSessions() {
	_, _ = d.sql.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
}

// --- WebAuthn Credentials ---

type StoredPasskey struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Label          string `json:"label"`
	CredentialJSON string `json:"-"`
	CreatedAt      int64  `json:"created_at"`
}

func (d *DB) AddPasskey(p StoredPasskey) error {
	_, err := d.sql.Exec(`INSERT INTO webauthn_credentials(id, user_id, label, credential_json, created_at) VALUES(?,?,?,?,?)`,
		p.ID, p.UserID, p.Label, p.CredentialJSON, p.CreatedAt)
	return err
}

func (d *DB) ListPasskeysByUser(userID string) ([]StoredPasskey, error) {
	rows, err := d.sql.Query(`SELECT id, user_id, label, credential_json, created_at FROM webauthn_credentials WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StoredPasskey{}
	for rows.Next() {
		var p StoredPasskey
		if err := rows.Scan(&p.ID, &p.UserID, &p.Label, &p.CredentialJSON, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) UpdatePasskeyJSON(id, credJSON string) error {
	_, err := d.sql.Exec(`UPDATE webauthn_credentials SET credential_json=? WHERE id=?`, credJSON, id)
	return err
}

func (d *DB) RenamePasskey(userID, id, label string) error {
	_, err := d.sql.Exec(`UPDATE webauthn_credentials SET label=? WHERE id=? AND user_id=?`, label, id, userID)
	return err
}

func (d *DB) DeletePasskey(userID, id string) error {
	_, err := d.sql.Exec(`DELETE FROM webauthn_credentials WHERE id=? AND user_id=?`, id, userID)
	return err
}

// --- Multi-TOTP MFA Authenticators ---

type UserMFA struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	SecretEnc string `json:"-"`
	CreatedAt int64  `json:"created_at"`
}

func (d *DB) AddUserMFA(m UserMFA) error {
	if _, err := d.sql.Exec(`INSERT INTO user_mfa(id, user_id, name, secret_enc, created_at) VALUES(?,?,?,?,?)`,
		m.ID, m.UserID, m.Name, m.SecretEnc, m.CreatedAt); err != nil {
		return err
	}
	_, err := d.sql.Exec(`UPDATE users SET totp_enabled=1 WHERE id=?`, m.UserID)
	return err
}

func (d *DB) ListUserMFA(userID string) ([]UserMFA, error) {
	rows, err := d.sql.Query(`SELECT id, user_id, name, secret_enc, created_at FROM user_mfa WHERE user_id=? ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserMFA{}
	for rows.Next() {
		var m UserMFA
		if err := rows.Scan(&m.ID, &m.UserID, &m.Name, &m.SecretEnc, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) RenameUserMFA(userID, id, name string) error {
	_, err := d.sql.Exec(`UPDATE user_mfa SET name=? WHERE id=? AND user_id=?`, name, id, userID)
	return err
}

func (d *DB) DeleteUserMFA(userID, id string) error {
	if _, err := d.sql.Exec(`DELETE FROM user_mfa WHERE id=? AND user_id=?`, id, userID); err != nil {
		return err
	}
	var cnt int
	_ = d.sql.QueryRow(`SELECT COUNT(*) FROM user_mfa WHERE user_id=?`, userID).Scan(&cnt)
	if cnt == 0 {
		_, _ = d.sql.Exec(`UPDATE users SET totp_enabled=0, totp_secret_enc='' WHERE id=?`, userID)
	}
	return nil
}

// --- API Keys ---

type APIKey struct {
	ID               string   `json:"id"`
	UserID           string   `json:"user_id"`
	Username         string   `json:"username,omitempty"`
	Name             string   `json:"name"`
	Prefix           string   `json:"prefix"`
	KeyHash          string   `json:"-"`
	Scopes           []string `json:"scopes"`
	AllowedServers   []string `json:"allowed_servers"`
	AllowedLibraries []string `json:"allowed_libraries"`
	ExpiresAt        int64    `json:"expires_at"`
	LastUsedAt       int64    `json:"last_used_at"`
	CreatedAt        int64    `json:"created_at"`
}

func (d *DB) CreateAPIKey(k APIKey) error {
	if k.AllowedServers == nil {
		k.AllowedServers = []string{}
	}
	if k.AllowedLibraries == nil {
		k.AllowedLibraries = []string{}
	}
	b, _ := json.Marshal(k.Scopes)
	sb, _ := json.Marshal(k.AllowedServers)
	lb, _ := json.Marshal(k.AllowedLibraries)
	_, err := d.sql.Exec(`INSERT INTO api_keys(id, user_id, name, prefix, key_hash, scopes_json, allowed_servers_json, allowed_libraries_json, expires_at, last_used_at, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		k.ID, k.UserID, k.Name, k.Prefix, k.KeyHash, string(b), string(sb), string(lb), k.ExpiresAt, k.LastUsedAt, k.CreatedAt)
	return err
}

func (d *DB) GetAPIKeyByHash(hash string) (*APIKey, error) {
	var k APIKey
	var scopesRaw, serversRaw, libsRaw string
	err := d.sql.QueryRow(`SELECT id, user_id, name, prefix, key_hash, scopes_json, allowed_servers_json, allowed_libraries_json, expires_at, last_used_at, created_at FROM api_keys WHERE key_hash=?`, hash).
		Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.KeyHash, &scopesRaw, &serversRaw, &libsRaw, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesRaw), &k.Scopes)
	_ = json.Unmarshal([]byte(serversRaw), &k.AllowedServers)
	_ = json.Unmarshal([]byte(libsRaw), &k.AllowedLibraries)
	if k.AllowedServers == nil {
		k.AllowedServers = []string{}
	}
	if k.AllowedLibraries == nil {
		k.AllowedLibraries = []string{}
	}
	return &k, nil
}

func (d *DB) TouchAPIKey(id string) {
	_, _ = d.sql.Exec(`UPDATE api_keys SET last_used_at=? WHERE id=?`, time.Now().Unix(), id)
}

func (d *DB) scanAPIKeyRows(rows *sql.Rows) ([]APIKey, error) {
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var scopesRaw, serversRaw, libsRaw string
		if err := rows.Scan(&k.ID, &k.UserID, &k.Username, &k.Name, &k.Prefix, &scopesRaw, &serversRaw, &libsRaw, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesRaw), &k.Scopes)
		_ = json.Unmarshal([]byte(serversRaw), &k.AllowedServers)
		_ = json.Unmarshal([]byte(libsRaw), &k.AllowedLibraries)
		if k.AllowedServers == nil {
			k.AllowedServers = []string{}
		}
		if k.AllowedLibraries == nil {
			k.AllowedLibraries = []string{}
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (d *DB) ListAPIKeys(userID string) ([]APIKey, error) {
	rows, err := d.sql.Query(`SELECT k.id, k.user_id, COALESCE(u.username,''), k.name, k.prefix, k.scopes_json, k.allowed_servers_json, k.allowed_libraries_json, k.expires_at, k.last_used_at, k.created_at
		FROM api_keys k LEFT JOIN users u ON u.id = k.user_id WHERE k.user_id=? ORDER BY k.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return d.scanAPIKeyRows(rows)
}

func (d *DB) ListAllAPIKeys() ([]APIKey, error) {
	rows, err := d.sql.Query(`SELECT k.id, k.user_id, COALESCE(u.username,''), k.name, k.prefix, k.scopes_json, k.allowed_servers_json, k.allowed_libraries_json, k.expires_at, k.last_used_at, k.created_at
		FROM api_keys k LEFT JOIN users u ON u.id = k.user_id ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return d.scanAPIKeyRows(rows)
}

func (d *DB) RenameAPIKey(id, userID string, isAdmin bool, name string) error {
	if isAdmin {
		_, err := d.sql.Exec(`UPDATE api_keys SET name=? WHERE id=?`, name, id)
		return err
	}
	_, err := d.sql.Exec(`UPDATE api_keys SET name=? WHERE id=? AND user_id=?`, name, id, userID)
	return err
}

func (d *DB) DeleteAPIKey(userID, id string) error {
	_, err := d.sql.Exec(`DELETE FROM api_keys WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (d *DB) DeleteAPIKeyByID(id string) error {
	_, err := d.sql.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	return err
}

func (d *DB) DeleteUserAPIKeys(userID string) error {
	_, err := d.sql.Exec(`DELETE FROM api_keys WHERE user_id=?`, userID)
	return err
}

func (d *DB) LinkUserOIDC(userID, issuer, subject string) error {
	_, err := d.sql.Exec(`UPDATE users SET oidc_issuer=?, oidc_subject=? WHERE id=?`, issuer, subject, userID)
	return err
}

func (d *DB) RevokeUserOIDC(userID string) error {
	_, err := d.sql.Exec(`UPDATE users SET oidc_issuer='', oidc_subject='' WHERE id=?`, userID)
	return err
}

func (d *DB) GetAppSetting(key string) string {
	var val string
	_ = d.sql.QueryRow(`SELECT value FROM app_settings WHERE key=?`, key).Scan(&val)
	return val
}

func (d *DB) SetAppSetting(key, val string) error {
	_, err := d.sql.Exec(`INSERT INTO app_settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, val)
	return err
}
