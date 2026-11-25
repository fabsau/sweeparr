package plex

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
)

var libraryPrefToCap = map[string]string{
	// Current Plex Media Server section preference IDs (/library/sections/{key}/prefs)
	"enableLoudnessAnalysis":        "loudness",
	"enableVoiceActivityGeneration": "voice",
	"enableChapterThumbnails":       "chapters",
	"enableIntroMarkerGeneration":   "intro",
	"enableCreditsMarkerGeneration": "credits",
	"enableAdMarkerGeneration":      "ads",
	"enableBIFGeneration":           "previews",
	"enableDeepMediaAnalysis":       "deep_analysis",
	"musicAnalysis":                 "sonic",
	// Legacy / fallback preference IDs
	"enableVoiceActivity":      "voice",
	"enableIntroDetection":     "intro",
	"enableCreditsDetection":   "credits",
	"enableAdDetection":        "ads",
	"enableSonicAnalysis":      "sonic",
	"musicAnalysisBehavior":    "sonic",
	"markerGenerationBehavior": "intro",
	"creditsDetectionBehavior": "credits",
	"voiceActivityBehavior":    "voice",
	"chapterThumbnailBehavior": "chapters",
	"videoPreviewBehavior":     "previews",
	"adDetectionBehavior":      "ads",
}

var serverPrefToCap = map[string]string{
	"LoudnessAnalysisBehavior":      "loudness",
	"GenerateVADBehavior":           "voice",
	"GenerateChapterThumbBehavior":  "chapters",
	"GenerateIntroMarkerBehavior":   "intro",
	"GenerateCreditsMarkerBehavior": "credits",
	"GenerateAdMarkerBehavior":      "ads",
	"GenerateBIFBehavior":           "previews",
	"ButlerTaskDeepMediaAnalysis":   "deep_analysis",
	"MusicAnalysisBehavior":         "sonic",
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string, sslVerify bool) *Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: !sslVerify},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		http: &http.Client{
			Transport: tr,
			// Rely on each request's context.Context deadline rather than a short global Timeout,
			// since TriggerAnalyze uses MaxItemScanMinutes while telemetry uses 8s.
			// Never follow HTTP redirects so X-Plex-Token can never be forwarded off-host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *Client) sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", config.RedactSecret(err.Error(), c.token))
}

func (c *Client) Kind() string { return "plex" }

