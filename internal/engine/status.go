package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
)

func (w *ServerWorker) GetStatus() map[string]any {
	w.mu.RLock()
	catVer := w.catalogVersion
	cachedVer := w.cachedCatalogVer
	w.mu.RUnlock()

	if cachedVer != catVer {
		agg, libs, _ := w.db.GetAggregates(w.rec.ID)
		prio, auto, prioCount, _ := w.db.GetQueuePreview(w.rec.ID, 1)
		var nextUp *db.MediaItem
		if len(prio) > 0 {
			nextUp = &prio[0]
		} else if len(auto) > 0 {
			nextUp = &auto[0]
		}
		w.mu.Lock()
		w.cachedAgg = agg
		w.cachedLibs = libs
		w.cachedNextUp = nextUp
		w.cachedPrioCount = prioCount
		w.cachedCatalogVer = catVer
		w.mu.Unlock()
	}

	usedBytes, activeParts, nextRel := w.db.UsedBudgetBytes(w.rec.ID)
	scanSummary := w.db.GetScanSummary(w.rec.ID)
	pendingTriggers := w.ListPendingArrTriggers()

	w.mu.RLock()
	defer w.mu.RUnlock()
	agg := w.cachedAgg
	libs := w.cachedLibs
	nextUp := w.cachedNextUp
	prioCount := w.cachedPrioCount
	transcodes := 0
	for _, s := range w.sessions {
		if s.Transcoding {
			transcodes++
		}
	}
	budgetBytes := int64(w.cfg.DriveBudgetGiB * gib)
	budgetPct := 0.0
	if budgetBytes > 0 {
		budgetPct = float64(usedBytes) / float64(budgetBytes) * 100.0
	}
	activeItem := w.currentItem
	activeStartedAt := w.currentStartedAt
	externalActive := false
	if activeItem == nil && w.externalActiveItem != nil {
		activeItem = w.externalActiveItem
		activeStartedAt = w.externalStartedAt
		externalActive = true
	}
	return map[string]any{
		"server": ServerSummary{
			ID:         w.rec.ID,
			Name:       w.rec.Name,
			Kind:       w.rec.Kind,
			URL:        w.rec.URL,
			SSLVerify:  w.rec.SSLVerify,
			EnvManaged: w.rec.EnvManaged,
			Enabled:    w.rec.Enabled,
			Connected:  w.connected,
			ServerName: w.serverName,
			Version:    w.serverVersion,
		},
		"version":         w.version,
		"catalog_version": w.catalogVersion,
		"daemon": map[string]any{
			"state":                   w.daemonState,
			"detail":                  w.daemonDetail,
			"paused":                  w.cfg.Paused,
			"paused_until":            w.cfg.PausedUntil,
			"indexing":                w.indexing,
			"index_progress":          map[string]any{"done": w.indexDone, "total": w.indexTotal, "library": w.indexLib},
			"priority_queue_size":     prioCount,
			"next_up":                 nextUp,
			"current_item":            activeItem,
			"current_item_started_at": activeStartedAt,
			"external_active":         externalActive,
			"scan_summary":            scanSummary,
			"pending_triggers":        pendingTriggers,
		},
		"plex": map[string]any{
			"connected":         w.connected,
			"server_name":       w.serverName,
			"version":           w.serverVersion,
			"error":             w.lastErr,
			"active_streams":    len(w.sessions),
			"active_transcodes": transcodes,
			"sessions":          w.sessions,
			"activities":        len(w.activities),
			"activity_list":     w.activities,
		},
		"drive_budget": map[string]any{
			"used_bytes":           usedBytes,
			"budget_bytes":         budgetBytes,
			"used_gib":             math.Round(float64(usedBytes)/gib*100) / 100,
			"budget_gib":           w.cfg.DriveBudgetGiB,
			"percent":              math.Round(budgetPct*10) / 10,
			"window_hours":         w.cfg.DriveWindowHours,
			"active_parts":         activeParts,
			"next_release_seconds": nextRel,
		},
		"totals":         agg,
		"libraries":      libs,
		"config":         w.cfg,
		"config_managed": w.configManaged,
	}
}

