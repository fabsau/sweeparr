package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var AllCapabilities = []string{
	"loudness", "voice", "chapters", "intro", "credits", "ads", "previews", "deep_analysis", "sonic",
}

var AllTriggers = []string{
	"analyze", "loudness", "chapters", "intros", "ads",
}

// EnvConfig holds process-level environment and security settings.
type EnvConfig struct {
	ConfigFile         string
	ConfigManaged      bool
	Servers            []ServerSpec
	StateDir           string
	Port               string
	LogLevel           string
	LogFormat          string
	PlexURL            string
	PlexToken          string
	PlexTokenSource    string
	PlexSSLVerify      bool
	AllowedHosts       []string
	AdminUser          string
	AdminPassword      string
	OIDCIssuer         string
	OIDCClientID       string
	OIDCClientSecret   string
	OIDCRedirectURL    string
	OIDCAutoCreate     bool
	OIDCGroupsClaim    string
	OIDCAdminGroups    []string
	OIDCOperatorGroups []string
	OIDCViewerGroups   []string
	RPID               string
	RPDisplayName      string
	RPOrigins          []string
	PublicURL          string
	SessionTTL         time.Duration
	MasterKey          [32]byte
}

func (e EnvConfig) OIDCEnabled() bool {
	return e.OIDCIssuer != "" && e.OIDCClientID != "" && e.OIDCClientSecret != "" && e.OIDCRedirectURL != ""
}

