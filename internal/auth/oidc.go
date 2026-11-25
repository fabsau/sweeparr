package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/crypto"
	"github.com/fabsau/sweeparr/internal/db"
)

type OIDCSettings struct {
	Issuer         string   `json:"issuer"`
	ClientID       string   `json:"client_id"`
	HasSecret      bool     `json:"has_secret"`
	RedirectURL    string   `json:"redirect_url"`
	AutoCreate     bool     `json:"auto_create"`
	GroupsClaim    string   `json:"groups_claim"`
	AdminGroups    []string `json:"admin_groups"`
	OperatorGroups []string `json:"operator_groups"`
	ViewerGroups   []string `json:"viewer_groups"`
	Configured     bool     `json:"configured"`
	EnvManaged     bool     `json:"env_managed"`
}

type storedOIDC struct {
	Issuer          string   `json:"issuer"`
	ClientID        string   `json:"client_id"`
	ClientSecretEnc string   `json:"client_secret_enc"`
	RedirectURL     string   `json:"redirect_url"`
	AutoCreate      bool     `json:"auto_create"`
	GroupsClaim     string   `json:"groups_claim"`
	AdminGroups     []string `json:"admin_groups"`
	OperatorGroups  []string `json:"operator_groups"`
	ViewerGroups    []string `json:"viewer_groups"`
}

func (s *Service) loadStoredOIDC() {
	if s.cfg.OIDCEnabled() {
		return // env/YAML config takes precedence
	}
	raw := s.db.GetAppSetting("oidc_config")
	if raw == "" {
		return
	}
	var st storedOIDC
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return
	}
	secret := ""
	if st.ClientSecretEnc != "" {
		if dec, err := crypto.Decrypt(s.cfg.MasterKey, st.ClientSecretEnc); err == nil {
			secret = dec
		}
	}
	s.applyOIDCConfig(st.Issuer, st.ClientID, secret, st.RedirectURL, st.AutoCreate, st.GroupsClaim, st.AdminGroups, st.OperatorGroups, st.ViewerGroups)
}

func (s *Service) applyOIDCConfig(issuer, clientID, clientSecret, redirectURL string, autoCreate bool, groupsClaim string, adminGroups, operatorGroups, viewerGroups []string) error {
	s.mu.Lock()
	s.cfg.OIDCIssuer = strings.TrimSpace(issuer)
	s.cfg.OIDCClientID = strings.TrimSpace(clientID)
	if clientSecret != "" {
		s.cfg.OIDCClientSecret = clientSecret
	}
	s.cfg.OIDCRedirectURL = strings.TrimSpace(redirectURL)
	s.cfg.OIDCAutoCreate = autoCreate
	if strings.TrimSpace(groupsClaim) == "" {
		groupsClaim = "groups"
	}
	s.cfg.OIDCGroupsClaim = strings.TrimSpace(groupsClaim)
	s.cfg.OIDCAdminGroups = config.SanitizeStringSlice(adminGroups, 64, 128)
	s.cfg.OIDCOperatorGroups = config.SanitizeStringSlice(operatorGroups, 64, 128)
	s.cfg.OIDCViewerGroups = config.SanitizeStringSlice(viewerGroups, 64, 128)
	cfgCopy := s.cfg
	s.mu.Unlock()

	if !cfgCopy.OIDCEnabled() {
		s.mu.Lock()
		s.oidcProv = nil
		s.oidcVer = nil
		s.oauthCfg = nil
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(ctx, cfgCopy.OIDCIssuer)
	if err != nil {
		slog.Warn("failed to initialize OIDC provider", "issuer", cfgCopy.OIDCIssuer, "error", err)
		return err
	}
	s.mu.Lock()
	s.oidcProv = prov
	s.oidcVer = prov.Verifier(&oidc.Config{ClientID: cfgCopy.OIDCClientID})
	s.oauthCfg = &oauth2.Config{
		ClientID:     cfgCopy.OIDCClientID,
		ClientSecret: cfgCopy.OIDCClientSecret,
		RedirectURL:  cfgCopy.OIDCRedirectURL,
		Endpoint:     prov.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}
	s.mu.Unlock()
	slog.Info("initialized OIDC provider", "issuer", cfgCopy.OIDCIssuer)
	return nil
}

func (s *Service) GetOIDCSettings() OIDCSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return OIDCSettings{
		Issuer:         s.cfg.OIDCIssuer,
		ClientID:       s.cfg.OIDCClientID,
		HasSecret:      s.cfg.OIDCClientSecret != "",
		RedirectURL:    s.cfg.OIDCRedirectURL,
		AutoCreate:     s.cfg.OIDCAutoCreate,
		GroupsClaim:    s.cfg.OIDCGroupsClaim,
		AdminGroups:    s.cfg.OIDCAdminGroups,
		OperatorGroups: s.cfg.OIDCOperatorGroups,
		ViewerGroups:   s.cfg.OIDCViewerGroups,
		Configured:     s.oauthCfg != nil && s.oidcVer != nil,
		EnvManaged:     s.cfg.ConfigManaged,
	}
}

