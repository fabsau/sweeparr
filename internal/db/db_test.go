package db

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open test DB: %v", err)
	}
	if err := d.UpsertServer(ServerRecord{ID: "srv1", Name: "TestPlex", Kind: "plex", URL: "https://plex.local:32400", Enabled: true}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestActivitySubtitleMatching(t *testing.T) {
	d := openTestDB(t)
	err := d.UpsertItems("srv1", []MediaItem{
		{
			Key: "Series:101", RatingKey: "101", Library: "Series", Kind: "episode",
			Title: "Pilot", GrandparentTitle: "2 Broke Girls", ParentTitle: "Season 1",
			ParentIndex: 1, ItemIndex: 1, Status: "pending", Missing: []string{"loudness"},
		},
		{
			Key: "Series:102", RatingKey: "102", Library: "Series", Kind: "episode",
			Title: "And the Break-up Scene", GrandparentTitle: "2 Broke Girls", ParentTitle: "Season 1",
			ParentIndex: 1, ItemIndex: 2, Status: "pending", Missing: []string{"intro"},
		},
		{
			Key: "Movies:201", RatingKey: "201", Library: "Movies", Kind: "movie",
			Title: "3 Idiots", Status: "pending", Missing: []string{"voice"},
		},
		{
			Key: "Music:301", RatingKey: "301", Library: "Music", Kind: "track",
			Title: "Happiness in Liquid Form", GrandparentTitle: "Alfie Templeman",
			Status: "pending", Missing: []string{"sonic"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	// Episode subtitle format from Plex 1.43.5: "<Show> S01 E01"
	got := d.FindItemByActivitySubtitle("srv1", "2 Broke Girls S01 E01")
	if got == nil || got.Key != "Series:101" {
		t.Fatalf("expected Series:101 for episode subtitle, got %+v", got)
	}

	// Season-level intro subtitle format: "<Show> S01"
	gotSeason := d.FindItemByActivitySubtitle("srv1", "2 Broke Girls S01")
	if gotSeason == nil || gotSeason.Key != "Series:101" {
		t.Fatalf("expected Series:101 for season subtitle, got %+v", gotSeason)
	}

	// Movie exact title
	gotMovie := d.FindItemByActivitySubtitle("srv1", "3 Idiots")
	if gotMovie == nil || gotMovie.Key != "Movies:201" {
		t.Fatalf("expected Movies:201 for movie subtitle, got %+v", gotMovie)
	}

	// Track "<Artist> - <Title>"
	gotTrack := d.FindItemByActivitySubtitle("srv1", "Alfie Templeman - Happiness in Liquid Form")
	if gotTrack == nil || gotTrack.Key != "Music:301" {
		t.Fatalf("expected Music:301 for track subtitle, got %+v", gotTrack)
	}

	// SyncExternalActiveTitles marks matched item active and clears previous active items
	if !d.SyncExternalActiveTitles("srv1", []string{"2 Broke Girls S01 E02"}) {
		t.Fatalf("expected SyncExternalActiveTitles to report change")
	}
	it, _ := d.GetItem("srv1", "Series:102")
	if it == nil || it.Status != "active" {
		t.Fatalf("expected Series:102 status=active, got %+v", it)
	}
	if !d.SyncExternalActiveTitles("srv1", nil) {
		t.Fatalf("expected clearing active items to report change")
	}
	it2, _ := d.GetItem("srv1", "Series:102")
	if it2 == nil || it2.Status != "pending" {
		t.Fatalf("expected Series:102 restored to pending, got %+v", it2)
	}
}

func TestUserDeletionCascadesAPIKeysAndSessions(t *testing.T) {
	d := openTestDB(t)
	u := User{ID: "u1", Username: "alice", Role: "operator", PasswordHash: "hash", CreatedAt: time.Now().Unix()}
	if err := d.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := d.CreateSession(Session{ID: "s1", UserID: "u1", TokenHash: "th1", IPAddress: "10.0.0.1", ExpiresAt: time.Now().Add(time.Hour).Unix(), CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := d.CreateAPIKey(APIKey{ID: "k1", UserID: "u1", Name: "alice-key", Prefix: "swp_abc", KeyHash: "kh1", Scopes: []string{"read"}, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if err := d.DeleteUser("u1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if s, _ := d.GetSessionByTokenHash("th1"); s != nil {
		t.Fatalf("expected session to be cascade-deleted, still found %+v", s)
	}
	if k, _ := d.GetAPIKeyByHash("kh1"); k != nil {
		t.Fatalf("expected API key to be cascade-deleted, still found %+v", k)
	}
}

func TestHistoryAddedMissingCapsAndSummary(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().Unix()
	d.AddScanHistory(HistoryRow{
		ServerID: "srv1", ItemKey: "Movies:1", Title: "Dune", Library: "Movies", Kind: "movie",
		Status: "complete", Detail: "added:loudness,voice", AddedCaps: []string{"loudness", "voice"}, MissingCaps: []string{},
		Bytes: 4000, QueuedBy: "Radarr", QueuedReason: "auto-scan (1 file)", DurationMs: 12000, FinishedAt: now - 60,
	})
	d.AddScanHistory(HistoryRow{
		ServerID: "srv1", ItemKey: "Movies:2", Title: "Tenet", Library: "Movies", Kind: "movie",
		Status: "retry", Detail: "added:loudness|missing:voice", AddedCaps: []string{"loudness"}, MissingCaps: []string{"voice"},
		Bytes: 8000, QueuedBy: "admin", QueuedReason: "manual", DurationMs: 25000, FinishedAt: now - 10,
	})
	d.AddScanHistory(HistoryRow{
		ServerID: "srv1", ItemKey: "Movies:3", Title: "Corrupt", Library: "Movies", Kind: "movie",
		Status: "exhausted", Detail: "error:timeout", MissingCaps: []string{"loudness"},
		Bytes: 2000, QueuedBy: "system", QueuedReason: "auto", DurationMs: 5000, FinishedAt: now - 7200,
	})

	sum := d.GetScanSummary("srv1")
	if sum.Completed != 1 || sum.Retries != 1 || sum.Failed != 1 {
		t.Fatalf("unexpected scan summary: %+v", sum)
	}

	// Filter by since = last 1 hour -> should exclude Corrupt (2h ago)
	hp, err := d.QueryScanHistoryPage("srv1", "", "", "", "", now-3600, "duration_desc", 1, 50)
	if err != nil || hp.Total != 2 || len(hp.Items) != 2 {
		t.Fatalf("expected 2 recent history rows, got total=%d len=%d err=%v", hp.Total, len(hp.Items), err)
	}
	if hp.Items[0].Title != "Tenet" || len(hp.Items[0].AddedCaps) != 1 || hp.Items[0].AddedCaps[0] != "loudness" || len(hp.Items[0].MissingCaps) != 1 || hp.Items[0].MissingCaps[0] != "voice" {
		t.Fatalf("unexpected first history row: %+v", hp.Items[0])
	}
	if hp.Items[1].QueuedBy != "Radarr" || hp.Items[1].Bytes != 4000 {
		t.Fatalf("unexpected second history row attribution/bytes: %+v", hp.Items[1])
	}
}

func TestBlocklistAttributionAndRetention(t *testing.T) {
	d := openTestDB(t)
	_ = d.UpsertItems("srv1", []MediaItem{{
		Key: "Movies:9", RatingKey: "9", Library: "Movies", Kind: "movie", Title: "Blocked Film", Status: "pending", Missing: []string{"loudness"},
	}})
	n, err := d.SetSkipBy("srv1", []string{"Movies:9"}, true, "admin")
	if err != nil || n != 1 {
		t.Fatalf("SetSkipBy failed: n=%d err=%v", n, err)
	}
	bp, err := d.QueryBlocklistPage("srv1", "", "", 0, "blocklisted_desc", 1, 50)
	if err != nil || bp.Total != 1 || len(bp.Items) != 1 || bp.Items[0].BlocklistedBy != "admin" || bp.Items[0].BlocklistedAt == 0 {
		t.Fatalf("unexpected blocklist row: %+v err=%v", bp, err)
	}
	cleared, err := d.ClearBlocklist("srv1")
	if err != nil || cleared != 1 {
		t.Fatalf("ClearBlocklist failed: cleared=%d err=%v", cleared, err)
	}

	// Retention pruning
	for i := 0; i < 10; i++ {
		d.AddEventUser("srv1", "info", "scan", "admin", "event")
	}
	evDel, _ := d.PruneRetention("srv1", 14, 30, 5)
	if evDel != 5 {
		t.Fatalf("expected 5 excess events pruned by max_log_rows=5, got %d", evDel)
	}
}