// ConfigureLogger initializes the global log/slog logger from EnvConfig.
func ConfigureLogger(levelStr, formatStr string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToUpper(strings.TrimSpace(levelStr)) {
	case "DEBUG":
		lvl = slog.LevelDebug
	case "WARN", "WARNING":
		lvl = slog.LevelWarn
	case "ERROR":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(formatStr), "text") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// EvaluateOIDCRole maps a user's OIDC groups to a Sweeparr role ("admin", "operator", or "viewer") based on configured group lists.
func (e EnvConfig) EvaluateOIDCRole(userGroups []string) (role string, allowed bool) {
	for _, ug := range userGroups {
		ug = strings.TrimSpace(ug)
		if ug == "" {
			continue
		}
		for _, ag := range e.OIDCAdminGroups {
			if strings.EqualFold(ug, strings.TrimSpace(ag)) {
				return "admin", true
			}
		}
	}
	for _, ug := range userGroups {
		ug = strings.TrimSpace(ug)
		if ug == "" {
			continue
		}
		for _, og := range e.OIDCOperatorGroups {
			if strings.EqualFold(ug, strings.TrimSpace(og)) {
				return "operator", true
			}
		}
	}
	if len(e.OIDCViewerGroups) == 0 {
		return "viewer", true
	}
	for _, ug := range userGroups {
		ug = strings.TrimSpace(ug)
		if ug == "" {
			continue
		}
		for _, vg := range e.OIDCViewerGroups {
			if strings.EqualFold(ug, strings.TrimSpace(vg)) {
				return "viewer", true
			}
		}
	}
	return "", false
}

// LoadEnv loads process configuration from environment variables and Docker secrets (*_FILE).
func LoadEnv() (EnvConfig, error) {
	stateDir, err := ValidateSafePath(firstEnv("SWEEPARR_STATE_DIR", "", "/state"))
	if err != nil {
		return EnvConfig{}, fmt.Errorf("invalid SWEEPARR_STATE_DIR: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return EnvConfig{}, fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	port, err := ValidatePort(firstEnv("SWEEPARR_PORT", "", "8080"))
	if err != nil {
		return EnvConfig{}, fmt.Errorf("invalid SWEEPARR_PORT: %w", err)
	}
	logLevel := SanitizeText(firstEnv("SWEEPARR_LOG_LEVEL", "", "INFO"), 16)
	logFormat := SanitizeText(firstEnv("SWEEPARR_LOG_FORMAT", "", "json"), 16)

	plexURL := strings.TrimRight(SanitizeText(firstEnv("PLEX_URL", "", "https://plex:32400"), 2048), "/")
	plexToken, tokenSource := ReadSecretEnvWithSource("PLEX_TOKEN")
	sslVerify := !isFalsy(os.Getenv("PLEX_SSL_VERIFY"))
	var allowedHosts []string
	for _, h := range splitCSV(os.Getenv("SWEEPARR_ALLOWED_HOSTS")) {
		if cleanH, hErr := SanitizeHost(h); hErr == nil {
			allowedHosts = append(allowedHosts, cleanH)
		}
	}

	publicURL := ""
	if rawPub := strings.TrimSpace(os.Getenv("SWEEPARR_PUBLIC_URL")); rawPub != "" {
		if cleanPub, pErr := ValidateHTTPURL(rawPub, false); pErr == nil {
			publicURL = cleanPub
		}
	}
	derivedRPID, derivedOrigin := deriveRPFromPublicURL(publicURL)

	rpID := "localhost"
	if rawRPID := strings.TrimSpace(os.Getenv("SWEEPARR_RP_ID")); rawRPID != "" {
		if cleanRPID, rErr := SanitizeHost(rawRPID); rErr == nil {
			rpID = cleanRPID
		}
	} else if derivedRPID != "" {
		rpID = derivedRPID
	}

	var rpOrigins []string
	if rawOrigins := strings.TrimSpace(os.Getenv("SWEEPARR_RP_ORIGINS")); rawOrigins != "" {
		for _, o := range splitCSV(rawOrigins) {
			if cleanO, oErr := ValidateOrigin(o); oErr == nil {
				rpOrigins = append(rpOrigins, cleanO)
			}
		}
	} else if derivedOrigin != "" {
		rpOrigins = []string{derivedOrigin}
	}
	if len(rpOrigins) == 0 {
		rpOrigins = []string{"http://localhost:8080", "http://localhost:5173"}
	}

	ttlHours := 168
	if rawTTL := strings.TrimSpace(os.Getenv("SWEEPARR_SESSION_TTL_HOURS")); rawTTL != "" {
		if parsed, err := strconv.Atoi(rawTTL); err == nil && parsed >= 1 && parsed <= 8760 {
			ttlHours = parsed
		}
	}

	masterKey, err := loadOrCreateMasterKey(stateDir)
	if err != nil {
		return EnvConfig{}, err
	}

	oidcSecret, _ := ReadSecretEnvWithSource("SWEEPARR_OIDC_CLIENT_SECRET")
	adminPass, _ := ReadSecretEnvWithSource("SWEEPARR_ADMIN_PASSWORD")
	oidcIssuer := ""
	if rawIss := strings.TrimSpace(os.Getenv("SWEEPARR_OIDC_ISSUER")); rawIss != "" {
		if cleanIss, iErr := ValidateHTTPURL(rawIss, false); iErr == nil {
			oidcIssuer = cleanIss
		}
	}
	oidcRedirect := ""
	if rawRedir := strings.TrimSpace(os.Getenv("SWEEPARR_OIDC_REDIRECT_URL")); rawRedir != "" {
		if cleanRedir, rErr := ValidateHTTPURL(rawRedir, false); rErr == nil {
			oidcRedirect = cleanRedir
		}
	}

	env := EnvConfig{
		StateDir:           stateDir,
		Port:               port,
		LogLevel:           logLevel,
		LogFormat:          logFormat,
		PlexURL:            plexURL,
		PlexToken:          plexToken,
		PlexTokenSource:    tokenSource,
		PlexSSLVerify:      sslVerify,
		AllowedHosts:       allowedHosts,
		AdminUser:          SanitizeText(os.Getenv("SWEEPARR_ADMIN_USER"), 64),
		AdminPassword:      adminPass,
		OIDCIssuer:         oidcIssuer,
		OIDCClientID:       SanitizeText(os.Getenv("SWEEPARR_OIDC_CLIENT_ID"), 256),
		OIDCClientSecret:   oidcSecret,
		OIDCRedirectURL:    oidcRedirect,
		OIDCAutoCreate:     !isFalsy(os.Getenv("SWEEPARR_OIDC_AUTO_CREATE")),
		OIDCGroupsClaim:    SanitizeText(firstEnv("SWEEPARR_OIDC_GROUPS_CLAIM", "", "groups"), 64),
		OIDCAdminGroups:    SanitizeStringSlice(splitCSV(firstEnv("SWEEPARR_OIDC_ADMIN_GROUPS", "", "IT_Admin")), 64, 128),
		OIDCOperatorGroups: SanitizeStringSlice(splitCSV(os.Getenv("SWEEPARR_OIDC_OPERATOR_GROUPS")), 64, 128),
		OIDCViewerGroups:   SanitizeStringSlice(splitCSV(os.Getenv("SWEEPARR_OIDC_VIEWER_GROUPS")), 64, 128),
		RPID:               rpID,
		RPDisplayName:      SanitizeText(firstEnv("SWEEPARR_RP_NAME", "", "Sweeparr"), 64),
		RPOrigins:          rpOrigins,
		PublicURL:          publicURL,
		SessionTTL:         time.Duration(ttlHours) * time.Hour,
		MasterKey:          masterKey,
	}

	cfgPath := strings.TrimSpace(os.Getenv("SWEEPARR_CONFIG_FILE"))
	if cfgPath == "" {
		if _, err := os.Stat("/config/config.yaml"); err == nil {
			cfgPath = "/config/config.yaml"
		}
	}
	if cfgPath != "" {
		cleanCfg, pErr := ValidateSafePath(cfgPath)
		if pErr != nil {
			return EnvConfig{}, fmt.Errorf("invalid SWEEPARR_CONFIG_FILE: %w", pErr)
		}
		if err := ApplyYAMLConfig(&env, cleanCfg); err != nil {
			return EnvConfig{}, err
		}
	}
	return env, nil
}

func loadOrCreateMasterKey(stateDir string) ([32]byte, error) {
	var key [32]byte
	if raw, _ := ReadSecretEnvWithSource("SWEEPARR_MASTER_KEY"); raw != "" {
		b, err := hex.DecodeString(raw)
		if err != nil || len(b) != 32 {
			return key, fmt.Errorf("SWEEPARR_MASTER_KEY must be 64 hex chars (32 bytes)")
		}
		copy(key[:], b)
		return key, nil
	}
	keyPath := filepath.Join(stateDir, ".master.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		b, decErr := hex.DecodeString(strings.TrimSpace(string(data)))
		if decErr == nil && len(b) == 32 {
			copy(key[:], b)
			return key, nil
		}
	}
	if _, err := rand.Read(key[:]); err != nil {
		return key, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		return key, fmt.Errorf("persist master key: %w", err)
	}
	return key, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstEnv(primary, fallback, def string) string {
	if v := strings.TrimSpace(os.Getenv(primary)); v != "" {
		return v
	}
	if fallback != "" {
		if v := strings.TrimSpace(os.Getenv(fallback)); v != "" {
			return v
		}
	}
	return def
}

// ReadSecretEnvWithSource reads a secret from <NAME>_FILE (Docker secret) first, then falls back to <NAME>.
func ReadSecretEnvWithSource(name string) (value string, source string) {
	if filePath := strings.TrimSpace(os.Getenv(name + "_FILE")); filePath != "" {
		if b, err := ReadBoundedRegularFile(filePath, MaxSecretFileBytes); err == nil {
			if val, vErr := ValidateSecretValue(string(b), name+"_FILE"); vErr == nil {
				return val, "secret_file"
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if val, vErr := ValidateSecretValue(raw, name); vErr == nil {
			return val, "env"
		}
	}
	return "", "none"
}

func isFalsy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

// ValidatePlexURL validates a media server URL, blocks SSRF metadata targets, and enforces SWEEPARR_ALLOWED_HOSTS if set.
func ValidatePlexURL(raw string, allowedHosts ...string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL scheme must be http or https")
	}
	if u.User != nil {
		return "", fmt.Errorf("credentials in URL are not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("URL host is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsLoopback() || ip.IsUnspecified() {
			return "", fmt.Errorf("loopback and link-local metadata addresses are blocked")
		}
	} else if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".internal") {
		return "", fmt.Errorf("localhost and internal metadata hosts are blocked")
	}
	if len(allowedHosts) > 0 {
		matched := false
		for _, ah := range allowedHosts {
			if strings.EqualFold(host, strings.TrimSpace(ah)) {
				matched = true
				break
			}
		}
		if !matched {
			return "", fmt.Errorf("host %q is not in SWEEPARR_ALLOWED_HOSTS", host)
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// RedactSecret replaces any occurrence of secret in text with "[REDACTED]".
func RedactSecret(text string, secrets ...string) string {
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len(s) >= 4 && strings.Contains(text, s) {
			text = strings.ReplaceAll(text, s, "[REDACTED]")
		}
	}
	return text
}

type PathMapping struct {
	Remote string `json:"remote" yaml:"remote"`
	Local  string `json:"local" yaml:"local"`
}

// ScannerConfig stores per-server continuous scanner settings.
type ScannerConfig struct {
	EnabledLibraries           []string            `json:"enabled_libraries"`
	LibraryPriority            []string            `json:"library_priority"`
	LibraryTriggers            map[string][]string `json:"library_triggers"`
	LibraryCapabilities        map[string][]string `json:"library_capabilities"`
	Triggers                   []string            `json:"triggers"`
	OrderStrategy              string              `json:"order_strategy"`
	NumberLocale               string              `json:"number_locale"`
	DateTimeLocale             string              `json:"datetime_locale"`
	TimeFormat                 string              `json:"time_format"`
	Theme                      string              `json:"theme"`
	AccentColor                string              `json:"accent_color"`
	DefaultPageSize            int                 `json:"default_page_size"`
	DriveBudgetGiB             float64             `json:"drive_budget_gib"`
	DriveWindowHours           float64             `json:"drive_window_hours"`
	MaxAttempts                int                 `json:"max_attempts"`
	InterItemDelaySeconds      int                 `json:"inter_item_delay_seconds"`
	MaxItemScanMinutes         int                 `json:"max_item_scan_minutes"`
	CircuitBreakerThreshold    int                 `json:"circuit_breaker_threshold"`
	CatalogRefreshMinutes      int                 `json:"catalog_refresh_minutes"`
	FullSyncIntervalHours      int                 `json:"full_sync_interval_hours"`
	IndexWorkers               int                 `json:"index_workers"`
	BulkPageSize               int                 `json:"bulk_page_size"`
	MaxActiveStreams           int                 `json:"max_active_streams"`
	RetryInitialSeconds        int                 `json:"retry_initial_seconds"`
	RetryMaxSeconds            int                 `json:"retry_max_seconds"`
	PauseOnActiveStreams       bool                `json:"pause_on_active_streams"`
	PauseOnTranscodesOnly      bool                `json:"pause_on_transcodes_only"`
	RespectPlexLibrarySettings bool                `json:"respect_plex_library_settings"`
	DryRun                     bool                `json:"dry_run"`
	QuietHoursEnabled          bool                `json:"quiet_hours_enabled"`
	QuietHoursStart            int                 `json:"quiet_hours_start"`
	QuietHoursEnd              int                 `json:"quiet_hours_end"`
	WebhookPrioritizeTop       bool                `json:"webhook_prioritize_top"`
	WebhookHoldSeconds         int                 `json:"webhook_hold_seconds"`
	ArrAutoscanEnabled         bool                `json:"arr_autoscan_enabled"`
	WebhookDebounceSeconds     int                 `json:"webhook_debounce_seconds"`
	WebhookMaxWaitSeconds      int                 `json:"webhook_max_wait_seconds"`
	WebhookPickupWaitSeconds   int                 `json:"webhook_pickup_wait_seconds"`
	ArrPathMappings            []PathMapping       `json:"arr_path_mappings"`
	HoldPlayingItems           bool                `json:"hold_playing_items"`
	HoldModifiedFiles          bool                `json:"hold_modified_files"`
	PathMappings               []PathMapping       `json:"path_mappings"`
	LogRetentionDays           int                 `json:"log_retention_days"`
	HistoryRetentionDays       int                 `json:"history_retention_days"`
	MaxLogRows                 int                 `json:"max_log_rows"`
	Paused                     bool                `json:"paused"`
	PausedUntil                float64             `json:"paused_until"`
}

func DefaultScannerConfig() ScannerConfig {
	return ScannerConfig{
		EnabledLibraries:           []string{},
		LibraryPriority:            []string{},
		LibraryTriggers:            map[string][]string{},
		LibraryCapabilities:        map[string][]string{},
		Triggers:                   []string{"analyze", "loudness", "chapters", "intros"},
		OrderStrategy:              "season_first",
		NumberLocale:               "de-DE",
		DateTimeLocale:             "de-DE",
		TimeFormat:                 "24h",
		Theme:                      "system",
		AccentColor:                "amber",
		DefaultPageSize:            50,
		DriveBudgetGiB:             450,
		DriveWindowHours:           24,
		MaxAttempts:                3,
		InterItemDelaySeconds:      5,
		MaxItemScanMinutes:         45,
		CircuitBreakerThreshold:    5,
		CatalogRefreshMinutes:      60,
		FullSyncIntervalHours:      24,
		IndexWorkers:               4,
		BulkPageSize:               200,
		MaxActiveStreams:           1,
		RetryInitialSeconds:        300,
		RetryMaxSeconds:            86400,
		PauseOnActiveStreams:       true,
		PauseOnTranscodesOnly:      false,
		RespectPlexLibrarySettings: true,
		DryRun:                     false,
		QuietHoursEnabled:          false,
		QuietHoursStart:            19,
		QuietHoursEnd:              23,
		WebhookPrioritizeTop:       true,
		WebhookHoldSeconds:         60,
		ArrAutoscanEnabled:         true,
		WebhookDebounceSeconds:     30,
		WebhookMaxWaitSeconds:      180,
		WebhookPickupWaitSeconds:   15,
		ArrPathMappings:            []PathMapping{},
		HoldPlayingItems:           true,
		HoldModifiedFiles:          false,
		PathMappings:               []PathMapping{},
		LogRetentionDays:           14,
		HistoryRetentionDays:       30,
		MaxLogRows:                 5000,
		Paused:                     false,
		PausedUntil:                0,
	}
}

func SanitizeScannerConfig(c ScannerConfig) ScannerConfig {
	c.EnabledLibraries = SanitizeStringSlice(c.EnabledLibraries, 256, 256)
	c.LibraryPriority = SanitizeStringSlice(c.LibraryPriority, 256, 256)
	validCaps := make(map[string]bool, len(AllCapabilities))
	for _, capName := range AllCapabilities {
		validCaps[capName] = true
	}
	validTrigs := map[string]bool{"analyze": true, "loudness": true, "chapters": true, "intros": true, "ads": true}

	cleanLibTrigs := make(map[string][]string, len(c.LibraryTriggers))
	for k, vals := range c.LibraryTriggers {
		ck := SanitizeText(k, 256)
		if ck == "" {
			continue
		}
		var kept []string
		for _, v := range vals {
			if validTrigs[v] {
				kept = append(kept, v)
			}
		}
		cleanLibTrigs[ck] = kept
	}
	c.LibraryTriggers = cleanLibTrigs

	cleanLibCaps := make(map[string][]string, len(c.LibraryCapabilities))
	for k, vals := range c.LibraryCapabilities {
		ck := SanitizeText(k, 256)
		if ck == "" {
			continue
		}
		kept := make([]string, 0, len(vals))
		seen := make(map[string]bool, len(vals))
		for _, v := range vals {
			if validCaps[v] && !seen[v] {
				seen[v] = true
				kept = append(kept, v)
			}
		}
		cleanLibCaps[ck] = kept
	}
	c.LibraryCapabilities = cleanLibCaps

	var cleanTrigs []string
	for _, t := range c.Triggers {
		if validTrigs[t] {
			cleanTrigs = append(cleanTrigs, t)
		}
	}
	if len(cleanTrigs) == 0 {
		cleanTrigs = []string{"analyze", "loudness"}
	}
	c.Triggers = cleanTrigs
	switch c.OrderStrategy {
	case "season_first", "newest_first", "oldest_first", "smallest_first", "largest_first", "alphabetical", "alphabetical_desc", "fewest_attempts_first":
	default:
		c.OrderStrategy = "season_first"
	}
	switch c.NumberLocale {
	case "de-DE", "en-US", "de-CH", "en-GB", "fr-FR", "es-ES", "it-IT", "en-IN", "ar-EG", "sv-SE", "ja-JP", "zh-CN", "ko-KR", "pt-BR", "nl-NL", "pl-PL":
	default:
		c.NumberLocale = "de-DE"
	}
	switch c.DateTimeLocale {
	case "sv-SE", "de-DE", "en-US", "en-GB", "en-CA", "de-CH", "fr-FR", "es-ES", "it-IT", "nl-NL", "ja-JP", "zh-CN", "ko-KR", "pt-BR", "ar-SA":
	default:
		c.DateTimeLocale = "de-DE"
	}
	switch c.TimeFormat {
	case "12h", "24h":
	default:
		c.TimeFormat = "24h"
	}
	switch c.Theme {
	case "system", "light", "dark":
	default:
		c.Theme = "system"
	}
	switch c.AccentColor {
	case "amber", "blue", "emerald", "rose", "violet", "orange":
	default:
		c.AccentColor = "amber"
	}
	if c.DefaultPageSize != 50 && c.DefaultPageSize != 100 && c.DefaultPageSize != 250 {
		c.DefaultPageSize = 50
	}
	c.DriveBudgetGiB = clampFloat(c.DriveBudgetGiB, 1, 100000, 450)
	c.DriveWindowHours = clampFloat(c.DriveWindowHours, 0.5, 720, 24)
	c.MaxAttempts = clampInt(c.MaxAttempts, 1, 20, 3)
	c.InterItemDelaySeconds = clampInt(c.InterItemDelaySeconds, 0, 3600, 5)
	c.MaxItemScanMinutes = clampInt(c.MaxItemScanMinutes, 1, 360, 45)
	c.CircuitBreakerThreshold = clampInt(c.CircuitBreakerThreshold, 2, 50, 5)
	c.CatalogRefreshMinutes = clampInt(c.CatalogRefreshMinutes, 0, 1440, 60)
	c.FullSyncIntervalHours = clampInt(c.FullSyncIntervalHours, 0, 720, 24)
	c.IndexWorkers = clampInt(c.IndexWorkers, 1, 16, 4)
	c.BulkPageSize = clampInt(c.BulkPageSize, 50, 500, 200)
	c.MaxActiveStreams = clampInt(c.MaxActiveStreams, 1, 50, 1)
	c.RetryInitialSeconds = clampInt(c.RetryInitialSeconds, 10, 86400, 300)
	c.RetryMaxSeconds = clampInt(c.RetryMaxSeconds, 30, 604800, 86400)
	c.QuietHoursStart = clampInt(c.QuietHoursStart, 0, 23, 19)
	c.QuietHoursEnd = clampInt(c.QuietHoursEnd, 0, 23, 23)
	c.WebhookHoldSeconds = clampInt(c.WebhookHoldSeconds, 0, 3600, 60)
	c.WebhookDebounceSeconds = clampInt(c.WebhookDebounceSeconds, 0, 600, 30)
	c.WebhookMaxWaitSeconds = clampInt(c.WebhookMaxWaitSeconds, 10, 1800, 180)
	c.WebhookPickupWaitSeconds = clampInt(c.WebhookPickupWaitSeconds, 2, 120, 15)
	c.LogRetentionDays = clampInt(c.LogRetentionDays, 1, 365, 14)
	c.HistoryRetentionDays = clampInt(c.HistoryRetentionDays, 1, 365, 30)
	c.MaxLogRows = clampInt(c.MaxLogRows, 500, 100000, 5000)
	cleanMaps := make([]PathMapping, 0, len(c.PathMappings))
	for _, pm := range c.PathMappings {
		rem := strings.TrimRight(SanitizeText(pm.Remote, 512), "/")
		loc := strings.TrimRight(SanitizeText(pm.Local, 512), "/")
		if rem != "" && loc != "" {
			cleanMaps = append(cleanMaps, PathMapping{Remote: rem, Local: loc})
		}
	}
	c.PathMappings = cleanMaps
	cleanArrMaps := make([]PathMapping, 0, len(c.ArrPathMappings))
	for _, pm := range c.ArrPathMappings {
		rem := strings.TrimRight(SanitizeText(pm.Remote, 512), "/")
		loc := strings.TrimRight(SanitizeText(pm.Local, 512), "/")
		if rem != "" && loc != "" {
			cleanArrMaps = append(cleanArrMaps, PathMapping{Remote: rem, Local: loc})
		}
	}
	c.ArrPathMappings = cleanArrMaps
	if !isFinite(c.PausedUntil) || c.PausedUntil < 0 {
		c.PausedUntil = 0
	}
	return c
}

func clampInt(v, minV, maxV, def int) int {
	if v < minV || v > maxV {
		if v == 0 && minV > 0 {
			return def
		}
		if v < minV {
			return minV
		}
		return maxV
	}
	return v
}

func clampFloat(v, minV, maxV, def float64) float64 {
	if !isFinite(v) || v == 0 {
		return def
	}
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}