func (s *Service) UpdateOIDCSettings(issuer, clientID, clientSecret, redirectURL string, autoCreate bool, groupsClaim string, adminGroups, operatorGroups, viewerGroups []string) error {
	if issuer != "" {
		cleanIss, err := config.ValidateHTTPURL(issuer, false)
		if err != nil {
			return fmt.Errorf("invalid issuer URL: %w", err)
		}
		issuer = cleanIss
	}
	if redirectURL != "" {
		cleanRedir, err := config.ValidateHTTPURL(redirectURL, false)
		if err != nil {
			return fmt.Errorf("invalid redirect URL: %w", err)
		}
		redirectURL = cleanRedir
	}
	s.mu.Lock()
	existingSecret := s.cfg.OIDCClientSecret
	s.mu.Unlock()
	secToSave := strings.TrimSpace(clientSecret)
	if secToSave == "" {
		secToSave = existingSecret
	}
	encSec := ""
	if secToSave != "" {
		var err error
		encSec, err = crypto.Encrypt(s.cfg.MasterKey, secToSave)
		if err != nil {
			return err
		}
	}
	st := storedOIDC{
		Issuer:          issuer,
		ClientID:        config.SanitizeText(clientID, 256),
		ClientSecretEnc: encSec,
		RedirectURL:     redirectURL,
		AutoCreate:      autoCreate,
		GroupsClaim:     config.SanitizeText(groupsClaim, 64),
		AdminGroups:     config.SanitizeStringSlice(adminGroups, 64, 128),
		OperatorGroups:  config.SanitizeStringSlice(operatorGroups, 64, 128),
		ViewerGroups:    config.SanitizeStringSlice(viewerGroups, 64, 128),
	}
	b, _ := json.Marshal(st)
	if err := s.db.SetAppSetting("oidc_config", string(b)); err != nil {
		return err
	}
	return s.applyOIDCConfig(st.Issuer, st.ClientID, secToSave, st.RedirectURL, st.AutoCreate, st.GroupsClaim, st.AdminGroups, st.OperatorGroups, st.ViewerGroups)
}

func (s *Service) TestOIDCDiscovery(ctx context.Context, issuer string) (map[string]any, error) {
	cleanIss, err := config.ValidateHTTPURL(issuer, false)
	if err != nil {
		return nil, err
	}
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(tctx, cleanIss)
	if err != nil {
		return nil, err
	}
	ep := prov.Endpoint()
	return map[string]any{
		"ok":        true,
		"issuer":    cleanIss,
		"auth_url":  ep.AuthURL,
		"token_url": ep.TokenURL,
	}, nil
}

func (s *Service) BeginOIDCLogin() (string, error) {
	return s.beginOIDCFlow("")
}

func (s *Service) BeginOIDCLink(userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("user ID is required")
	}
	return s.beginOIDCFlow(userID)
}

func (s *Service) beginOIDCFlow(linkUserID string) (string, error) {
	if !s.OIDCConfigured() {
		return "", errors.New("OIDC is not configured")
	}
	state, _, err := crypto.RandomToken(24)
	if err != nil {
		return "", err
	}
	nonce, _, err := crypto.RandomToken(24)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pruneChallengesLocked()
	s.oidcStates[state] = OIDCState{Nonce: nonce, LinkUserID: linkUserID, ExpiresAt: time.Now().Add(10 * time.Minute)}
	oauthCfg := s.oauthCfg
	s.mu.Unlock()
	return oauthCfg.AuthCodeURL(state, oidc.Nonce(nonce)), nil
}

