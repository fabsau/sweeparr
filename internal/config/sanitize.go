package config

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxConfigFileBytes = 1 << 20  // 1 MiB
	MaxSecretFileBytes = 64 << 10 // 64 KiB
	MaxSecretValueLen  = 8192
)

var (
	serverIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@+-]{1,63}$`)
	hostCharRe = regexp.MustCompile(`^[a-zA-Z0-9.\[\]:-]{1,253}$`)
)

// HasControlChars reports whether s contains any ASCII/Unicode control character (including CR, LF, NUL).
func HasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// SanitizeText strips control characters and NUL bytes, trims whitespace, and clamps length to maxRunes.
func SanitizeText(s string, maxRunes int) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == 0 || r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			continue
		}
		if !utf8.ValidRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if maxRunes > 0 && utf8.RuneCountInString(out) > maxRunes {
		runes := []rune(out)
		out = strings.TrimSpace(string(runes[:maxRunes]))
	}
	return out
}

// ValidatePort ensures a TCP port string is strictly numeric in 1..65535.
func ValidatePort(raw string) (string, error) {
	if HasControlChars(raw) {
		return "", fmt.Errorf("invalid port: contains control characters")
	}
	p := strings.TrimSpace(raw)
	if p == "" {
		return "8080", nil
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid port %q: must be between 1 and 65535", SanitizeText(p, 32))
	}
	return strconv.Itoa(n), nil
}

// ValidateSafePath rejects NUL bytes, control characters, and overly long filesystem paths.
func ValidateSafePath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}
	if len(p) > 4096 || HasControlChars(p) {
		return "", fmt.Errorf("invalid filesystem path")
	}
	return filepath.Clean(p), nil
}

// ReadBoundedRegularFile safely reads a regular file up to maxBytes, refusing devices, directories, and oversized files.
func ReadBoundedRegularFile(path string, maxBytes int64) ([]byte, error) {
	clean, err := ValidateSafePath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(clean)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", clean)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%q exceeds maximum size of %d bytes", clean, maxBytes)
	}
	f, err := os.Open(clean)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%q exceeds maximum size of %d bytes", clean, maxBytes)
	}
	return data, nil
}

// ValidateSecretValue trims surrounding whitespace/newlines and rejects embedded CR/LF/NUL or oversized secrets.
func ValidateSecretValue(raw, fieldName string) (string, error) {
	val := strings.TrimSpace(raw)
	if val == "" {
		return "", fmt.Errorf("%s is empty", fieldName)
	}
	if len(val) > MaxSecretValueLen {
		return "", fmt.Errorf("%s exceeds maximum length (%d bytes)", fieldName, MaxSecretValueLen)
	}
	if HasControlChars(val) {
		return "", fmt.Errorf("%s contains invalid control characters or embedded newlines", fieldName)
	}
	return val, nil
}

// ValidateHTTPURL validates an http/https URL, blocking control characters and embedded credentials.
func ValidateHTTPURL(raw string, allowQuery bool) (string, error) {
	if HasControlChars(raw) {
		return "", fmt.Errorf("invalid URL")
	}
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 2048 {
		return "", fmt.Errorf("invalid URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL scheme must be http or https")
	}
	if u.User != nil {
		return "", fmt.Errorf("credentials in URL are not allowed")
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("URL host is required")
	}
	u.Fragment = ""
	if !allowQuery {
		u.RawQuery = ""
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// ValidateOrigin validates a WebAuthn / CORS origin (scheme + host + optional port, no path/query).
func ValidateOrigin(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 512 || HasControlChars(s) {
		return "", fmt.Errorf("invalid origin")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid origin %q", SanitizeText(s, 64))
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin %q must not contain path, query, or fragment", SanitizeText(s, 64))
	}
	return u.Scheme + "://" + u.Host, nil
}

// ValidateServerID enforces a URL-safe slug for server IDs.
func ValidateServerID(id string) error {
	if !serverIDRe.MatchString(id) {
		return fmt.Errorf("invalid server id %q: must match [a-zA-Z0-9][a-zA-Z0-9._-]{0,63}", SanitizeText(id, 64))
	}
	if id == "." || id == ".." {
		return fmt.Errorf("invalid server id %q", id)
	}
	return nil
}

// ValidateUsername enforces safe characters and bounds for usernames.
func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return fmt.Errorf("username must be 2–64 characters (letters, digits, . _ @ + -)")
	}
	return nil
}

// SanitizeHost validates a single hostname or IP for AllowedHosts / RPID.
func SanitizeHost(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" || !hostCharRe.MatchString(h) {
		return "", fmt.Errorf("invalid host %q", SanitizeText(h, 64))
	}
	return h, nil
}

// SanitizeStringSlice strips control chars, trims, drops empty entries, deduplicates, and bounds count/length.
func SanitizeStringSlice(in []string, maxItems, maxRunes int) []string {
	out := make([]string, 0, min(len(in), maxItems))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		s := SanitizeText(raw, maxRunes)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
