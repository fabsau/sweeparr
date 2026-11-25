package config

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func deriveRPFromPublicURL(raw string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", ""
	}
	rpID, err := SanitizeHost(u.Hostname())
	if err != nil {
		return "", ""
	}
	origin, err := ValidateOrigin(u.Scheme + "://" + u.Host)
	if err != nil {
		return rpID, ""
	}
	return rpID, origin
}

// ServerSpec is a declarative media server definition loaded from config.yaml.
// Keeping Kind extensible ("plex" today, future "jellyfin"/"emby") allows adding adapters without schema breaks.
type ServerSpec struct {
	ID            string
	Name          string
	Kind          string
	URL           string
	Token         string
	TokenSource   string
	SSLVerify     bool
	Enabled       bool
	Config        ScannerConfig
	ConfigManaged bool
}

type YAMLFile struct {
	Server  YAMLServerSection  `yaml:"server"`
	Auth    YAMLAuthSection    `yaml:"auth"`
	Scanner YAMLScannerSection `yaml:"scanner"`
	Servers []YAMLMediaServer  `yaml:"servers"`
}

type YAMLServerSection struct {
	PublicURL       string   `yaml:"public_url"`
	AllowedHosts    []string `yaml:"allowed_hosts"`
	LogLevel        string   `yaml:"log_level"`
	LogFormat       string   `yaml:"log_format"`
	RPID            string   `yaml:"rp_id"`
	RPDisplayName   string   `yaml:"rp_display_name"`
	RPOrigins       []string `yaml:"rp_origins"`
	SessionTTLHours int      `yaml:"session_ttl_hours"`
	MasterKeyFile   string   `yaml:"master_key_file"`
	MasterKey       string   `yaml:"master_key"`
}

type YAMLAuthSection struct {
	BootstrapAdmin YAMLBootstrapAdmin `yaml:"bootstrap_admin"`
	OIDC           YAMLOIDCSection    `yaml:"oidc"`
}

type YAMLBootstrapAdmin struct {
	Username     string `yaml:"username"`
	PasswordFile string `yaml:"password_file"`
	Password     string `yaml:"password"`
}

type YAMLOIDCSection struct {
	Issuer           string   `yaml:"issuer"`
	ClientID         string   `yaml:"client_id"`
	ClientSecretFile string   `yaml:"client_secret_file"`
	ClientSecret     string   `yaml:"client_secret"`
	RedirectURL      string   `yaml:"redirect_url"`
	AutoCreate       *bool    `yaml:"auto_create"`
	GroupsClaim      string   `yaml:"groups_claim"`
	AdminGroups      []string `yaml:"admin_groups"`
	OperatorGroups   []string `yaml:"operator_groups"`
	ViewerGroups     []string `yaml:"viewer_groups"`
}

type YAMLQuietHours struct {
	Enabled *bool `yaml:"enabled"`
	Start   *int  `yaml:"start"`
	End     *int  `yaml:"end"`
}

type YAMLScannerSection struct {
	OrderStrategy           string         `yaml:"order_strategy"`
	DriveBudgetGiB          *float64       `yaml:"drive_budget_gib"`
	DriveWindowHours        *float64       `yaml:"drive_window_hours"`
	InterItemDelaySeconds   *int           `yaml:"inter_item_delay_seconds"`
	MaxItemScanMinutes      *int           `yaml:"max_item_scan_minutes"`
	MaxAttempts             *int           `yaml:"max_attempts"`
	CircuitBreakerThreshold *int           `yaml:"circuit_breaker_threshold"`
	RetryInitialSeconds     *int           `yaml:"retry_initial_seconds"`
	RetryMaxSeconds         *int           `yaml:"retry_max_seconds"`
	CatalogRefreshMinutes   *int           `yaml:"catalog_refresh_minutes"`
	FullSyncIntervalHours   *int           `yaml:"full_sync_interval_hours"`
	BulkPageSize            *int           `yaml:"bulk_page_size"`
	PauseOnActiveStreams    *bool          `yaml:"pause_on_active_streams"`
	PauseOnTranscodesOnly   *bool          `yaml:"pause_on_transcodes_only"`
	MaxActiveStreams        *int           `yaml:"max_active_streams"`
	RespectLibrarySettings  *bool          `yaml:"respect_library_settings"`
	DryRun                  *bool          `yaml:"dry_run"`
	QuietHours              YAMLQuietHours `yaml:"quiet_hours"`
	WebhookPrioritizeTop     *bool          `yaml:"webhook_prioritize_top"`
	WebhookHoldSeconds       *int           `yaml:"webhook_hold_seconds"`
	ArrAutoscanEnabled       *bool          `yaml:"arr_autoscan_enabled"`
	WebhookDebounceSeconds   *int           `yaml:"webhook_debounce_seconds"`
	WebhookMaxWaitSeconds    *int           `yaml:"webhook_max_wait_seconds"`
	WebhookPickupWaitSeconds *int           `yaml:"webhook_pickup_wait_seconds"`
	ArrPathMappings          []PathMapping  `yaml:"arr_path_mappings"`
	HoldPlayingItems         *bool          `yaml:"hold_playing_items"`
	HoldModifiedFiles       *bool          `yaml:"hold_modified_files"`
	PathMappings            []PathMapping  `yaml:"path_mappings"`
	LogRetentionDays        *int           `yaml:"log_retention_days"`
	HistoryRetentionDays    *int           `yaml:"history_retention_days"`
	MaxLogRows              *int           `yaml:"max_log_rows"`
}