func (s *Service) HandleOIDCCallback(ctx context.Context, state, code string, ip ...string) (*db.User, string, bool, error) {
	if !s.OIDCConfigured() {
		return nil, "", false, errors.New("OIDC is not configured")
	}
	s.mu.Lock()
	st, ok := s.oidcStates[state]
	delete(s.oidcStates, state)
	oauthCfg := s.oauthCfg
	ver := s.oidcVer
	cfg := s.cfg
	s.mu.Unlock()
	if !ok || time.Now().After(st.ExpiresAt) {
		return nil, "", false, errors.New("invalid or expired OIDC state")
	}
	tok, err := oauthCfg.Exchange(ctx, code)
	if err != nil {
		return nil, "", false, fmt.Errorf("oidc exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, "", false, errors.New("missing id_token")
	}
	idToken, err := ver.Verify(ctx, rawID)
	if err != nil {
		return nil, "", false, fmt.Errorf("verify id_token: %w", err)
	}
	if idToken.Nonce != st.Nonce {
		return nil, "", false, errors.New("invalid OIDC nonce")
	}
	var claims struct {
		Sub               string `json:"sub"`
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, "", false, err
	}

	// Account-linking flow for an already signed-in Sweeparr user
	if st.LinkUserID != "" {
		existing, err := s.db.GetUserByOIDC(cfg.OIDCIssuer, claims.Sub)
		if err != nil {
			return nil, "", true, err
		}
		if existing != nil && existing.ID != st.LinkUserID {
			return nil, "", true, errors.New("this OIDC identity is already linked to another user")
		}
		if err := s.db.LinkUserOIDC(st.LinkUserID, cfg.OIDCIssuer, claims.Sub); err != nil {
			return nil, "", true, err
		}
		u, err := s.db.GetUserByID(st.LinkUserID)
		return u, "", true, err
	}

	var rawClaims map[string]any
	_ = idToken.Claims(&rawClaims)
	groups := ExtractClaimsGroups(rawClaims, cfg.OIDCGroupsClaim)
	mappedRole, allowed := cfg.EvaluateOIDCRole(groups)
	if !allowed {
		slog.Warn("oidc login denied by group policy", "username", claims.PreferredUsername, "groups", groups)
		return nil, "", false, errors.New("user is not a member of any authorized OIDC group")
	}

	u, err := s.db.GetUserByOIDC(cfg.OIDCIssuer, claims.Sub)
	if err != nil {
		return nil, "", false, err
	}
	if u == nil {
		if !cfg.OIDCAutoCreate {
			return nil, "", false, errors.New("OIDC user not provisioned")
		}
		uname := claims.PreferredUsername
		if uname == "" {
			uname = claims.Email
		}
		if uname == "" {
			uname = "oidc-" + claims.Sub[:min(8, len(claims.Sub))]
		}
		newUser := db.User{
			ID:               uuid.NewString(),
			Username:         uname,
			DisplayName:      claims.Name,
			Email:            claims.Email,
			Role:             mappedRole,
			CanCreateAPIKeys: mappedRole != RoleViewer,
			OIDCSubject:      claims.Sub,
			OIDCIssuer:       cfg.OIDCIssuer,
			CreatedAt:        time.Now().Unix(),
		}
		if err := s.db.CreateUser(newUser); err != nil {
			return nil, "", false, err
		}
		u = &newUser
	} else if u.Role != mappedRole {
		_ = s.db.UpdateUserRole(u.ID, mappedRole)
		u.Role = mappedRole
	}
	rawTok, _, err := s.issueSession(u.ID, false, ip...)
	return u, rawTok, false, err
}

// ExtractClaimsGroups extracts a string slice of group names from an arbitrary OIDC claims map.
func ExtractClaimsGroups(raw map[string]any, claimName string) []string {
	if raw == nil {
		return nil
	}
	if claimName == "" {
		claimName = "groups"
	}
	val, ok := raw[claimName]
	if !ok || val == nil {
		return nil
	}
	switch v := val.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, p := range strings.Split(v, ",") {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
