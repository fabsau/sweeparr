package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
)

type ServerWorker struct {
	db      *db.DB
	rec     db.ServerRecord
	adapter media.Adapter

	mu               sync.RWMutex
	cfg              config.ScannerConfig
	daemonState      string
	daemonDetail     string
	connected        bool
	serverName       string
	serverVersion    string
	lastErr          string
	sessions         []media.SessionInfo
	activities       []media.ActivityInfo
	indexing         bool
	indexDone        int
	indexTotal       int
	indexLib         string
	version          int64
	catalogVersion   int64
	cachedCatalogVer int64
	cachedAgg        db.Aggregates
	cachedLibs       []db.LibrarySummary
	cachedNextUp     *db.MediaItem
	cachedPrioCount  int
	refreshReq       bool
	fullRefresh      bool
	stepReq          bool
	consecFailures   int
	currentItem        *db.MediaItem
	currentStartedAt   int64
	externalActiveItem *db.MediaItem
	externalStartedAt  int64
	abortReason        string
	itemCancel         context.CancelFunc
	configManaged      bool

	wakeCh      chan struct{}
	abortCh     chan struct{}
	arrWakeCh   chan struct{}
	stopCh      chan struct{}
	started     bool
	arrMu       sync.Mutex
	arrTriggers map[string]*PendingArrTrigger
	subMu       sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func newServerWorker(database *db.DB, rec db.ServerRecord, adapter media.Adapter, configManaged bool) *ServerWorker {
	database.ResetStaleActiveItems(rec.ID)
	return &ServerWorker{
		db:               database,
		rec:              rec,
		adapter:          adapter,
		cfg:              config.SanitizeScannerConfig(rec.Config),
		configManaged:    configManaged,
		daemonState:      "starting",
		daemonDetail:     "Initializing scanner engine…",
		cachedCatalogVer: -1,
		refreshReq:       true,
		wakeCh:           make(chan struct{}, 1),
		abortCh:          make(chan struct{}, 1),
		arrWakeCh:        make(chan struct{}, 1),
		stopCh:           make(chan struct{}),
		arrTriggers:      map[string]*PendingArrTrigger{},
		subscribers:      map[chan struct{}]struct{}{},
	}
}

func (w *ServerWorker) Start() {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()
	go w.telemetryLoop()
	go w.scanLoop()
	go w.arrLoop()
}

func (w *ServerWorker) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.started = false
	close(w.stopCh)
	w.mu.Unlock()
}

func (w *ServerWorker) wake() {
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

func (w *ServerWorker) NotifyCatalogChanged() {
	w.bumpVersion(true)
}

func (w *ServerWorker) bumpVersion(catalog bool) {
	w.mu.Lock()
	w.version++
	if catalog {
		w.catalogVersion++
	}
	w.mu.Unlock()
	w.subMu.Lock()
	for ch := range w.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	w.subMu.Unlock()
}

func (w *ServerWorker) Subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.subMu.Lock()
	w.subscribers[ch] = struct{}{}
	w.subMu.Unlock()
	return ch, func() {
		w.subMu.Lock()
		delete(w.subscribers, ch)
		w.subMu.Unlock()
	}
}

func (w *ServerWorker) Summary() ServerSummary {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return ServerSummary{
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
	}
}

func (w *ServerWorker) telemetryLoop() {
	w.pollTelemetry()
	tk := time.NewTicker(4 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-tk.C:
			w.pollTelemetry()
		}
	}
}

