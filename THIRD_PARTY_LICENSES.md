# Third-Party Licenses & Dependency Management

All Sweeparr frontend and backend assets are compiled and embedded directly into the Go binary (`embed.go`). Zero external CDNs, fonts, or runtime scripts are loaded at runtime.

## Version Management
- **Backend (Go `1.26.0`)**: Pinned and checksum-verified via `go.mod` and `go.sum` (`go mod verify`).
- **Frontend (Node / Vite)**: Pinned via `web/package.json` and `web/package-lock.json` (`npm ci --ignore-scripts`).

## Backend Dependencies (`go.mod`)
| Package | Version | License |
| --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | `v3.21.0` | Apache-2.0 |
| `github.com/go-chi/chi/v5` | `v5.5.0` | MIT |
| `github.com/go-webauthn/webauthn` | `v0.18.2` | BSD-3-Clause |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause |
| `github.com/pquerna/otp` | `v1.5.0` | Apache-2.0 |
| `golang.org/x/crypto` | `v0.59.0` | BSD-3-Clause |
| `golang.org/x/oauth2` | `v0.38.0` | BSD-3-Clause |
| `modernc.org/sqlite` | `v1.63.0` | BSD-3-Clause |

## Frontend Dependencies (`web/package.json`)
| Package | Resolved Version | License |
| --- | --- | --- |
| `@simplewebauthn/browser` | `13.3.0` | MIT |
| `i18next` | `27.2.4` | MIT |
| `i18next-browser-languagedetector` | `8.3.1` | MIT |
| `lucide-react` | `0.475.0` | ISC |
| `qrcode.react` | `4.2.0` | ISC |
| `react` | `19.3.1` | MIT |
| `react-dom` | `19.3.1` | MIT |
| `react-i18next` | `18.0.3` | MIT |
| `typescript` (dev) | `5.9.3` | Apache-2.0 |
| `vite` (dev) | `6.4.6` | MIT |
| `@vitejs/plugin-react` (dev) | `4.7.0` | MIT |
