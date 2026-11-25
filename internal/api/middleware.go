package api

import (
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/fabsau/sweeparr/internal/auth"
	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/engine"
)

func requireWorker(w http.ResponseWriter, r *http.Request, mgr *engine.Manager) (*engine.ServerWorker, bool) {
	sid := config.SanitizeText(chi.URLParam(r, "serverID"), 64)
	wk := mgr.GetWorker(sid)
	if wk == nil {
		writeErr(w, http.StatusNotFound, "Server not found")
		return nil, false
	}
	if u := auth.UserFromContext(r.Context()); u != nil && !auth.CanAccessServerForRequest(r.Context(), wk.Summary().ID) {
		writeErr(w, http.StatusForbidden, "Access to this server is denied")
		return nil, false
	}
	return wk, true
}

func filterServersForUser(r *http.Request, servers []engine.ServerSummary) []engine.ServerSummary {
	out := make([]engine.ServerSummary, 0, len(servers))
	for _, s := range servers {
		if auth.CanAccessServerForRequest(r.Context(), s.ID) {
			out = append(out, s)
		}
	}
	return out
}

func notifyCatalog(mgr *engine.Manager, serverID string, resume bool) {
	if wk := mgr.GetWorker(serverID); wk != nil {
		if resume {
			wk.Resume()
		}
		wk.NotifyCatalogChanged()
	}
}

func parseItemQuery(r *http.Request) db.ItemQuery {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	} else if page > 100000 {
		page = 100000
	}
	ps, _ := strconv.Atoi(q.Get("page_size"))
	if ps < 0 {
		ps = 0
	} else if ps > 500 {
		ps = 500
	}
	return db.ItemQuery{
		ServerID: config.SanitizeText(chi.URLParam(r, "serverID"), 64),
		Search:   config.SanitizeText(q.Get("search"), 256),
		Library:  config.SanitizeText(q.Get("library"), 256),
		Status:   config.SanitizeText(q.Get("status"), 64),
		Cap:      config.SanitizeText(q.Get("capability"), 64),
		Sort:     config.SanitizeText(q.Get("sort"), 64),
		Page:     page,
		PageSize: ps,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON")
		return false
	}
	return true
}

func bodyLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_ip", clientIP(r),
		)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peerIP := net.ParseIP(host)
	// Only trust X-Forwarded-For / X-Real-IP when immediate peer is loopback or private reverse-proxy network.
	if peerIP != nil && (peerIP.IsLoopback() || peerIP.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if candidate := strings.TrimSpace(parts[0]); net.ParseIP(candidate) != nil {
				return candidate
			}
		}
		if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(xrip) != nil {
			return xrip
		}
	}
	return host
}

func csvSafe(s string) string {
	if s == "" {
		return ""
	}
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

type ipRateLimiter struct {
	mu           sync.Mutex
	max          int
	window       time.Duration
	hits         map[string][]time.Time
	blockedUntil map[string]time.Time
	strikes      map[string]int
}

func newRateLimiter(max int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		max:          max,
		window:       window,
		hits:         map[string][]time.Time{},
		blockedUntil: map[string]time.Time{},
		strikes:      map[string]int{},
	}
}

func (l *ipRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		now := time.Now()
		cutoff := now.Add(-l.window)
		l.mu.Lock()
		if until, ok := l.blockedUntil[ip]; ok && now.Before(until) {
			retryAfter := int(time.Until(until).Seconds()) + 1
			l.mu.Unlock()
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			slog.Warn("brute-force lockout active", "remote_ip", ip, "path", r.URL.Path, "retry_after_s", retryAfter)
			writeErr(w, http.StatusTooManyRequests, "Too many authentication attempts; try again later")
			return
		}
		if len(l.hits) > 512 {
			for k, ts := range l.hits {
				if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
					delete(l.hits, k)
					delete(l.strikes, k)
					delete(l.blockedUntil, k)
				}
			}
		}
		arr := l.hits[ip]
		kept := arr[:0]
		for _, t := range arr {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) >= l.max {
			l.hits[ip] = kept
			l.strikes[ip]++
			lockMins := 2 << min(l.strikes[ip]-1, 3) // 2m, 4m, 8m, 16m progressive lockout
			if lockMins > 15 {
				lockMins = 15
			}
			l.blockedUntil[ip] = now.Add(time.Duration(lockMins) * time.Minute)
			l.mu.Unlock()
			w.Header().Set("Retry-After", strconv.Itoa(lockMins*60))
			slog.Warn("auth rate limit exceeded; IP temporarily blocked", "remote_ip", ip, "path", r.URL.Path, "lockout_minutes", lockMins)
			writeErr(w, http.StatusTooManyRequests, "Too many authentication attempts; try again later")
			return
		}
		l.hits[ip] = append(kept, now)
		l.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Embedder-Policy", "require-corp")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "accelerometer=(), autoplay=(), bluetooth=(), browsing-topics=(), camera=(), display-capture=(), fullscreen=(self), geolocation=(), gyroscope=(), hid=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-create=(self), publickey-credentials-get=(self), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; manifest-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Del("Server")
		h.Del("X-Powered-By")
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		next.ServeHTTP(w, r)
	})
}

