package engine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
)

type mockMediaAdapter struct {
	mu               sync.Mutex
	sections         []media.Section
	items            []db.MediaItem
	activities       []media.ActivityInfo
	refreshedPaths   []string
	cancelledUUIDs   []string
	analyzeCalls     int
	inspectOverrides map[string]*db.MediaItem
}

func (m *mockMediaAdapter) Kind() string { return "plex" }
func (m *mockMediaAdapter) Identity(context.Context) (media.ServerIdentity, error) {
	return media.ServerIdentity{FriendlyName: "MockPlex", Version: "1.43.5"}, nil
}
func (m *mockMediaAdapter) Sections(context.Context) ([]media.Section, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]media.Section(nil), m.sections...), nil
}
func (m *mockMediaAdapter) LibraryDisabledCaps(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *mockMediaAdapter) LibraryCapModes(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (m *mockMediaAdapter) ListSectionItems(_ context.Context, sec media.Section, start, size int) ([]db.MediaItem, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []db.MediaItem
	for _, it := range m.items {
		if it.Library == sec.Title {
			out = append(out, it)
		}
	}
	if start >= len(out) {
		return nil, len(out), nil
	}
	end := start + size
	if end > len(out) {
		end = len(out)
	}
	return out[start:end], len(out), nil
}
func (m *mockMediaAdapter) InspectItem(_ context.Context, rk, _ string) (*db.MediaItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inspectOverrides != nil {
		if it, ok := m.inspectOverrides[rk]; ok {
			cp := *it
			return &cp, nil
		}
	}
	for _, it := range m.items {
		if it.RatingKey == rk {
			cp := it
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *mockMediaAdapter) TriggerAnalyze(context.Context, string, []string) (int, error) {
	m.mu.Lock()
	m.analyzeCalls++
	m.mu.Unlock()
	return 200, nil
}
func (m *mockMediaAdapter) RefreshSectionPath(_ context.Context, secKey, dirPath string) error {
	m.mu.Lock()
	m.refreshedPaths = append(m.refreshedPaths, secKey+":"+dirPath)
	m.mu.Unlock()
	return nil
}
func (m *mockMediaAdapter) Sessions(context.Context) ([]media.SessionInfo, error) { return nil, nil }
func (m *mockMediaAdapter) Activities(context.Context) ([]media.ActivityInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]media.ActivityInfo(nil), m.activities...), nil
}
func (m *mockMediaAdapter) CancelActivity(_ context.Context, uuid string) error {
	m.mu.Lock()
	m.cancelledUUIDs = append(m.cancelledUUIDs, uuid)
	m.mu.Unlock()
	return nil
}
func (m *mockMediaAdapter) CheckServerSettings(context.Context) ([]media.SettingsCheckRow, error) {
	return nil, nil
}
func (m *mockMediaAdapter) FetchThumb(context.Context, string) ([]byte, string, error) {
	return nil, "", nil
}

func newTestWorker(t *testing.T, adapter *mockMediaAdapter, cfg config.ScannerConfig) (*ServerWorker, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "engine.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	rec := db.ServerRecord{
		ID:      "srv1",
		Name:    "MockPlex",
		Kind:    "plex",
		URL:     "https://plex.local:32400",
		Enabled: true,
		Config:  cfg,
	}
	if err := database.UpsertServer(rec); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	wk := newServerWorker(database, rec, adapter, false)
	return wk, database
}

