package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/media"
)

func (c *Client) Sessions(ctx context.Context) ([]media.SessionInfo, error) {
	var raw struct {
		MediaContainer struct {
			Metadata []struct {
				Title            string `json:"title"`
				GrandparentTitle string `json:"grandparentTitle"`
				ParentTitle      string `json:"parentTitle"`
				ParentIndex      int    `json:"parentIndex"`
				Index            int    `json:"index"`
				Year             int    `json:"year"`
				Type             string `json:"type"`
				Duration         int64  `json:"duration"`
				ViewOffset       int64  `json:"viewOffset"`
				Thumb            string `json:"thumb"`
				ParentThumb      string `json:"parentThumb"`
				GrandparentThumb string `json:"grandparentThumb"`
				User             struct {
					Title string `json:"title"`
				} `json:"User"`
				Player struct {
					Title    string `json:"title"`
					Platform string `json:"platform"`
					Product  string `json:"product"`
					State    string `json:"state"`
					Address  string `json:"address"`
					Local    bool   `json:"local"`
				} `json:"Player"`
				Session struct {
					Bandwidth int    `json:"bandwidth"`
					Location  string `json:"location"`
				} `json:"Session"`
				TranscodeSession *struct {
					VideoDecision string `json:"videoDecision"`
					AudioDecision string `json:"audioDecision"`
				} `json:"TranscodeSession"`
				Media []struct {
					VideoResolution string `json:"videoResolution"`
					VideoCodec      string `json:"videoCodec"`
					AudioCodec      string `json:"audioCodec"`
					AudioChannels   int    `json:"audioChannels"`
					Bitrate         int    `json:"bitrate"`
				} `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if _, err := c.doJSON(ctx, http.MethodGet, "/status/sessions", &raw); err != nil {
		return nil, err
	}
	out := make([]media.SessionInfo, 0, len(raw.MediaContainer.Metadata))
	for _, it := range raw.MediaContainer.Metadata {
		title := it.Title
		if it.GrandparentTitle != "" {
			title = it.GrandparentTitle + " — " + it.Title
		}
		vDec, aDec := "direct play", "direct play"
		isTrans := false
		if it.TranscodeSession != nil {
			vDec = strings.ToLower(it.TranscodeSession.VideoDecision)
			aDec = strings.ToLower(it.TranscodeSession.AudioDecision)
			if vDec == "" {
				vDec = "copy"
			}
			if aDec == "" {
				aDec = "copy"
			}
			isTrans = vDec == "transcode" || aDec == "transcode"
		}
		var mRes, mVCodec, mACodec string
		var mAChan, mBit int
		if len(it.Media) > 0 {
			mRes = it.Media[0].VideoResolution
			mVCodec = it.Media[0].VideoCodec
			mACodec = it.Media[0].AudioCodec
			mAChan = it.Media[0].AudioChannels
			mBit = it.Media[0].Bitrate
		}
		pct := 0.0
		if it.Duration > 0 {
			pct = float64(it.ViewOffset) / float64(it.Duration) * 100.0
		}
		thumb := it.Thumb
		if thumb == "" {
			thumb = it.ParentThumb
		}
		if thumb == "" {
			thumb = it.GrandparentThumb
		}
		if !strings.HasPrefix(thumb, "/library/metadata/") {
			thumb = ""
		}
		loc := it.Session.Location
		if loc == "" {
			if it.Player.Local {
				loc = "lan"
			} else {
				loc = "wan"
			}
		}
		user := it.User.Title
		if user == "" {
			user = "Unknown"
		}
		state := it.Player.State
		if state == "" {
			state = "playing"
		}
		out = append(out, media.SessionInfo{
			User:            user,
			Title:           title,
			RawTitle:        it.Title,
			Grandparent:     it.GrandparentTitle,
			Parent:          it.ParentTitle,
			ParentIndex:     it.ParentIndex,
			Index:           it.Index,
			Year:            it.Year,
			Kind:            it.Type,
			Player:          it.Player.Title,
			Platform:        it.Player.Platform,
			Product:         it.Player.Product,
			Address:         it.Player.Address,
			Local:           it.Player.Local,
			Location:        loc,
			BandwidthKbps:   it.Session.Bandwidth,
			State:           state,
			Transcoding:     isTrans,
			VideoDecision:   vDec,
			AudioDecision:   aDec,
			VideoResolution: mRes,
			VideoCodec:      mVCodec,
			AudioCodec:      mACodec,
			AudioChannels:   mAChan,
			BitrateKbps:     mBit,
			DurationMs:      it.Duration,
			ViewOffsetMs:    it.ViewOffset,
			ProgressPct:     pct,
			ThumbPath:       thumb,
		})
	}
	return out, nil
}

func valOrUnset(v string) string {
	if v == "" {
		return "unset"
	}
	return v
}

func boolStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "warn"
}

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*f = flexString(str)
		return nil
	}
	*f = flexString(s)
	return nil
}

type plexMetadata struct {
	RatingKey               flexString `json:"ratingKey"`
	Type                    string     `json:"type"`
	Title                   string     `json:"title"`
	GrandparentTitle        string     `json:"grandparentTitle"`
	ParentTitle             string     `json:"parentTitle"`
	ParentIndex             int        `json:"parentIndex"`
	Index                   int        `json:"index"`
	AddedAt                 int64      `json:"addedAt"`
	ChapterSource           flexString `json:"chapterSource"`
	LoudnessAnalysisVersion flexString `json:"loudnessAnalysisVersion"`
	MusicAnalysisVersion    flexString `json:"musicAnalysisVersion"`
	ChannelIdentifier       flexString `json:"channelIdentifier"`
	ChannelTitle            string     `json:"channelTitle"`
	ChannelID               flexString `json:"channelID"`
	Live                    flexString `json:"live"`
	Collection              []struct {
		Tag string `json:"tag"`
	} `json:"Collection"`
	Media []struct {
		HasVoiceActivity flexString `json:"hasVoiceActivity"`
		Part             []struct {
			ID                  int        `json:"id"`
			Key                 string     `json:"key"`
			Size                int64      `json:"size"`
			Indexes             flexString `json:"indexes"`
			HasThumb            flexString `json:"hasThumbnail"`
			DeepAnalysisVersion flexString `json:"deepAnalysisVersion"`
			RequiredBandwidths  flexString `json:"requiredBandwidths"`
			Stream              []struct {
				StreamType              int        `json:"streamType"`
				Loudness                flexString `json:"loudness"`
				LoudnessAnalysisVersion flexString `json:"loudnessAnalysisVersion"`
				Gain                    flexString `json:"gain"`
				GainOffset              flexString `json:"gainOffset"`
				Peak                    flexString `json:"peak"`
				LRA                     flexString `json:"lra"`
				HasVoiceActivity        flexString `json:"hasVoiceActivity"`
				RequiredBandwidths      flexString `json:"requiredBandwidths"`
				MusicAnalysis           flexString `json:"musicAnalysis"`
			} `json:"Stream"`
		} `json:"Part"`
	} `json:"Media"`
	Marker []struct {
		Type string `json:"type"`
	} `json:"Marker"`
	Chapter []struct {
		ID     int        `json:"id"`
		Filter flexString `json:"filter"`
		Thumb  flexString `json:"thumb"`
	} `json:"Chapter"`
}

func isFlexTruthy(v flexString) bool {
	s := strings.ToLower(strings.TrimSpace(string(v)))
	return s == "1" || s == "true"
}

func (m plexMetadata) toMediaItem(library string) db.MediaItem {
	var totalBytes int64
	var parts []string
	loudOK := m.LoudnessAnalysisVersion != ""
	sonicOK := m.MusicAnalysisVersion != ""
	voiceOK := false
	deepOK := false
	prevOK := false
	hasStreams := false
	hasAudio := false
	hasVideo := false

	for _, med := range m.Media {
		if isFlexTruthy(med.HasVoiceActivity) {
			voiceOK = true
		}
		for _, p := range med.Part {
			totalBytes += p.Size
			if p.Key != "" {
				parts = append(parts, p.Key)
			} else if p.ID > 0 {
				parts = append(parts, strconv.Itoa(p.ID))
			}
			if p.Indexes == "sd" || isFlexTruthy(p.HasThumb) {
				prevOK = true
			}
			if p.DeepAnalysisVersion != "" || p.RequiredBandwidths != "" {
				deepOK = true
			}
			for _, st := range p.Stream {
				hasStreams = true
				switch st.StreamType {
				case 1:
					hasVideo = true
					if st.RequiredBandwidths != "" {
						deepOK = true
					}
				case 2:
					hasAudio = true
					if st.Loudness != "" || st.Gain != "" || st.GainOffset != "" || st.Peak != "" || st.LRA != "" || st.LoudnessAnalysisVersion != "" {
						loudOK = true
					}
					if isFlexTruthy(st.HasVoiceActivity) {
						voiceOK = true
					}
					if st.MusicAnalysis != "" {
						sonicOK = true
					}
				}
			}
		}
	}
	if hasStreams && !hasAudio {
		loudOK = true
		voiceOK = true
	}
	if hasStreams && !hasVideo {
		deepOK = true
	}

	introOK, creditsOK, adsOK := false, false, false
	for _, mk := range m.Marker {
		switch mk.Type {
		case "intro":
			introOK = true
		case "credits":
			creditsOK = true
		case "commercial", "ad":
			adsOK = true
		}
	}
	chapOK := m.ChapterSource != "" || len(m.Chapter) > 0
	var missing []string
	if !loudOK {
		missing = append(missing, "loudness")
	}
	if m.Type == "movie" || m.Type == "episode" {
		if !voiceOK {
			missing = append(missing, "voice")
		}
		if !chapOK {
			missing = append(missing, "chapters")
		}
		if m.Type == "episode" && !introOK {
			missing = append(missing, "intro")
		}
		if !creditsOK {
			missing = append(missing, "credits")
		}
		if !adsOK {
			missing = append(missing, "ads")
		}
		if !prevOK {
			missing = append(missing, "previews")
		}
		if !deepOK {
			missing = append(missing, "deep_analysis")
		}
	} else if m.Type == "track" {
		if !sonicOK {
			missing = append(missing, "sonic")
		}
	}
	status := "complete"
	if len(missing) > 0 {
		status = "pending"
	}
	collection := ""
	if len(m.Collection) > 0 {
		collection = strings.TrimSpace(m.Collection[0].Tag)
	}
	isDVR := m.ChannelIdentifier != "" || strings.TrimSpace(m.ChannelTitle) != "" || m.ChannelID != "" || isFlexTruthy(m.Live)
	return db.MediaItem{
		Key:              library + ":" + string(m.RatingKey),
		RatingKey:        string(m.RatingKey),
		Library:          library,
		Kind:             m.Type,
		Title:            m.Title,
		GrandparentTitle: m.GrandparentTitle,
		ParentTitle:      m.ParentTitle,
		Collection:       collection,
		IsDVR:            isDVR,
		ParentIndex:      m.ParentIndex,
		ItemIndex:        m.Index,
		Bytes:            totalBytes,
		Parts:            parts,
		Missing:          missing,
		Status:           status,
		AddedAt:          m.AddedAt,
	}
}
