package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/crypto"
	"github.com/fabsau/sweeparr/internal/db"
)

const (
	SessionCookieName = "sweeparr_session"
	RoleAdmin         = "admin"
	RoleOperator      = "operator"
	RoleViewer        = "viewer"
)

type ctxKey string

const (
	ctxUserKey       ctxKey = "user"
	ctxScopesKey     ctxKey = "scopes"
	ctxKeyServersKey ctxKey = "key_servers"
	ctxKeyLibsKey    ctxKey = "key_libraries"
)

type WebAuthnChallenge struct {
	SessionData webauthn.SessionData
	UserID      string
	Label       string
	ExpiresAt   time.Time
}

type OIDCState struct {
	Nonce      string
	LinkUserID string
	ExpiresAt  time.Time
}

type Service struct {
	db       *db.DB
	cfg      config.EnvConfig
	wa       *webauthn.WebAuthn
	oidcProv *oidc.Provider
	oidcVer  *oidc.IDTokenVerifier
	oauthCfg *oauth2.Config

	mu          sync.Mutex
	waSessions  map[string]WebAuthnChallenge
	oidcStates  map[string]OIDCState
	pendingTOTP map[string]string
}

func (s *Service) Env() config.EnvConfig {
	return s.cfg
}

func NewService(database *db.DB, cfg config.EnvConfig) (*Service, error) {
	wCfg := &webauthn.Config{
		RPDisplayName: cfg.RPDisplayName,
		RPID:          cfg.RPID,
		RPOrigins:     cfg.RPOrigins,
	}
	wa, err := webauthn.New(wCfg)
	if err != nil {
		return nil, fmt.Errorf("init webauthn: %w", err)
	}
	s := &Service{
		db:          database,
		cfg:         cfg,
		wa:          wa,
		waSessions:  map[string]WebAuthnChallenge{},
		oidcStates:  map[string]OIDCState{},
		pendingTOTP: map[string]string{},
	}
	if cfg.OIDCEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		prov, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
		if err != nil {
			slog.Warn("failed to initialize OIDC provider", "issuer", cfg.OIDCIssuer, "error", err)
		} else {
			slog.Info("initialized OIDC provider", "issuer", cfg.OIDCIssuer, "admin_groups", cfg.OIDCAdminGroups)
			s.oidcProv = prov
			s.oidcVer = prov.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
			s.oauthCfg = &oauth2.Config{
				ClientID:     cfg.OIDCClientID,
				ClientSecret: cfg.OIDCClientSecret,
				RedirectURL:  cfg.OIDCRedirectURL,
				Endpoint:     prov.Endpoint(),
				Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
			}
		}
	} else {
		s.loadStoredOIDC()
	}
	return s, nil
}

func (s *Service) OIDCConfigured() bool {
	return s.oauthCfg != nil && s.oidcVer != nil
}

// --- Password & Initial Setup ---

func (s *Service) NeedsSetup() (bool, error) {
	n, err := s.db.CountUsers()
	return n == 0, err
}

func (s *Service) SetupAdmin(username, password, displayName string, ip ...string) (*db.User, string, error) {
	needs, err := s.NeedsSetup()
	if err != nil {
		return nil, "", err
	}
	if !needs {
		return nil, "", errors.New("initial setup already completed")
	}
	username = strings.TrimSpace(username)
	if len(username) < 2 {
		return nil, "", errors.New("username (>=2 chars) is required")
	}
	if err := ValidatePasswordStrength(password, username); err != nil {
		return nil, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, "", err
	}
	u := db.User{
		ID:               uuid.NewString(),
		Username:         username,
		DisplayName:      strings.TrimSpace(displayName),
		PasswordHash:     string(hash),
		Role:             RoleAdmin,
		CanCreateAPIKeys: true,
		CreatedAt:        time.Now().Unix(),
	}
	if err := s.db.CreateUser(u); err != nil {
		return nil, "", err
	}
	rawTok, _, err := s.issueSession(u.ID, false, ip...)
	return &u, rawTok, err
}

