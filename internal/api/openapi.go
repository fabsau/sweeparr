package api

import "github.com/fabsau/sweeparr/internal/version"

// OpenAPISpec returns the OpenAPI 3.0.3 JSON specification for Sweeparr's API.
func OpenAPISpec(baseURL string) map[string]any {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Sweeparr API",
			"version":     version.Clean(version.Version),
			"description": "Multi-server media analysis orchestrator API with per-server and per-library API-key RBAC, native Plex library.new webhook ingestion, and catalog sync / library / item scan endpoints.",
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"ApiKeyAuth": map[string]any{
					"type": "apiKey",
					"in":   "header",
					"name": "X-Api-Key",
				},
				"WebhookQueryKey": map[string]any{
					"type": "apiKey",
					"in":   "query",
					"name": "api_key",
				},
				"BearerAuth": map[string]any{
					"type":   "http",
					"scheme": "bearer",
				},
				"SessionCookie": map[string]any{
					"type": "apiKey",
					"in":   "cookie",
					"name": "sweeparr_session",
				},
			},
		},
		"security": []map[string]any{
			{"ApiKeyAuth": []string{}},
			{"BearerAuth": []string{}},
			{"SessionCookie": []string{}},
		},
		"paths": map[string]any{
			"/healthz": map[string]any{
				"get": map[string]any{
					"summary":     "Health check",
					"description": "Unauthenticated liveness endpoint.",
					"security":    []any{},
					"responses":   map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
			"/api/v1/webhooks/plex": map[string]any{
				"post": map[string]any{
					"summary":     "Native Plex webhook (multi-server auto-match)",
					"description": "Receives Plex native webhooks (multipart/form-data payload field or JSON body) and automatically matches the target server by Plex Server.title (or ?server_id=). On library.new events, immediately inspects the newly imported movie, episode, or track via its ratingKey and queues it for capability analysis. Supports ?api_key=swp_... for Plex's Webhooks UI. Requires scope: webhook.",
					"parameters": []map[string]any{
						{"name": "api_key", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						{"name": "server_id", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Event accepted (item ingested or delta sync queued)"},
						"401": map[string]any{"description": "Missing or invalid API key"},
						"403": map[string]any{"description": "Insufficient scope or server access denied"},
					},
				},
			},
			"/api/v1/servers/{serverID}/webhooks/plex": map[string]any{
				"post": map[string]any{
					"summary":     "Native Plex webhook (server-scoped, recommended)",
					"description": "Explicitly targets a specific configured server by serverID. Receives Plex native webhooks (multipart/form-data payload or JSON) and immediately inspects & queues library.new items. Supports ?api_key=swp_... for Plex's Webhooks UI. Enforces both user and API key allowed_servers and allowed_libraries RBAC. Requires scope: webhook.",
					"parameters": []map[string]any{
						{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "api_key", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Event accepted"},
						"401": map[string]any{"description": "Missing or invalid API key"},
						"403": map[string]any{"description": "Insufficient scope or server access denied"},
					},
				},
			},
			"/api/v1/webhooks/arr": map[string]any{
				"post": map[string]any{
					"summary":     "Native *arr Auto-Scan webhook (Sonarr/Radarr/Lidarr, multi-server)",
					"description": "Receives Sonarr, Radarr, Lidarr, or generic JSON webhooks (e.g. replacing tools like Autoscan or Autopulse), coalesces season/album import bursts per directory with a sliding debounce window, triggers a targeted Plex library directory refresh, waits for Plex pickup, and queues all incomplete items for capability analysis. Supports ?api_key=swp_... and handles Test events cleanly. Requires scope: webhook.",
					"parameters": []map[string]any{
						{"name": "api_key", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
						{"name": "server_id", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Webhook accepted & coalesced"},
						"401": map[string]any{"description": "Missing or invalid API key"},
						"403": map[string]any{"description": "Insufficient scope or server/library access denied"},
					},
				},
			},
			"/api/v1/servers/{serverID}/webhooks/arr": map[string]any{
				"post": map[string]any{
					"summary":     "Native *arr Auto-Scan webhook (server-scoped, recommended)",
					"description": "Explicitly targets a specific configured server by serverID. Receives Sonarr, Radarr, Lidarr, or script webhooks, debounces & coalesces full season/album imports, triggers Plex targeted directory refresh, syncs newly picked-up items, and queues them for capability analysis. Supports ?api_key=swp_... Requires scope: webhook.",
					"parameters": []map[string]any{
						{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "api_key", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Webhook accepted & coalesced"},
						"401": map[string]any{"description": "Missing or invalid API key"},
						"403": map[string]any{"description": "Insufficient scope or server/library access denied"},
					},
				},
			},
			"/api/v1/servers": map[string]any{
				"get": map[string]any{
					"summary":     "List accessible media servers",
					"description": "Returns all configured media servers permitted for the authenticated user/API key and their connection status. Requires scope: read.",
					"responses":   map[string]any{"200": map[string]any{"description": "Server list"}},
				},
			},
			"/api/v1/servers/{serverID}/status": map[string]any{
				"get": map[string]any{
					"summary":     "Get server scanner status",
					"description": "Returns live daemon state, active streams, drive budget, library completeness, and config. Requires scope: read.",
					"parameters":  []map[string]any{{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}},
					"responses":   map[string]any{"200": map[string]any{"description": "Server status"}},
				},
			},
			"/api/v1/servers/{serverID}/libraries": map[string]any{
				"get": map[string]any{
					"summary":     "List libraries on a server",
					"description": "Returns all libraries on the server with item counts, completeness percentage, detected 3-state capability modes (disabled/recorded_only/all), and per-capability stats. Requires scope: read.",
					"parameters":  []map[string]any{{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}},
					"responses":   map[string]any{"200": map[string]any{"description": "Library list"}},
				},
			},
			"/api/v1/servers/{serverID}/sync": map[string]any{
				"post": map[string]any{
					"summary":     "Sync media catalog (delta or full)",
					"description": "Triggers a catalog sync on the media server. Pass ?mode=delta (default, fast incremental sync) or ?mode=full (full re-index of all audio streams, chapters, and markers), or JSON body {\"mode\": \"delta\" | \"full\"}. Requires scope: scan.",
					"parameters": []map[string]any{
						{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "mode", "in": "query", "required": false, "schema": map[string]any{"type": "string", "enum": []string{"delta", "full"}}},
					},
					"responses": map[string]any{"200": map[string]any{"description": "Sync queued"}},
				},
			},
			"/api/v1/servers/{serverID}/libraries/scan": map[string]any{
				"post": map[string]any{
					"summary":     "Trigger scan for all (or selected) libraries",
					"description": "Queues all incomplete items across all enabled libraries (or specific libraries via optional JSON body {\"library\": \"Movies\"} or {\"libraries\": [\"Movies\", \"Music\"]}) and resumes scanning. Requires scope: scan.",
					"parameters":  []map[string]any{{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}},
					"responses":   map[string]any{"200": map[string]any{"description": "Items queued for analysis"}},
				},
			},
			"/api/v1/servers/{serverID}/libraries/{library}/scan": map[string]any{
				"post": map[string]any{
					"summary":     "Trigger scan for a single library",
					"description": "Queues all incomplete items in the named library and resumes scanning. Requires scope: scan.",
					"parameters": []map[string]any{
						{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "library", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{"200": map[string]any{"description": "Library items queued"}},
				},
			},
			"/api/v1/servers/{serverID}/items/{ratingKey}/scan": map[string]any{
				"post": map[string]any{
					"summary":     "Inspect and scan a single media item",
					"description": "Fetches the item directly from the media server by ratingKey, evaluates missing capabilities, upserts it, and immediately queues it for analysis if incomplete. Requires scope: scan.",
					"parameters": []map[string]any{
						{"name": "serverID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "ratingKey", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						{"name": "library", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
					},
					"responses": map[string]any{"200": map[string]any{"description": "Item inspected and queued if incomplete"}},
				},
			},
		},
	}
	if baseURL != "" {
		spec["servers"] = []map[string]any{{"url": baseURL}}
	}
	return spec
}
