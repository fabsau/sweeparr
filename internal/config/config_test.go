package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyYAMLConfigValidMultiServerAndSecrets(t *testing.T) {
	dir := t.TempDir()
	tok1 := filepath.Join(dir, "plex1.tok")
	tok2 := filepath.Join(dir, "plex2.tok")
	mkFile := filepath.Join(dir, "master.key")
	pwFile := filepath.Join(dir, "admin.pw")
	_ = os.WriteFile(tok1, []byte("secret-token-one\n"), 0o600)
	_ = os.WriteFile(tok2, []byte("secret-token-two\n"), 0o600)
	_ = os.WriteFile(mkFile, []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"), 0o600)
	_ = os.WriteFile(pwFile, []byte("S3cur3-Boot-Pass!\n"), 0o600)

	cfgPath := filepath.Join(dir, "config.yaml")
	yamlContent := `
server:
  public_url: "https://sweeparr.example.com"
  allowed_hosts: ["plex1.local", "plex2.local"]
  log_level: "debug"
  log_format: "text"
  session_ttl_hours: 48
  master_key_file: "` + mkFile + `"
auth:
  bootstrap_admin:
    username: "rootadmin"
    password_file: "` + pwFile + `"
scanner:
  order_strategy: "newest_first"
  drive_budget_gib: 300
  pause_on_active_streams: true
  quiet_hours:
    enabled: true
    start: 20
    end: 23
servers:
  - id: "plex-main"
    name: "Main Plex"
    kind: "plex"
    url: "https://plex1.local:32400"
    token_file: "` + tok1 + `"
    ssl_verify: false
    enabled_libraries: ["Movies", "TV Shows"]
    library_capabilities:
      Movies: ["loudness", "chapters"]
  - id: "plex-backup"
    name: "Backup Plex"
    kind: "plex"
    url: "https://plex2.local:32400"
    token_file: "` + tok2 + `"
    scanner:
      drive_budget_gib: 120
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	env := EnvConfig{StateDir: dir, Port: "8080"}
	if err := ApplyYAMLConfig(&env, cfgPath); err != nil {
		t.Fatalf("ApplyYAMLConfig failed: %v", err)
	}
	if !env.ConfigManaged {
		t.Fatalf("expected ConfigManaged=true")
	}
	if env.PublicURL != "https://sweeparr.example.com" {
		t.Fatalf("unexpected public_url: %q", env.PublicURL)
	}
	if env.RPID != "sweeparr.example.com" {
		t.Fatalf("expected rp_id auto-derived from public_url, got %q", env.RPID)
	}
	if len(env.RPOrigins) != 1 || env.RPOrigins[0] != "https://sweeparr.example.com" {
		t.Fatalf("expected rp_origins auto-derived from public_url, got %v", env.RPOrigins)
	}
	if env.AdminUser != "rootadmin" || env.AdminPassword != "S3cur3-Boot-Pass!" {
		t.Fatalf("unexpected bootstrap admin: %q", env.AdminUser)
	}
	if len(env.MasterKey) != 32 {
		t.Fatalf("expected 32-byte master key, got %d", len(env.MasterKey))
	}
	if len(env.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(env.Servers))
	}
	if env.Servers[0].Token != "secret-token-one" || env.Servers[0].Config.DriveBudgetGiB != 300 || !env.Servers[0].Config.QuietHoursEnabled {
		t.Fatalf("unexpected server[0]: %+v", env.Servers[0])
	}
	if env.Servers[1].Token != "secret-token-two" || env.Servers[1].Config.DriveBudgetGiB != 120 {
		t.Fatalf("unexpected server[1] override: %+v", env.Servers[1])
	}
}

func TestApplyYAMLConfigRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(cfgPath, []byte("unknown_root_key: 123\nservers: []\n"), 0o600)
	env := EnvConfig{}
	err := ApplyYAMLConfig(&env, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected unknown field error, got: %v", err)
	}
}

func TestApplyYAMLConfigRejectsDuplicateServerID(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "plex.tok")
	_ = os.WriteFile(tok, []byte("abc123\n"), 0o600)
	cfgPath := filepath.Join(dir, "dup.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
servers:
  - id: "dup"
    kind: "plex"
    url: "https://a:32400"
    token_file: "`+tok+`"
  - id: "dup"
    kind: "plex"
    url: "https://b:32400"
    token_file: "`+tok+`"
`), 0o600)
	env := EnvConfig{}
	err := ApplyYAMLConfig(&env, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate id error, got: %v", err)
	}
}