// CanAccessServer enforces per-server RBAC.
// Admins always have access to all servers. Operators and Viewers with an empty AllowedServers list
// (or containing "*") have access to all servers; otherwise access is strictly restricted to listed server IDs.
func CanAccessServer(u *db.User, serverID string) bool {
	if u == nil || strings.TrimSpace(serverID) == "" {
		return false
	}
	if u.Role == RoleAdmin {
		return true
	}
	if len(u.AllowedServers) == 0 {
		return true
	}
	for _, id := range u.AllowedServers {
		if id == "*" || id == serverID {
			return true
		}
	}
	return false
}

func (s *Service) CreateUser(username, password, displayName, email, role string, allowedServers ...[]string) (*db.User, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 {
		return nil, errors.New("username (>=2 chars) is required")
	}
	if err := ValidatePasswordStrength(password, username); err != nil {
		return nil, err
	}
	if role != RoleAdmin && role != RoleOperator && role != RoleViewer {
		role = RoleViewer
	}
	var allowed []string
	if len(allowedServers) > 0 && role != RoleAdmin {
		allowed = config.SanitizeStringSlice(allowedServers[0], 128, 64)
	}
	if allowed == nil {
		allowed = []string{}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	u := db.User{
		ID:               uuid.NewString(),
		Username:         username,
		DisplayName:      strings.TrimSpace(displayName),
		Email:            strings.TrimSpace(email),
		PasswordHash:     string(hash),
		Role:             role,
		AllowedServers:   allowed,
		CanCreateAPIKeys: role != RoleViewer,
		CreatedAt:        time.Now().Unix(),
	}
	if err := s.db.CreateUser(u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) LoginPassword(username, password string, ip ...string) (*db.User, string, bool, error) {
	u, err := s.db.GetUserByUsername(strings.TrimSpace(username))
	if err != nil || u == nil || u.PasswordHash == "" || u.PasswordLoginDisabled {
		return nil, "", false, errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, "", false, errors.New("invalid credentials")
	}
	rawTok, _, err := s.issueSession(u.ID, u.TOTPEnabled, ip...)
	return u, rawTok, u.TOTPEnabled, err
}

func (s *Service) VerifyUserPassword(u *db.User, password string) error {
	if u == nil || u.PasswordLoginDisabled || u.PasswordHash == "" {
		return nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return errors.New("invalid credentials")
	}
	return nil
}

func (s *Service) ChangePassword(userID, currentPassword, newPassword, mfaCode, passkeyChallengeID string, passkeyAssertion []byte, r *http.Request) error {
	u, err := s.db.GetUserByID(userID)
	if err != nil || u == nil {
		return errors.New("user not found")
	}
	if err := ValidatePasswordStrength(newPassword, u.Username); err != nil {
		return err
	}
	if u.PasswordHash != "" && !u.PasswordLoginDisabled {
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(currentPassword)); err != nil {
			return errors.New("current password is incorrect")
		}
	}
	pks, _ := s.db.ListPasskeysByUser(userID)
	mfas, _ := s.db.ListUserMFA(userID)
	if u.TOTPEnabled || len(mfas) > 0 || len(pks) > 0 {
		verified := false
		if strings.TrimSpace(mfaCode) != "" && s.VerifyTOTPCode(u, mfaCode) {
			verified = true
		}
		if !verified && strings.TrimSpace(passkeyChallengeID) != "" && len(passkeyAssertion) > 0 && r != nil {
			if s.FinishPasskeyStepUp(u, passkeyChallengeID, passkeyAssertion, r) == nil {
				verified = true
			}
		}
		if !verified {
			return errors.New("2FA or passkey verification is required to change password")
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}
	if err := s.db.UpdateUserPassword(userID, string(hash)); err != nil {
		return err
	}
	_ = s.db.DeleteUserSessions(userID)
	return nil
}

func (s *Service) pruneChallengesLocked() {
	now := time.Now()
	for k, v := range s.waSessions {
		if now.After(v.ExpiresAt) || len(s.waSessions) > 512 {
			delete(s.waSessions, k)
		}
	}
	for k, v := range s.oidcStates {
		if now.After(v.ExpiresAt) || len(s.oidcStates) > 512 {
			delete(s.oidcStates, k)
		}
	}
}

func (s *Service) issueSession(userID string, mfaPending bool, ip ...string) (string, *db.Session, error) {
	s.db.PruneExpiredSessions()
	raw, hash, err := crypto.RandomToken(32)
	if err != nil {
		return "", nil, err
	}
	clientIP := ""
	if len(ip) > 0 {
		clientIP = ip[0]
	}
	sess := db.Session{
		ID:         uuid.NewString(),
		UserID:     userID,
		TokenHash:  hash,
		IPAddress:  clientIP,
		MFAPending: mfaPending,
		ExpiresAt:  time.Now().Add(s.cfg.SessionTTL).Unix(),
		CreatedAt:  time.Now().Unix(),
	}
	if err := s.db.CreateSession(sess); err != nil {
		return "", nil, err
	}
	return raw, &sess, nil
}

func (s *Service) IsSessionIPBindingEnabled() bool {
	v := strings.TrimSpace(s.db.GetAppSetting("session_ip_binding"))
	if v == "false" || v == "0" {
		return false
	}
	return true
}

func RequestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peerIP := net.ParseIP(host)
	if peerIP != nil && (peerIP.IsLoopback() || peerIP.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if candidate := strings.TrimSpace(strings.Split(xff, ",")[0]); net.ParseIP(candidate) != nil {
				return candidate
			}
		}
		if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(xrip) != nil {
			return xrip
		}
	}
	return host
}

func secureSessionCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Service) SetSessionCookie(w http.ResponseWriter, r *http.Request, rawToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureSessionCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
}

func (s *Service) ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		_ = s.db.DeleteSessionByTokenHash(crypto.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureSessionCookie(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// --- TOTP MFA ---

func (s *Service) BeginTOTPEnroll(u *db.User) (secret string, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Sweeparr",
		AccountName: u.Username,
	})
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	s.pendingTOTP[u.ID] = key.Secret()
	s.mu.Unlock()
	return key.Secret(), key.URL(), nil
}

func (s *Service) ConfirmTOTPEnroll(u *db.User, name, code string) (*db.UserMFA, error) {
	s.mu.Lock()
	secret := s.pendingTOTP[u.ID]
	s.mu.Unlock()
	if secret == "" {
		return nil, errors.New("no pending MFA enrollment")
	}
	if !totp.Validate(strings.TrimSpace(code), secret) {
		return nil, errors.New("invalid 6-digit authenticator code")
	}
	enc, err := crypto.Encrypt(s.cfg.MasterKey, secret)
	if err != nil {
		return nil, err
	}
	label := strings.TrimSpace(name)
	if label == "" {
		label = "Authenticator"
	}
	rec := db.UserMFA{
		ID:        uuid.NewString(),
		UserID:    u.ID,
		Name:      label,
		SecretEnc: enc,
		CreatedAt: time.Now().Unix(),
	}
	if err := s.db.AddUserMFA(rec); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.pendingTOTP, u.ID)
	s.mu.Unlock()
	return &rec, nil
}

func (s *Service) VerifyTOTPCode(u *db.User, code string) bool {
	if u == nil {
		return false
	}
	clean := strings.TrimSpace(code)
	if clean == "" {
		return false
	}
	mfas, err := s.db.ListUserMFA(u.ID)
	if err == nil {
		for _, m := range mfas {
			if sec, err := crypto.Decrypt(s.cfg.MasterKey, m.SecretEnc); err == nil && totp.Validate(clean, sec) {
				return true
			}
		}
	}
	return false
}

func (s *Service) VerifyLoginTOTP(r *http.Request, code string) (*db.User, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return nil, errors.New("authentication failed")
	}
	sess, err := s.db.GetSessionByTokenHash(crypto.HashToken(c.Value))
	if err != nil || sess == nil || sess.ExpiresAt < time.Now().Unix() {
		return nil, errors.New("authentication failed")
	}
	u, err := s.db.GetUserByID(sess.UserID)
	if err != nil || u == nil || !u.TOTPEnabled {
		return nil, errors.New("authentication failed")
	}
	if !s.VerifyTOTPCode(u, code) {
		return nil, errors.New("invalid authenticator code")
	}
	if err := s.db.CompleteSessionMFA(sess.ID); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Service) DisableTOTP(u *db.User, code string) error {
	if !u.TOTPEnabled {
		return nil
	}
	if !s.VerifyTOTPCode(u, code) {
		return errors.New("invalid authenticator code")
	}
	mfas, _ := s.db.ListUserMFA(u.ID)
	for _, m := range mfas {
		_ = s.db.DeleteUserMFA(u.ID, m.ID)
	}
	return s.db.SetUserTOTP(u.ID, "", false)
}