func (w *ServerWorker) pollTelemetry() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	id, err := w.adapter.Identity(ctx)
	if err != nil {
		w.mu.Lock()
		wasConn := w.connected
		w.connected = false
		w.lastErr = err.Error()
		w.mu.Unlock()
		if wasConn {
			slog.Warn("media server disconnected", "server_id", w.rec.ID, "url", w.rec.URL, "error", err)
			w.bumpVersion(false)
		}
		return
	}
	sess, _ := w.adapter.Sessions(ctx)
	acts, _ := w.adapter.Activities(ctx)
	w.mu.Lock()
	firstConn := !w.connected
	changed := firstConn || len(w.sessions) != len(sess) || len(w.activities) != len(acts)
	w.connected = true
	w.serverName = id.FriendlyName
	w.serverVersion = id.Version
	w.lastErr = ""
	w.sessions = sess
	w.activities = acts
	hasCurrent := w.currentItem != nil
	w.mu.Unlock()
	if firstConn {
		slog.Info("connected to media server", "server_id", w.rec.ID, "name", id.FriendlyName, "version", id.Version, "url", w.rec.URL)
	}
	tableChanged := false
	if !hasCurrent {
		var subs []string
		var picked *db.MediaItem
		for _, a := range acts {
			if a.Subtitle != "" {
				subs = append(subs, a.Subtitle)
				if picked == nil && strings.HasPrefix(a.Type, "media.generate.") {
					picked = w.db.FindItemByActivitySubtitle(w.rec.ID, a.Subtitle)
				}
			}
		}
		if picked == nil && len(subs) > 0 {
			for _, sub := range subs {
				if p := w.db.FindItemByActivitySubtitle(w.rec.ID, sub); p != nil {
					picked = p
					break
				}
			}
		}
		w.mu.Lock()
		prevExtKey := ""
		if w.externalActiveItem != nil {
			prevExtKey = w.externalActiveItem.Key
		}
		if picked != nil {
			w.externalActiveItem = picked
			if prevExtKey != picked.Key {
				w.externalStartedAt = time.Now().Unix()
				changed = true
			}
		} else if w.externalActiveItem != nil {
			w.externalActiveItem = nil
			w.externalStartedAt = 0
			changed = true
		}
		w.mu.Unlock()
		tableChanged = w.db.SyncExternalActiveTitles(w.rec.ID, subs)
	} else {
		w.mu.Lock()
		w.externalActiveItem = nil
		w.externalStartedAt = 0
		w.mu.Unlock()
	}
	if tableChanged {
		w.bumpVersion(true)
	} else if changed || len(sess) > 0 || len(acts) > 0 {
		w.bumpVersion(false)
	}
}

func (w *ServerWorker) hasActiveAnalysis() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, a := range w.activities {
		if strings.HasPrefix(a.Type, "media.generate.") {
			return true
		}
	}
	return false
}

func (w *ServerWorker) abortCurrent(reason string) bool {
	w.mu.Lock()
	active := w.currentItem != nil
	if active {
		w.abortReason = reason
		if w.itemCancel != nil {
			w.itemCancel()
		}
	}
	w.mu.Unlock()
	if active {
		select {
		case w.abortCh <- struct{}{}:
		default:
		}
	}
	return active
}

func (w *ServerWorker) consumeAbortReason() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.abortReason
	w.abortReason = ""
	return r
}