type YAMLMediaServer struct {
	ID                  string              `yaml:"id"`
	Name                string              `yaml:"name"`
	Kind                string              `yaml:"kind"`
	URL                 string              `yaml:"url"`
	TokenFile           string              `yaml:"token_file"`
	Token               string              `yaml:"token"`
	SSLVerify           *bool               `yaml:"ssl_verify"`
	Enabled             *bool               `yaml:"enabled"`
	EnabledLibraries    []string            `yaml:"enabled_libraries"`
	LibraryPriority     []string            `yaml:"library_priority"`
	LibraryCapabilities map[string][]string `yaml:"library_capabilities"`
	Scanner             YAMLScannerSection  `yaml:"scanner"`
}

func readSecretFileStrict(path, fieldName string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", nil
	}
	b, err := ReadBoundedRegularFile(p, MaxSecretFileBytes)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: check that the file is readable, regular, and within the size limit", fieldName)
	}
	return ValidateSecretValue(string(b), fieldName)
}

func applyScannerOverlay(base ScannerConfig, ov YAMLScannerSection) ScannerConfig {
	c := base
	if ov.OrderStrategy != "" {
		c.OrderStrategy = ov.OrderStrategy
	}
	if ov.DriveBudgetGiB != nil {
		c.DriveBudgetGiB = *ov.DriveBudgetGiB
	}
	if ov.DriveWindowHours != nil {
		c.DriveWindowHours = *ov.DriveWindowHours
	}
	if ov.InterItemDelaySeconds != nil {
		c.InterItemDelaySeconds = *ov.InterItemDelaySeconds
	}
	if ov.MaxItemScanMinutes != nil {
		c.MaxItemScanMinutes = *ov.MaxItemScanMinutes
	}
	if ov.MaxAttempts != nil {
		c.MaxAttempts = *ov.MaxAttempts
	}
	if ov.CircuitBreakerThreshold != nil {
		c.CircuitBreakerThreshold = *ov.CircuitBreakerThreshold
	}
	if ov.RetryInitialSeconds != nil {
		c.RetryInitialSeconds = *ov.RetryInitialSeconds
	}
	if ov.RetryMaxSeconds != nil {
		c.RetryMaxSeconds = *ov.RetryMaxSeconds
	}
	if ov.CatalogRefreshMinutes != nil {
		c.CatalogRefreshMinutes = *ov.CatalogRefreshMinutes
	}
	if ov.FullSyncIntervalHours != nil {
		c.FullSyncIntervalHours = *ov.FullSyncIntervalHours
	}
	if ov.BulkPageSize != nil {
		c.BulkPageSize = *ov.BulkPageSize
	}
	if ov.PauseOnActiveStreams != nil {
		c.PauseOnActiveStreams = *ov.PauseOnActiveStreams
	}
	if ov.PauseOnTranscodesOnly != nil {
		c.PauseOnTranscodesOnly = *ov.PauseOnTranscodesOnly
	}
	if ov.MaxActiveStreams != nil {
		c.MaxActiveStreams = *ov.MaxActiveStreams
	}
	if ov.RespectLibrarySettings != nil {
		c.RespectPlexLibrarySettings = *ov.RespectLibrarySettings
	}
	if ov.DryRun != nil {
		c.DryRun = *ov.DryRun
	}
	if ov.QuietHours.Enabled != nil {
		c.QuietHoursEnabled = *ov.QuietHours.Enabled
	}
	if ov.QuietHours.Start != nil {
		c.QuietHoursStart = *ov.QuietHours.Start
	}
	if ov.QuietHours.End != nil {
		c.QuietHoursEnd = *ov.QuietHours.End
	}
	if ov.WebhookPrioritizeTop != nil {
		c.WebhookPrioritizeTop = *ov.WebhookPrioritizeTop
	}
	if ov.WebhookHoldSeconds != nil {
		c.WebhookHoldSeconds = *ov.WebhookHoldSeconds
	}
	if ov.ArrAutoscanEnabled != nil {
		c.ArrAutoscanEnabled = *ov.ArrAutoscanEnabled
	}
	if ov.WebhookDebounceSeconds != nil {
		c.WebhookDebounceSeconds = *ov.WebhookDebounceSeconds
	}
	if ov.WebhookMaxWaitSeconds != nil {
		c.WebhookMaxWaitSeconds = *ov.WebhookMaxWaitSeconds
	}
	if ov.WebhookPickupWaitSeconds != nil {
		c.WebhookPickupWaitSeconds = *ov.WebhookPickupWaitSeconds
	}
	if len(ov.ArrPathMappings) > 0 {
		c.ArrPathMappings = ov.ArrPathMappings
	}
	if ov.HoldPlayingItems != nil {
		c.HoldPlayingItems = *ov.HoldPlayingItems
	}
	if ov.HoldModifiedFiles != nil {
		c.HoldModifiedFiles = *ov.HoldModifiedFiles
	}
	if len(ov.PathMappings) > 0 {
		c.PathMappings = ov.PathMappings
	}
	if ov.LogRetentionDays != nil {
		c.LogRetentionDays = *ov.LogRetentionDays
	}
	if ov.HistoryRetentionDays != nil {
		c.HistoryRetentionDays = *ov.HistoryRetentionDays
	}
	if ov.MaxLogRows != nil {
		c.MaxLogRows = *ov.MaxLogRows
	}
	return SanitizeScannerConfig(c)
}

