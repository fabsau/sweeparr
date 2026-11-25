# Plex Media Server API Reference (Sweeparr Engineering Notes)

Empirically verified against **Plex Media Server `1.43.5.11079`** (`apiVersion: 1.2.3`).
Use this document as the canonical reference when extending Sweeparr's Plex adapter (`internal/media/plex/`), scanner engine (`internal/engine/`), or webhooks.

---

## 1. Authentication & Transport

- **Header**: `X-Plex-Token: <token>` (or query `?X-Plex-Token=...`). Never log or expose tokens.
- **Accept Header**: Always pass `Accept: application/json`; otherwise Plex defaults to XML.
- **TLS**: Self-signed / `plex.direct` certs are common on LAN; `ssl_verify` is configurable per server.

---

## 2. Server Identity, Library Fetching & Activated Capabilities

### `GET /identity` (and `GET /`)
Returns `MediaContainer.{friendlyName, claimed, machineIdentifier, version, apiVersion}`. Used for liveness, display name, and Plex version telemetry.

### `GET /library/sections`
Returns all library sections under `MediaContainer.Directory[]`:
- `key`: Section ID (string, e.g. `"1"`, `"10"`).
- `title`: Library display name (e.g. `"Movies"`, `"Series"`, `"Music"`).
- `type`: `"movie"` | `"show"` | `"artist"` (Sweeparr ignores `"photo"`).
- `Location[]`: Array of `{ "id": ..., "path": "/0/series" }`. **Used by Sweeparr's `*arr` Auto-Scan coalescer** to map incoming Sonarr/Radarr/Lidarr paths to the owning Plex section via longest-prefix match.

### Resolving Activated Capabilities (`LibraryCapModes`)
To avoid queuing work that Plex itself has disabled, Sweeparr queries **two** preference endpoints during library sync and persists the resolved per-library capability state (`library_cap_modes` & `library_disabled_caps`):

1. **Per-library preferences — `GET /library/sections/{sectionKey}/prefs`** (`MediaContainer.Setting[]` with `id` + `value`):

| Plex Library Setting ID | Sweeparr Capability | Mode Resolution |
| --- | --- | --- |
| `enableLoudnessAnalysis` | `loudness` | `0` / `false` / `never` → `disabled`; else `all` |
| `enableVoiceActivityGeneration` / `voiceActivityBehavior` | `voice` | `0` / `false` / `never` → `disabled`; else `all` |
| `enableChapterThumbnails` / `chapterThumbnailBehavior` | `chapters` | `0` / `false` / `never` → `disabled`; else `all` |
| `enableIntroMarkerGeneration` | `intro` | `0` / `false` / `never` → `disabled`; else `all` |
| `enableCreditsMarkerGeneration` | `credits` | `0` / `false` / `never` → `disabled`; else `all` |
| `enableAdMarkerGeneration` / `enableAdDetection` / `adDetectionBehavior` | `ads` | `0`/`false`/`never` or missing → `disabled`; `1`/`recorded`/`dvr` → `recorded_only`; else `all` |
| `enableBIFGeneration` / `videoPreviewBehavior` | `previews` | `0` / `false` / `never` → `disabled`; else `all` |
| `enableDeepMediaAnalysis` | `deep_analysis` | `0` / `false` / `never` → `disabled`; else `all` |
| `musicAnalysis` | `sonic` | `0` / `false` / `never` → `disabled`; else `all` |

2. **Server-wide preferences — `GET /:/prefs`** (`MediaContainer.Setting[]`):
   Overrides any capability to `disabled` if the global Plex Scheduled Tasks / Library preference is turned off (`0`, `false`, or `never`):
   - `LoudnessAnalysisBehavior` → `loudness`
   - `GenerateVADBehavior` → `voice`
   - `GenerateChapterThumbBehavior` → `chapters`
   - `GenerateIntroMarkerBehavior` → `intro`
   - `GenerateCreditsMarkerBehavior` → `credits`
   - `GenerateAdMarkerBehavior` → `ads`
   - `GenerateBIFBehavior` → `previews`
   - `ButlerTaskDeepMediaAnalysis` → `deep_analysis`
   - `MusicAnalysisBehavior` → `sonic`

