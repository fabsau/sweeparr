package engine

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
)

// PendingArrTrigger tracks a debounced/coalesced *arr directory refresh + capability scan batch.
type PendingArrTrigger struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Actor       string   `json:"actor"`
	Library     string   `json:"library"`
	SectionKey  string   `json:"section_key"`
	Dir         string   `json:"dir"`
	FileCount   int      `json:"file_count"`
	Titles      []string `json:"titles,omitempty"`
	State       string   `json:"state"` // debouncing | refreshing_plex | syncing | queued
	FirstSeenAt int64    `json:"first_seen_at"`
	FiresAt     int64    `json:"fires_at"`

	files map[string]struct{}
}

// ArrSubmitResult summarizes how an incoming *arr webhook batch was accepted/coalesced.
type ArrSubmitResult struct {
	Accepted  bool                `json:"accepted"`
	Coalesced bool                `json:"coalesced"`
	Triggers  []PendingArrTrigger `json:"triggers"`
}

func isMediaFilePath(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".m2ts", ".wmv", ".webm",
		".flac", ".mp3", ".m4a", ".ogg", ".opus", ".wav", ".alac", ".aac", ".wma", ".aiff",
		".srt", ".ass", ".ssa", ".sub", ".vtt", ".nfo":
		return true
	default:
		return false
	}
}

func normalizeArrPath(raw string, mappings []config.PathMapping) string {
	p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if p == "" {
		return ""
	}
	// Apply longest-prefix *arr -> Plex mapping
	bestLen := 0
	mapped := p
	for _, m := range mappings {
		rem := strings.TrimRight(strings.ReplaceAll(m.Remote, "\\", "/"), "/")
		loc := strings.TrimRight(strings.ReplaceAll(m.Local, "\\", "/"), "/")
		if rem == "" || loc == "" {
			continue
		}
		if (p == rem || strings.HasPrefix(p, rem+"/")) && len(rem) > bestLen {
			bestLen = len(rem)
			mapped = loc + p[len(rem):]
		}
	}
	return path.Clean(mapped)
}

func dirForArrPath(p string) string {
	if p == "" {
		return ""
	}
	if isMediaFilePath(p) {
		return path.Dir(p)
	}
	return strings.TrimRight(p, "/")
}

func matchSectionForPath(sections []media.Section, dirPath, libraryHint string) (media.Section, bool) {
	bestLen := 0
	var best media.Section
	found := false
	if dirPath != "" {
		normDir := strings.TrimRight(dirPath, "/")
		for _, sec := range sections {
			for _, loc := range sec.Locations {
				normLoc := strings.TrimRight(strings.ReplaceAll(loc, "\\", "/"), "/")
				if normLoc == "" {
					continue
				}
				if (normDir == normLoc || strings.HasPrefix(normDir, normLoc+"/")) && len(normLoc) > bestLen {
					bestLen = len(normLoc)
					best = sec
					found = true
				}
			}
		}
	}
	if found {
		return best, true
	}
	if hint := strings.TrimSpace(libraryHint); hint != "" {
		for _, sec := range sections {
			if strings.EqualFold(sec.Title, hint) {
				return sec, true
			}
		}
	}
	return media.Section{}, false
}

func (w *ServerWorker) getCfgSnapshot() config.ScannerConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cfg
}