func (c *Client) doJSON(ctx context.Context, method, path string, out any) (int, error) {
	maxAttempts := 1
	if method == http.MethodGet {
		maxAttempts = 3 // Retry transient network/5xx errors on slow or flaky links
	}
	var lastStatus int
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 750 * time.Millisecond
			select {
			case <-ctx.Done():
				return 0, c.sanitizeErr(ctx.Err())
			case <-time.After(backoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
		if err != nil {
			return 0, c.sanitizeErr(err)
		}
		req.Header.Set("X-Plex-Token", c.token)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = c.sanitizeErr(err)
			continue
		}
		if resp.StatusCode >= 502 && resp.StatusCode <= 504 && attempt+1 < maxAttempts {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			lastErr = fmt.Errorf("plex HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			return resp.StatusCode, fmt.Errorf("plex HTTP %d: %s", resp.StatusCode, config.RedactSecret(strings.TrimSpace(string(b)), c.token))
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				_ = resp.Body.Close()
				return resp.StatusCode, c.sanitizeErr(err)
			}
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	return lastStatus, lastErr
}

func (c *Client) Identity(ctx context.Context) (media.ServerIdentity, error) {
	var raw struct {
		MediaContainer struct {
			FriendlyName string `json:"friendlyName"`
			Version      string `json:"version"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/", &raw); err != nil {
		return media.ServerIdentity{}, err
	}
	return media.ServerIdentity{
		FriendlyName: raw.MediaContainer.FriendlyName,
		Version:      raw.MediaContainer.Version,
	}, nil
}

func (c *Client) Sections(ctx context.Context) ([]media.Section, error) {
	var raw struct {
		MediaContainer struct {
			Directory []struct {
				Key      string `json:"key"`
				Title    string `json:"title"`
				Type     string `json:"type"`
				Location []struct {
					Path string `json:"path"`
				} `json:"Location"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/library/sections", &raw); err != nil {
		return nil, err
	}
	out := make([]media.Section, 0, len(raw.MediaContainer.Directory))
	for _, d := range raw.MediaContainer.Directory {
		if d.Type == "movie" || d.Type == "show" || d.Type == "artist" {
			locs := make([]string, 0, len(d.Location))
			for _, l := range d.Location {
				if p := strings.TrimSpace(l.Path); p != "" {
					locs = append(locs, p)
				}
			}
			out = append(out, media.Section{Key: d.Key, Title: d.Title, Type: d.Type, Locations: locs})
		}
	}
	return out, nil
}

// RefreshSectionPath triggers a targeted Plex library scan for a specific directory path
// (GET /library/sections/{sectionKey}/refresh?path=...), replicating Autoscan/Autopulse behavior.
func (c *Client) RefreshSectionPath(ctx context.Context, sectionKey, dirPath string) error {
	endpoint := "/library/sections/" + url.PathEscape(sectionKey) + "/refresh"
	if p := strings.TrimSpace(dirPath); p != "" {
		endpoint += "?path=" + url.QueryEscape(p)
	}
	_, err := c.doJSON(ctx, http.MethodGet, endpoint, nil)
	return err
}

// LibraryCapModes returns per-capability Plex settings for a section:
// "disabled", "recorded_only" (e.g. ad detection set to recorded/DVR items only), or "all".
func (c *Client) LibraryCapModes(ctx context.Context, sectionKey string) (map[string]string, error) {
	var raw struct {
		MediaContainer struct {
			Setting []struct {
				ID    string `json:"id"`
				Value any    `json:"value"`
			} `json:"Setting"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/library/sections/"+sectionKey+"/prefs", &raw); err != nil {
		return nil, err
	}
	modes := make(map[string]string)
	hasAdPref := false
	for _, s := range raw.MediaContainer.Setting {
		if s.ID == "enableAdMarkerGeneration" || s.ID == "enableAdDetection" || s.ID == "adDetectionBehavior" {
			hasAdPref = true
		}
		capName, ok := libraryPrefToCap[s.ID]
		if !ok {
			continue
		}
		val := strings.ToLower(strings.TrimSpace(fmt.Sprint(s.Value)))
		if capName == "ads" {
			switch val {
			case "", "0", "false", "never":
				modes[capName] = "disabled"
			case "1", "recorded", "dvr":
				modes[capName] = "recorded_only"
			default:
				modes[capName] = "all"
			}
			continue
		}
		if val == "0" || val == "false" || val == "never" {
			modes[capName] = "disabled"
		} else if modes[capName] != "disabled" {
			modes[capName] = "all"
		}
	}
	if !hasAdPref {
		modes["ads"] = "disabled"
	}
	// Also inspect global server preferences (/:/prefs) for server-wide disabled capabilities (e.g. GenerateIntroMarkerBehavior="never").
	var srvRaw struct {
		MediaContainer struct {
			Setting []struct {
				ID    string `json:"id"`
				Value any    `json:"value"`
			} `json:"Setting"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/:/prefs", &srvRaw); err == nil {
		for _, s := range srvRaw.MediaContainer.Setting {
			if capName, ok := serverPrefToCap[s.ID]; ok {
				val := strings.ToLower(strings.TrimSpace(fmt.Sprint(s.Value)))
				if val == "0" || val == "false" || val == "never" {
					modes[capName] = "disabled"
				}
			}
		}
	}
	return modes, nil
}

func (c *Client) LibraryDisabledCaps(ctx context.Context, sectionKey string) ([]string, error) {
	modes, err := c.LibraryCapModes(ctx, sectionKey)
	if err != nil {
		return nil, err
	}
	disabled := make([]string, 0)
	for capName, mode := range modes {
		if mode == "disabled" {
			disabled = append(disabled, capName)
		}
	}
	return disabled, nil
}

func (c *Client) ListSectionItems(ctx context.Context, sec media.Section, start, size int) ([]db.MediaItem, int, error) {
	typeFilter := "1"
	switch sec.Type {
	case "show":
		typeFilter = "4" // episodes
	case "artist":
		typeFilter = "10" // tracks
	}
	path := fmt.Sprintf("/library/sections/%s/all?type=%s&includeChapters=1&includeMarkers=1&includeBandwidths=1&X-Plex-Container-Start=%d&X-Plex-Container-Size=%d",
		sec.Key, typeFilter, start, size)
	var raw struct {
		MediaContainer struct {
			TotalSize int            `json:"totalSize"`
			Size      int            `json:"size"`
			Metadata  []plexMetadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, path, &raw); err != nil {
		return nil, 0, err
	}
	tot := raw.MediaContainer.TotalSize
	if tot == 0 {
		tot = raw.MediaContainer.Size
	}
	metaList := raw.MediaContainer.Metadata
	// Plex's bulk /library/sections/{key}/all endpoint strips Marker[], Chapter[], Part.deepAnalysisVersion,
	// Part.requiredBandwidths, and Part.Stream[]. Hydrate the page in one batch call via /library/metadata/{id1,id2,...}.
	if len(metaList) > 0 {
		needBatch := false
		rks := make([]string, 0, len(metaList))
		for _, m := range metaList {
			if rk := strings.TrimSpace(string(m.RatingKey)); rk != "" {
				rks = append(rks, rk)
			}
			for _, med := range m.Media {
				for _, p := range med.Part {
					if len(p.Stream) == 0 {
						needBatch = true
					}
				}
			}
		}
		if needBatch && len(rks) > 0 {
			batchPath := fmt.Sprintf("/library/metadata/%s?includeChapters=1&includeMarkers=1&includeBandwidths=1", strings.Join(rks, ","))
			var batchRaw struct {
				MediaContainer struct {
					Metadata []plexMetadata `json:"Metadata"`
				} `json:"MediaContainer"`
			}
			if _, bErr := c.doJSON(ctx, http.MethodGet, batchPath, &batchRaw); bErr == nil && len(batchRaw.MediaContainer.Metadata) > 0 {
				metaList = batchRaw.MediaContainer.Metadata
			}
		}
	}
	out := make([]db.MediaItem, 0, len(metaList))
	for _, m := range metaList {
		out = append(out, m.toMediaItem(sec.Title))
	}
	return out, tot, nil
}

func (c *Client) InspectItem(ctx context.Context, ratingKey, library string) (*db.MediaItem, error) {
	path := fmt.Sprintf("/library/metadata/%s?includeChapters=1&includeMarkers=1&includeBandwidths=1", ratingKey)
	var raw struct {
		MediaContainer struct {
			Metadata []plexMetadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, path, &raw); err != nil {
		return nil, err
	}
	if len(raw.MediaContainer.Metadata) == 0 {
		return nil, fmt.Errorf("metadata %s not found", ratingKey)
	}
	it := raw.MediaContainer.Metadata[0].toMediaItem(library)
	return &it, nil
}

func (c *Client) TriggerAnalyze(ctx context.Context, ratingKey string, triggers []string) (int, error) {
	trigSet := make(map[string]bool, len(triggers))
	for _, t := range triggers {
		trigSet[strings.TrimSpace(t)] = true
	}
	if len(trigSet) == 0 {
		trigSet["analyze"] = true
	}

	type endpointCall struct {
		path     string
		required bool
	}
	var calls []endpointCall
	if trigSet["analyze"] {
		calls = append(calls, endpointCall{
			path:     fmt.Sprintf("/library/metadata/%s/analyze?deep=1", ratingKey),
			required: true,
		})
	}
	if trigSet["voice"] {
		calls = append(calls, endpointCall{
			path:     fmt.Sprintf("/library/metadata/%s/voiceActivity?force=1&manual=1", ratingKey),
			required: len(calls) == 0,
		})
	}
	if trigSet["loudness"] {
		calls = append(calls, endpointCall{
			path:     fmt.Sprintf("/library/metadata/%s/analyzeLoudness?force=1&manual=1", ratingKey),
			required: len(calls) == 0,
		})
	}
	if trigSet["chapters"] {
		calls = append(calls, endpointCall{
			path:     fmt.Sprintf("/library/metadata/%s/chapterThumbs?force=1&manual=1", ratingKey),
			required: len(calls) == 0,
		})
	}
	if trigSet["intros"] {
		calls = append(calls, endpointCall{
			path:     fmt.Sprintf("/library/metadata/%s/intro?force=1", ratingKey),
			required: len(calls) == 0,
		})
	}
	if trigSet["ads"] {
		calls = append(calls, endpointCall{
			path: fmt.Sprintf("/library/metadata/%s/addetect", ratingKey),
		})
	}
	if len(calls) == 0 {
		calls = append(calls, endpointCall{
			path:     fmt.Sprintf("/library/metadata/%s/analyze?deep=1", ratingKey),
			required: true,
		})
	}

	lastStatus := http.StatusOK
	var firstErr error
	for _, call := range calls {
		st, err := c.doJSON(ctx, http.MethodPut, call.path, nil)
		if err != nil {
			// Sub-endpoints like /intro or /addetect may return 400/404 on item types where they don't apply (e.g. movies or tracks).
			if call.required && firstErr == nil {
				firstErr = err
				lastStatus = st
			}
			continue
		}
		lastStatus = st
	}
	return lastStatus, firstErr
}

func (c *Client) Activities(ctx context.Context) ([]media.ActivityInfo, error) {
	var raw struct {
		MediaContainer struct {
			Activity []struct {
				UUID        string  `json:"uuid"`
				Type        string  `json:"type"`
				Title       string  `json:"title"`
				Subtitle    string  `json:"subtitle"`
				Progress    float64 `json:"progress"`
				Cancellable bool    `json:"cancellable"`
			} `json:"Activity"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/activities", &raw); err != nil {
		return nil, err
	}
	out := make([]media.ActivityInfo, 0, len(raw.MediaContainer.Activity))
	for _, a := range raw.MediaContainer.Activity {
		out = append(out, media.ActivityInfo{
			UUID:        a.UUID,
			Type:        a.Type,
			Title:       a.Title,
			Subtitle:    a.Subtitle,
			Progress:    a.Progress,
			Cancellable: a.Cancellable,
		})
	}
	return out, nil
}

func (c *Client) CancelActivity(ctx context.Context, uuid string) error {
	_, err := c.doJSON(ctx, http.MethodDelete, "/activities/"+uuid, nil)
	return err
}

func (c *Client) CheckServerSettings(ctx context.Context) ([]media.SettingsCheckRow, error) {
	var raw struct {
		MediaContainer struct {
			Setting []struct {
				ID    string `json:"id"`
				Value any    `json:"value"`
			} `json:"Setting"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/:/prefs", &raw); err != nil {
		return nil, err
	}
	prefs := map[string]string{}
	for _, s := range raw.MediaContainer.Setting {
		prefs[s.ID] = strings.TrimSpace(fmt.Sprint(s.Value))
	}
	deep := prefs["ButlerTaskDeepMediaAnalysis"]
	upgrade := prefs["ButlerTaskUpgradeMediaAnalysis"]
	deepOK := deep == "true" || deep == "1"
	upgradeOK := upgrade == "false" || upgrade == "0" || upgrade == ""

	checkBehavior := func(id, label, okMsg, warnMsg string) media.SettingsCheckRow {
		val := strings.ToLower(prefs[id])
		ok := val != "" && val != "never" && val != "0" && val != "false"
		rec := okMsg
		if !ok {
			rec = warnMsg
		}
		return media.SettingsCheckRow{
			ID:             id,
			Label:          label,
			Value:          valOrUnset(prefs[id]),
			Status:         boolStatus(ok),
			Recommendation: rec,
		}
	}

	rows := []media.SettingsCheckRow{
		{
			ID:     "ButlerTaskDeepMediaAnalysis",
			Label:  "Perform extensive media analysis during maintenance (Deep Analysis)",
			Value:  valOrUnset(deep),
			Status: boolStatus(deepOK),
			Recommendation: func() string {
				if deepOK {
					return "Enabled — server computes bitrate peaks during scheduled maintenance."
				}
				return "Turn ON so the server computes Deep Media Analysis bitrate peaks during maintenance."
			}(),
		},
		{
			ID:     "ButlerTaskUpgradeMediaAnalysis",
			Label:  "Upgrade media analysis during maintenance",
			Value:  valOrUnset(upgrade),
			Status: boolStatus(upgradeOK),
			Recommendation: func() string {
				if upgradeOK {
					return "Disabled — avoids redundant re-scans of unchanged media."
				}
				return "Turn OFF to avoid redundant maintenance re-scans of unchanged files."
			}(),
		},
		checkBehavior("GenerateIntroMarkerBehavior", "Generate intro video markers", "Enabled — intro detection runs on scheduled/added episodes.", "Set to 'scheduled' or 'as a scheduled task and when media is added'."),
		checkBehavior("GenerateCreditsMarkerBehavior", "Generate credits video markers", "Enabled — credits detection runs on movies and episodes.", "Set to 'scheduled' or 'as a scheduled task and when media is added'."),
		checkBehavior("GenerateVADBehavior", "Generate voice activity data", "Enabled — voice activity detection runs on video audio streams.", "Set to 'scheduled' or 'as a scheduled task and when media is added'."),
		checkBehavior("GenerateChapterThumbBehavior", "Generate chapter thumbnails", "Enabled — chapter thumbnails are generated for video items.", "Enable if you want Sweeparr to track chapter thumbnails."),
		checkBehavior("LoudnessAnalysisBehavior", "Analyze audio tracks for loudness", "Enabled — audio loudness/gain normalization data is computed.", "Set to 'scheduled' or 'as a scheduled task and when media is added'."),
		checkBehavior("MusicAnalysisBehavior", "Analyze audio tracks for sonic features", "Enabled — sonic analysis runs on music libraries.", "Enable if you use Plexamp / Sonic Analysis."),
		checkBehavior("GenerateBIFBehavior", "Generate video preview thumbnails", "Enabled — seek-bar BIF preview thumbnails are generated.", "Optional: enable if you want seek-bar video previews."),
	}
	if startH, endH := prefs["ButlerStartHour"], prefs["ButlerEndHour"]; startH != "" || endH != "" {
		rows = append(rows, media.SettingsCheckRow{
			ID:             "ButlerWindow",
			Label:          "Scheduled maintenance window (Butler hours)",
			Value:          fmt.Sprintf("%s:00 – %s:00", valOrUnset(startH), valOrUnset(endH)),
			Status:         "ok",
			Recommendation: "Ensure Quiet Hours in Sweeparr do not conflict if you rely on Butler maintenance.",
		})
	}
	return rows, nil
}

func (c *Client) FetchThumb(ctx context.Context, path string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-Plex-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", c.sanitizeErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("thumb HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, "", err
	}
	ctype := strings.Split(resp.Header.Get("Content-Type"), ";")[0]
	if !strings.HasPrefix(ctype, "image/") {
		ctype = "image/jpeg"
	}
	return body, ctype, nil
}