3. **How Sweeparr computes effective capabilities per item**:
   - Start with the user's configured `library_capabilities[library]` (or defaults for `movie` / `show` / `artist`).
   - If `respect_plex_library_settings` is enabled (default `true`), strip every capability whose resolved Plex mode is `disabled`, and only require `ads` on DVR items (`item.IsDVR == true`) when its mode is `recorded_only`.
   - When scanning an item, `triggersForItem(item, cfg)` computes the intersection of `item.Missing` with the library's enabled capabilities so Sweeparr **only** invokes the exact Plex `PUT` endpoint(s) needed for that item (never firing `/analyze?deep=1`, `/voiceActivity`, or `/analyzeLoudness` when already present).

---

## 3. Targeted Directory Refresh (`*arr` Auto-Scan)

### `GET /library/sections/{sectionKey}/refresh?path={url_encoded_dir}`
Triggers a fast, targeted Plex library scan of **only** the specified folder (e.g. `/0/series/Show/Season 01` or `/0/music/Artist/Album`), typically completing in 1–3 seconds without scanning the rest of the library.
- Omit `?path=` to refresh the entire section.
- **Best practice for seasons & albums**: Coalesce all episode/track webhooks in the same folder with a sliding debounce window (default `30s`) before calling `/refresh?path=...` so Plex sees all episodes of a season (required for intro fingerprinting) or all tracks of an album at once.

---

## 4. Inspecting Items & Verifying Capabilities

### `GET /library/sections/{sectionKey}/all?type={type}&X-Plex-Container-Start={start}&X-Plex-Container-Size={size}`
- `type=1`: Movies
- `type=4`: Episodes
- `type=10`: Tracks
- **Important quirk**: Paginated section listings often omit `Marker[]` and `Chapter[]` arrays. Batch-fetch full metadata when needed via:
  `GET /library/metadata/{rk1},{rk2},...?includeChapters=1&includeMarkers=1&includeBandwidths=1`

### `GET /library/metadata/{ratingKey}?includeChapters=1&includeMarkers=1&includeBandwidths=1`
How each Sweeparr capability is verified in `MediaContainer.Metadata[0]`:

| Capability | Verification Location in Plex JSON |
| --- | --- |
| `loudness` | Any audio `Stream` (`streamType == 2`) has `loudness` / `albumPeak` / `peak` populated |
| `voice` | Any audio `Stream` (`streamType == 2`) has `hasVoiceActivity == "1"` (or `true`) |
| `chapters` | `Chapter[]` entries contain `thumb` paths (or `hasChapterTextStream` / chapter thumbs) |
| `previews` | `Media[].Part[].hasThumbnail == "1"` (BIF index files) |
| `intro` | `Marker[]` contains an entry with `type == "intro"` (episodes only; requires >= 2 episodes in season) |
| `credits` | `Marker[]` contains an entry with `type == "credits"` |
| `ads` | `Marker[]` contains an entry with `type == "commercial"` |
| `deep_analysis` | `Media[].Part[].Stream[]` has `requiredBandwidths` populated (from `includeBandwidths=1`) |
| `sonic` | Track metadata has `musicAnalysisVersion` > 0 (or sonic distance data present) |

---

## 5. Triggering Capability Analysis (Live-Verified Behavior)

All analysis triggers use HTTP **`PUT`** on `/library/metadata/{ratingKey}/...`:

| Endpoint | Observed HTTP Status (Plex 1.43.5) | Sync vs Async | Activities Spawned |
| --- | --- | --- | --- |
| `PUT /library/metadata/{id}/analyze?deep=1` | `200 OK` (~3–4s) | Partially sync, spawns async tasks | `media.generate.chapter.thumbs`, `media.generate.intros` (when applicable) |
| `PUT /library/metadata/{id}/voiceActivity?force=1&manual=1` | `202 Accepted` (~45ms) | Async | `media.generate.voice.activity` |
| `PUT /library/metadata/{id}/analyzeLoudness?force=1&manual=1` | `202 Accepted` (~45ms) | Async | `media.generate.loudness` |
| `PUT /library/metadata/{id}/chapterThumbs?force=1&manual=1` | `200 OK` (~50ms) | Async | `media.generate.chapter.thumbs` |
| `PUT /library/metadata/{id}/intro?force=1` | `200 OK` (~50ms) | Async (episodes) | `media.generate.intros` (season-level!) |
| `PUT /library/metadata/{id}/addetect` | `400 Bad Request` on non-DVR items | Async (DVR only) | Ad marker detection |