// SubmitArrEvent coalesces incoming Sonarr/Radarr/Lidarr/script paths by (library, directory)
// with a sliding debounce window so full season or album imports trigger ONE targeted Plex refresh
// followed by automatic capability analysis of all picked-up items.
func (w *ServerWorker) SubmitArrEvent(ctx context.Context, source, actor, libraryHint string, rawPaths []string, titles []string) (ArrSubmitResult, error) {
	cfg := w.getCfgSnapshot()
	if !cfg.ArrAutoscanEnabled {
		return ArrSubmitResult{Accepted: false}, fmt.Errorf("*arr auto-scan is disabled for this server")
	}
	if strings.TrimSpace(source) == "" {
		source = "Arr"
	}
	if strings.TrimSpace(actor) == "" {
		actor = source
	}

	sections, err := w.adapter.Sections(ctx)
	if err != nil {
		return ArrSubmitResult{}, fmt.Errorf("fetch media server libraries: %w", err)
	}

	// Group paths by (sectionKey, targetDir)
	type groupKey struct {
		sec media.Section
		dir string
	}
	grouped := map[string]*struct {
		key   groupKey
		files []string
	}{}

	if len(rawPaths) == 0 && strings.TrimSpace(libraryHint) != "" {
		if sec, ok := matchSectionForPath(sections, "", libraryHint); ok {
			id := sec.Key + "|"
			grouped[id] = &struct {
				key   groupKey
				files []string
			}{key: groupKey{sec: sec, dir: ""}}
		}
	}

	for _, rp := range rawPaths {
		mapped := normalizeArrPath(rp, cfg.ArrPathMappings)
		if mapped == "" {
			continue
		}
		dir := dirForArrPath(mapped)
		sec, ok := matchSectionForPath(sections, dir, libraryHint)
		if !ok {
			continue
		}
		id := sec.Key + "|" + dir
		g, exists := grouped[id]
		if !exists {
			g = &struct {
				key   groupKey
				files []string
			}{key: groupKey{sec: sec, dir: dir}}
			grouped[id] = g
		}
		g.files = append(g.files, mapped)
	}

	if len(grouped) == 0 {
		return ArrSubmitResult{Accepted: false}, nil
	}

	now := time.Now().Unix()
	debounce := int64(cfg.WebhookDebounceSeconds)
	maxWait := int64(cfg.WebhookMaxWaitSeconds)
	if maxWait < debounce {
		maxWait = debounce
	}

	anyCoalesced := false
	out := make([]PendingArrTrigger, 0, len(grouped))

	w.arrMu.Lock()
	if w.arrTriggers == nil {
		w.arrTriggers = map[string]*PendingArrTrigger{}
	}
	for id, g := range grouped {
		existing, ok := w.arrTriggers[id]
		if ok && existing.State == "debouncing" {
			anyCoalesced = true
			for _, f := range g.files {
				existing.files[f] = struct{}{}
			}
			existing.FileCount = len(existing.files)
			if existing.FileCount == 0 {
				existing.FileCount = 1
			}
			for _, tt := range titles {
				tt = strings.TrimSpace(tt)
				if tt != "" && !containsString(existing.Titles, tt) && len(existing.Titles) < 10 {
					existing.Titles = append(existing.Titles, tt)
				}
			}
			nextFire := now + debounce
			capFire := existing.FirstSeenAt + maxWait
			if nextFire > capFire {
				nextFire = capFire
			}
			existing.FiresAt = nextFire
			out = append(out, *existing)
		} else {
			fileMap := map[string]struct{}{}
			for _, f := range g.files {
				fileMap[f] = struct{}{}
			}
			fc := len(fileMap)
			if fc == 0 {
				fc = 1
			}
			cleanTitles := make([]string, 0, len(titles))
			for _, tt := range titles {
				tt = strings.TrimSpace(tt)
				if tt != "" && !containsString(cleanTitles, tt) && len(cleanTitles) < 10 {
					cleanTitles = append(cleanTitles, tt)
				}
			}
			tr := &PendingArrTrigger{
				ID:          id,
				Source:      source,
				Actor:       actor,
				Library:     g.key.sec.Title,
				SectionKey:  g.key.sec.Key,
				Dir:         g.key.dir,
				FileCount:   fc,
				Titles:      cleanTitles,
				State:       "debouncing",
				FirstSeenAt: now,
				FiresAt:     now + debounce,
				files:       fileMap,
			}
			w.arrTriggers[id] = tr
			out = append(out, *tr)
			w.db.AddEventUser(w.rec.ID, "info", "webhook", actor, fmt.Sprintf("%s webhook: coalescing %s in %s (%d file(s), refreshing Plex in %ds)", source, g.key.dir, g.key.sec.Title, fc, debounce))
		}
	}
	w.arrMu.Unlock()

	w.bumpVersion(false)
	w.wakeArr()

	return ArrSubmitResult{
		Accepted:  true,
		Coalesced: anyCoalesced,
		Triggers:  out,
	}, nil
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}