// --- API Keys & Middleware ---

func (s *Service) CreateAPIKey(userID, name string, scopes []string, allowedServers []string, allowedLibraries []string, expiresAt ...int64) (rawKey string, rec db.APIKey, err error) {
	if strings.TrimSpace(name) == "" {
		return "", db.APIKey{}, errors.New("API key name is required")
	}
	validScopes := map[string]bool{"read": true, "scan": true, "webhook": true, "admin": true}
	var cleanScopes []string
	seen := map[string]bool{}
	for _, sc := range scopes {
		sc = strings.ToLower(strings.TrimSpace(sc))
		if validScopes[sc] && !seen[sc] {
			seen[sc] = true
			cleanScopes = append(cleanScopes, sc)
		}
	}
	if len(cleanScopes) == 0 {
		return "", db.APIKey{}, errors.New("at least one API key scope is required")
	}
	scopes = cleanScopes
	servers := config.SanitizeStringSlice(allowedServers, 128, 64)
	if servers == nil {
		servers = []string{}
	}
	libs := config.SanitizeStringSlice(allowedLibraries, 256, 128)
	if libs == nil {
		libs = []string{}
	}
	var exp int64
	if len(expiresAt) > 0 && expiresAt[0] > 0 {
		exp = expiresAt[0]
	}
	randPart, _, err := crypto.RandomToken(24)
	if err != nil {
		return "", db.APIKey{}, err
	}
	rawKey = "swp_" + randPart
	rec = db.APIKey{
		ID:               uuid.NewString(),
		UserID:           userID,
		Name:             strings.TrimSpace(name),
		Prefix:           rawKey[:min(7, len(rawKey))],
		KeyHash:          crypto.HashToken(rawKey),
		Scopes:           scopes,
		AllowedServers:   servers,
		AllowedLibraries: libs,
		ExpiresAt:        exp,
		CreatedAt:        time.Now().Unix(),
	}
	if err := s.db.CreateAPIKey(rec); err != nil {
		return "", db.APIKey{}, err
	}
	return rawKey, rec, nil
}

func (s *Service) Authenticate(r *http.Request) (*db.User, []string, []string, []string, bool, bool) {
	apiKey := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	if apiKey == "" {
		if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
			apiKey = strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		}
	}
	if apiKey == "" && strings.HasPrefix(r.URL.Path, "/api/v1/") && strings.Contains(r.URL.Path, "/webhooks/") {
		apiKey = strings.TrimSpace(r.URL.Query().Get("api_key"))
	}
	if apiKey != "" {
		k, err := s.db.GetAPIKeyByHash(crypto.HashToken(apiKey))
		if err == nil && k != nil {
			if k.ExpiresAt > 0 && k.ExpiresAt < time.Now().Unix() {
				return nil, nil, nil, nil, false, false
			}
			u, err := s.db.GetUserByID(k.UserID)
			if err == nil && u != nil {
				s.db.TouchAPIKey(k.ID)
				return u, k.Scopes, k.AllowedServers, k.AllowedLibraries, false, false
			}
		}
		return nil, nil, nil, nil, false, false
	}

	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return nil, nil, nil, nil, false, false
	}
	tokHash := crypto.HashToken(c.Value)
	sess, err := s.db.GetSessionByTokenHash(tokHash)
	if err != nil || sess == nil || sess.ExpiresAt < time.Now().Unix() {
		return nil, nil, nil, nil, false, false
	}
	if s.IsSessionIPBindingEnabled() && sess.IPAddress != "" {
		reqIP := RequestClientIP(r)
		if reqIP != "" && reqIP != sess.IPAddress {
			slog.Warn("session IP mismatch; revoking session", "user_id", sess.UserID, "expected_ip", sess.IPAddress, "actual_ip", reqIP)
			_ = s.db.DeleteSessionByTokenHash(tokHash)
			return nil, nil, nil, nil, false, false
		}
	}
	u, err := s.db.GetUserByID(sess.UserID)
	if err != nil || u == nil {
		return nil, nil, nil, nil, false, false
	}
	if sess.MFAPending {
		return u, nil, nil, nil, true, true
	}
	scopes := []string{"read"}
	switch u.Role {
	case RoleAdmin:
		scopes = []string{"read", "scan", "webhook", "admin"}
	case RoleOperator:
		scopes = []string{"read", "scan", "webhook"}
	}
	return u, scopes, nil, nil, false, true
}

