# Changelog

All notable changes to **Sweeparr** are documented in this file.

This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) and uses [Conventional Commits](https://www.conventionalcommits.org/) with automated releases via Release Please.

## [0.9.0](https://github.com/fabsau/sweeparr/releases/tag/v0.9.0) (2026-11-25)

### Features

- **Multi-Server & RBAC**: Full multi-server management with per-user server access, per-server and per-library scoped API keys with expiration, consolidated user editing, and session revocation.
- **Authentication & Security**: WebAuthn Passkeys, multi-authenticator TOTP MFA, OIDC/SSO with group-to-role mapping and account linking, IP-bound sessions, CSP hardening, and offline-by-default update checks.
- **Programmatic Scanning & Webhooks**: Native Plex `library.new` webhook ingestion, scheduled delta and full catalog sync, drive transfer budget windows, active playback and recently-modified file holds with path mapping verification, and drag-and-drop queue ordering.
- **Modern UI & Localization**: Light and dark themes with selectable accent colors, responsive desktop/tablet/mobile layouts, CSV/JSON exports across Explorer/History/Blocklist/Logs, and full 5-language support (`en`, `de`, `es`, `fr`, `it`).