// ListPendingArrTriggers returns a sorted snapshot of active/debouncing *arr triggers.
func (w *ServerWorker) ListPendingArrTriggers() []PendingArrTrigger {
	w.arrMu.Lock()
	defer w.arrMu.Unlock()
	if len(w.arrTriggers) == 0 {
		return []PendingArrTrigger{}
	}
	out := make([]PendingArrTrigger, 0, len(w.arrTriggers))
	for _, tr := range w.arrTriggers {
		cp := *tr
		cp.files = nil
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FiresAt != out[j].FiresAt {
			return out[i].FiresAt < out[j].FiresAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// FlushArrTriggers immediately fires all pending debounced *arr triggers without waiting for countdown.
func (w *ServerWorker) FlushArrTriggers() int {
	now := time.Now().Unix()
	flushed := 0
	w.arrMu.Lock()
	for _, tr := range w.arrTriggers {
		if tr.State == "debouncing" {
			tr.FiresAt = now
			flushed++
		}
	}
	w.arrMu.Unlock()
	if flushed > 0 {
		w.bumpVersion(false)
		w.wakeArr()
	}
	return flushed
}

func (w *ServerWorker) wakeArr() {
	select {
	case w.arrWakeCh <- struct{}{}:
	default:
	}
}

func (w *ServerWorker) arrLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-w.arrWakeCh:
			w.processReadyArrTriggers()
		case <-ticker.C:
			w.processReadyArrTriggers()
		}
	}
}

func (w *ServerWorker) processReadyArrTriggers() {
	now := time.Now().Unix()
	var ready []*PendingArrTrigger
	w.arrMu.Lock()
	for _, tr := range w.arrTriggers {
		if tr.State == "debouncing" && now >= tr.FiresAt {
			tr.State = "refreshing_plex"
			ready = append(ready, tr)
		}
	}
	w.arrMu.Unlock()

	for _, tr := range ready {
		w.bumpVersion(false)
		go w.executeArrTrigger(tr)
	}
}