**Critical implementation note**: Because `/voiceActivity` and `/analyzeLoudness` return `202 Accepted` in <50ms, `/activities` may still be empty for 300–1500ms. Always wait with a short startup grace period (~5s) and require **2 consecutive empty `/activities` polls** before running post-scan verification.

---

## 6. Activities, Subtitles & Cancellation (Live-Verified Quirks)

### `GET /activities`
Example live response from Plex `1.43.5`:

```json
{
  "MediaContainer": {
    "size": 3,
    "Activity": [
      {
        "uuid": "5862f42a-a415-4e12-846f-871bbb80bc63",
        "type": "media.generate.chapter.thumbs",
        "cancellable": false,
        "userID": 1,
        "title": "Generating video chapter thumbnails",
        "subtitle": "2 Broke Girls S01 E01",
        "progress": -1
      },
      {
        "uuid": "72ad3adf-6353-4e66-a6b3-42247e8e4ca9",
        "type": "media.generate.intros",
        "cancellable": false,
        "userID": 1,
        "title": "Detecting intros",
        "subtitle": "2 Broke Girls S01",
        "progress": 87
      },
      {
        "uuid": "939a13be-fafb-4611-b1a7-760c8905113f",
        "type": "media.generate.loudness",
        "cancellable": false,
        "userID": 1,
        "title": "Generating loudness data",
        "subtitle": "2 Broke Girls S01 E01",
        "progress": 0
      }
    ]
  }
}
```

### Critical Quirks Observed Live
1. **Cancellation (`DELETE /activities/{uuid}`)**:
   - Plex sets **`"cancellable": false`** on ALL `media.generate.*` analysis tasks (`chapter.thumbs`, `loudness`, `voice.activity`, `intros`).
   - Calling `DELETE /activities/{uuid}` on a `cancellable: false` activity strictly returns **`400 Bad Request`** and the task continues until Plex finishes it.
   - Only `cancellable: true` activities (e.g. `library.refresh.items`, butler jobs) can be cancelled via `DELETE /activities/{uuid}`.
   - Therefore, Sweeparr's **Interrupt** action cancels all `cancellable: true` Plex activities, aborts Sweeparr's own wait cleanly (restoring the item to `pending` without counting a failure), pauses the scanner, and reports any non-cancellable Plex tasks still finishing in the background.
2. **Subtitle Formats (for Active Scan Pickup)**:
   - **Episodes**: `"<GrandparentTitle> S<ParentIndex:02d> E<ItemIndex:02d>"` (e.g. `"2 Broke Girls S01 E01"`), **NOT** the episode title (`"Pilot"`).
   - **Season Intros**: `"<GrandparentTitle> S<ParentIndex:02d>"` (e.g. `"2 Broke Girls S01"`).
   - **Movies**: `"<Title>"` (e.g. `"3 Idiots"`).
   - Sweeparr parses all three patterns in `DB.FindItemByActivitySubtitle` so externally-started or post-restart active scans are picked up accurately in Overview & Queue, and `scanLoop` waits for active `media.generate.*` tasks before starting another item.
3. **Indeterminate Progress**:
   - Plex returns **`"progress": -1`** when an activity has no percentage estimate. UI components must treat `progress < 0` as an indeterminate "Active / Running" state, never `-1%`.

---

## 7. Sessions & Webhooks

- **`GET /status/sessions`**: Returns active playback sessions (`User.title`, `Player`, `TranscodeSession`, `Session.bandwidth`, `viewOffset`, `duration`). Used for `pause_on_active_streams`, `pause_on_transcodes_only`, and `hold_playing_items`.
- **Native Plex Webhooks**: Sent by Plex as `multipart/form-data` with a JSON `payload` field (`event`, `Server.title`, `Metadata.ratingKey`, `Metadata.type`, `Metadata.librarySectionTitle`). Sweeparr handles `library.new` by immediately inspecting `ratingKey` and queuing analysis, and acknowledges all other events (`media.play`, `media.scrobble`, etc.) with `200 OK` so Plex keeps the webhook active.
