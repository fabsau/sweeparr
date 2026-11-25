package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fabsau/sweeparr/internal/auth"
	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/engine"
)

func registerServerRoutes(r chi.Router, database *db.DB, authSvc *auth.Service, mgr *engine.Manager) {
	// --- Multi-Server Scanner Read Routes ---
	r.Group(func(r chi.Router) {
		r.Use(authSvc.RequireSessionOrAPIKey("read"))

		r.Get("/api/servers", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"servers": filterServersForUser(r, mgr.ListServers())})
		})

		r.Get("/api/servers/{serverID}/status", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				writeJSON(w, http.StatusOK, wk.GetStatus())
			}
		})

		r.Get("/api/servers/{serverID}/stream", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher, ok := w.(http.Flusher)
			if !ok {
				writeErr(w, http.StatusInternalServerError, "Streaming unsupported")
				return
			}
			ch, unsub := wk.Subscribe()
			defer unsub()

			send := func() {
				b, _ := json.Marshal(wk.GetStatus())
				_, _ = fmt.Fprintf(w, "event: status\ndata: %s\n\n", b)
				flusher.Flush()
			}
			send()
			tk := time.NewTicker(15 * time.Second)
			defer tk.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case <-ch:
					send()
				case <-tk.C:
					_, _ = fmt.Fprintf(w, ": ping\n\n")
					flusher.Flush()
				}
			}
		})

		r.Get("/api/servers/{serverID}/items", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requireWorker(w, r, mgr); !ok {
				return
			}
			res, err := database.QueryItems(parseItemQuery(r))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, res)
		})

		r.Get("/api/servers/{serverID}/groups", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requireWorker(w, r, mgr); !ok {
				return
			}
			res, err := database.QueryGroups(parseItemQuery(r))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, res)
		})

		r.Get("/api/servers/{serverID}/export", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requireWorker(w, r, mgr); !ok {
				return
			}
			items, err := database.ExportItems(parseItemQuery(r))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if strings.EqualFold(r.URL.Query().Get("format"), "json") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-export.json"`)
				w.WriteHeader(http.StatusOK)
				enc := json.NewEncoder(w)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				_ = enc.Encode(map[string]any{"exported_count": len(items), "items": items})
				return
			}
			var buf bytes.Buffer
			buf.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM for clean Excel/spreadsheet ingestion
			cw := csv.NewWriter(&buf)
			_ = cw.Write([]string{"key", "library", "kind", "title", "show_or_artist", "season_or_album", "status", "bytes", "missing", "attempts", "last_error", "added_at", "updated_at"})
			for _, it := range items {
				_ = cw.Write([]string{
					csvSafe(it.Key), csvSafe(it.Library), csvSafe(it.Kind), csvSafe(it.Title),
					csvSafe(it.GrandparentTitle), csvSafe(it.ParentTitle),
					csvSafe(it.Status), strconv.FormatInt(it.Bytes, 10), csvSafe(strings.Join(it.Missing, "; ")),
					strconv.Itoa(it.Attempts), csvSafe(it.LastError),
					strconv.FormatInt(it.AddedAt, 10), strconv.FormatInt(it.UpdatedAt, 10),
				})
			}
			cw.Flush()
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-export.csv"`)
			_, _ = w.Write(buf.Bytes())
		})

		r.Get("/api/servers/{serverID}/items/keys", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requireWorker(w, r, mgr); !ok {
				return
			}
			keys, err := database.QueryMatchingKeys(parseItemQuery(r), 25000)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "total": len(keys)})
		})

		r.Get("/api/servers/{serverID}/queue", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit < 0 {
				limit = 0
			} else if limit > 500 {
				limit = 500
			}
			prio, auto, prioCount, err := database.GetQueuePreview(sid, limit)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			history, _ := database.QueryScanHistory(sid, 100)
			blocklist, _ := database.QueryBlocklist(sid, 250)
			st := wk.GetStatus()
			daemonMap, _ := st["daemon"].(map[string]any)
			writeJSON(w, http.StatusOK, map[string]any{
				"prioritized":       prio,
				"automatic":         auto,
				"prioritized_count": prioCount,
				"current_item":      daemonMap["current_item"],
				"history":           history,
				"blocklist":         blocklist,
			})
		})

		parseSince := func(raw string) int64 {
			now := time.Now().Unix()
			switch strings.ToLower(strings.TrimSpace(raw)) {
			case "1h":
				return now - 3600
			case "6h":
				return now - 6*3600
			case "24h":
				return now - 86400
			case "7d":
				return now - 7*86400
			case "30d":
				return now - 30*86400
			default:
				if ts, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && ts > 0 {
					return ts
				}
				return 0
			}
		}

		r.Get("/api/servers/{serverID}/history", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			q := r.URL.Query()
			page, _ := strconv.Atoi(q.Get("page"))
			ps, _ := strconv.Atoi(q.Get("page_size"))
			if ps > 500 {
				ps = 500
			}
			res, err := database.QueryScanHistoryPage(
				sid,
				config.SanitizeText(q.Get("search"), 256),
				config.SanitizeText(q.Get("library"), 256),
				config.SanitizeText(q.Get("status"), 64),
				config.SanitizeText(q.Get("info"), 64),
				parseSince(q.Get("since")),
				config.SanitizeText(q.Get("sort"), 64),
				page, ps,
			)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, res)
		})

		r.Get("/api/servers/{serverID}/history/export", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			q := r.URL.Query()
			res, err := database.QueryScanHistoryPage(
				wk.Summary().ID,
				config.SanitizeText(q.Get("search"), 256),
				config.SanitizeText(q.Get("library"), 256),
				config.SanitizeText(q.Get("status"), 64),
				config.SanitizeText(q.Get("info"), 64),
				parseSince(q.Get("since")),
				config.SanitizeText(q.Get("sort"), 64),
				1, 250,
			)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if q.Get("format") == "json" {
				w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-history.json"`)
				writeJSON(w, http.StatusOK, res.Items)
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-history.csv"`)
			cw := csv.NewWriter(w)
			_ = cw.Write([]string{"Time", "Title", "Library", "Status", "Added", "Missing", "DurationMs", "Bytes", "Source", "Detail"})
			for _, h := range res.Items {
				_ = cw.Write([]string{
					time.Unix(h.FinishedAt, 0).UTC().Format(time.RFC3339),
					csvSafe(h.Title), csvSafe(h.Library), csvSafe(h.Status),
					csvSafe(strings.Join(h.AddedCaps, ", ")),
					csvSafe(strings.Join(h.MissingCaps, ", ")),
					strconv.FormatInt(h.DurationMs, 10),
					strconv.FormatInt(h.Bytes, 10),
					csvSafe(h.QueuedBy),
					csvSafe(h.Detail),
				})
			}
			cw.Flush()
		})

		r.Get("/api/servers/{serverID}/blocklist", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			q := r.URL.Query()
			page, _ := strconv.Atoi(q.Get("page"))
			ps, _ := strconv.Atoi(q.Get("page_size"))
			if ps > 500 {
				ps = 500
			}
			res, err := database.QueryBlocklistPage(
				sid,
				config.SanitizeText(q.Get("search"), 256),
				config.SanitizeText(q.Get("library"), 256),
				parseSince(q.Get("since")),
				config.SanitizeText(q.Get("sort"), 64),
				page, ps,
			)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, res)
		})

		r.Get("/api/servers/{serverID}/blocklist/export", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			q := r.URL.Query()
			res, err := database.QueryBlocklistPage(
				wk.Summary().ID,
				config.SanitizeText(q.Get("search"), 256),
				config.SanitizeText(q.Get("library"), 256),
				parseSince(q.Get("since")),
				config.SanitizeText(q.Get("sort"), 64),
				1, 250,
			)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if q.Get("format") == "json" {
				w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-blocklist.json"`)
				writeJSON(w, http.StatusOK, res.Items)
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-blocklist.csv"`)
			cw := csv.NewWriter(w)
			_ = cw.Write([]string{"BlocklistedAt", "Title", "Library", "MissingCapabilities", "BlocklistedBy"})
			for _, it := range res.Items {
				ts := it.BlocklistedAt
				if ts == 0 {
					ts = it.UpdatedAt
				}
				_ = cw.Write([]string{
					time.Unix(ts, 0).UTC().Format(time.RFC3339),
					csvSafe(it.Title), csvSafe(it.Library),
					csvSafe(strings.Join(it.Missing, ", ")),
					csvSafe(it.BlocklistedBy),
				})
			}
			cw.Flush()
		})

		r.Get("/api/servers/{serverID}/events", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			q := r.URL.Query()
			page, _ := strconv.Atoi(q.Get("page"))
			ps, _ := strconv.Atoi(q.Get("page_size"))
			if ps <= 0 {
				ps, _ = strconv.Atoi(q.Get("limit"))
			}
			if ps > 500 {
				ps = 500
			}
			res, err := database.QueryEvents(
				sid,
				config.SanitizeText(q.Get("level"), 32),
				config.SanitizeText(q.Get("category"), 64),
				config.SanitizeText(q.Get("search"), 256),
				parseSince(q.Get("since")),
				config.SanitizeText(q.Get("sort"), 32),
				page, ps,
			)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, res)
		})

		r.Get("/api/servers/{serverID}/events/export", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			q := r.URL.Query()
			res, err := database.QueryEvents(
				wk.Summary().ID,
				config.SanitizeText(q.Get("level"), 32),
				config.SanitizeText(q.Get("category"), 64),
				config.SanitizeText(q.Get("search"), 256),
				parseSince(q.Get("since")),
				config.SanitizeText(q.Get("sort"), 32),
				1, 250,
			)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if q.Get("format") == "json" {
				w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-logs.json"`)
				writeJSON(w, http.StatusOK, res.Events)
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="sweeparr-logs.csv"`)
			cw := csv.NewWriter(w)
			_ = cw.Write([]string{"Time", "Level", "Category", "User", "Message"})
			for _, ev := range res.Events {
				_ = cw.Write([]string{
					time.Unix(ev.TS, 0).UTC().Format(time.RFC3339),
					csvSafe(ev.Level), csvSafe(ev.Category), csvSafe(ev.Username), csvSafe(ev.Message),
				})
			}
			cw.Flush()
		})

		r.Get("/api/servers/{serverID}/plex/settings-check", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			rows, err := wk.CheckPlexSettings(r.Context())
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "checks": rows})
		})

		r.Get("/api/servers/{serverID}/plex/thumb", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			p := r.URL.Query().Get("path")
			if !rePlexThumbPath.MatchString(p) {
				writeErr(w, http.StatusBadRequest, "Invalid thumbnail path")
				return
			}
			body, ctype, err := wk.FetchThumb(r.Context(), p)
			if err != nil {
				writeErr(w, http.StatusBadGateway, err.Error())
				return
			}
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "private, max-age=300")
			_, _ = w.Write(body)
		})
	})

	// --- Multi-Server Scanner Control & Config Routes ---
	r.Group(func(r chi.Router) {
		r.Use(authSvc.RequireSessionOrAPIKey("scan"))

		r.Post("/api/servers/{serverID}/control/pause", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			var req struct {
				DurationMinutes int `json:"duration_minutes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.DurationMinutes < 0 {
				req.DurationMinutes = 0
			} else if req.DurationMinutes > 43200 {
				req.DurationMinutes = 43200 // max 30 days
			}
			wk.Pause(req.DurationMinutes)
			writeJSON(w, http.StatusOK, wk.GetStatus())
		})

		r.Post("/api/servers/{serverID}/control/resume", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				wk.Resume()
				writeJSON(w, http.StatusOK, wk.GetStatus())
			}
		})

		r.Post("/api/servers/{serverID}/control/step", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				wk.TriggerStep()
				writeJSON(w, http.StatusOK, wk.GetStatus())
			}
		})

		r.Post("/api/servers/{serverID}/control/ignore-current", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				n := wk.IgnoreCurrent()
				writeJSON(w, http.StatusOK, map[string]any{"ignored": n, "status": wk.GetStatus()})
			}
		})

		r.Post("/api/servers/{serverID}/control/refresh", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				wk.TriggerRefresh(r.URL.Query().Get("full") == "true")
				writeJSON(w, http.StatusOK, wk.GetStatus())
			}
		})

		r.Post("/api/servers/{serverID}/control/clear-queue", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			n, _ := database.ClearPriority(sid)
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"cleared": n})
		})

		r.Post("/api/servers/{serverID}/control/reset-retries", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			var req struct {
				Keys []string `json:"keys"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := database.ResetRetries(sid, config.SanitizeStringSlice(req.Keys, 10000, 512))
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"reset": n})
		})

		r.Post("/api/servers/{serverID}/queue/reorder", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			var req struct {
				Keys []string `json:"keys"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			n, _ := database.ReorderPriority(sid, config.SanitizeStringSlice(req.Keys, 10000, 512))
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"reordered": n})
		})

		r.Post("/api/servers/{serverID}/queue/move-top", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			u := auth.UserFromContext(r.Context())
			var req struct {
				Key string `json:"key"`
			}
			if !decodeJSON(w, r, &req) {
				return
			}
			key := config.SanitizeText(req.Key, 512)
			actor := "system"
			if u != nil && u.Username != "" {
				actor = u.Username
			}
			n, _ := database.EnqueueKeysBy(sid, []string{key}, true, actor, "manual")
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"moved": n})
		})

		r.Post("/api/servers/{serverID}/queue/remove", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			var req struct {
				Key string `json:"key"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := database.RemoveFromPriority(sid, config.SanitizeText(req.Key, 512))
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"removed": n})
		})

		r.Post("/api/servers/{serverID}/items/enqueue", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requireWorker(w, r, mgr); !ok {
				return
			}
			sid := config.SanitizeText(chi.URLParam(r, "serverID"), 64)
			u := auth.UserFromContext(r.Context())
			actor := "system"
			if u != nil && u.Username != "" {
				actor = u.Username
			}
			var req struct {
				Keys  []string `json:"keys"`
				Front bool     `json:"front"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := database.EnqueueKeysBy(sid, config.SanitizeStringSlice(req.Keys, 10000, 512), req.Front, actor, "manual")
			notifyCatalog(mgr, sid, true)
			writeJSON(w, http.StatusOK, map[string]any{"queued": n})
		})

		r.Post("/api/servers/{serverID}/items/enqueue-group", func(w http.ResponseWriter, r *http.Request) {
			if _, ok := requireWorker(w, r, mgr); !ok {
				return
			}
			sid := config.SanitizeText(chi.URLParam(r, "serverID"), 64)
			var req struct {
				Library          string `json:"library"`
				GrandparentTitle string `json:"grandparent_title"`
				ParentTitle      string `json:"parent_title"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := database.EnqueueGroup(
				sid,
				config.SanitizeText(req.Library, 256),
				config.SanitizeText(req.GrandparentTitle, 256),
				config.SanitizeText(req.ParentTitle, 256),
			)
			notifyCatalog(mgr, sid, true)
			writeJSON(w, http.StatusOK, map[string]any{"queued": n})
		})

		r.Post("/api/servers/{serverID}/items/skip", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			u := auth.UserFromContext(r.Context())
			actor := "system"
			if u != nil && u.Username != "" {
				actor = u.Username
			}
			var req struct {
				Key  string `json:"key"`
				Skip bool   `json:"skip"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := database.SetSkipBy(sid, []string{config.SanitizeText(req.Key, 512)}, req.Skip, actor)
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"updated": n})
		})

		r.Post("/api/servers/{serverID}/items/bulk-skip", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			sid := wk.Summary().ID
			u := auth.UserFromContext(r.Context())
			actor := "system"
			if u != nil && u.Username != "" {
				actor = u.Username
			}
			var req struct {
				Keys []string `json:"keys"`
				Skip bool     `json:"skip"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n, _ := database.SetSkipBy(sid, config.SanitizeStringSlice(req.Keys, 10000, 512), req.Skip, actor)
			notifyCatalog(mgr, sid, false)
			writeJSON(w, http.StatusOK, map[string]any{"updated": n})
		})

		r.Post("/api/servers/{serverID}/libraries/scan", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			var req struct {
				Library string `json:"library"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			writeJSON(w, http.StatusOK, map[string]any{"queued": wk.ScanLibrary(config.SanitizeText(req.Library, 256))})
		})

		r.Put("/api/servers/{serverID}/config", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			var cfg config.ScannerConfig
			if !decodeJSON(w, r, &cfg) {
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"config": wk.UpdateConfig(cfg, auth.HasScope(r.Context(), "admin"))})
		})

		r.Post("/api/servers/{serverID}/connection/test", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			id, secs, err := wk.TestConnection(r.Context())
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":            true,
				"friendly_name": id.FriendlyName,
				"version":       id.Version,
				"libraries":     secs,
			})
		})

		r.Post("/api/servers/{serverID}/libraries/discover", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			id, secs, err := wk.TestConnection(r.Context())
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":          true,
				"server_name": id.FriendlyName,
				"libraries":   secs,
			})
		})

		r.Delete("/api/servers/{serverID}/plex/activities/{uuid}", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			uuid := chi.URLParam(r, "uuid")
			if !rePlexActivityUUID.MatchString(uuid) {
				writeErr(w, http.StatusBadRequest, "Invalid activity UUID")
				return
			}
			if err := wk.CancelActivity(r.Context(), uuid); err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/servers/{serverID}/plex/activities/cancel-all", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				cancelled, nonCancellable, interrupted := wk.CancelAllActivities(r.Context())
				writeJSON(w, http.StatusOK, map[string]any{
					"cancelled":       cancelled,
					"non_cancellable": nonCancellable,
					"interrupted":     interrupted,
				})
			}
		})

		r.Post("/api/servers/{serverID}/history/clear", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			if err := database.ClearScanHistory(wk.Summary().ID); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/servers/{serverID}/blocklist/clear", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			n, err := database.ClearBlocklist(wk.Summary().ID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			notifyCatalog(mgr, wk.Summary().ID, false)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unblocked": n})
		})

		r.Post("/api/servers/{serverID}/events/clear", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			if err := database.ClearEvents(wk.Summary().ID); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			notifyCatalog(mgr, wk.Summary().ID, false)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})

		r.Post("/api/servers/{serverID}/maintenance/check-path", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			var req struct {
				Path         string               `json:"path"`
				RemotePath   string               `json:"remote_path"`
				PathMappings []config.PathMapping `json:"path_mappings"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			p := strings.TrimSpace(req.RemotePath)
			if p == "" {
				p = strings.TrimSpace(req.Path)
			}
			writeJSON(w, http.StatusOK, wk.CheckPathAccess(config.SanitizeText(p, 512), req.PathMappings))
		})

		r.Post("/api/servers/{serverID}/arr-triggers/flush", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			flushed := wk.FlushArrTriggers()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "flushed": flushed})
		})

		r.Post("/api/servers/{serverID}/maintenance/cleanup", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			evDel, histDel := wk.RunCleanup()
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":              true,
				"events_deleted":  evDel,
				"history_deleted": histDel,
			})
		})
	})

	// --- Protected External v1 API & Webhooks ---
	r.Route("/api/v1", func(r chi.Router) {
		// Native Plex Webhook receiver (multipart/form-data "payload" or JSON body).
		// Handles library.new by immediately inspecting and queuing the newly added item (replicating Plex's "when media is added" analysis programmatically).
		handlePlexWebhook := func(w http.ResponseWriter, r *http.Request) {
			if pathSrv := config.SanitizeText(chi.URLParam(r, "serverID"), 64); pathSrv != "" {
				if _, ok := requireWorker(w, r, mgr); !ok {
					return
				}
			} else if srvID := config.SanitizeText(r.URL.Query().Get("server_id"), 64); srvID != "" {
				if !auth.CanAccessServerForRequest(r.Context(), srvID) {
					writeErr(w, http.StatusForbidden, "Access to this server is denied")
					return
				}
			}
			var rawPayload []byte
			ct := strings.ToLower(r.Header.Get("Content-Type"))
			if strings.HasPrefix(ct, "multipart/form-data") {
				if err := r.ParseMultipartForm(2 << 20); err == nil {
					if p := strings.TrimSpace(r.FormValue("payload")); p != "" {
						rawPayload = []byte(p)
					}
				}
			}
			var hook struct {
				Event  string `json:"event"`
				Server struct {
					Title string `json:"title"`
					UUID  string `json:"uuid"`
				} `json:"Server"`
				Metadata struct {
					RatingKey           string `json:"ratingKey"`
					Type                string `json:"type"`
					Title               string `json:"title"`
					LibrarySectionTitle string `json:"librarySectionTitle"`
				} `json:"Metadata"`
			}
			if len(rawPayload) > 0 {
				_ = json.Unmarshal(rawPayload, &hook)
			} else {
				_ = json.NewDecoder(r.Body).Decode(&hook)
			}

			ev := config.SanitizeText(hook.Event, 64)
			if ev == "" {
				writeErr(w, http.StatusBadRequest, "Missing Plex webhook payload")
				return
			}
			// Ignore non-addition events (e.g. media.play, media.scrobble) with 200 OK so Plex keeps the webhook healthy.
			if ev != "library.new" {
				writeJSON(w, http.StatusOK, map[string]any{
					"accepted": true,
					"ignored":  true,
					"event":    ev,
				})
				return
			}

			var targets []*engine.ServerWorker
			if pathSrv := config.SanitizeText(chi.URLParam(r, "serverID"), 64); pathSrv != "" {
				wk, ok := requireWorker(w, r, mgr)
				if !ok {
					return
				}
				targets = append(targets, wk)
			} else if srvID := config.SanitizeText(r.URL.Query().Get("server_id"), 64); srvID != "" {
				if !auth.CanAccessServerForRequest(r.Context(), srvID) {
					writeErr(w, http.StatusForbidden, "Access to this server is denied")
					return
				}
				if wk := mgr.GetWorker(srvID); wk != nil {
					targets = append(targets, wk)
				}
			} else {
				srvTitle := strings.TrimSpace(hook.Server.Title)
				allowed := filterServersForUser(r, mgr.ListServers())
				for _, s := range allowed {
					if !s.Enabled {
						continue
					}
					if srvTitle != "" && (strings.EqualFold(s.ServerName, srvTitle) || strings.EqualFold(s.Name, srvTitle)) {
						if wk := mgr.GetWorker(s.ID); wk != nil {
							targets = append(targets, wk)
						}
					}
				}
				if len(targets) == 0 {
					for _, s := range allowed {
						if s.Enabled {
							if wk := mgr.GetWorker(s.ID); wk != nil {
								targets = append(targets, wk)
							}
						}
					}
				}
			}

			rk := config.SanitizeText(hook.Metadata.RatingKey, 64)
			libTitle := config.SanitizeText(hook.Metadata.LibrarySectionTitle, 256)
			kind := strings.ToLower(config.SanitizeText(hook.Metadata.Type, 32))
			directItem := rk != "" && (kind == "movie" || kind == "episode" || kind == "track")

			triggered := make([]string, 0, len(targets))
			queuedCount := 0
			if libTitle != "" && !auth.CanAccessLibraryForRequest(r.Context(), libTitle) {
				writeJSON(w, http.StatusOK, map[string]any{
					"accepted": true,
					"ignored":  true,
					"reason":   "library_not_permitted",
					"library":  libTitle,
				})
				return
			}
			if libTitle == "" && auth.LibraryRestricted(r.Context()) {
				writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "ignored": true, "reason": "library_not_permitted"})
				return
			}
			for _, wk := range targets {
				sid := wk.Summary().ID
				if directItem {
					if _, queued, err := wk.IngestItem(r.Context(), rk, libTitle, func(l string) bool { return auth.CanAccessLibraryForRequest(r.Context(), l) }); err == nil {
						triggered = append(triggered, sid)
						if queued {
							queuedCount++
						}
						continue
					}
				}
				// Container-level addition (show/season/album) or fallback: trigger fast delta sync.
				wk.TriggerRefresh(false)
				triggered = append(triggered, sid)
			}
			mode := "delta_sync"
			if directItem {
				mode = "item_ingest"
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"accepted":   true,
				"source":     "plex",
				"event":      ev,
				"mode":       mode,
				"rating_key": rk,
				"library":    libTitle,
				"queued":     queuedCount,
				"servers":    triggered,
			})
		}

		r.With(authSvc.RequireSessionOrAPIKey("webhook")).Post("/webhooks/plex", handlePlexWebhook)
		r.With(authSvc.RequireSessionOrAPIKey("webhook")).Post("/servers/{serverID}/webhooks/plex", handlePlexWebhook)

		// Native *arr webhook handler (Sonarr, Radarr, Lidarr, scripts) — full Autopulse/Autoscan replacement.
		// Coalesces season/album bursts per directory, triggers targeted Plex library refresh, waits for pickup,
		// and automatically queues all newly imported/incomplete items for capability analysis.
		handleArrWebhook := func(w http.ResponseWriter, r *http.Request) {
			var targets []*engine.ServerWorker
			if pathSrv := config.SanitizeText(chi.URLParam(r, "serverID"), 64); pathSrv != "" {
				wk, ok := requireWorker(w, r, mgr)
				if !ok {
					return
				}
				targets = append(targets, wk)
			} else if srvID := config.SanitizeText(r.URL.Query().Get("server_id"), 64); srvID != "" {
				if !auth.CanAccessServerForRequest(r.Context(), srvID) {
					writeErr(w, http.StatusForbidden, "Access to this server is denied")
					return
				}
				if wk := mgr.GetWorker(srvID); wk != nil {
					targets = append(targets, wk)
				}
			} else {
				for _, s := range filterServersForUser(r, mgr.ListServers()) {
					if !s.Enabled {
						continue
					}
					if wk := mgr.GetWorker(s.ID); wk != nil {
						targets = append(targets, wk)
					}
				}
			}

			var payload struct {
				EventType   string   `json:"eventType"`
				Source      string   `json:"source"`
				Library     string   `json:"library"`
				Path        string   `json:"path"`
				Dir         string   `json:"dir"`
				Paths       []string `json:"paths"`
				Instance    string   `json:"instanceName"`
				Series      *struct {
					Title string `json:"title"`
					Path  string `json:"path"`
				} `json:"series"`
				EpisodeFile *struct {
					Path         string `json:"path"`
					RelativePath string `json:"relativePath"`
				} `json:"episodeFile"`
				EpisodeFiles []struct {
					Path         string `json:"path"`
					RelativePath string `json:"relativePath"`
				} `json:"episodeFiles"`
				RenamedEpisodeFiles []struct {
					Path string `json:"path"`
				} `json:"renamedEpisodeFiles"`
				Movie *struct {
					Title      string `json:"title"`
					FolderPath string `json:"folderPath"`
				} `json:"movie"`
				MovieFile *struct {
					Path         string `json:"path"`
					RelativePath string `json:"relativePath"`
				} `json:"movieFile"`
				Artist *struct {
					Name string `json:"name"`
					Path string `json:"path"`
				} `json:"artist"`
				Album *struct {
					Title string `json:"title"`
				} `json:"album"`
				TrackFiles []struct {
					Path string `json:"path"`
				} `json:"trackFiles"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)

			source := config.SanitizeText(payload.Source, 32)
			if source == "" {
				switch {
				case payload.Series != nil || payload.EpisodeFile != nil || len(payload.EpisodeFiles) > 0:
					source = "Sonarr"
				case payload.Movie != nil || payload.MovieFile != nil:
					source = "Radarr"
				case payload.Artist != nil || payload.Album != nil || len(payload.TrackFiles) > 0:
					source = "Lidarr"
				case payload.Instance != "":
					source = config.SanitizeText(payload.Instance, 32)
				default:
					source = "Webhook"
				}
			}

			evType := strings.TrimSpace(payload.EventType)
			if strings.EqualFold(evType, "Test") {
				writeJSON(w, http.StatusOK, map[string]any{
					"accepted": true,
					"test":     true,
					"source":   source,
				})
				return
			}

			var paths []string
			var titles []string
			addPath := func(p string) {
				p = strings.TrimSpace(p)
				if p != "" {
					paths = append(paths, p)
				}
			}
			addTitle := func(tt string) {
				tt = strings.TrimSpace(tt)
				if tt != "" {
					titles = append(titles, tt)
				}
			}

			if qDir := strings.TrimSpace(r.URL.Query().Get("dir")); qDir != "" {
				addPath(qDir)
			}
			if qPath := strings.TrimSpace(r.URL.Query().Get("path")); qPath != "" {
				addPath(qPath)
			}
			addPath(payload.Path)
			addPath(payload.Dir)
			for _, p := range payload.Paths {
				addPath(p)
			}
			if payload.EpisodeFile != nil {
				if payload.EpisodeFile.Path != "" {
					addPath(payload.EpisodeFile.Path)
				} else if payload.Series != nil && payload.Series.Path != "" && payload.EpisodeFile.RelativePath != "" {
					addPath(strings.TrimRight(payload.Series.Path, "/") + "/" + strings.TrimLeft(payload.EpisodeFile.RelativePath, "/"))
				}
			}
			for _, ef := range payload.EpisodeFiles {
				if ef.Path != "" {
					addPath(ef.Path)
				} else if payload.Series != nil && payload.Series.Path != "" && ef.RelativePath != "" {
					addPath(strings.TrimRight(payload.Series.Path, "/") + "/" + strings.TrimLeft(ef.RelativePath, "/"))
				}
			}
			for _, rf := range payload.RenamedEpisodeFiles {
				addPath(rf.Path)
			}
			if payload.Series != nil {
				addTitle(payload.Series.Title)
				if len(paths) == 0 {
					addPath(payload.Series.Path)
				}
			}
			if payload.MovieFile != nil {
				if payload.MovieFile.Path != "" {
					addPath(payload.MovieFile.Path)
				} else if payload.Movie != nil && payload.Movie.FolderPath != "" && payload.MovieFile.RelativePath != "" {
					addPath(strings.TrimRight(payload.Movie.FolderPath, "/") + "/" + strings.TrimLeft(payload.MovieFile.RelativePath, "/"))
				}
			}
			if payload.Movie != nil {
				addTitle(payload.Movie.Title)
				if len(paths) == 0 {
					addPath(payload.Movie.FolderPath)
				}
			}
			for _, tf := range payload.TrackFiles {
				addPath(tf.Path)
			}
			if payload.Artist != nil {
				addTitle(payload.Artist.Name)
				if len(paths) == 0 {
					addPath(payload.Artist.Path)
				}
			}
			if payload.Album != nil {
				addTitle(payload.Album.Title)
			}

			libHint := config.SanitizeText(payload.Library, 128)
			if libHint == "" {
				libHint = config.SanitizeText(r.URL.Query().Get("library"), 128)
			}
			if libHint != "" && !auth.CanAccessLibraryForRequest(r.Context(), libHint) {
				writeErr(w, http.StatusForbidden, "Access to this library is denied")
				return
			}

			actor := source
			if u := auth.UserFromContext(r.Context()); u != nil && u.Username != "" {
				actor = u.Username
			}

			var triggeredServers []string
			var allTriggers []engine.PendingArrTrigger
			coalesced := false
			for _, wk := range targets {
				res, err := wk.SubmitArrEvent(r.Context(), source, actor, libHint, paths, titles)
				if err != nil || !res.Accepted {
					continue
				}
				triggeredServers = append(triggeredServers, wk.Summary().ID)
				allTriggers = append(allTriggers, res.Triggers...)
				if res.Coalesced {
					coalesced = true
				}
			}

			writeJSON(w, http.StatusOK, map[string]any{
				"accepted":  true,
				"source":    source,
				"event":     evType,
				"coalesced": coalesced,
				"servers":   triggeredServers,
				"triggers":  allTriggers,
			})
		}

		r.With(authSvc.RequireSessionOrAPIKey("webhook")).Post("/webhooks/arr", handleArrWebhook)
		r.With(authSvc.RequireSessionOrAPIKey("webhook")).Post("/servers/{serverID}/webhooks/arr", handleArrWebhook)

		r.With(authSvc.RequireSessionOrAPIKey("read")).Get("/servers", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"servers": filterServersForUser(r, mgr.ListServers())})
		})

		r.With(authSvc.RequireSessionOrAPIKey("read")).Get("/servers/{serverID}/status", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				writeJSON(w, http.StatusOK, wk.GetStatus())
			}
		})

		r.With(authSvc.RequireSessionOrAPIKey("read")).Get("/servers/{serverID}/libraries", func(w http.ResponseWriter, r *http.Request) {
			if wk, ok := requireWorker(w, r, mgr); ok {
				libs := wk.ListLibraries()
				if auth.LibraryRestricted(r.Context()) {
					filtered := libs[:0:0]
					for _, l := range libs {
						if auth.CanAccessLibraryForRequest(r.Context(), l.Title) {
							filtered = append(filtered, l)
						}
					}
					libs = filtered
				}
				writeJSON(w, http.StatusOK, map[string]any{"libraries": libs})
			}
		})

		r.With(authSvc.RequireSessionOrAPIKey("scan")).Post("/servers/{serverID}/sync", func(w http.ResponseWriter, r *http.Request) {
			if auth.LibraryRestricted(r.Context()) {
				writeErr(w, http.StatusForbidden, "Library-scoped API keys cannot trigger a full server sync")
				return
			}
			if wk, ok := requireWorker(w, r, mgr); ok {
				var req struct {
					Mode string `json:"mode"`
					Full bool   `json:"full"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				qMode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
				bMode := strings.ToLower(strings.TrimSpace(req.Mode))
				full := r.URL.Query().Get("full") == "true" || req.Full || qMode == "full" || bMode == "full"
				wk.TriggerRefresh(full)
				mode := "delta"
				if full {
					mode = "full"
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": mode})
			}
		})

		r.With(authSvc.RequireSessionOrAPIKey("scan")).Post("/servers/{serverID}/libraries/scan", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			var req struct {
				Library   string   `json:"library"`
				Libraries []string `json:"libraries"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			queued := 0
			var targets []string
			if len(req.Libraries) > 0 {
				targets = config.SanitizeStringSlice(req.Libraries, 64, 256)
			} else if lib := config.SanitizeText(req.Library, 256); lib != "" {
				targets = []string{lib}
			}
			for _, lib := range targets {
				if !auth.CanAccessLibraryForRequest(r.Context(), lib) {
					writeErr(w, http.StatusForbidden, "Library access denied for this API key: "+lib)
					return
				}
			}
			if len(targets) == 0 {
				if restricted := auth.KeyAllowedLibrariesFromContext(r.Context()); len(restricted) > 0 {
					targets = restricted
				} else {
					targets = []string{""}
				}
			}
			for _, lib := range targets {
				if lib == "*" {
					lib = ""
				}
				queued += wk.ScanLibrary(lib)
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued})
		})

		r.With(authSvc.RequireSessionOrAPIKey("scan")).Post("/servers/{serverID}/libraries/{library}/scan", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			rawLib := chi.URLParam(r, "library")
			if decoded, err := url.PathUnescape(rawLib); err == nil {
				rawLib = decoded
			}
			lib := config.SanitizeText(rawLib, 256)
			if lib == "" {
				writeErr(w, http.StatusBadRequest, "Library name is required")
				return
			}
			if !auth.CanAccessLibraryForRequest(r.Context(), lib) {
				writeErr(w, http.StatusForbidden, "Library access denied for this API key")
				return
			}
			queued := wk.ScanLibrary(lib)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "library": lib, "queued": queued})
		})

		r.With(authSvc.RequireSessionOrAPIKey("scan")).Post("/servers/{serverID}/items/{ratingKey}/scan", func(w http.ResponseWriter, r *http.Request) {
			wk, ok := requireWorker(w, r, mgr)
			if !ok {
				return
			}
			rk := config.SanitizeText(chi.URLParam(r, "ratingKey"), 64)
			if rk == "" {
				writeErr(w, http.StatusBadRequest, "ratingKey is required")
				return
			}
			lib := config.SanitizeText(r.URL.Query().Get("library"), 256)
			it, queued, err := wk.IngestItem(r.Context(), rk, lib, func(l string) bool { return auth.CanAccessLibraryForRequest(r.Context(), l) })
			if errors.Is(err, engine.ErrLibraryDenied) {
				writeErr(w, http.StatusForbidden, "Library access denied for this API key")
				return
			}
			if err != nil {
				writeErr(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued, "item": it})
		})
	})
}
