package media

import (
	"context"

	"github.com/fabsau/sweeparr/internal/db"
)

type ServerIdentity struct {
	FriendlyName string `json:"friendly_name"`
	Version      string `json:"version"`
}

type Section struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Locations []string `json:"locations,omitempty"`
}

type SessionInfo struct {
	User            string  `json:"user"`
	Title           string  `json:"title"`
	RawTitle        string  `json:"raw_title"`
	Grandparent     string  `json:"grandparent_title"`
	Parent          string  `json:"parent_title"`
	ParentIndex     int     `json:"parent_index"`
	Index           int     `json:"index"`
	Year            int     `json:"year"`
	Kind            string  `json:"kind"`
	Player          string  `json:"player"`
	Platform        string  `json:"platform"`
	Product         string  `json:"product"`
	Address         string  `json:"address"`
	Local           bool    `json:"local"`
	Location        string  `json:"location"`
	BandwidthKbps   int     `json:"bandwidth_kbps"`
	State           string  `json:"state"`
	Transcoding     bool    `json:"transcoding"`
	VideoDecision   string  `json:"video_decision"`
	AudioDecision   string  `json:"audio_decision"`
	VideoResolution string  `json:"video_resolution"`
	VideoCodec      string  `json:"video_codec"`
	AudioCodec      string  `json:"audio_codec"`
	AudioChannels   int     `json:"audio_channels"`
	BitrateKbps     int     `json:"bitrate_kbps"`
	DurationMs      int64   `json:"duration_ms"`
	ViewOffsetMs    int64   `json:"view_offset_ms"`
	ProgressPct     float64 `json:"progress_pct"`
	ThumbPath       string  `json:"thumb_path"`
}

type ActivityInfo struct {
	UUID        string  `json:"uuid"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Subtitle    string  `json:"subtitle"`
	Progress    float64 `json:"progress"`
	Cancellable bool    `json:"cancellable"`
}

type SettingsCheckRow struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Value          string `json:"value"`
	Status         string `json:"status"` // "ok" or "warn"
	Recommendation string `json:"recommendation"`
}

// Adapter abstracts media server operations (currently implemented for Plex; ready for Jellyfin/Emby).
type Adapter interface {
	Kind() string
	Identity(ctx context.Context) (ServerIdentity, error)
	Sections(ctx context.Context) ([]Section, error)
	LibraryDisabledCaps(ctx context.Context, sectionKey string) ([]string, error)
	LibraryCapModes(ctx context.Context, sectionKey string) (map[string]string, error)
	ListSectionItems(ctx context.Context, sec Section, start, size int) ([]db.MediaItem, int, error)
	InspectItem(ctx context.Context, ratingKey, library string) (*db.MediaItem, error)
	TriggerAnalyze(ctx context.Context, ratingKey string, triggers []string) (int, error)
	RefreshSectionPath(ctx context.Context, sectionKey, dirPath string) error
	Sessions(ctx context.Context) ([]SessionInfo, error)
	Activities(ctx context.Context) ([]ActivityInfo, error)
	CancelActivity(ctx context.Context, uuid string) error
	CheckServerSettings(ctx context.Context) ([]SettingsCheckRow, error)
	FetchThumb(ctx context.Context, path string) ([]byte, string, error)
}