// ApplyYAMLConfig parses a strict YAML config file (KnownFields=true) and applies it on top of EnvConfig.
func ApplyYAMLConfig(env *EnvConfig, path string) error {
	raw, err := ReadBoundedRegularFile(path, MaxConfigFileBytes)
	if err != nil {
		return fmt.Errorf("read config file %q: %w", path, err)
	}
	var yf YAMLFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&yf); err != nil {
		return fmt.Errorf("parse config file %q: %w", path, err)
	}

	env.ConfigFile = path
	env.ConfigManaged = true

	if yf.Server.PublicURL != "" {
		cleanPub, err := ValidateHTTPURL(yf.Server.PublicURL, false)
		if err != nil {
			return fmt.Errorf("server.public_url: %w", err)
		}
		env.PublicURL = cleanPub
	}
	if len(yf.Server.AllowedHosts) > 0 {
		cleanHosts := make([]string, 0, len(yf.Server.AllowedHosts))
		for _, h := range yf.Server.AllowedHosts {
			ch, err := SanitizeHost(h)
			if err != nil {
				return fmt.Errorf("server.allowed_hosts: %w", err)
			}
			cleanHosts = append(cleanHosts, ch)
		}
		env.AllowedHosts = cleanHosts
	}
	if yf.Server.LogLevel != "" {
		lvl := strings.ToUpper(strings.TrimSpace(yf.Server.LogLevel))
		switch lvl {
		case "DEBUG", "INFO", "WARN", "WARNING", "ERROR":
			env.LogLevel = lvl
		default:
			return fmt.Errorf("server.log_level %q is invalid (expected debug, info, warn, or error)", SanitizeText(yf.Server.LogLevel, 32))
		}
	}
	if yf.Server.LogFormat != "" {
		lf := strings.ToLower(strings.TrimSpace(yf.Server.LogFormat))
		if lf != "json" && lf != "text" {
			return fmt.Errorf("server.log_format %q is invalid (expected json or text)", SanitizeText(yf.Server.LogFormat, 32))
		}
		env.LogFormat = lf
	}
	if yf.Server.RPID != "" {
		rpID, err := SanitizeHost(yf.Server.RPID)
		if err != nil {
			return fmt.Errorf("server.rp_id: %w", err)
		}
		env.RPID = rpID
	} else if yf.Server.PublicURL != "" {
		if derivedID, _ := deriveRPFromPublicURL(env.PublicURL); derivedID != "" {
			env.RPID = derivedID
		}
	}
	if yf.Server.RPDisplayName != "" {
		env.RPDisplayName = SanitizeText(yf.Server.RPDisplayName, 64)
	}
	if len(yf.Server.RPOrigins) > 0 {
		cleanOrigins := make([]string, 0, len(yf.Server.RPOrigins))
		for _, o := range yf.Server.RPOrigins {
			co, err := ValidateOrigin(o)
			if err != nil {
				return fmt.Errorf("server.rp_origins: %w", err)
			}
			cleanOrigins = append(cleanOrigins, co)
		}
		env.RPOrigins = cleanOrigins
	} else if yf.Server.PublicURL != "" {
		if _, derivedOrigin := deriveRPFromPublicURL(env.PublicURL); derivedOrigin != "" {
			env.RPOrigins = []string{derivedOrigin}
		}
	}
	if yf.Server.SessionTTLHours != 0 {
		if yf.Server.SessionTTLHours < 1 || yf.Server.SessionTTLHours > 8760 {
			return fmt.Errorf("server.session_ttl_hours must be between 1 and 8760")
		}
		env.SessionTTL = time.Duration(yf.Server.SessionTTLHours) * time.Hour
	}
	if yf.Server.MasterKeyFile != "" {
		hexKey, err := readSecretFileStrict(yf.Server.MasterKeyFile, "server.master_key_file")
		if err != nil {
			return err
		}
		b, decErr := hex.DecodeString(hexKey)
		if decErr != nil || len(b) != 32 {
			return fmt.Errorf("server.master_key_file must contain 64 hex chars (32 bytes)")
		}
		copy(env.MasterKey[:], b)
	} else if strings.TrimSpace(yf.Server.MasterKey) != "" {
		slog.Warn("config.yaml uses inline plaintext server.master_key; prefer server.master_key_file (Docker Secret) for better security")
		hexKey, err := ValidateSecretValue(yf.Server.MasterKey, "server.master_key")
		if err != nil {
			return err
		}
		b, decErr := hex.DecodeString(hexKey)
		if decErr != nil || len(b) != 32 {
			return fmt.Errorf("server.master_key must contain 64 hex chars (32 bytes)")
		}
		copy(env.MasterKey[:], b)
	}

	// Auth: bootstrap admin & OIDC
	if yf.Auth.BootstrapAdmin.Username != "" {
		user := strings.TrimSpace(yf.Auth.BootstrapAdmin.Username)
		if err := ValidateUsername(user); err != nil {
			return fmt.Errorf("auth.bootstrap_admin.username: %w", err)
		}
		env.AdminUser = user
		var pw string
		if yf.Auth.BootstrapAdmin.PasswordFile != "" {
			var err error
			pw, err = readSecretFileStrict(yf.Auth.BootstrapAdmin.PasswordFile, "auth.bootstrap_admin.password_file")
			if err != nil {
				return err
			}
		} else if strings.TrimSpace(yf.Auth.BootstrapAdmin.Password) != "" {
			slog.Warn("config.yaml uses inline plaintext auth.bootstrap_admin.password; prefer password_file (Docker Secret)")
			var err error
			pw, err = ValidateSecretValue(yf.Auth.BootstrapAdmin.Password, "auth.bootstrap_admin.password")
			if err != nil {
				return err
			}
		}
		if len(pw) < 10 {
			return fmt.Errorf("auth.bootstrap_admin password must be at least 10 characters")
		}
		env.AdminPassword = pw
	}
	if yf.Auth.OIDC.Issuer != "" {
		cleanIss, err := ValidateHTTPURL(yf.Auth.OIDC.Issuer, false)
		if err != nil {
			return fmt.Errorf("auth.oidc.issuer: %w", err)
		}
		env.OIDCIssuer = cleanIss
	}
	if yf.Auth.OIDC.ClientID != "" {
		cid := SanitizeText(yf.Auth.OIDC.ClientID, 256)
		if cid == "" {
			return fmt.Errorf("auth.oidc.client_id is invalid")
		}
		env.OIDCClientID = cid
	}
	if yf.Auth.OIDC.ClientSecretFile != "" {
		sec, err := readSecretFileStrict(yf.Auth.OIDC.ClientSecretFile, "auth.oidc.client_secret_file")
		if err != nil {
			return err
		}
		env.OIDCClientSecret = sec
	} else if strings.TrimSpace(yf.Auth.OIDC.ClientSecret) != "" {
		slog.Warn("config.yaml uses inline plaintext auth.oidc.client_secret; prefer client_secret_file (Docker Secret)")
		sec, err := ValidateSecretValue(yf.Auth.OIDC.ClientSecret, "auth.oidc.client_secret")
		if err != nil {
			return err
		}
		env.OIDCClientSecret = sec
	}
	if yf.Auth.OIDC.RedirectURL != "" {
		cleanRedir, err := ValidateHTTPURL(yf.Auth.OIDC.RedirectURL, false)
		if err != nil {
			return fmt.Errorf("auth.oidc.redirect_url: %w", err)
		}
		env.OIDCRedirectURL = cleanRedir
	}
	if yf.Auth.OIDC.AutoCreate != nil {
		env.OIDCAutoCreate = *yf.Auth.OIDC.AutoCreate
	}
	if yf.Auth.OIDC.GroupsClaim != "" {
		env.OIDCGroupsClaim = SanitizeText(yf.Auth.OIDC.GroupsClaim, 64)
	}
	if len(yf.Auth.OIDC.AdminGroups) > 0 {
		env.OIDCAdminGroups = SanitizeStringSlice(yf.Auth.OIDC.AdminGroups, 64, 128)
	}
	if len(yf.Auth.OIDC.OperatorGroups) > 0 {
		env.OIDCOperatorGroups = SanitizeStringSlice(yf.Auth.OIDC.OperatorGroups, 64, 128)
	}
	if len(yf.Auth.OIDC.ViewerGroups) > 0 {
		env.OIDCViewerGroups = SanitizeStringSlice(yf.Auth.OIDC.ViewerGroups, 64, 128)
	}

	globalScanner := applyScannerOverlay(DefaultScannerConfig(), yf.Scanner)
	seenIDs := map[string]bool{}
	specs := make([]ServerSpec, 0, len(yf.Servers))
	for i, s := range yf.Servers {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			if i == 0 {
				id = "default"
			} else {
				id = fmt.Sprintf("server-%d", i+1)
			}
		}
		if err := ValidateServerID(id); err != nil {
			return err
		}
		if seenIDs[id] {
			return fmt.Errorf("duplicate server id %q in config.yaml", id)
		}
		seenIDs[id] = true

		kind := strings.ToLower(strings.TrimSpace(s.Kind))
		if kind == "" {
			kind = "plex"
		}
		if kind != "plex" {
			return fmt.Errorf("server %q: unsupported kind %q (currently supported: plex)", id, kind)
		}
		cleanURL, err := ValidatePlexURL(s.URL, env.AllowedHosts...)
		if err != nil {
			return fmt.Errorf("server %q url: %w", id, err)
		}
		var tok string
		tokenSource := "secret_file"
		if s.TokenFile != "" {
			var err error
			tok, err = readSecretFileStrict(s.TokenFile, fmt.Sprintf("servers[%s].token_file", id))
			if err != nil {
				return err
			}
		} else if strings.TrimSpace(s.Token) != "" {
			slog.Warn("config.yaml uses inline plaintext server token; prefer token_file (Docker Secret)", "server_id", id)
			var err error
			tok, err = ValidateSecretValue(s.Token, fmt.Sprintf("servers[%s].token", id))
			if err != nil {
				return err
			}
			tokenSource = "yaml_inline"
		}
		sslVerify := true
		if s.SSLVerify != nil {
			sslVerify = *s.SSLVerify
		}
		enabled := true
		if s.Enabled != nil {
			enabled = *s.Enabled
		}
		srvCfg := applyScannerOverlay(globalScanner, s.Scanner)
		if len(s.EnabledLibraries) > 0 {
			srvCfg.EnabledLibraries = s.EnabledLibraries
		}
		if len(s.LibraryPriority) > 0 {
			srvCfg.LibraryPriority = s.LibraryPriority
		}
		if len(s.LibraryCapabilities) > 0 {
			srvCfg.LibraryCapabilities = s.LibraryCapabilities
		}
		name := SanitizeText(s.Name, 128)
		if name == "" {
			name = id
		}
		specs = append(specs, ServerSpec{
			ID:            id,
			Name:          name,
			Kind:          kind,
			URL:           cleanURL,
			Token:         tok,
			TokenSource:   tokenSource,
			SSLVerify:     sslVerify,
			Enabled:       enabled,
			Config:        SanitizeScannerConfig(srvCfg),
			ConfigManaged: true,
		})
	}
	env.Servers = specs
	return nil
}