var reSafeHostHeader = regexp.MustCompile(`^([a-zA-Z0-9.-]+|\[[0-9a-fA-F:]+\])(:[0-9]{1,5})?$`)

// resolvePublicBaseURL returns SWEEPARR_PUBLIC_URL if configured, otherwise securely derives the
// request origin after validating Host against trusted format/RPOrigins rules.
func resolvePublicBaseURL(r *http.Request, env config.EnvConfig) string {
	if env.PublicURL != "" {
		return env.PublicURL
	}
	host := strings.TrimSpace(r.Host)
	if host != "" && reSafeHostHeader.MatchString(host) {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		candidate := scheme + "://" + host
		// If non-default RPOrigins are configured, only accept matching origins or RPID host.
		hasCustomOrigin := false
		for _, o := range env.RPOrigins {
			if !strings.Contains(o, "localhost") {
				hasCustomOrigin = true
			}
			if strings.EqualFold(strings.TrimRight(o, "/"), candidate) {
				return candidate
			}
		}
		if !hasCustomOrigin || (env.RPID != "" && env.RPID != "localhost" && strings.EqualFold(strings.Split(host, ":")[0], env.RPID)) {
			return candidate
		}
	}
	for _, o := range env.RPOrigins {
		if pu, err := url.Parse(o); err == nil && (pu.Scheme == "http" || pu.Scheme == "https") && pu.Hostname() != "" && pu.Hostname() != "localhost" {
			return strings.TrimRight(o, "/")
		}
	}
	return "https://your-sweeparr-host"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"detail": msg})
}

func renderStandaloneDocsHTML(baseURL string) string {
	spec := OpenAPISpec(baseURL)
	paths, _ := spec["paths"].(map[string]any)
	var b strings.Builder
	for path, methodsAny := range paths {
		methods, _ := methodsAny.(map[string]any)
		for m, opAny := range methods {
			op, _ := opAny.(map[string]any)
			sum, _ := op["summary"].(string)
			desc, _ := op["description"].(string)
			b.WriteString(`<div class="ep"><div><span class="method">`)
			b.WriteString(html.EscapeString(strings.ToUpper(m)))
			b.WriteString(`</span><strong>`)
			b.WriteString(html.EscapeString(path))
			b.WriteString(`</strong></div><p>`)
			b.WriteString(html.EscapeString(sum))
			if desc != "" {
				b.WriteString(" — ")
				b.WriteString(html.EscapeString(desc))
			}
			b.WriteString(`</p></div>`)
		}
	}
	escBase := html.EscapeString(baseURL)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <meta name="robots" content="noindex, nofollow, noarchive, nosnippet" />
  <title>Sweeparr API Docs</title>
  <style>
    body { margin:0; font-family:system-ui,-apple-system,sans-serif; background:#0b0e14; color:#eef2f7; padding:32px; max-width:960px; margin:0 auto; }
    h1 { color:#f5b82e; }
    .ep { background:#121722; border:1px solid rgba(255,255,255,0.08); border-radius:10px; padding:14px 16px; margin-bottom:12px; }
    .method { font-family:monospace; font-weight:700; padding:2px 8px; border-radius:5px; margin-right:8px; background:rgba(229,160,13,0.18); color:#f5b82e; }
    code { font-family:monospace; background:#181f2e; padding:2px 6px; border-radius:4px; }
    a { color:#38bdf8; }
  </style>
</head>
<body>
  <h1>Sweeparr API v2</h1>
  <p>Base URL: <code>%s</code> &middot; OpenAPI 3.0.3 JSON spec: <a href="/api/openapi.json">/api/openapi.json</a></p>
  <p>Authenticate external calls using <code>X-Api-Key: swp_...</code> or <code>Authorization: Bearer swp_...</code>.</p>
  <div id="list">%s</div>
</body>
</html>`, escBase, b.String())
}
