package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/fabsau/sweeparr/internal/api"
	"github.com/fabsau/sweeparr/internal/auth"
	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/engine"
	"github.com/fabsau/sweeparr/internal/media"
)

type fakeAdapter struct{}

func (f fakeAdapter) Kind() string { return "plex" }
func (f fakeAdapter) Identity(context.Context) (media.ServerIdentity, error) {
	return media.ServerIdentity{FriendlyName: "TestPlex", Version: "1.43.0"}, nil
}
func (f fakeAdapter) Sections(context.Context) ([]media.Section, error) {
	return []media.Section{
		{Key: "1", Title: "Movies", Type: "movie", Locations: []string{"/media/movies"}},
		{Key: "2", Title: "TV Shows", Type: "show", Locations: []string{"/media/tv"}},
		{Key: "3", Title: "Music", Type: "artist", Locations: []string{"/media/music"}},
	}, nil
}
func (f fakeAdapter) LibraryDisabledCaps(context.Context, string) ([]string, error) { return nil, nil }
func (f fakeAdapter) LibraryCapModes(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (f fakeAdapter) ListSectionItems(context.Context, media.Section, int, int) ([]db.MediaItem, int, error) {
	return []db.MediaItem{{
		Key:       "Movies:1",
		RatingKey: "1",
		Library:   "Movies",
		Kind:      "movie",
		Title:     "Inception",
		Bytes:     1024,
		Missing:   []string{"loudness"},
		Status:    "pending",
	}}, 1, nil
}
func (f fakeAdapter) InspectItem(context.Context, string, string) (*db.MediaItem, error) {
	return nil, nil
}
func (f fakeAdapter) TriggerAnalyze(context.Context, string, []string) (int, error) { return 200, nil }
func (f fakeAdapter) RefreshSectionPath(context.Context, string, string) error      { return nil }
func (f fakeAdapter) Sessions(context.Context) ([]media.SessionInfo, error)         { return nil, nil }
func (f fakeAdapter) Activities(context.Context) ([]media.ActivityInfo, error)      { return nil, nil }
func (f fakeAdapter) CancelActivity(context.Context, string) error                  { return nil }
func (f fakeAdapter) CheckServerSettings(context.Context) ([]media.SettingsCheckRow, error) {
	return []media.SettingsCheckRow{{ID: "ButlerTaskDeepMediaAnalysis", Label: "Deep", Value: "true", Status: "ok"}}, nil
}
func (f fakeAdapter) FetchThumb(context.Context, string) ([]byte, string, error) {
	return []byte("img"), "image/jpeg", nil
}

func newTestServer(t *testing.T) (*httptest.Server, *http.Client, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	env := config.EnvConfig{
		StateDir:      dir,
		Port:          "8080",
		PlexURL:       "https://plex:32400",
		PlexToken:     "secret-plex-token",
		PlexSSLVerify: false,
		RPID:          "localhost",
		RPDisplayName: "Sweeparr",
		RPOrigins:     []string{"http://localhost"},
		SessionTTL:    24 * time.Hour,
	}
	authSvc, err := auth.NewService(database, env)
	if err != nil {
		t.Fatalf("auth svc: %v", err)
	}
	mgr, err := engine.NewManager(database, env, func(_ db.ServerRecord, _ string) media.Adapter {
		return fakeAdapter{}
	})
	if err != nil {
		t.Fatalf("mgr: %v", err)
	}
	t.Cleanup(mgr.StopAll)
	ts := httptest.NewServer(api.NewRouter(database, authSvc, mgr, nil))
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	return ts, &http.Client{Jar: jar}, database
}

func postJSON(t *testing.T, client *http.Client, url string, body any, headers map[string]string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if _, ok := headers["X-Api-Key"]; !ok {
		req.Header.Set("X-Requested-With", "sweeparr")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}

func TestAuthMFAPasskeyRBACAndProtectedAutopulseAPI(t *testing.T) {
	ts, client, database := newTestServer(t)

	// 1. Unauthenticated access returns 401 and includes SOC HTTP security headers
	req1, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/servers", nil)
	req1.Header.Set("X-Forwarded-Proto", "https")
	r1, _ := client.Do(req1)
	if r1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 before login, got %d", r1.StatusCode)
	}
	for _, hdr := range []string{
		"Content-Security-Policy",
		"Permissions-Policy",
		"Cross-Origin-Opener-Policy",
		"Cross-Origin-Embedder-Policy",
		"Cross-Origin-Resource-Policy",
		"X-Permitted-Cross-Domain-Policies",
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
		"Strict-Transport-Security",
	} {
		if r1.Header.Get(hdr) == "" {
			t.Fatalf("missing required SOC security header %s", hdr)
		}
	}
	_ = r1.Body.Close()

	// 2. First-run admin setup
	rSetup := postJSON(t, client, ts.URL+"/api/auth/setup", map[string]any{
		"username":     "fabio",
		"password":     "supersecurepass",
		"display_name": "Fabio",
	}, nil)
	if rSetup.StatusCode != http.StatusOK {
		t.Fatalf("setup failed: %d", rSetup.StatusCode)
	}
	_ = rSetup.Body.Close()

	// 3. Enroll & confirm TOTP MFA
	rEnroll := postJSON(t, client, ts.URL+"/api/auth/mfa/enroll", map[string]any{}, nil)
	var enrollRes struct {
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(rEnroll.Body).Decode(&enrollRes)
	_ = rEnroll.Body.Close()
	code, err := totp.GenerateCode(enrollRes.Secret, time.Now())
	if err != nil {
		t.Fatalf("totp gen: %v", err)
	}
	rConf := postJSON(t, client, ts.URL+"/api/auth/mfa/confirm", map[string]any{"code": code}, nil)
	if rConf.StatusCode != http.StatusOK {
		t.Fatalf("mfa confirm failed: %d", rConf.StatusCode)
	}
	_ = rConf.Body.Close()

	// 4. Logout & verify login requires MFA step
	rOut := postJSON(t, client, ts.URL+"/api/auth/logout", map[string]any{}, nil)
	_ = rOut.Body.Close()

	rLogin := postJSON(t, client, ts.URL+"/api/auth/login", map[string]any{
		"username": "fabio",
		"password": "supersecurepass",
	}, nil)
	var loginRes struct {
		MFAPending bool `json:"mfa_pending"`
	}
	_ = json.NewDecoder(rLogin.Body).Decode(&loginRes)
	_ = rLogin.Body.Close()
	if !loginRes.MFAPending {
		t.Fatalf("expected mfa_pending=true")
	}

	rPend, _ := client.Get(ts.URL + "/api/servers")
	if rPend.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 during mfa_pending, got %d", rPend.StatusCode)
	}
	_ = rPend.Body.Close()

	code2, _ := totp.GenerateCode(enrollRes.Secret, time.Now())
	rVer := postJSON(t, client, ts.URL+"/api/auth/mfa/verify", map[string]any{"code": code2}, nil)
	if rVer.StatusCode != http.StatusOK {
		t.Fatalf("mfa verify failed: %d", rVer.StatusCode)
	}
	_ = rVer.Body.Close()

	// 5. Seed item & verify GetAggregates + /status never deadlock
	_ = database.UpsertItems("default", []db.MediaItem{{
		Key: "Movies:1", RatingKey: "1", Library: "Movies", Kind: "movie", Title: "Inception", Bytes: 1024, Missing: []string{"loudness"}, Status: "pending",
	}})
	for _, ep := range []string{
		"/api/servers/default/status",
		"/api/servers/default/items",
		"/api/servers/default/groups",
		"/api/servers/default/export?format=csv",
		"/api/servers/default/export?format=json",
		"/api/servers/default/queue",
		"/api/servers/default/events",
		"/api/servers/default/plex/settings-check",
		"/api/servers/default/plex/thumb?path=/library/metadata/1/thumb/1",
	} {
		resp, err := client.Get(ts.URL + ep)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s failed: err=%v status=%v", ep, err, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// Control, enqueue, reorder, skip, and config endpoints
	for _, post := range []struct {
		path string
		body map[string]any
	}{
		{"/api/servers/default/items/enqueue", map[string]any{"keys": []string{"Movies:1"}, "front": true}},
		{"/api/servers/default/queue/reorder", map[string]any{"keys": []string{"Movies:1"}}},
		{"/api/servers/default/queue/remove", map[string]any{"key": "Movies:1"}},
		{"/api/servers/default/items/enqueue-group", map[string]any{"library": "Movies", "grandparent_title": ""}},
		{"/api/servers/default/items/skip", map[string]any{"key": "Movies:1", "skip": true}},
		{"/api/servers/default/items/bulk-skip", map[string]any{"keys": []string{"Movies:1"}, "skip": false}},
		{"/api/servers/default/control/pause", map[string]any{"duration_minutes": 15}},
		{"/api/servers/default/control/resume", map[string]any{}},
		{"/api/servers/default/control/step", map[string]any{}},
		{"/api/servers/default/control/refresh", map[string]any{}},
		{"/api/servers/default/control/clear-queue", map[string]any{}},
		{"/api/servers/default/control/reset-retries", map[string]any{}},
		{"/api/servers/default/libraries/scan", map[string]any{"library": "Movies"}},
		{"/api/servers/default/connection/test", map[string]any{}},
	} {
		resp := postJSON(t, client, ts.URL+post.path, post.body, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s failed: %d", post.path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// 6. Passkey registration begin
	rPKBegin := postJSON(t, client, ts.URL+"/api/auth/passkey/register/begin", map[string]any{"label": "YubiKey"}, nil)
	if rPKBegin.StatusCode != http.StatusOK {
		t.Fatalf("passkey register begin failed: %d", rPKBegin.StatusCode)
	}
	var pkBeginRes struct {
		ChallengeID string         `json:"challenge_id"`
		Options     map[string]any `json:"options"`
	}
	_ = json.NewDecoder(rPKBegin.Body).Decode(&pkBeginRes)
	_ = rPKBegin.Body.Close()
	if pkBeginRes.ChallengeID == "" || pkBeginRes.Options["publicKey"] == nil {
		t.Fatalf("invalid passkey register begin payload: %+v", pkBeginRes)
	}

	// 7. CSRF blocks cookie-authenticated POST without X-Requested-With
	reqNoCSRF, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/servers/default/control/pause", bytes.NewReader([]byte("{}")))
	reqNoCSRF.Header.Set("Content-Type", "application/json")
	rNoCSRF, _ := client.Do(reqNoCSRF)
	if rNoCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for missing CSRF header, got %d", rNoCSRF.StatusCode)
	}
	_ = rNoCSRF.Body.Close()

	// 8. Multi-server & Autopulse API key protection
	rAddSrv := postJSON(t, client, ts.URL+"/api/servers", map[string]any{
		"name": "Backup Plex", "kind": "plex", "url": "https://plex2:32400", "token": "tok2", "ssl_verify": false,
	}, nil)
	if rAddSrv.StatusCode != http.StatusCreated {
		t.Fatalf("add server failed: %d", rAddSrv.StatusCode)
	}
	_ = rAddSrv.Body.Close()

	rKeyRead := postJSON(t, client, ts.URL+"/api/api-keys", map[string]any{"name": "readonly", "scopes": []string{"read"}}, nil)
	var readKeyRes struct {
		APIKey string `json:"api_key"`
	}
	_ = json.NewDecoder(rKeyRead.Body).Decode(&readKeyRes)
	_ = rKeyRead.Body.Close()

	rKeyHook := postJSON(t, client, ts.URL+"/api/api-keys", map[string]any{"name": "plex-hook", "scopes": []string{"webhook"}}, nil)
	var hookKeyRes struct {
		APIKey string `json:"api_key"`
	}
	_ = json.NewDecoder(rKeyHook.Body).Decode(&hookKeyRes)
	_ = rKeyHook.Body.Close()

	anonClient := &http.Client{}
	rAnon := postJSON(t, anonClient, ts.URL+"/api/v1/webhooks/plex", map[string]any{"event": "media.play"}, nil)
	if rAnon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated plex webhook, got %d", rAnon.StatusCode)
	}
	_ = rAnon.Body.Close()

	rForbidden := postJSON(t, anonClient, ts.URL+"/api/v1/webhooks/plex", map[string]any{"event": "media.play"}, map[string]string{"X-Api-Key": readKeyRes.APIKey})
	if rForbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only key, got %d", rForbidden.StatusCode)
	}
	_ = rForbidden.Body.Close()

	rHookOK := postJSON(t, anonClient, ts.URL+"/api/v1/webhooks/plex", map[string]any{"event": "media.play"}, map[string]string{"X-Api-Key": hookKeyRes.APIKey})
	if rHookOK.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid webhook key, got %d", rHookOK.StatusCode)
	}
	_ = rHookOK.Body.Close()

	// Verify removed /api/v1/webhooks/ingest returns 404
	rRemovedIngest := postJSON(t, anonClient, ts.URL+"/api/v1/webhooks/ingest", map[string]any{}, map[string]string{"X-Api-Key": hookKeyRes.APIKey})
	if rRemovedIngest.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for removed ingest webhook, got %d", rRemovedIngest.StatusCode)
	}
	_ = rRemovedIngest.Body.Close()

	// Verify /api/version is admin-only (401 for anonymous)
	rVerAnon, _ := anonClient.Get(ts.URL + "/api/version")
	if rVerAnon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/version, got %d", rVerAnon.StatusCode)
	}
	_ = rVerAnon.Body.Close()

	// 9. Thumbnail SSRF regex guard & CSV formula-injection neutralization & /api/docs CSP
	rBadThumb, err := client.Get(ts.URL + "/api/servers/default/plex/thumb?path=/library/metadata/1/analyze")
	if err != nil || rBadThumb.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-thumb metadata path, got err=%v status=%v", err, rBadThumb.StatusCode)
	}
	_ = rBadThumb.Body.Close()

	_ = database.UpsertItems("default", []db.MediaItem{{
		Key: "Movies:99", RatingKey: "99", Library: "Movies", Kind: "movie",
		Title: "=CMD|' /C calc'!A0", Status: "pending", Missing: []string{"loudness"},
	}})
	rCSV, err := client.Get(ts.URL + "/api/servers/default/export?format=csv")
	if err != nil || rCSV.StatusCode != http.StatusOK {
		t.Fatalf("export csv failed: err=%v status=%v", err, rCSV.StatusCode)
	}
	csvBytes, _ := io.ReadAll(rCSV.Body)
	_ = rCSV.Body.Close()
	if !strings.Contains(string(csvBytes), "'=CMD") {
		t.Fatalf("expected formula-injection prefix in CSV output, got: %s", string(csvBytes))
	}

	rDocs, err := client.Get(ts.URL + "/api/docs")
	if err != nil || rDocs.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/docs failed: err=%v status=%v", err, rDocs.StatusCode)
	}
	_ = rDocs.Body.Close()
	if rDocs.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("expected Content-Security-Policy header on /api/docs")
	}

	// 10. Server-scoped plex webhook, queue move-top, and music capability matrix exclusion
	rPlexSrv := postJSON(t, anonClient, ts.URL+"/api/v1/servers/default/webhooks/plex", map[string]any{"event": "media.play"}, map[string]string{"X-Api-Key": hookKeyRes.APIKey})
	if rPlexSrv.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for server-scoped plex webhook, got %d", rPlexSrv.StatusCode)
	}
	_ = rPlexSrv.Body.Close()

	rMoveTop := postJSON(t, client, ts.URL+"/api/servers/default/queue/move-top", map[string]any{"key": "Movies:99"}, nil)
	if rMoveTop.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for queue move-top, got %d", rMoveTop.StatusCode)
	}
	_ = rMoveTop.Body.Close()

	_ = database.UpsertItems("default", []db.MediaItem{{
		Key: "Music:500", RatingKey: "500", Library: "Music", Kind: "track",
		Title: "Track 1", Status: "pending", Missing: []string{"loudness", "sonic"},
	}})
	_, libs, err := database.GetAggregates("default")
	if err != nil {
		t.Fatalf("GetAggregates failed: %v", err)
	}
	for _, l := range libs {
		if l.Title == "Music" {
			if _, hasChapters := l.CapStats["chapters"]; hasChapters {
				t.Fatalf("music library should not have video capability 'chapters'")
			}
			if _, hasLoud := l.CapStats["loudness"]; !hasLoud {
				t.Fatalf("music library should include 'loudness'")
			}
		}
		if l.Title == "Movies" {
			if _, hasIntro := l.CapStats["intro"]; hasIntro {
				t.Fatalf("movie library should not have episode-only capability 'intro'")
			}
			if _, hasSonic := l.CapStats["sonic"]; hasSonic {
				t.Fatalf("movie library should not have music-only capability 'sonic'")
			}
		}
	}

	// Verify /api/openapi.json and /api/docs require authentication
	anon := &http.Client{}
	for _, p := range []string{"/api/openapi.json", "/api/docs"} {
		resp, err := anon.Get(ts.URL + p)
		if err != nil {
			t.Fatalf("anon GET %s error: %v", p, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for unauthenticated %s, got %d", p, resp.StatusCode)
		}
	}

	// Verify progressive brute-force rate limiting returns 429 with Retry-After
	got429 := false
	for i := 0; i < 15; i++ {
		rBF := postJSON(t, anon, ts.URL+"/api/auth/login", map[string]any{"username": "nobody", "password": "wrong"}, nil)
		_ = rBF.Body.Close()
		if rBF.StatusCode == http.StatusTooManyRequests {
			if rBF.Header.Get("Retry-After") == "" {
				t.Fatalf("expected Retry-After header on 429 response")
			}
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatalf("expected brute-force login attempts to trigger HTTP 429")
	}
}

func TestMultiServerRBAC(t *testing.T) {
	ts, adminClient, _ := newTestServer(t)

	// 1. Setup admin
	rSetup := postJSON(t, adminClient, ts.URL+"/api/auth/setup", map[string]any{
		"username": "admin", "password": "Str0ng#Pass!99", "display_name": "Admin",
	}, nil)
	if rSetup.StatusCode != 200 {
		t.Fatalf("setup failed: %d", rSetup.StatusCode)
	}
	_ = rSetup.Body.Close()

	// 2. Add a second server
	rSrv := postJSON(t, adminClient, ts.URL+"/api/servers", map[string]any{
		"name": "Backup Plex", "kind": "plex", "url": "https://plex-backup.local:32400", "token": "backup-tok-123", "ssl_verify": false,
	}, nil)
	if rSrv.StatusCode != 201 {
		b, _ := io.ReadAll(rSrv.Body)
		t.Fatalf("add server failed: %d %s", rSrv.StatusCode, string(b))
	}
	var srvResp struct {
		Server struct {
			ID string `json:"id"`
		} `json:"server"`
	}
	_ = json.NewDecoder(rSrv.Body).Decode(&srvResp)
	_ = rSrv.Body.Close()
	srv2ID := srvResp.Server.ID
	if srv2ID == "" {
		t.Fatalf("expected non-empty second server ID")
	}

	// 3. Admin creates an operator restricted to ["default"] only
	rUser := postJSON(t, adminClient, ts.URL+"/api/users", map[string]any{
		"username":        "scoped_op",
		"password":        "Oper@tor#Key!88",
		"role":            "operator",
		"allowed_servers": []string{"default"},
	}, nil)
	if rUser.StatusCode != 201 {
		b, _ := io.ReadAll(rUser.Body)
		t.Fatalf("create scoped operator failed: %d %s", rUser.StatusCode, string(b))
	}
	var userResp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.NewDecoder(rUser.Body).Decode(&userResp)
	_ = rUser.Body.Close()

	// 4. Sign in as scoped_op
	opJar, _ := cookiejar.New(nil)
	opClient := &http.Client{Jar: opJar}
	rLogin := postJSON(t, opClient, ts.URL+"/api/auth/login", map[string]any{
		"username": "scoped_op", "password": "Oper@tor#Key!88",
	}, nil)
	if rLogin.StatusCode != 200 {
		t.Fatalf("scoped_op login failed: %d", rLogin.StatusCode)
	}
	_ = rLogin.Body.Close()

	// 5. GET /api/servers should only return ["default"]
	resp, err := opClient.Get(ts.URL + "/api/servers")
	if err != nil {
		t.Fatalf("get servers: %v", err)
	}
	var listResp struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"servers"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&listResp)
	_ = resp.Body.Close()
	if len(listResp.Servers) != 1 || listResp.Servers[0].ID != "default" {
		t.Fatalf("expected only [default] for scoped_op, got %+v", listResp.Servers)
	}

	// 6. Access to default -> 200; access to srv2 -> 403 across status, events, pause
	rOK, _ := opClient.Get(ts.URL + "/api/servers/default/status")
	_ = rOK.Body.Close()
	if rOK.StatusCode != 200 {
		t.Fatalf("expected 200 for allowed server, got %d", rOK.StatusCode)
	}
	for _, p := range []string{"/status", "/events", "/history", "/blocklist"} {
		rDeny, _ := opClient.Get(ts.URL + "/api/servers/" + srv2ID + p)
		_ = rDeny.Body.Close()
		if rDeny.StatusCode != 403 {
			t.Fatalf("expected 403 for disallowed server %s, got %d", p, rDeny.StatusCode)
		}
	}
	rPause := postJSON(t, opClient, ts.URL+"/api/servers/"+srv2ID+"/control/pause", map[string]any{"duration_minutes": 10}, nil)
	_ = rPause.Body.Close()
	if rPause.StatusCode != 403 {
		t.Fatalf("expected 403 for pause on disallowed server, got %d", rPause.StatusCode)
	}

	// 7. Scoped operator webhook API key cannot trigger disallowed server
	rKey := postJSON(t, opClient, ts.URL+"/api/api-keys", map[string]any{"name": "op-hook", "scopes": []string{"webhook"}}, nil)
	var keyResp struct {
		APIKey string `json:"api_key"`
	}
	_ = json.NewDecoder(rKey.Body).Decode(&keyResp)
	_ = rKey.Body.Close()
	hookClient := &http.Client{}
	rHookDeny := postJSON(t, hookClient, ts.URL+"/api/v1/servers/"+srv2ID+"/webhooks/plex", map[string]any{"event": "media.play"}, map[string]string{"X-Api-Key": keyResp.APIKey})
	_ = rHookDeny.Body.Close()
	if rHookDeny.StatusCode != 403 {
		t.Fatalf("expected 403 for webhook targeting disallowed server, got %d", rHookDeny.StatusCode)
	}
	rHookAll := postJSON(t, hookClient, ts.URL+"/api/v1/webhooks/plex", map[string]any{"event": "media.play"}, map[string]string{"X-Api-Key": keyResp.APIKey})
	if rHookAll.StatusCode != 200 {
		t.Fatalf("expected 200 for wildcard plex webhook, got %d", rHookAll.StatusCode)
	}
	_ = rHookAll.Body.Close()

	// 7b. Per-server API key scoping (admin key scoped to "default" only cannot access srv2)
	rScopedKey := postJSON(t, adminClient, ts.URL+"/api/api-keys", map[string]any{
		"name":            "default-only",
		"scopes":          []string{"webhook"},
		"allowed_servers": []string{"default"},
	}, nil)
	var scopedKeyResp struct {
		APIKey string `json:"api_key"`
	}
	_ = json.NewDecoder(rScopedKey.Body).Decode(&scopedKeyResp)
	_ = rScopedKey.Body.Close()

	rKeyOK := postJSON(t, hookClient, ts.URL+"/api/v1/servers/default/webhooks/plex?api_key="+scopedKeyResp.APIKey, map[string]any{"event": "media.play"}, nil)
	_ = rKeyOK.Body.Close()
	if rKeyOK.StatusCode != 200 {
		t.Fatalf("expected 200 for server-scoped Plex webhook on allowed server, got %d", rKeyOK.StatusCode)
	}
	rKeyDeny := postJSON(t, hookClient, ts.URL+"/api/v1/servers/"+srv2ID+"/webhooks/plex", map[string]any{"event": "media.play"}, map[string]string{"X-Api-Key": scopedKeyResp.APIKey})
	_ = rKeyDeny.Body.Close()
	if rKeyDeny.StatusCode != 403 {
		t.Fatalf("expected 403 for server-scoped API key on disallowed server, got %d", rKeyDeny.StatusCode)
	}

	// 8. Admin grants access to srv2 via PATCH /api/users/{id}/servers -> now 200
	patchBody, _ := json.Marshal(map[string]any{"allowed_servers": []string{"default", srv2ID}})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/users/"+userResp.User.ID+"/servers", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "sweeparr")
	rPatch, err := adminClient.Do(req)
	if err != nil || rPatch.StatusCode != 200 {
		t.Fatalf("patch allowed_servers failed: %v", err)
	}
	_ = rPatch.Body.Close()

	rNowOK, _ := opClient.Get(ts.URL + "/api/servers/" + srv2ID + "/status")
	_ = rNowOK.Body.Close()
	if rNowOK.StatusCode != 200 {
		t.Fatalf("expected 200 after granting srv2 access, got %d", rNowOK.StatusCode)
	}

	// 9. Verify new v1 library/item endpoints and native Plex multipart webhook
	rLibs, _ := opClient.Get(ts.URL + "/api/v1/servers/default/libraries")
	if rLibs.StatusCode != 200 {
		t.Fatalf("expected 200 for GET /api/v1/servers/default/libraries, got %d", rLibs.StatusCode)
	}
	_ = rLibs.Body.Close()

	rLibScan := postJSON(t, opClient, ts.URL+"/api/v1/servers/default/libraries/Movies/scan", map[string]any{}, nil)
	if rLibScan.StatusCode != 200 {
		t.Fatalf("expected 200 for single library scan, got %d", rLibScan.StatusCode)
	}
	_ = rLibScan.Body.Close()

	rAllLibScan := postJSON(t, opClient, ts.URL+"/api/v1/servers/default/libraries/scan", map[string]any{}, nil)
	if rAllLibScan.StatusCode != 200 {
		t.Fatalf("expected 200 for all libraries scan, got %d", rAllLibScan.StatusCode)
	}
	_ = rAllLibScan.Body.Close()

	rItemScan := postJSON(t, opClient, ts.URL+"/api/v1/servers/default/items/42/scan?library=Movies", map[string]any{}, nil)
	if rItemScan.StatusCode != 200 {
		t.Fatalf("expected 200 for single item scan, got %d", rItemScan.StatusCode)
	}
	_ = rItemScan.Body.Close()

	// Native Plex multipart webhook with ?api_key=...
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("payload", `{"event":"library.new","Server":{"title":"TestPlex"},"Metadata":{"ratingKey":"42","type":"movie","title":"Inception","librarySectionTitle":"Movies"}}`)
	_ = mw.Close()
	plexReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/plex?api_key="+keyResp.APIKey, &mp)
	plexReq.Header.Set("Content-Type", mw.FormDataContentType())
	rPlex, err := hookClient.Do(plexReq)
	if err != nil || rPlex.StatusCode != 200 {
		t.Fatalf("plex webhook failed: err=%v status=%v", err, rPlex)
	}
	var plexResp map[string]any
	_ = json.NewDecoder(rPlex.Body).Decode(&plexResp)
	_ = rPlex.Body.Close()
	if plexResp["mode"] != "item_ingest" {
		t.Fatalf("expected mode=item_ingest for plex library.new, got %+v", plexResp)
	}

	// Non-addition Plex event ignored cleanly with 200
	rPlay := postJSON(t, hookClient, ts.URL+"/api/v1/webhooks/plex?api_key="+keyResp.APIKey, map[string]any{"event": "media.play"}, nil)
	var playResp map[string]any
	_ = json.NewDecoder(rPlay.Body).Decode(&playResp)
	_ = rPlay.Body.Close()
	if rPlay.StatusCode != 200 || playResp["ignored"] != true {
		t.Fatalf("expected media.play to be ignored with 200, got %d %+v", rPlay.StatusCode, playResp)
	}

	// Programmatic catalog sync with mode=full
	rSyncFull := postJSON(t, opClient, ts.URL+"/api/v1/servers/default/sync?mode=full", map[string]any{}, nil)
	if rSyncFull.StatusCode != 200 {
		t.Fatalf("expected 200 for v1 sync?mode=full, got %d", rSyncFull.StatusCode)
	}
	_ = rSyncFull.Body.Close()

	// Native *arr Auto-Scan webhook (Test event + Sonarr season burst coalescing + disallowed server 403)
	rArrTest := postJSON(t, hookClient, ts.URL+"/api/v1/servers/default/webhooks/arr?api_key="+scopedKeyResp.APIKey, map[string]any{"eventType": "Test"}, nil)
	var arrTestResp map[string]any
	_ = json.NewDecoder(rArrTest.Body).Decode(&arrTestResp)
	_ = rArrTest.Body.Close()
	if rArrTest.StatusCode != 200 || arrTestResp["test"] != true {
		t.Fatalf("expected 200 + test=true for *arr Test webhook, got %d %+v", rArrTest.StatusCode, arrTestResp)
	}

	rArrDeny := postJSON(t, hookClient, ts.URL+"/api/v1/servers/"+srv2ID+"/webhooks/arr?api_key="+scopedKeyResp.APIKey, map[string]any{"eventType": "Download"}, nil)
	_ = rArrDeny.Body.Close()
	if rArrDeny.StatusCode != 403 {
		t.Fatalf("expected 403 for *arr webhook on disallowed server, got %d", rArrDeny.StatusCode)
	}

	rSonarr1 := postJSON(t, hookClient, ts.URL+"/api/v1/servers/default/webhooks/arr?api_key="+scopedKeyResp.APIKey, map[string]any{
		"eventType":   "Download",
		"series":      map[string]any{"title": "Andor", "path": "/media/tv/Andor"},
		"episodeFile": map[string]any{"path": "/media/tv/Andor/Season 01/S01E01.mkv"},
	}, nil)
	_ = rSonarr1.Body.Close()
	if rSonarr1.StatusCode != 200 {
		t.Fatalf("expected 200 for Sonarr ep1 webhook, got %d", rSonarr1.StatusCode)
	}

	rSonarr2 := postJSON(t, hookClient, ts.URL+"/api/v1/servers/default/webhooks/arr?api_key="+scopedKeyResp.APIKey, map[string]any{
		"eventType":   "Download",
		"series":      map[string]any{"title": "Andor", "path": "/media/tv/Andor"},
		"episodeFile": map[string]any{"path": "/media/tv/Andor/Season 01/S01E02.mkv"},
	}, nil)
	var sonarr2Resp struct {
		Coalesced bool `json:"coalesced"`
		Triggers  []struct {
			Dir       string `json:"dir"`
			FileCount int    `json:"file_count"`
			Library   string `json:"library"`
		} `json:"triggers"`
	}
	_ = json.NewDecoder(rSonarr2.Body).Decode(&sonarr2Resp)
	_ = rSonarr2.Body.Close()
	if rSonarr2.StatusCode != 200 || !sonarr2Resp.Coalesced || len(sonarr2Resp.Triggers) != 1 || sonarr2Resp.Triggers[0].FileCount != 2 || sonarr2Resp.Triggers[0].Library != "TV Shows" {
		t.Fatalf("expected Sonarr season episodes to coalesce into 1 trigger with 2 files in TV Shows, got %+v", sonarr2Resp)
	}

	// 9b. Library-scoped API key
	rLibKey := postJSON(t, adminClient, ts.URL+"/api/api-keys", map[string]any{
		"name": "movies-only", "scopes": []string{"scan", "webhook"}, "allowed_libraries": []string{"Movies"},
	}, nil)
	var libKeyResp struct {
		APIKey string `json:"api_key"`
	}
	_ = json.NewDecoder(rLibKey.Body).Decode(&libKeyResp)
	_ = rLibKey.Body.Close()
	rLibDeny := postJSON(t, hookClient, ts.URL+"/api/v1/servers/default/libraries/Music/scan", map[string]any{}, map[string]string{"X-Api-Key": libKeyResp.APIKey})
	_ = rLibDeny.Body.Close()
	if rLibDeny.StatusCode != 403 {
		t.Fatalf("expected 403 for library-scoped key on other library, got %d", rLibDeny.StatusCode)
	}
	rLibOK := postJSON(t, hookClient, ts.URL+"/api/v1/servers/default/libraries/Movies/scan", map[string]any{}, map[string]string{"X-Api-Key": libKeyResp.APIKey})
	_ = rLibOK.Body.Close()
	if rLibOK.StatusCode != 200 {
		t.Fatalf("expected 200 for library-scoped key on allowed library, got %d", rLibOK.StatusCode)
	}
	rLibInternal := postJSON(t, hookClient, ts.URL+"/api/servers/default/libraries/scan", map[string]any{}, map[string]string{"X-Api-Key": libKeyResp.APIKey})
	_ = rLibInternal.Body.Close()
	if rLibInternal.StatusCode != 403 {
		t.Fatalf("expected 403 for library-scoped key on internal API, got %d", rLibInternal.StatusCode)
	}

	// 10. RBAC security policy (disable API key creation) & kill-sessions
	polBody, _ := json.Marshal(map[string]any{
		"can_create_api_keys": false,
		"require_mfa":         true,
		"require_passkey":     true,
	})
	polReq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/users/"+userResp.User.ID+"/policy", bytes.NewReader(polBody))
	polReq.Header.Set("Content-Type", "application/json")
	polReq.Header.Set("X-Requested-With", "sweeparr")
	rPol, err := adminClient.Do(polReq)
	if err != nil || rPol.StatusCode != 200 {
		t.Fatalf("patch user policy failed: err=%v status=%v", err, rPol)
	}
	_ = rPol.Body.Close()

	rDenyKey := postJSON(t, opClient, ts.URL+"/api/api-keys", map[string]any{"name": "blocked-key", "scopes": []string{"read"}}, nil)
	_ = rDenyKey.Body.Close()
	if rDenyKey.StatusCode != 403 {
		t.Fatalf("expected 403 when can_create_api_keys=false, got %d", rDenyKey.StatusCode)
	}

	rKill := postJSON(t, adminClient, ts.URL+"/api/users/"+userResp.User.ID+"/kill-sessions", map[string]any{}, nil)
	_ = rKill.Body.Close()
	if rKill.StatusCode != 200 {
		t.Fatalf("kill-sessions failed: %d", rKill.StatusCode)
	}
	rAfterKill, _ := opClient.Get(ts.URL + "/api/auth/me")
	_ = rAfterKill.Body.Close()
	if rAfterKill.StatusCode != 401 {
		t.Fatalf("expected 401 after kill-sessions, got %d", rAfterKill.StatusCode)
	}

	// 9c. Admin credential resets
	rWeak := postJSON(t, adminClient, ts.URL+"/api/users/"+userResp.User.ID+"/password", map[string]any{"password": "short", "must_change": true}, nil)
	_ = rWeak.Body.Close()
	if rWeak.StatusCode != 400 {
		t.Fatalf("expected 400 for weak admin-set password, got %d", rWeak.StatusCode)
	}
	rReset := postJSON(t, adminClient, ts.URL+"/api/users/"+userResp.User.ID+"/reset-auth", map[string]any{"mfa": true, "passkeys": true}, nil)
	_ = rReset.Body.Close()
	if rReset.StatusCode != 200 {
		t.Fatalf("expected 200 for reset-auth, got %d", rReset.StatusCode)
	}
	rSetPw := postJSON(t, adminClient, ts.URL+"/api/users/"+userResp.User.ID+"/password", map[string]any{"password": "Str0ng-Temp-Pass-91!", "must_change": true}, nil)
	_ = rSetPw.Body.Close()
	if rSetPw.StatusCode != 200 {
		t.Fatalf("expected 200 for admin-set password, got %d", rSetPw.StatusCode)
	}
	forcedJar, _ := cookiejar.New(nil)
	forcedClient := &http.Client{Jar: forcedJar}
	rForced := postJSON(t, forcedClient, ts.URL+"/api/auth/login", map[string]any{"username": "scoped_op", "password": "Str0ng-Temp-Pass-91!"}, nil)
	_ = rForced.Body.Close()
	if rForced.StatusCode != 200 {
		t.Fatalf("expected 200 for login with admin-set password, got %d", rForced.StatusCode)
	}
	rForcedBlocked, _ := forcedClient.Get(ts.URL + "/api/servers/default/status")
	_ = rForcedBlocked.Body.Close()
	if rForcedBlocked.StatusCode != 403 {
		t.Fatalf("expected 403 while password change is required, got %d", rForcedBlocked.StatusCode)
	}
	rForcedMe, _ := forcedClient.Get(ts.URL + "/api/auth/me")
	_ = rForcedMe.Body.Close()
	if rForcedMe.StatusCode != 200 {
		t.Fatalf("expected /api/auth/me to stay reachable, got %d", rForcedMe.StatusCode)
	}

	// 11. Offline-by-default /api/version (admin-only), security-settings toggle, consolidated PATCH /api/users/{id}, and exports
	rVerAdmin, err := adminClient.Get(ts.URL + "/api/version")
	if err != nil || rVerAdmin.StatusCode != 200 {
		t.Fatalf("expected 200 for admin /api/version, got err=%v status=%v", err, rVerAdmin)
	}
	var verBody map[string]any
	_ = json.NewDecoder(rVerAdmin.Body).Decode(&verBody)
	_ = rVerAdmin.Body.Close()
	if verBody["update_check_enabled"] != false || verBody["update_available"] != false {
		t.Fatalf("expected offline-by-default update check, got %+v", verBody)
	}
	relURL, _ := verBody["release_url"].(string)
	if !strings.Contains(relURL, "github.com/fabsau/sweeparr") {
		t.Fatalf("expected fabsau/sweeparr release_url, got %+v", verBody)
	}

	// Consolidated PATCH /api/users/{id} must reject password_login_disabled=true when user has 0 passkeys
	badPatch, _ := json.Marshal(map[string]any{
		"role":                    "operator",
		"password_login_disabled": true,
	})
	badReq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/users/"+userResp.User.ID, bytes.NewReader(badPatch))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.Header.Set("X-Requested-With", "sweeparr")
	rBadPatch, _ := adminClient.Do(badReq)
	_ = rBadPatch.Body.Close()
	if rBadPatch.StatusCode != 400 {
		t.Fatalf("expected 400 when disabling password login without passkey, got %d", rBadPatch.StatusCode)
	}

	// Export endpoints (history, blocklist, events) return 200 CSV & JSON
	for _, ep := range []string{
		"/api/servers/default/history/export?format=csv",
		"/api/servers/default/history/export?format=json",
		"/api/servers/default/blocklist/export?format=csv",
		"/api/servers/default/events/export?format=csv",
	} {
		re, err := adminClient.Get(ts.URL + ep)
		if err != nil || re.StatusCode != 200 {
			t.Fatalf("export %s failed: err=%v status=%v", ep, err, re)
		}
		_ = re.Body.Close()
	}
}