func (w *ServerWorker) executeArrTrigger(tr *PendingArrTrigger) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := w.getCfgSnapshot()
	fileWord := "files"
	if tr.FileCount == 1 {
		fileWord = "file"
	}

	w.db.AddEventUser(w.rec.ID, "info", "webhook", tr.Actor, fmt.Sprintf("%s auto-scan: triggering targeted Plex refresh for %s (%s, %d %s)", tr.Source, tr.Library, tr.Dir, tr.FileCount, fileWord))
	if err := w.adapter.RefreshSectionPath(ctx, tr.SectionKey, tr.Dir); err != nil {
		w.db.AddEventUser(w.rec.ID, "error", "webhook", tr.Actor, fmt.Sprintf("%s auto-scan: Plex refresh failed for %s (%s): %v", tr.Source, tr.Library, tr.Dir, err))
		w.removeArrTrigger(tr.ID)
		return
	}

	w.arrMu.Lock()
	if cur, ok := w.arrTriggers[tr.ID]; ok {
		cur.State = "syncing"
	}
	w.arrMu.Unlock()
	w.bumpVersion(false)

	// Wait for Plex to pick up the newly imported files (minimum pickup wait, plus brief settle if Plex is actively scanning)
	pickupWait := time.Duration(cfg.WebhookPickupWaitSeconds) * time.Second
	if pickupWait < 2*time.Second {
		pickupWait = 2 * time.Second
	}
	select {
	case <-w.stopCh:
		w.removeArrTrigger(tr.ID)
		return
	case <-time.After(pickupWait):
	}

	// Targeted sync of this single library section so newly picked up items appear in Sweeparr immediately
	sections, err := w.adapter.Sections(ctx)
	if err != nil {
		w.db.AddEventUser(w.rec.ID, "warning", "webhook", tr.Actor, fmt.Sprintf("%s auto-scan: failed to list libraries after refresh: %v", tr.Source, err))
		w.removeArrTrigger(tr.ID)
		return
	}

	var targetSec *media.Section
	for i := range sections {
		if sections[i].Key == tr.SectionKey || strings.EqualFold(sections[i].Title, tr.Library) {
			targetSec = &sections[i]
			break
		}
	}
	if targetSec == nil {
		w.removeArrTrigger(tr.ID)
		return
	}

	disabledMap := map[string]bool{}
	if cfg.RespectPlexLibrarySettings {
		if dis, err := w.adapter.LibraryDisabledCaps(ctx, targetSec.Key); err == nil {
			for _, c := range dis {
				disabledMap[c] = true
			}
		}
	}

	pageSize := cfg.BulkPageSize
	if pageSize <= 0 {
		pageSize = 200
	}
	start := 0
	var syncedItems []db.MediaItem
	for {
		batch, total, err := w.adapter.ListSectionItems(ctx, *targetSec, start, pageSize)
		if err != nil || len(batch) == 0 {
			break
		}
		for i := range batch {
			batch[i].Missing = filterAllowedMissing(batch[i].Kind, batch[i].Missing, disabledMap, cfg.LibraryCapabilities[targetSec.Title])
			if len(batch[i].Missing) == 0 {
				batch[i].Status = "complete"
			}
		}
		_ = w.db.UpsertItems(w.rec.ID, batch)
		syncedItems = append(syncedItems, batch...)
		start += len(batch)
		if total > 0 && start >= total {
			break
		}
	}

	// Match incomplete items that belong to this import batch (by title, folder name, or recently added timestamp)
	folderBase := strings.ToLower(path.Base(strings.TrimRight(tr.Dir, "/")))
	parentBase := strings.ToLower(path.Base(path.Dir(strings.TrimRight(tr.Dir, "/"))))
	cutoffTs := tr.FirstSeenAt - 180

	keysToQueue := make([]string, 0)
	for _, it := range syncedItems {
		if it.Status == "complete" || it.Skipped || len(it.Missing) == 0 {
			continue
		}
		matched := it.AddedAt >= cutoffTs
		if !matched {
			hay := strings.ToLower(it.GrandparentTitle + " " + it.ParentTitle + " " + it.Title)
			for _, tt := range tr.Titles {
				if tt != "" && strings.Contains(hay, strings.ToLower(tt)) {
					matched = true
					break
				}
			}
		}
		if !matched && folderBase != "" && folderBase != "." && folderBase != "/" {
			hay := strings.ToLower(it.GrandparentTitle + " " + it.ParentTitle + " " + it.Title)
			if strings.Contains(hay, folderBase) || (parentBase != "" && parentBase != "." && parentBase != "/" && strings.Contains(hay, parentBase)) {
				matched = true
			}
		}
		if matched {
			keysToQueue = append(keysToQueue, it.Key)
		}
	}

	queued := 0
	if len(keysToQueue) > 0 {
		reason := fmt.Sprintf("auto-scan (%d %s)", tr.FileCount, fileWord)
		queued, _ = w.db.EnqueueKeysBy(w.rec.ID, keysToQueue, cfg.WebhookPrioritizeTop, tr.Source, reason)
		w.Resume()
	}

	w.db.AddEventUser(w.rec.ID, "info", "webhook", tr.Actor, fmt.Sprintf("%s auto-scan complete: %s (%s) — queued %d item(s) for capability analysis", tr.Source, tr.Library, tr.Dir, queued))
	w.removeArrTrigger(tr.ID)
	w.bumpVersion(true)
	w.wake()
}

func (w *ServerWorker) removeArrTrigger(id string) {
	w.arrMu.Lock()
	delete(w.arrTriggers, id)
	w.arrMu.Unlock()
	w.bumpVersion(false)
}

func filterAllowedMissing(kind string, missing []string, disabled map[string]bool, libAllowed []string) []string {
	var allowedSet map[string]bool
	if len(libAllowed) > 0 {
		allowedSet = make(map[string]bool, len(libAllowed))
		for _, c := range libAllowed {
			allowedSet[c] = true
		}
	}
	out := make([]string, 0, len(missing))
	for _, c := range missing {
		if disabled[c] {
			continue
		}
		if allowedSet != nil && !allowedSet[c] {
			continue
		}
		if kind == "track" && c != "loudness" && c != "sonic" {
			continue
		}
		out = append(out, c)
	}
	return out
}