func TestInterruptDoesNotRecordFailureAndPauses(t *testing.T) {
	adapter := &mockMediaAdapter{
		activities: []media.ActivityInfo{
			{UUID: "u-cancel", Type: "library.refresh.items", Title: "Scanning", Cancellable: true, Progress: 50},
			{UUID: "u-nocancel", Type: "media.generate.loudness", Title: "Loudness", Subtitle: "Inception", Cancellable: false, Progress: -1},
		},
	}
	cfg := config.DefaultScannerConfig()
	wk, database := newTestWorker(t, adapter, cfg)

	item := db.MediaItem{
		Key: "Movies:1", RatingKey: "1", Library: "Movies", Kind: "movie",
		Title: "Inception", Status: "pending", Missing: []string{"loudness"}, Bytes: 1024,
	}
	_ = database.UpsertItems("srv1", []db.MediaItem{item})

	done := make(chan struct{})
	go func() {
		wk.processItem(&item, cfg)
		close(done)
	}()

	// Wait until processItem marks currentItem active
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		wk.mu.RLock()
		cur := wk.currentItem
		wk.mu.RUnlock()
		if cur != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancelled, nonCancellable, interrupted := wk.CancelAllActivities(context.Background())
	if cancelled != 1 || nonCancellable != 1 || !interrupted {
		t.Fatalf("expected cancelled=1 nonCancellable=1 interrupted=true, got %d/%d/%v", cancelled, nonCancellable, interrupted)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("processItem did not abort promptly after CancelAllActivities")
	}

	// Verify item restored to pending with Attempts == 0 and ZERO failure history rows!
	after, _ := database.GetItem("srv1", "Movies:1")
	if after == nil || after.Status != "pending" || after.Attempts != 0 {
		t.Fatalf("expected item restored to pending with 0 attempts, got %+v", after)
	}
	hist, _ := database.QueryScanHistory("srv1", 10)
	if len(hist) != 0 {
		t.Fatalf("expected 0 failure history rows after interrupt, got %+v", hist)
	}
	if !wk.getCfgSnapshot().Paused {
		t.Fatalf("expected scanner to be paused after Interrupt")
	}
}