func (s *Service) RequireSessionOrAPIKey(requiredScope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, scopes, keyServers, keyLibs, mfaPending, isCookie := s.Authenticate(r)
			if u == nil {
				http.Error(w, `{"detail":"Authentication required"}`, http.StatusUnauthorized)
				return
			}
			if mfaPending {
				http.Error(w, `{"detail":"MFA verification required"}`, http.StatusUnauthorized)
				return
			}
			if len(keyLibs) > 0 && !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				http.Error(w, `{"detail":"Library-scoped API keys may only use /api/v1"}`, http.StatusForbidden)
				return
			}
			if isCookie && u.MustChangePassword && !strings.HasPrefix(r.URL.Path, "/api/auth/") {
				http.Error(w, `{"detail":"Password change required"}`, http.StatusForbidden)
				return
			}
			if isCookie && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
				if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") || r.Header.Get("X-Requested-With") == "" {
					http.Error(w, `{"detail":"CSRF check failed"}`, http.StatusForbidden)
					return
				}
			}
			if requiredScope != "" && !hasScope(scopes, requiredScope) {
				http.Error(w, `{"detail":"Insufficient permissions"}`, http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), ctxUserKey, u)
			ctx = context.WithValue(ctx, ctxScopesKey, scopes)
			if len(keyServers) > 0 {
				ctx = context.WithValue(ctx, ctxKeyServersKey, keyServers)
			}
			if len(keyLibs) > 0 {
				ctx = context.WithValue(ctx, ctxKeyLibsKey, keyLibs)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserFromContext(ctx context.Context) *db.User {
	u, _ := ctx.Value(ctxUserKey).(*db.User)
	return u
}

func KeyAllowedServersFromContext(ctx context.Context) []string {
	s, _ := ctx.Value(ctxKeyServersKey).([]string)
	return s
}

// CanAccessServerForRequest enforces both the authenticated user's per-server RBAC
// and (when authenticated via API key) the API key's allowed_servers restriction.
func CanAccessServerForRequest(ctx context.Context, serverID string) bool {
	u := UserFromContext(ctx)
	if !CanAccessServer(u, serverID) {
		return false
	}
	keyServers := KeyAllowedServersFromContext(ctx)
	if len(keyServers) == 0 {
		return true
	}
	for _, id := range keyServers {
		if id == "*" || id == serverID {
			return true
		}
	}
	return false
}

func HasScope(ctx context.Context, wanted string) bool {
	scopes, _ := ctx.Value(ctxScopesKey).([]string)
	return hasScope(scopes, wanted)
}

func hasScope(scopes []string, wanted string) bool {
	for _, s := range scopes {
		if s == wanted || s == "admin" {
			return true
		}
	}
	return false
}

func KeyAllowedLibrariesFromContext(ctx context.Context) []string {
	l, _ := ctx.Value(ctxKeyLibsKey).([]string)
	return l
}

// LibraryRestricted reports whether the request is authenticated by an API key limited to specific libraries.
func LibraryRestricted(ctx context.Context) bool {
	return len(KeyAllowedLibrariesFromContext(ctx)) > 0
}

// CanAccessLibraryForRequest enforces an API key's allowed_libraries restriction (empty = all libraries).
func CanAccessLibraryForRequest(ctx context.Context, library string) bool {
	libs := KeyAllowedLibrariesFromContext(ctx)
	if len(libs) == 0 {
		return true
	}
	for _, l := range libs {
		if l == "*" || strings.EqualFold(l, library) {
			return true
		}
	}
	return false
}

// AdminSetPassword sets a user's password (admin action), terminates their sessions, and optionally forces a change at next sign-in.
func (s *Service) AdminSetPassword(userID, newPassword string, mustChange bool) error {
	u, err := s.db.GetUserByID(userID)
	if err != nil || u == nil {
		return errors.New("user not found")
	}
	if err := ValidatePasswordStrength(newPassword, u.Username); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}
	if err := s.db.AdminSetUserPassword(userID, string(hash), mustChange); err != nil {
		return err
	}
	return s.db.DeleteUserSessions(userID)
}