func (w *ServerWorker) scanLoop() {
	lastRefresh := time.Time{}
	lastFullSync := time.Now()
	lastCleanup := time.Time{}
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		w.mu.RLock()
		cfg := w.cfg
		needDelta := cfg.CatalogRefreshMinutes > 0 && time.Since(lastRefresh) >= time.Duration(cfg.CatalogRefreshMinutes)*time.Minute
		needFull := cfg.FullSyncIntervalHours > 0 && time.Since(lastFullSync) >= time.Duration(cfg.FullSyncIntervalHours)*time.Hour
		needRefresh := w.refreshReq || needDelta || needFull
		full := w.fullRefresh || needFull
		step := w.stepReq
		w.mu.RUnlock()

		if time.Since(lastCleanup) >= 6*time.Hour {
			w.db.PruneRetention(w.rec.ID, cfg.LogRetentionDays, cfg.HistoryRetentionDays, cfg.MaxLogRows)
			lastCleanup = time.Now()
		}

		if needRefresh {
			w.syncCatalog(full)
			lastRefresh = time.Now()
			if full {
				lastFullSync = time.Now()
			}
		}

		if cfg.Paused && cfg.PausedUntil > 0 && float64(time.Now().Unix()) >= cfg.PausedUntil {
			w.Resume()
			cfg.Paused = false
		}

		if cfg.Paused && !step {
			w.setDaemon("paused", "Scanner paused by user")
			w.waitNext(15 * time.Second)
			continue
		}

		if cfg.PauseOnActiveStreams && !step {
			w.mu.RLock()
			sess := w.sessions
			w.mu.RUnlock()
			active := len(sess)
			if cfg.PauseOnTranscodesOnly {
				active = 0
				for _, s := range sess {
					if s.Transcoding {
						active++
					}
				}
			}
			if active >= cfg.MaxActiveStreams {
				w.setDaemon("paused_streams", fmt.Sprintf("Paused: %d active stream(s)", active))
				w.waitNext(10 * time.Second)
				continue
			}
		}

		if cfg.QuietHoursEnabled && !step && inQuietHours(cfg.QuietHoursStart, cfg.QuietHoursEnd, time.Now().Hour()) {
			w.setDaemon("quiet_hours", fmt.Sprintf("Quiet hours active (%02d:00–%02d:00)", cfg.QuietHoursStart, cfg.QuietHoursEnd))
			w.waitNext(30 * time.Second)
			continue
		}

		// If Plex is already running a media analysis task (e.g. external trigger or post-restart pickup),
		// wait for it to finish instead of piling a second concurrent scan on top of Plex.
		if !step && w.hasActiveAnalysis() {
			w.setDaemon("scanning", "Active Plex analysis in progress…")
			w.waitNext(4 * time.Second)
			continue
		}

		item, err := w.db.PickNextItem(w.rec.ID, cfg)
		if err != nil || item == nil {
			w.setDaemon("idle", "All caught up — waiting for new or incomplete media")
			w.waitNext(20 * time.Second)
			continue
		}

		w.mu.Lock()
		w.stepReq = false
		w.mu.Unlock()

		if (cfg.HoldPlayingItems || cfg.HoldModifiedFiles) && !step {
			if holdReason := w.checkItemInUse(item, cfg); holdReason != "" {
				holdSec := int64(60)
				if cfg.WebhookHoldSeconds > 60 {
					holdSec = int64(cfg.WebhookHoldSeconds)
				}
				_ = w.db.SetItemHoldUntil(w.rec.ID, item.Key, time.Now().Unix()+holdSec)
				w.db.AddEvent(w.rec.ID, "info", "scan", fmt.Sprintf("Held %s (%s)", item.Title, holdReason))
				w.bumpVersion(true)
				w.waitNext(5 * time.Second)
				continue
			}
		}

		budgetBytes := int64(cfg.DriveBudgetGiB * gib)
		if item.Bytes > budgetBytes {
			_ = w.db.MarkItemResult(w.rec.ID, item.Key, "blocked", item.Attempts, 0, "Item exceeds Drive budget", item.Missing)
			w.db.AddEvent(w.rec.ID, "error", "budget", fmt.Sprintf("Blocked %s: exceeds Drive budget", item.Title))
			w.bumpVersion(true)
			continue
		}
		used, _, nextRel := w.db.UsedBudgetBytes(w.rec.ID)
		if used+item.Bytes > budgetBytes && !step {
			w.setDaemon("budget_wait", fmt.Sprintf("Drive budget full — next slot in %ds", nextRel))
			w.waitNext(30 * time.Second)
			continue
		}

		w.processItem(item, cfg)
		w.waitNext(time.Duration(cfg.InterItemDelaySeconds) * time.Second)
	}
}