func (w *ServerWorker) Pause(minutes int) {
	w.mu.Lock()
	w.cfg.Paused = true
	if minutes > 0 {
		w.cfg.PausedUntil = float64(time.Now().Add(time.Duration(minutes) * time.Minute).Unix())
	} else {
		w.cfg.PausedUntil = 0
	}
	w.daemonState = "paused"
	if w.currentItem != nil {
		w.daemonDetail = "Paused — finishing active Plex task"
	} else {
		w.daemonDetail = "Scanner paused by user"
	}
	cfg := w.cfg
	w.mu.Unlock()
	_ = w.db.UpdateServerConfig(w.rec.ID, cfg)
	w.db.AddEvent(w.rec.ID, "info", "control", "Scanner paused")
	w.wake()
	w.bumpVersion(false)
}

func (w *ServerWorker) Resume() {
	w.mu.Lock()
	w.cfg.Paused = false
	w.cfg.PausedUntil = 0
	cfg := w.cfg
	w.mu.Unlock()
	_ = w.db.UpdateServerConfig(w.rec.ID, cfg)
	w.db.AddEvent(w.rec.ID, "info", "control", "Scanner resumed")
	w.wake()
	w.bumpVersion(false)
}

func (w *ServerWorker) TriggerRefresh(full bool) {
	w.mu.Lock()
	w.refreshReq = true
	if full {
		w.fullRefresh = true
	}
	w.mu.Unlock()
	w.db.AddEvent(w.rec.ID, "info", "control", "Catalog sync queued")
	w.wake()
}

func (w *ServerWorker) TriggerStep() {
	w.mu.Lock()
	w.stepReq = true
	acts := w.activities
	w.mu.Unlock()
	for _, a := range acts {
		if a.Cancellable {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = w.adapter.CancelActivity(ctx, a.UUID)
			cancel()
		}
	}
	w.abortCurrent("skip")
	w.db.AddEvent(w.rec.ID, "info", "control", "Skip Current requested")
	w.wake()
}

func (w *ServerWorker) IgnoreCurrent() int64 {
	w.mu.RLock()
	cur := w.currentItem
	if cur == nil {
		cur = w.externalActiveItem
	}
	w.mu.RUnlock()
	if cur == nil {
		return 0
	}
	n, _ := w.db.SetSkip(w.rec.ID, []string{cur.Key}, true)
	w.abortCurrent("blocklist")
	w.db.AddEvent(w.rec.ID, "info", "control", "Blocklisted current item: "+cur.Title)
	w.bumpVersion(true)
	w.wake()
	return n
}

func (w *ServerWorker) UpdateConfig(cfg config.ScannerConfig, allowMediaMappings bool) config.ScannerConfig {
	clean := config.SanitizeScannerConfig(cfg)
	w.mu.Lock()
	if w.configManaged {
		// Strictly read-only for IaC-managed scanner settings: only GUI display/locale prefs may be updated
		merged := w.cfg
		merged.NumberLocale = clean.NumberLocale
		merged.DateTimeLocale = clean.DateTimeLocale
		merged.TimeFormat = clean.TimeFormat
		merged.Theme = clean.Theme
		merged.AccentColor = clean.AccentColor
		merged.DefaultPageSize = clean.DefaultPageSize
		clean = config.SanitizeScannerConfig(merged)
	} else {
		clean.Paused = w.cfg.Paused
		clean.PausedUntil = w.cfg.PausedUntil
		if !allowMediaMappings {
			clean.PathMappings = w.cfg.PathMappings
		}
	}
	w.cfg = clean
	w.mu.Unlock()
	_ = w.db.UpdateServerConfig(w.rec.ID, clean)
	w.db.AddEvent(w.rec.ID, "info", "config", "Settings updated")
	w.wake()
	w.bumpVersion(true)
	return clean
}

