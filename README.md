# Sweeparr

**Sweeparr** is a self-hosted, security-hardened media analysis orchestrator and webhook-driven library scanner for Plex Media Server.

It inspects your Plex libraries for missing media capabilities — **loudness, voice activity, intro & credits markers, chapter thumbnails, video preview thumbnails, ad markers, deep media analysis, and music sonic analysis** — and schedules them safely within a rolling disk transfer budget, playback pauses, and quiet hours. When Sonarr, Radarr, or Lidarr imports new media, Sweeparr coalesces full seasons or albums, triggers a targeted Plex directory refresh (replacing tools like Autoscan or Autopulse), waits for pickup, and automatically queues only the missing capability scans.

---

## Key Features

- **Native `*arr` Auto-Scan**: Built-in webhooks for **Sonarr, Radarr, Lidarr**, and custom scripts (so you don't need separate tools like Autoscan or Autopulse) with per-folder season/album coalescing, targeted Plex directory refresh (`GET /library/sections/{id}/refresh?path=...`), and automatic capability scanning once picked up.
- **Native Plex Webhook**: Instant `library.new` ingestion directly from Plex Webhooks.
- **Targeted Capability Control**: Only invokes the exact Plex analysis endpoints missing on each item (`triggersForItem`), respecting both per-library and server-wide Plex preferences.
- **Drive & Playback Protection**: Rolling GiB transfer budget, active stream & transcode pausing, quiet hours, currently-playing item holds, and recently-modified file holds with path mapping verification.
- **Multi-Server & RBAC**: Manage multiple Plex servers with role-based access (`admin`, `operator`, `viewer`), per-user server scoping, and server/library-scoped API keys with expiration.
- **Security Hardened**:
  - WebAuthn **Passkeys**, multi-authenticator **TOTP MFA**, and **OIDC / SSO** with group-to-role mapping and account linking.
  - **AES-256-GCM** encryption at rest for tokens and secrets, `SameSite=Strict` + IP-bound sessions, progressive login rate limiting, non-root / read-only container support, and strict CSP (`default-src 'self'`).
  - **Zero external resources**: All UI code, icons, styles, and translations are bundled into the Go binary (no CDNs, fonts, or telemetry). Update checks are **disabled by default** — outbound traffic is strictly limited to your configured media server(s) (and OIDC provider if enabled), while inbound traffic is only the Web UI and webhooks (`*arr`, Plex, scripts).
- **Modern UI & Exports**: Light and dark themes with selectable accent colors, responsive desktop/tablet/mobile layouts, drag-and-drop queue ordering, CSV/JSON exports, interactive OpenAPI docs, and 5 languages (`en`, `de`, `es`, `fr`, `it`).

---

## Quick Setup (Hardened Docker Compose)

1. **Create directories and secrets** (never store secrets in plaintext YAML or env vars):

   ```bash
   mkdir -p sweeparr/state sweeparr/secrets
   chmod 700 sweeparr/secrets

   # 32-byte hex AES-256-GCM master key
   openssl rand -hex 32 > sweeparr/secrets/sweeparr_master_key.txt

   # Initial admin password (>= 10 characters)
   printf '%s' 'ChangeMeToAStrongPassword!' > sweeparr/secrets/sweeparr_admin_password.txt

   # Your Plex X-Plex-Token
   printf '%s' 'YOUR_PLEX_TOKEN' > sweeparr/secrets/plex_token.txt

   chmod 600 sweeparr/secrets/*.txt
   ```

2. **Copy and customize the sample files**:
   - [`docker-compose.example.yml`](docker-compose.example.yml) → `sweeparr/docker-compose.yml`
   - [`config.example.yaml`](config.example.yaml) → `sweeparr/config.yaml` (every field is documented inline)

3. **Validate configuration and start**:

   ```bash
   cd sweeparr
   docker compose run --rm sweeparr -validate-config
   docker compose up -d
   ```

4. **Put Sweeparr behind an HTTPS reverse proxy** (Traefik, Caddy, Nginx) matching `server.public_url` (or `SWEEPARR_PUBLIC_URL`) — Secure cookies and WebAuthn Passkeys (`rp_id` & `rp_origins`) are configured automatically from it.

---

## Recommended Plex Server Settings

To let Sweeparr orchestrate analysis cleanly without Plex's background jobs hammering your drives at the same time:

### 1. Plex Library Settings (*Manage → Libraries → Edit Library → Advanced*)
Enable the capabilities you actually want Plex to generate, and set their triggers to **Scheduled Tasks** (or **Never** where Sweeparr triggers them on demand) rather than *"as a scheduled task and when media is added"* — Sweeparr handles the on-import trigger programmatically:
- **Movies / TV Shows**: Enable *Generate video preview thumbnails*, *Generate chapter thumbnails*, *Generate intro video markers* (TV), *Generate credits video markers*, *Generate ad video markers* (optional), *Analyze audio tracks for loudness*, and *Voice activity detection* as desired.
- **Music**: Enable *Loudness analysis* and *Sonic analysis* as desired.
- Keep **Respect Plex Library Settings** enabled in Sweeparr so disabled Plex capabilities are automatically skipped.

### 2. Plex Scheduled Tasks (*Settings → Scheduled Tasks*)
- Keep **Backup database**, **Optimize database**, and **Remove old bundles/cache** enabled.
- Disable or narrow heavy media analysis tasks (such as *Generate intro video markers*, *Generate credits markers*, *Perform extensive media analysis*, *Analyze audio tracks for loudness/sonic*) if you want **Sweeparr** to be the sole controller of disk I/O via its rolling transfer budget and playback pauses.

### 3. Plex Library Auto-Scan (*Settings → Library*)
- When using Sweeparr's `*arr` Auto-Scan webhooks (e.g. instead of Autoscan/Autopulse), you can disable *"Scan my library automatically"* and *"Run a partial scan when changes are detected"* in Plex to avoid redundant inotify/FUSE scans on network mounts (NFS/SMB/MergerFS).

---

## Webhook Configuration

1. In Sweeparr, go to **Access → API Keys** and create a key with the **`webhook`** scope (optionally restricted to a specific server and/or libraries).
2. Copy the ready-to-use URL from **API Docs** (automatically prefilled with your active server ID):

### Sonarr / Radarr / Lidarr (Native `*arr` Auto-Scan)
In **Settings → Connect → Add → Webhook**:
- **URL**: `https://sweeparr.example.com/api/v1/servers/<serverID>/webhooks/arr?api_key=swp_...`
- **Method**: `POST`
- **Notification Triggers**: Enable **On Import**, **On Upgrade**, and **On Rename**.
- Click **Test** — Sweeparr returns `200 OK` immediately.
- If your `*arr` container mount paths differ from Plex's library root paths (e.g. `/data/tv` vs `/media/tv`), add the mapping under **Settings → Webhooks & File Protection → `*arr` → Plex Path Mappings**.

### Native Plex Webhook (Optional)
In **Plex Settings → Webhooks → Add Webhook**:
- **URL**: `https://sweeparr.example.com/api/v1/servers/<serverID>/webhooks/plex?api_key=swp_...`
- Whenever Plex emits `library.new`, Sweeparr inspects that exact item and queues it for capability analysis immediately.

---

## Security & License

- **Reporting vulnerabilities**: Please report security issues privately via [GitHub Security Advisories](https://github.com/fabsau/sweeparr/security/advisories/new) instead of opening a public issue.
- **License**: MIT — see [`LICENSE`](LICENSE).
