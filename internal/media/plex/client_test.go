package plex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fabsau/sweeparr/internal/media"
	"github.com/fabsau/sweeparr/internal/media/plex"
)

func TestPlexClientNumericAndStringJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "tok123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"friendlyName":"SaunaPlex","version":"1.43.6"}}`))
	})
	mux.HandleFunc("/library/sections/1/all", func(w http.ResponseWriter, r *http.Request) {
		// Simulate real Plex bulk listing where Marker, Chapter, and Part.Stream are omitted
		_, _ = w.Write([]byte(`{
			"MediaContainer": {
				"totalSize": 1,
				"Metadata": [{
					"ratingKey": 101,
					"type": "episode",
					"title": "Pilot",
					"grandparentTitle": "Severance",
					"parentTitle": "Season 1",
					"chapterSource": "media",
					"Media": [{
						"hasVoiceActivity": true,
						"Part": [{
							"id": 55,
							"key": "/library/parts/55/file.mkv",
							"size": 1073741824,
							"indexes": "sd"
						}]
					}]
				}]
			}
		}`))
	})
	mux.HandleFunc("/library/metadata/101", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"MediaContainer": {
				"Metadata": [{
					"ratingKey": 101,
					"type": "episode",
					"title": "Pilot",
					"grandparentTitle": "Severance",
					"parentTitle": "Season 1",
					"chapterSource": "media",
					"loudnessAnalysisVersion": "0.02",
					"Media": [{
						"hasVoiceActivity": true,
						"Part": [{
							"id": 55,
							"key": "/library/parts/55/file.mkv",
							"size": 1073741824,
							"indexes": "sd",
							"deepAnalysisVersion": "6",
							"Stream": [
								{"streamType": 1, "requiredBandwidths": "15000"},
								{"streamType": 2, "loudness": -18.4}
							]
						}]
					}],
					"Marker": [{"type": "intro"}, {"type": "credits"}, {"type": "commercial"}],
					"Chapter": [{"id": 1, "filter": "thumb=1"}]
				}]
			}
		}`))
	})
	mux.HandleFunc("/library/sections/1/prefs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"MediaContainer": {
				"Setting": [
					{"id": "enableIntroMarkerGeneration", "value": "true"},
					{"id": "enableAdMarkerGeneration", "value": "0"},
					{"id": "musicAnalysis", "value": "false"}
				]
			}
		}`))
	})
	mux.HandleFunc("/:/prefs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"MediaContainer": {
				"Setting": [
					{"id": "GenerateChapterThumbBehavior", "value": "never"},
					{"id": "ButlerTaskDeepMediaAnalysis", "value": true}
				]
			}
		}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := plex.NewClient(ts.URL, "tok123", false)
	id, err := c.Identity(context.Background())
	if err != nil || id.FriendlyName != "SaunaPlex" {
		t.Fatalf("identity failed: %v %+v", err, id)
	}
	items, tot, err := c.ListSectionItems(context.Background(), media.Section{Key: "1", Title: "TV Shows", Type: "show"}, 0, 50)
	if err != nil || tot != 1 || len(items) != 1 {
		t.Fatalf("list section items failed: err=%v tot=%d len=%d", err, tot, len(items))
	}
	if items[0].Status != "complete" || len(items[0].Missing) != 0 {
		t.Fatalf("expected hydrated item with Media.hasVoiceActivity=true and batch markers/streams to be complete, got status=%s missing=%v", items[0].Status, items[0].Missing)
	}
	dis, err := c.LibraryDisabledCaps(context.Background(), "1")
	if err != nil || len(dis) != 3 {
		t.Fatalf("expected 3 disabled caps from section + server prefs (ads, sonic, chapters), got err=%v dis=%v", err, dis)
	}

	// Redirects must never be followed (prevents X-Plex-Token leak to external redirect target)
	leakedToken := false
	evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "" {
			leakedToken = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer evilSrv.Close()

	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evilSrv.URL, http.StatusFound)
	}))
	defer redirSrv.Close()

	rc := plex.NewClient(redirSrv.URL, "super-secret-plex-tok", false)
	if _, err := rc.Identity(context.Background()); err == nil {
		t.Fatalf("expected redirect response to be rejected")
	}
	if leakedToken {
		t.Fatalf("X-Plex-Token was leaked across HTTP redirect!")
	}
}