func TestInputSanitizationGuards(t *testing.T) {
	// Ports
	if p, err := ValidatePort("8443"); err != nil || p != "8443" {
		t.Fatalf("expected valid port 8443, got %q %v", p, err)
	}
	for _, bad := range []string{"0", "70000", "-1", "abc", "8080\r\n"} {
		if _, err := ValidatePort(bad); err == nil {
			t.Fatalf("expected port %q to be rejected", bad)
		}
	}

	// Secrets: reject embedded CRLF/NUL and oversized values
	if _, err := ValidateSecretValue("tok\r\nX-Injected: 1", "token"); err == nil {
		t.Fatalf("expected CRLF in secret to be rejected")
	}
	if _, err := ValidateSecretValue("tok\x00abc", "token"); err == nil {
		t.Fatalf("expected NUL in secret to be rejected")
	}
	if _, err := ValidateSecretValue(strings.Repeat("a", MaxSecretValueLen+1), "token"); err == nil {
		t.Fatalf("expected oversized secret to be rejected")
	}

	// Server IDs
	for _, bad := range []string{"../etc", "bad/id", "has space", "ctrl\nid", ""} {
		if err := ValidateServerID(bad); err == nil {
			t.Fatalf("expected server id %q to be rejected", bad)
		}
	}

	// URLs & Origins
	for _, bad := range []string{"javascript:alert(1)", "http://user:pass@host", "https://host\r\n"} {
		if _, err := ValidateHTTPURL(bad, false); err == nil {
			t.Fatalf("expected URL %q to be rejected", bad)
		}
	}
	if _, err := ValidateOrigin("https://example.com/path"); err == nil {
		t.Fatalf("expected origin with path to be rejected")
	}

	// Bounded regular file reader rejects directories and oversized files
	dir := t.TempDir()
	if _, err := ReadBoundedRegularFile(dir, 1024); err == nil {
		t.Fatalf("expected directory read to be rejected")
	}
	bigFile := filepath.Join(dir, "big.txt")
	_ = os.WriteFile(bigFile, make([]byte, 2048), 0o600)
	if _, err := ReadBoundedRegularFile(bigFile, 1024); err == nil {
		t.Fatalf("expected oversized file to be rejected")
	}

	// ScannerConfig handles NaN, Inf, unknown capabilities, and control chars
	cfg := DefaultScannerConfig()
	cfg.DriveBudgetGiB = math.NaN()
	cfg.DriveWindowHours = math.Inf(1)
	cfg.PausedUntil = math.NaN()
	cfg.EnabledLibraries = []string{"Movies\r\n", "", "Movies"}
	cfg.LibraryCapabilities = map[string][]string{
		"Movies\x00": {"loudness", "evil_cap", "loudness"},
	}
	clean := SanitizeScannerConfig(cfg)
	if math.IsNaN(clean.DriveBudgetGiB) || clean.DriveBudgetGiB != 450 {
		t.Fatalf("expected NaN DriveBudgetGiB reset to 450, got %v", clean.DriveBudgetGiB)
	}
	if math.IsInf(clean.DriveWindowHours, 0) || clean.DriveWindowHours != 24 {
		t.Fatalf("expected Inf DriveWindowHours reset to 24, got %v", clean.DriveWindowHours)
	}
	if len(clean.EnabledLibraries) != 1 || clean.EnabledLibraries[0] != "Movies" {
		t.Fatalf("expected sanitized deduped EnabledLibraries, got %v", clean.EnabledLibraries)
	}
	if caps := clean.LibraryCapabilities["Movies"]; len(caps) != 1 || caps[0] != "loudness" {
		t.Fatalf("expected allowlisted LibraryCapabilities, got %v", caps)
	}
}
