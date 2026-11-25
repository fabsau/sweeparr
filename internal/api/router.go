package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/fabsau/sweeparr/internal/auth"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/engine"
	"github.com/fabsau/sweeparr/internal/version"
)

var versionCache struct {
	mu        sync.Mutex
	checkedAt time.Time
	latest    string
	url       string
}

func isUpdateCheckEnabled(database *db.DB) bool {
	if database == nil {
		return false
	}
	v := strings.TrimSpace(database.GetAppSetting("update_check_enabled"))
	if v == "" {
		return false // Off by default so Sweeparr stays completely sealed off from the internet
	}
	return strings.EqualFold(v, "true") || v == "1"
}

func getVersionInfo(database *db.DB) map[string]any {
	cur := version.Clean(version.Version)
	repo := strings.TrimSpace(os.Getenv("SWEEPARR_GITHUB_REPO"))
	if repo == "" {
		repo = version.DefaultRepo
	}
	releaseURL := "https://github.com/" + repo + "/releases/tag/v" + cur
	checkEnabled := isUpdateCheckEnabled(database)

	if !checkEnabled {
		return map[string]any{
			"current_version":      cur,
			"commit":               version.Commit,
			"latest_version":       cur,
			"update_available":     false,
			"update_check_enabled": false,
			"release_url":          releaseURL,
		}
	}

	versionCache.mu.Lock()
	if time.Since(versionCache.checkedAt) > 6*time.Hour {
		client := &http.Client{Timeout: 3 * time.Second}
		req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "Sweeparr/"+cur)
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var rel struct {
					TagName string `json:"tag_name"`
					HTMLURL string `json:"html_url"`
				}
				if json.NewDecoder(resp.Body).Decode(&rel) == nil && rel.TagName != "" {
					versionCache.latest = version.Clean(rel.TagName)
					if rel.HTMLURL != "" {
						versionCache.url = rel.HTMLURL
					}
				}
			}
		}
		versionCache.checkedAt = time.Now()
	}
	latest := versionCache.latest
	urlOut := versionCache.url
	versionCache.mu.Unlock()

	if urlOut == "" {
		urlOut = "https://github.com/" + repo + "/releases/latest"
	}
	if latest == "" {
		latest = cur
	}
	return map[string]any{
		"current_version":      cur,
		"commit":               version.Commit,
		"latest_version":       latest,
		"update_available":     version.IsNewer(latest, cur),
		"update_check_enabled": true,
		"release_url":          urlOut,
	}
}

var (
	rePlexThumbPath    = regexp.MustCompile(`^/library/metadata/[0-9]+/(thumb|art|banner|poster|clearLogo)(/[0-9]+)?$`)
	rePlexActivityUUID = regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)
)

func NewRouter(database *db.DB, authSvc *auth.Service, mgr *engine.Manager, webFS fs.FS) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(bodyLimitMiddleware(1 << 20)) // 1 MiB max request body
	r.Use(requestLoggerMiddleware)
	r.Use(securityHeadersMiddleware)
	authLimiter := newRateLimiter(12, time.Minute)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	r.With(authSvc.RequireSessionOrAPIKey("admin")).Get("/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, getVersionInfo(database))
	})

	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})

	// Protected OpenAPI & Standalone API Docs (authenticated users / read API keys only)
	r.Group(func(r chi.Router) {
		r.Use(authSvc.RequireSessionOrAPIKey("read"))
		r.Get("/api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, OpenAPISpec(resolvePublicBaseURL(r, authSvc.Env())))
		})
		r.Get("/api/docs", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(renderStandaloneDocsHTML(resolvePublicBaseURL(r, authSvc.Env()))))
		})
	})

	registerAuthRoutes(r, database, authSvc, mgr, authLimiter)
	registerServerRoutes(r, database, authSvc, mgr)
	registerSPAHandler(r, webFS)

	return r
}

func registerSPAHandler(r chi.Router, webFS fs.FS) {
	if webFS == nil {
		return
	}
	fileServer := http.FileServer(http.FS(webFS))
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "Not found")
			return
		}
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean != "" {
			if f, err := webFS.Open(clean); err == nil {
				_ = f.Close()
				if strings.HasPrefix(clean, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		idx, err := fs.ReadFile(webFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(idx)
	})
}
