package version

import (
	"strconv"
	"strings"
)

// Build-time metadata injected via -ldflags.
var (
	Version   = "0.9.0" // x-release-please-version
	Commit    = "dev"
	BuildDate = ""
)

// DefaultRepo is the canonical public GitHub repository for Sweeparr.
const DefaultRepo = "fabsau/sweeparr"

// Clean returns the semantic version without a leading "v".
func Clean(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// parseCore parses major.minor.patch from a SemVer string (ignoring prerelease/build suffix).
func parseCore(v string) ([3]int, bool) {
	v = Clean(v)
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// IsNewer reports whether latest is a strictly newer SemVer than current.
func IsNewer(latest, current string) bool {
	l, lok := parseCore(latest)
	c, cok := parseCore(current)
	if !lok || !cok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] > c[i] {
			return true
		}
		if l[i] < c[i] {
			return false
		}
	}
	// If core versions match, a stable release is newer than a prerelease (e.g. 2.0.0 > 2.0.0-rc.1).
	lPre := strings.Contains(Clean(latest), "-")
	cPre := strings.Contains(Clean(current), "-")
	return !lPre && cPre
}