func (w *ServerWorker) TestConnection(ctx context.Context) (media.ServerIdentity, []media.Section, error) {
	id, err := w.adapter.Identity(ctx)
	if err != nil {
		return media.ServerIdentity{}, nil, err
	}
	secs, _ := w.adapter.Sections(ctx)
	return id, secs, nil
}

func (w *ServerWorker) CheckPlexSettings(ctx context.Context) ([]media.SettingsCheckRow, error) {
	return w.adapter.CheckServerSettings(ctx)
}

func (w *ServerWorker) FetchThumb(ctx context.Context, path string) ([]byte, string, error) {
	return w.adapter.FetchThumb(ctx, path)
}

func (w *ServerWorker) CancelActivity(ctx context.Context, uuid string) error {
	return w.adapter.CancelActivity(ctx, uuid)
}

func (w *ServerWorker) CancelAllActivities(ctx context.Context) (cancelled int, nonCancellable int, interrupted bool) {
	// Poll live activities from Plex directly so we don't act on stale cached UUIDs
	acts, err := w.adapter.Activities(ctx)
	if err != nil {
		w.mu.RLock()
		acts = w.activities
		w.mu.RUnlock()
	}
	for _, a := range acts {
		if a.Cancellable {
			if w.adapter.CancelActivity(ctx, a.UUID) == nil {
				cancelled++
			}
		} else {
			nonCancellable++
		}
	}
	interrupted = w.abortCurrent("interrupt")
	// Pause the scanner so interrupting doesn't immediately launch the next item
	w.Pause(0)
	return cancelled, nonCancellable, interrupted
}

func (w *ServerWorker) ScanLibrary(library string) int {
	w.mu.RLock()
	enabled := w.cfg.EnabledLibraries
	w.mu.RUnlock()
	n, _ := w.db.EnqueueLibrary(w.rec.ID, library, enabled)
	w.Resume()
	w.bumpVersion(true)
	return n
}

func (w *ServerWorker) ListLibraries() []db.LibrarySummary {
	_, libs, _ := w.db.GetAggregates(w.rec.ID)
	if libs == nil {
		return []db.LibrarySummary{}
	}
	return libs
}