func (w *ServerWorker) processItem(item *db.MediaItem, cfg config.ScannerConfig) {
	startedAt := time.Now()
	itemCtx, itemCancel := context.WithTimeout(context.Background(), time.Duration(cfg.MaxItemScanMinutes)*time.Minute)
	// Drain any stale abort signal before starting
	select {
	case <-w.abortCh:
	default:
	}
	w.mu.Lock()
	w.currentItem = item
	w.currentStartedAt = startedAt.Unix()
	w.abortReason = ""
	w.itemCancel = itemCancel
	w.mu.Unlock()
	_ = w.db.MarkItemActive(w.rec.ID, item.Key)
	w.bumpVersion(true)
	defer func() {
		itemCancel()
		w.mu.Lock()
		w.currentItem = nil
		w.currentStartedAt = 0
		w.itemCancel = nil
		w.mu.Unlock()
	}()

	qBy := item.QueuedBy
	if qBy == "" {
		qBy = "system"
	}
	qReason := item.QueuedReason
	if qReason == "" {
		qReason = "auto"
	}

	w.setDaemon("scanning", fmt.Sprintf("Analyzing %s (%s)", item.Title, item.Library))
	if cfg.DryRun {
		w.db.AddEvent(w.rec.ID, "info", "scan", fmt.Sprintf("[dry-run] Would analyze %s (%s)", item.Title, item.Library))
		_ = w.db.MarkItemResult(w.rec.ID, item.Key, "complete", item.Attempts+1, 0, "", []string{})
		w.db.AddScanHistory(db.HistoryRow{
			ServerID: w.rec.ID, ItemKey: item.Key, Title: item.Title, Library: item.Library, Kind: item.Kind,
			Status: "complete", Detail: "Dry-run", AddedCaps: item.Missing, Bytes: item.Bytes, QueuedBy: qBy, QueuedReason: qReason,
			DurationMs: time.Since(startedAt).Milliseconds(), FinishedAt: time.Now().Unix(),
		})
		w.bumpVersion(true)
		return
	}

	_ = w.db.ReserveParts(w.rec.ID, item.Parts, item.Bytes, cfg.DriveWindowHours)
	trigList := triggersForItem(item, cfg)
	_, err := w.adapter.TriggerAnalyze(itemCtx, item.RatingKey, trigList)
	if reason := w.consumeAbortReason(); reason != "" {
		w.handleAbortedItem(item, reason)
		return
	}
	if err != nil {
		w.recordScanFailure(item, cfg, "error:"+err.Error(), nil, item.Missing, true, startedAt)
		return
	}
	w.mu.Lock()
	w.consecFailures = 0
	w.mu.Unlock()

	if aborted := w.waitForSettle(time.Duration(cfg.MaxItemScanMinutes) * time.Minute); aborted {
		reason := w.consumeAbortReason()
		if reason == "" {
			reason = "interrupt"
		}
		w.handleAbortedItem(item, reason)
		return
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	updated, err2 := w.adapter.InspectItem(ctx2, item.RatingKey, item.Library)
	cancel2()
	if err2 != nil || updated == nil {
		if err2 == nil && updated == nil {
			w.db.AddEvent(w.rec.ID, "info", "scan", fmt.Sprintf("Analyzed %s (%s)", item.Title, item.Library))
			_ = w.db.MarkItemResult(w.rec.ID, item.Key, "complete", item.Attempts+1, 0, "", []string{})
			w.db.AddScanHistory(db.HistoryRow{
				ServerID: w.rec.ID, ItemKey: item.Key, Title: item.Title, Library: item.Library, Kind: item.Kind,
				Status: "complete", AddedCaps: item.Missing, Bytes: item.Bytes, QueuedBy: qBy, QueuedReason: qReason,
				DurationMs: time.Since(startedAt).Milliseconds(), FinishedAt: time.Now().Unix(),
			})
			w.bumpVersion(true)
			return
		}
		w.recordScanFailure(item, cfg, fmt.Sprintf("error:post-scan verification failed: %v", err2), nil, item.Missing, false, startedAt)
		return
	}

	disabledMap := w.db.GetDisabledCapsMap(w.rec.ID)
	capModesMap := w.db.GetCapModesMap(w.rec.ID)
	st, filteredMissing := evaluateItemStatus(*updated, cfg, disabledMap[item.Library], capModesMap[item.Library])
	addedCaps := diffCapabilities(item.Missing, filteredMissing)
	if st == "complete" {
		detail := ""
		if len(addedCaps) > 0 {
			detail = "added:" + strings.Join(addedCaps, ",")
		}
		w.db.AddEvent(w.rec.ID, "info", "scan", fmt.Sprintf("Completed %s (%s)", item.Title, item.Library))
		_ = w.db.MarkItemResult(w.rec.ID, item.Key, "complete", item.Attempts+1, 0, "", filteredMissing)
		w.db.AddScanHistory(db.HistoryRow{
			ServerID: w.rec.ID, ItemKey: item.Key, Title: item.Title, Library: item.Library, Kind: item.Kind,
			Status: "complete", Detail: detail, AddedCaps: addedCaps, MissingCaps: []string{}, Bytes: item.Bytes, QueuedBy: qBy, QueuedReason: qReason,
			DurationMs: time.Since(startedAt).Milliseconds(), FinishedAt: time.Now().Unix(),
		})
		w.bumpVersion(true)
		return
	}
	var parts []string
	if len(addedCaps) > 0 {
		parts = append(parts, "added:"+strings.Join(addedCaps, ","))
	}
	if len(filteredMissing) > 0 {
		parts = append(parts, "missing:"+strings.Join(filteredMissing, ","))
	}
	w.recordScanFailure(item, cfg, strings.Join(parts, "|"), addedCaps, filteredMissing, false, startedAt)
}

func translatePath(remote string, mappings []config.PathMapping) string {
	for _, m := range mappings {
		if m.Remote != "" && m.Local != "" && strings.HasPrefix(remote, m.Remote) {
			return m.Local + strings.TrimPrefix(remote, m.Remote)
		}
	}
	return remote
}

func diffCapabilities(beforeMissing, afterMissing []string) []string {
	still := make(map[string]bool, len(afterMissing))
	for _, c := range afterMissing {
		still[c] = true
	}
	var added []string
	for _, c := range beforeMissing {
		if !still[c] {
			added = append(added, c)
		}
	}
	return added
}

// checkItemInUse checks active Plex playback (HoldPlayingItems) and/or recently modified local files (HoldModifiedFiles).
func (w *ServerWorker) checkItemInUse(item *db.MediaItem, cfg config.ScannerConfig) string {
	if cfg.HoldPlayingItems {
		w.mu.RLock()
		sess := w.sessions
		w.mu.RUnlock()
		itemTitle := strings.ToLower(strings.TrimSpace(item.Title))
		for _, s := range sess {
			sTitle := strings.ToLower(strings.TrimSpace(s.Title))
			sRaw := strings.ToLower(strings.TrimSpace(s.RawTitle))
			if (sTitle != "" && sTitle == itemTitle) || (sRaw != "" && sRaw == itemTitle) {
				return "active playback session"
			}
		}
	}
	if cfg.HoldModifiedFiles {
		minAge := time.Duration(max(30, cfg.WebhookHoldSeconds)) * time.Second
		for _, p := range item.Parts {
			if strings.TrimSpace(p) == "" {
				continue
			}
			localPath := translatePath(p, cfg.PathMappings)
			if fi, err := os.Stat(localPath); err == nil {
				if time.Since(fi.ModTime()) < minAge {
					return "file recently modified"
				}
			}
		}
	}
	return ""
}

func (w *ServerWorker) recordScanFailure(item *db.MediaItem, cfg config.ScannerConfig, reason string, addedCaps, missing []string, networkErr bool, startedAt time.Time) {
	attempts := item.Attempts + 1
	status := "retry"
	backoff := int64(math.Min(float64(cfg.RetryMaxSeconds), float64(cfg.RetryInitialSeconds)*math.Pow(2, float64(attempts-1))))
	nextRetry := time.Now().Unix() + backoff
	if attempts >= cfg.MaxAttempts {
		status = "exhausted"
		nextRetry = 0
	}
	qBy := item.QueuedBy
	if qBy == "" {
		qBy = "system"
	}
	qReason := item.QueuedReason
	if qReason == "" {
		qReason = "auto"
	}
	_ = w.db.MarkItemResult(w.rec.ID, item.Key, status, attempts, nextRetry, reason, missing)
	w.db.AddScanHistory(db.HistoryRow{
		ServerID: w.rec.ID, ItemKey: item.Key, Title: item.Title, Library: item.Library, Kind: item.Kind,
		Status: status, Detail: reason, AddedCaps: addedCaps, MissingCaps: missing, Bytes: item.Bytes, QueuedBy: qBy, QueuedReason: qReason,
		DurationMs: time.Since(startedAt).Milliseconds(), FinishedAt: time.Now().Unix(),
	})
	if status == "exhausted" {
		w.db.AddEvent(w.rec.ID, "warning", "scan", fmt.Sprintf("Parked %s after %d attempts: %s", item.Title, attempts, reason))
	} else {
		w.db.AddEvent(w.rec.ID, "warning", "scan", fmt.Sprintf("Retry %s in %ds (%d/%d): %s", item.Title, backoff, attempts, cfg.MaxAttempts, reason))
	}
	if networkErr {
		w.mu.Lock()
		w.consecFailures++
		fails := w.consecFailures
		w.mu.Unlock()
		if fails >= cfg.CircuitBreakerThreshold {
			w.Pause(0)
			w.db.AddEvent(w.rec.ID, "error", "circuit-breaker", fmt.Sprintf("Auto-paused after %d consecutive server errors", fails))
		}
	}
	w.bumpVersion(true)
}

func (w *ServerWorker) handleAbortedItem(item *db.MediaItem, reason string) {
	switch reason {
	case "blocklist":
		// Keep item skipped; do NOT overwrite with retry/exhausted
		w.bumpVersion(true)
	case "skip":
		_ = w.db.MarkItemResult(w.rec.ID, item.Key, "pending", item.Attempts, 0, "", item.Missing)
		w.db.AddEvent(w.rec.ID, "info", "control", fmt.Sprintf("Skipped %s (%s) — returned to pending", item.Title, item.Library))
		w.bumpVersion(true)
	default:
		_ = w.db.MarkItemResult(w.rec.ID, item.Key, "pending", item.Attempts, 0, "", item.Missing)
		w.db.AddEvent(w.rec.ID, "info", "control", fmt.Sprintf("Interrupted %s (%s) — restored to queue", item.Title, item.Library))
		w.bumpVersion(true)
	}
}

func (w *ServerWorker) waitForSettle(maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	graceUntil := time.Now().Add(5 * time.Second)
	sawActivity := false
	emptyPolls := 0

	for time.Now().Before(deadline) {
		select {
		case <-w.stopCh:
			return true
		case <-w.abortCh:
			return true
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		acts, err := w.adapter.Activities(ctx)
		cancel()

		if err == nil {
			if len(acts) > 0 {
				sawActivity = true
				emptyPolls = 0
			} else if sawActivity || time.Now().After(graceUntil) {
				emptyPolls++
				if emptyPolls >= 2 {
					return false
				}
			}
		}

		waitDur := 2 * time.Second
		if !sawActivity && time.Now().Before(graceUntil) {
			waitDur = 1 * time.Second
		}
		t := time.NewTimer(waitDur)
		select {
		case <-w.stopCh:
			t.Stop()
			return true
		case <-w.abortCh:
			t.Stop()
			return true
		case <-t.C:
		}
	}
	return false
}

// triggersForItem maps ONLY the capabilities actually missing on this specific item
// to the minimal set of Plex analysis endpoints, keeping Sweeparr strictly in the driver's seat.
func triggersForItem(item *db.MediaItem, cfg config.ScannerConfig) []string {
	if item == nil || len(item.Missing) == 0 {
		return triggersForLibrary(item.Library, cfg)
	}
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, c := range item.Missing {
		switch c {
		case "deep_analysis", "previews", "sonic", "credits":
			add("analyze")
		case "voice":
			add("voice")
		case "loudness":
			add("loudness")
		case "chapters":
			add("chapters")
		case "intro":
			if item.Kind == "episode" {
				add("intros")
			}
		case "ads":
			if item.IsDVR {
				add("ads")
			}
		}
	}
	if len(out) == 0 {
		return []string{"analyze"}
	}
	return out
}

func triggersForLibrary(library string, cfg config.ScannerConfig) []string {
	if caps, ok := cfg.LibraryCapabilities[library]; ok {
		if len(caps) == 0 {
			return nil
		}
		seen := map[string]bool{}
		var out []string
		add := func(t string) {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		for _, c := range caps {
			switch c {
			case "voice", "deep_analysis", "sonic":
				add("analyze")
			case "loudness":
				add("loudness")
			case "chapters":
				add("chapters")
			case "intro", "credits":
				add("intros")
			case "ads":
				add("ads")
			}
		}
		return out
	}
	if custom, ok := cfg.LibraryTriggers[library]; ok && len(custom) > 0 {
		return custom
	}
	return cfg.Triggers
}

func evaluateItemStatus(it db.MediaItem, cfg config.ScannerConfig, disabledCaps []string, capModes map[string]string) (string, []string) {
	wanted := map[string]bool{}
	if caps, ok := cfg.LibraryCapabilities[it.Library]; ok {
		for _, c := range caps {
			wanted[c] = true
		}
	} else {
		trigList := cfg.Triggers
		if custom, ok := cfg.LibraryTriggers[it.Library]; ok && len(custom) > 0 {
			trigList = custom
		}
		for _, t := range trigList {
			switch t {
			case "analyze":
				if it.Kind == "movie" || it.Kind == "episode" {
					wanted["voice"] = true
					wanted["deep_analysis"] = true
				} else if it.Kind == "track" {
					wanted["sonic"] = true
				}
				wanted["loudness"] = true
			case "loudness":
				wanted["loudness"] = true
			case "chapters":
				if it.Kind == "movie" || it.Kind == "episode" {
					wanted["chapters"] = true
				}
			case "intros":
				if it.Kind == "episode" {
					wanted["intro"] = true
				}
				if it.Kind == "movie" || it.Kind == "episode" {
					wanted["credits"] = true
				}
			case "ads":
				if it.Kind == "movie" || it.Kind == "episode" {
					wanted["ads"] = true
				}
			}
		}
	}
	disabledSet := map[string]bool{}
	if cfg.RespectPlexLibrarySettings {
		for _, dc := range disabledCaps {
			disabledSet[dc] = true
			delete(wanted, dc)
		}
		for capName, mode := range capModes {
			if mode == "disabled" {
				disabledSet[capName] = true
				delete(wanted, capName)
			} else if mode == "recorded_only" && !it.IsDVR {
				disabledSet[capName] = true
				delete(wanted, capName)
			}
		}
	}
	filteredMissing := make([]string, 0, len(it.Missing))
	status := "complete"
	for _, m := range it.Missing {
		if disabledSet[m] {
			continue
		}
		if !wanted[m] {
			continue
		}
		filteredMissing = append(filteredMissing, m)
		status = "pending"
	}
	return status, filteredMissing
}

func (w *ServerWorker) syncCatalog(full bool) {
	w.mu.Lock()
	w.refreshReq = false
	w.fullRefresh = false
	w.indexing = true
	w.indexDone = 0
	w.indexTotal = 0
	w.mu.Unlock()
	w.bumpVersion(false)

	defer func() {
		w.mu.Lock()
		w.indexing = false
		w.mu.Unlock()
		w.bumpVersion(true)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	secs, err := w.adapter.Sections(ctx)
	if err != nil {
		w.db.AddEvent(w.rec.ID, "warning", "sync", fmt.Sprintf("Catalog sync failed: %v", err))
		return
	}
	w.mu.RLock()
	cfg := w.cfg
	pageSize := cfg.BulkPageSize
	w.mu.RUnlock()
	totalCount := 0
	for _, sec := range secs {
		capModes, err := w.adapter.LibraryCapModes(ctx, sec.Key)
		var disabled []string
		if err == nil {
			_ = w.db.SetLibraryCapModes(w.rec.ID, sec.Title, capModes)
			for capName, mode := range capModes {
				if mode == "disabled" {
					disabled = append(disabled, capName)
				}
			}
			_ = w.db.SetLibraryDisabledCaps(w.rec.ID, sec.Title, disabled)
		}
		start := 0
		for {
			select {
			case <-w.stopCh:
				return
			default:
			}
			pageCtx, pageCancel := context.WithTimeout(ctx, 45*time.Second)
			items, tot, err := w.adapter.ListSectionItems(pageCtx, sec, start, pageSize)
			pageCancel()
			if err != nil {
				w.db.AddEvent(w.rec.ID, "warning", "sync", fmt.Sprintf("Failed syncing %s: %v", sec.Title, err))
				break
			}
			if len(items) == 0 {
				break
			}
			for i := range items {
				st, miss := evaluateItemStatus(items[i], cfg, disabled, capModes)
				items[i].Status = st
				items[i].Missing = miss
			}
			_ = w.db.UpsertItems(w.rec.ID, items)
			totalCount += len(items)
			start += len(items)
			w.mu.Lock()
			w.indexLib = sec.Title
			w.indexDone = start
			w.indexTotal = tot
			w.mu.Unlock()
			w.bumpVersion(false)
			if start >= tot {
				break
			}
		}
	}
	mode := "delta"
	if full {
		mode = "full"
	}
	w.db.AddEvent(w.rec.ID, "info", "sync", fmt.Sprintf("Catalog %s sync complete (%d items)", mode, totalCount))
}

func (w *ServerWorker) setDaemon(state, detail string) {
	w.mu.Lock()
	changed := w.daemonState != state || w.daemonDetail != detail
	w.daemonState = state
	w.daemonDetail = detail
	w.mu.Unlock()
	if changed {
		w.bumpVersion(false)
	}
}

func (w *ServerWorker) waitNext(d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-w.stopCh:
	case <-w.wakeCh:
	case <-t.C:
	}
}

func inQuietHours(start, end, hour int) bool {
	if start == end {
		return false
	}
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}