func TestBlocklistCurrentPreservesSkippedState(t *testing.T) {
	adapter := &mockMediaAdapter{
		activities: []media.ActivityInfo{
			{UUID: "u1", Type: "media.generate.loudness", Subtitle: "Inception", Cancellable: false, Progress: -1},
		},
	}
	cfg := config.DefaultScannerConfig()
	wk, database := newTestWorker(t, adapter, cfg)

	item := db.MediaItem{
		Key: "Movies:2", RatingKey: "2", Library: "Movies", Kind: "movie",
		Title: "Bad File", Status: "pending", Missing: []string{"loudness"}, Bytes: 1024,
	}
	_ = database.UpsertItems("srv1", []db.MediaItem{item})

	done := make(chan struct{})
	go func() {
		wk.processItem(&item, cfg)
		close(done)
	}()

	for i := 0; i < 100; i++ {
		wk.mu.RLock()
		cur := wk.currentItem
		wk.mu.RUnlock()
		if cur != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if n := wk.IgnoreCurrent(); n != 1 {
		t.Fatalf("expected IgnoreCurrent to skip 1 item, got %d", n)
	}
	<-done

	after, _ := database.GetItem("srv1", "Movies:2")
	if after == nil || !after.Skipped {
		t.Fatalf("expected blocklisted item to remain skipped=true after abort, got %+v", after)
	}
}

func TestExternalActiveScanPickup(t *testing.T) {
	adapter := &mockMediaAdapter{
		activities: []media.ActivityInfo{
			{UUID: "u-ext", Type: "media.generate.loudness", Title: "Generating loudness data", Subtitle: "2 Broke Girls S01 E01", Cancellable: false, Progress: 42},
		},
	}
	cfg := config.DefaultScannerConfig()
	wk, database := newTestWorker(t, adapter, cfg)
	_ = database.UpsertItems("srv1", []db.MediaItem{{
		Key: "Series:10", RatingKey: "10", Library: "Series", Kind: "episode",
		Title: "Pilot", GrandparentTitle: "2 Broke Girls", ParentIndex: 1, ItemIndex: 1,
		Status: "pending", Missing: []string{"loudness"},
	}})

	wk.pollTelemetry()

	if !wk.hasActiveAnalysis() {
		t.Fatalf("expected hasActiveAnalysis=true when Plex has media.generate.loudness")
	}
	st := wk.GetStatus()
	daemon := st["daemon"].(map[string]any)
	cur, ok := daemon["current_item"].(*db.MediaItem)
	if !ok || cur == nil || cur.Key != "Series:10" {
		t.Fatalf("expected externalActiveItem Series:10 picked up as current_item, got %+v", daemon["current_item"])
	}
	if daemon["external_active"] != true {
		t.Fatalf("expected external_active=true, got %v", daemon["external_active"])
	}
}

func TestArrAutoscanSeasonCoalescingAndPickup(t *testing.T) {
	now := time.Now().Unix()
	adapter := &mockMediaAdapter{
		sections: []media.Section{
			{Key: "2", Title: "Series", Type: "show", Locations: []string{"/0/series"}},
		},
		items: []db.MediaItem{
			{Key: "Series:1", RatingKey: "1", Library: "Series", Kind: "episode", Title: "Ep 1", GrandparentTitle: "Andor", ParentIndex: 1, ItemIndex: 1, Status: "pending", Missing: []string{"intro", "loudness"}, AddedAt: now},
			{Key: "Series:2", RatingKey: "2", Library: "Series", Kind: "episode", Title: "Ep 2", GrandparentTitle: "Andor", ParentIndex: 1, ItemIndex: 2, Status: "pending", Missing: []string{"intro", "loudness"}, AddedAt: now},
		},
	}
	cfg := config.DefaultScannerConfig()
	cfg.WebhookDebounceSeconds = 30
	cfg.WebhookPickupWaitSeconds = 2
	cfg.ArrPathMappings = []config.PathMapping{{Remote: "/data/tv", Local: "/0/series"}}
	wk, database := newTestWorker(t, adapter, cfg)
	go wk.arrLoop()
	t.Cleanup(func() { close(wk.stopCh) })

	// Two episodes arrive from Sonarr using /data/tv -> rewritten to /0/series -> coalesced into 1 trigger!
	res1, err := wk.SubmitArrEvent(context.Background(), "Sonarr", "sonarr", "", []string{"/data/tv/Andor/Season 01/S01E01.mkv"}, []string{"Andor"})
	if err != nil || !res1.Accepted || res1.Coalesced || len(res1.Triggers) != 1 {
		t.Fatalf("unexpected res1: %+v err=%v", res1, err)
	}
	res2, err := wk.SubmitArrEvent(context.Background(), "Sonarr", "sonarr", "", []string{"/data/tv/Andor/Season 01/S01E02.mkv"}, []string{"Andor"})
	if err != nil || !res2.Accepted || !res2.Coalesced || len(res2.Triggers) != 1 || res2.Triggers[0].FileCount != 2 || res2.Triggers[0].Dir != "/0/series/Andor/Season 01" {
		t.Fatalf("expected coalesced season trigger with 2 files, got %+v err=%v", res2, err)
	}

	// Flush immediately and verify Plex targeted refresh + automatic queue pickup!
	if flushed := wk.FlushArrTriggers(); flushed != 1 {
		t.Fatalf("expected 1 flushed trigger, got %d", flushed)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(wk.ListPendingArrTriggers()) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	adapter.mu.Lock()
	refreshed := append([]string(nil), adapter.refreshedPaths...)
	adapter.mu.Unlock()
	if len(refreshed) != 1 || refreshed[0] != "2:/0/series/Andor/Season 01" {
		t.Fatalf("expected targeted Plex refresh for 2:/0/series/Andor/Season 01, got %v", refreshed)
	}

	prio, _, _, _ := database.GetQueuePreview("srv1", 10)
	if len(prio) != 2 || prio[0].QueuedBy != "Sonarr" {
		t.Fatalf("expected 2 Andor episodes queued by Sonarr, got %+v", prio)
	}
}

func TestTriggersForItemTargetedCapabilities(t *testing.T) {
	cfg := config.DefaultScannerConfig()
	cfg.LibraryCapabilities = map[string][]string{
		"Movies": {"loudness", "voice", "chapters", "previews", "credits", "ads", "deep_analysis"},
		"Series": {"loudness", "voice", "chapters", "intro", "credits", "ads", "deep_analysis"},
	}

	ep := db.MediaItem{
		Library: "Series",
		Kind:    "episode",
		Missing: []string{"loudness"},
	}
	got := triggersForItem(&ep, cfg)
	if len(got) != 1 || got[0] != "loudness" {
		t.Fatalf("expected only [loudness] for episode missing loudness, got %v", got)
	}

	movie := db.MediaItem{
		Library: "Movies",
		Kind:    "movie",
		IsDVR:   false,
		Missing: []string{"intro", "ads", "voice"},
	}
	got = triggersForItem(&movie, cfg)
	if len(got) != 1 || got[0] != "voice" {
		t.Fatalf("expected only [voice] for non-DVR movie, got %v", got)
	}

	dvrEp := db.MediaItem{
		Library: "Series",
		Kind:    "episode",
		IsDVR:   true,
		Missing: []string{"ads", "intro"},
	}
	got = triggersForItem(&dvrEp, cfg)
	if len(got) != 2 {
		t.Fatalf("expected [ads intro] for DVR episode, got %v", got)
	}
}