// CheckPathAccess verifies whether the container can stat and read a remote media file path
// after applying configured path_mappings, reporting container UID/GID and accessibility.
func (w *ServerWorker) CheckPathAccess(remotePath string, overrideMappings []config.PathMapping) map[string]any {
	w.mu.RLock()
	savedMappings := w.cfg.PathMappings
	w.mu.RUnlock()
	mappings := savedMappings
	if len(overrideMappings) > 0 {
		mappings = overrideMappings
	}
	if remotePath == "" {
		page, err := w.db.QueryItems(db.ItemQuery{ServerID: w.rec.ID, Page: 1, PageSize: 20})
		if err == nil {
			for _, it := range page.Items {
				if len(it.Parts) > 0 && it.Parts[0] != "" {
					remotePath = it.Parts[0]
					break
				}
			}
		}
	}
	uid := os.Getuid()
	gid := os.Getgid()
	if remotePath == "" {
		return map[string]any{
			"ok":    false,
			"uid":   uid,
			"gid":   gid,
			"error": "No media file paths indexed yet; enter a sample remote path to test",
		}
	}
	localPath := translatePath(remotePath, mappings)
	res := map[string]any{
		"ok": false, "exists": false, "readable": false,
		"remote_path": remotePath, "local_path": localPath, "uid": uid, "gid": gid,
	}
	rootPath, relativePath := mediaCheckRoot(localPath, savedMappings)
	if rootPath == "" {
		res["error"] = "The path must be inside a saved local media mapping"
		return res
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	defer root.Close()

	info, err := root.Stat(relativePath)
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	res["exists"] = true
	if !info.Mode().IsRegular() {
		res["error"] = "The path must refer to a regular media file"
		return res
	}

	// Nonblocking open avoids hanging if the file is replaced with a FIFO.
	file, err := root.OpenFile(relativePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		res["error"] = "Unable to inspect a regular media file at this path"
		return res
	}
	res["ok"] = true
	res["readable"] = true
	res["size"] = info.Size()
	res["mod_time"] = info.ModTime().Unix()
	return res
}

func mediaCheckRoot(localPath string, mappings []config.PathMapping) (rootPath, relativePath string) {
	if !filepath.IsAbs(localPath) {
		return "", ""
	}
	for _, mapping := range mappings {
		if !filepath.IsAbs(mapping.Local) {
			continue
		}
		root := filepath.Clean(mapping.Local)
		if root == string(filepath.Separator) {
			continue
		}
		rel, err := filepath.Rel(root, localPath)
		if err == nil && rel != "." && filepath.IsLocal(rel) && len(root) > len(rootPath) {
			rootPath, relativePath = root, rel
		}
	}
	return rootPath, relativePath
}

// RunCleanup prunes expired logs and history according to configured retention settings.
func (w *ServerWorker) RunCleanup() (eventsDeleted, historyDeleted int64) {
	w.mu.RLock()
	cfg := w.cfg
	w.mu.RUnlock()
	evDel, histDel := w.db.PruneRetention(w.rec.ID, cfg.LogRetentionDays, cfg.HistoryRetentionDays, cfg.MaxLogRows)
	if evDel > 0 || histDel > 0 {
		w.db.AddEvent(w.rec.ID, "info", "config", fmt.Sprintf("Retention cleanup removed %d log(s) and %d history row(s)", evDel, histDel))
		w.bumpVersion(true)
	}
	return evDel, histDel
}

// IngestItem inspects a single media item directly from the media server (e.g. from a Plex library.new webhook),
// upserts it into SQLite, and queues it for analysis if any enabled capabilities are missing.
var ErrLibraryDenied = errors.New("library access denied for this API key")

func (w *ServerWorker) IngestItem(ctx context.Context, ratingKey, library string, allow ...func(string) bool) (*db.MediaItem, bool, error) {
	it, err := w.adapter.InspectItem(ctx, ratingKey, library)
	if err != nil {
		return nil, false, err
	}
	if it == nil {
		return nil, false, nil
	}
	if len(allow) > 0 && allow[0] != nil && !allow[0](it.Library) {
		return nil, false, ErrLibraryDenied
	}
	w.mu.RLock()
	cfg := w.cfg
	w.mu.RUnlock()
	disabledMap := w.db.GetDisabledCapsMap(w.rec.ID)
	capModesMap := w.db.GetCapModesMap(w.rec.ID)
	st, miss := evaluateItemStatus(*it, cfg, disabledMap[it.Library], capModesMap[it.Library])
	it.Status = st
	it.Missing = miss
	if err := w.db.UpsertItems(w.rec.ID, []db.MediaItem{*it}); err != nil {
		return it, false, err
	}
	queued := false
	if st != "complete" {
		if n, _ := w.db.EnqueueKeysBy(w.rec.ID, []string{it.Key}, cfg.WebhookPrioritizeTop, "system", "webhook"); n > 0 {
			queued = true
			it.Queued = true
		}
		if cfg.WebhookHoldSeconds > 0 {
			holdUntil := time.Now().Unix() + int64(cfg.WebhookHoldSeconds)
			_ = w.db.SetItemHoldUntil(w.rec.ID, it.Key, holdUntil)
			w.db.AddEvent(w.rec.ID, "info", "webhook", fmt.Sprintf("Ingested & held for %ds: %s", cfg.WebhookHoldSeconds, it.Title))
		} else {
			w.db.AddEvent(w.rec.ID, "info", "webhook", "Ingested and queued item: "+it.Title)
		}
		w.wake()
	} else {
		w.db.AddEvent(w.rec.ID, "info", "webhook", "Ingested item (already complete): "+it.Title)
	}
	w.bumpVersion(true)
	return it, queued, nil
}
