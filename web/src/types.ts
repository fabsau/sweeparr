export interface User {
  id: string;
  username: string;
  display_name: string;
  email: string;
  role: "admin" | "operator" | "viewer";
  allowed_servers?: string[];
  totp_enabled: boolean;
  password_login_disabled?: boolean;
  passkey_require_mfa?: boolean;
  can_create_api_keys?: boolean;
  require_mfa?: boolean;
  require_passkey?: boolean;
  must_change_password?: boolean;
  mfa_count?: number;
  passkey_count?: number;
  session_count?: number;
  oidc_linked?: boolean;
  created_at: number;
}

export interface AuthStatus {
  needs_setup: boolean;
  authenticated: boolean;
  mfa_pending: boolean;
  oidc_enabled: boolean;
  user: User | null;
}

export interface ServerSummary {
  id: string;
  name: string;
  kind: string;
  url: string;
  ssl_verify: boolean;
  env_managed: boolean;
  enabled: boolean;
  connected: boolean;
  server_name: string;
  version: string;
}

export interface SessionInfo {
  user: string;
  title: string;
  raw_title: string;
  grandparent_title: string;
  parent_title: string;
  collection?: string;
  parent_index: number;
  index: number;
  year: number;
  kind: string;
  player: string;
  platform: string;
  product: string;
  address: string;
  local: boolean;
  location: string;
  bandwidth_kbps: number;
  state: string;
  transcoding: boolean;
  video_decision: string;
  audio_decision: string;
  video_resolution: string;
  video_codec: string;
  audio_codec: string;
  audio_channels: number;
  bitrate_kbps: number;
  duration_ms: number;
  view_offset_ms: number;
  progress_pct: number;
  thumb_path: string;
}

export interface CapStat {
  have: number;
  missing: number;
  total: number;
  pct: number;
}

export interface LibrarySummary {
  title: string;
  kind?: string;
  total: number;
  complete: number;
  pending: number;
  pct: number;
  disabled_caps: string[];
  cap_modes?: Record<string, string>;
  cap_stats: Record<string, CapStat>;
}

export interface MediaItem {
  server_id: string;
  key: string;
  rating_key: string;
  library: string;
  kind: string;
  title: string;
  grandparent_title: string;
  parent_title: string;
  collection?: string;
  parent_index: number;
  item_index: number;
  bytes: number;
  missing: string[];
  status: string;
  attempts: number;
  priority_pos: number;
  queued: boolean;
  skipped: boolean;
  last_error: string;
  next_retry_at?: number;
  added_at: number;
  updated_at?: number;
  queued_by?: string;
  queued_reason?: string;
  blocklisted_by?: string;
  blocklisted_at?: number;
}

export interface PendingArrTrigger {
  id: string;
  source: string;
  actor: string;
  library: string;
  section_key: string;
  dir: string;
  file_count: number;
  titles?: string[];
  state: "debouncing" | "refreshing_plex" | "syncing" | "queued";
  first_seen_at: number;
  fires_at: number;
}

export interface PathMapping {
  remote: string;
  local: string;
}

export interface ScannerConfig {
  enabled_libraries: string[];
  library_priority: string[];
  library_triggers: Record<string, string[]>;
  library_capabilities: Record<string, string[]>;
  triggers: string[];
  order_strategy: string;
  number_locale: string;
  datetime_locale: string;
  time_format?: string;
  theme: string;
  accent_color?: string;
  default_page_size: number;
  drive_budget_gib: number;
  drive_window_hours: number;
  max_attempts: number;
  inter_item_delay_seconds: number;
  max_item_scan_minutes: number;
  circuit_breaker_threshold: number;
  catalog_refresh_minutes: number;
  full_sync_interval_hours: number;
  index_workers: number;
  bulk_page_size: number;
  max_active_streams: number;
  retry_initial_seconds: number;
  retry_max_seconds: number;
  pause_on_active_streams: boolean;
  pause_on_transcodes_only: boolean;
  respect_plex_library_settings: boolean;
  dry_run: boolean;
  quiet_hours_enabled: boolean;
  quiet_hours_start: number;
  quiet_hours_end: number;
  webhook_prioritize_top: boolean;
  webhook_hold_seconds: number;
  arr_autoscan_enabled?: boolean;
  webhook_debounce_seconds?: number;
  webhook_max_wait_seconds?: number;
  webhook_pickup_wait_seconds?: number;
  arr_path_mappings?: PathMapping[];
  hold_playing_items: boolean;
  hold_modified_files: boolean;
  path_mappings: PathMapping[];
  log_retention_days: number;
  history_retention_days: number;
  max_log_rows: number;
  paused: boolean;
  paused_until: number;
}

export interface ServerStatus {
  server: ServerSummary;
  version: number;
  catalog_version: number;
  daemon: {
    state: string;
    detail: string;
    paused: boolean;
    paused_until: number;
    indexing: boolean;
    index_progress: { done: number; total: number; library: string };
    priority_queue_size: number;
    next_up: MediaItem | null;
    current_item: MediaItem | null;
    current_item_started_at?: number;
    scan_summary?: { completed: number; retries: number; failed: number };
    pending_triggers?: PendingArrTrigger[];
  };
  plex: {
    connected: boolean;
    server_name: string;
    version: string;
    error: string;
    active_streams: number;
    active_transcodes: number;
    sessions: SessionInfo[];
    activities: number;
    activity_list: { uuid: string; title: string; subtitle: string; type: string; progress: number; cancellable: boolean }[];
  };
  drive_budget: {
    used_gib: number;
    budget_gib: number;
    percent: number;
    window_hours: number;
    active_parts: number;
    next_release_seconds: number;
  };
  totals: {
    total: number;
    complete: number;
    pending: number;
    retry: number;
    exhausted: number;
    blocked: number;
    skipped: number;
    pct: number;
    cap_stats: Record<string, CapStat>;
  };
  libraries: LibrarySummary[];
  config: ScannerConfig;
  config_managed?: boolean;
}

export interface EventRow {
  id: number;
  server_id: string;
  ts: number;
  level: string;
  category: string;
  username?: string;
  message: string;
}

export interface APIKeyRecord {
  id: string;
  user_id?: string;
  username?: string;
  name: string;
  prefix: string;
  scopes: string[];
  allowed_servers?: string[];
  allowed_libraries?: string[];
  last_used_at: number;
  expires_at?: number;
  created_at: number;
}

export interface GroupRow {
  library: string;
  kind: string;
  group_title: string;
  sub_title: string;
  total: number;
  complete: number;
  remaining: number;
  issues: number;
  bytes: number;
}

export interface PasskeyRecord {
  id: string;
  label: string;
  created_at: number;
}

export interface UserMFARecord {
  id: string;
  name: string;
  created_at: number;
}

export interface HistoryRow {
  id: number;
  server_id: string;
  item_key: string;
  title: string;
  library: string;
  kind: string;
  status: string;
  detail: string;
  added_caps?: string[];
  missing_caps?: string[];
  bytes?: number;
  queued_by?: string;
  queued_reason?: string;
  duration_ms: number;
  finished_at: number;
}

export interface OIDCSettings {
  enabled: boolean;
  issuer: string;
  client_id: string;
  has_client_secret: boolean;
  redirect_url: string;
  auto_create: boolean;
  groups_claim: string;
  admin_groups: string;
  operator_groups: string;
  viewer_groups: string;
  env_managed?: boolean;
}

export interface SecuritySettings {
  session_ip_binding: boolean;
  update_check_enabled: boolean;
}

export interface PathCheckResult {
  remote_path: string;
  local_path: string;
  exists: boolean;
  readable: boolean;
  is_dir?: boolean;
  uid: number;
  gid: number;
  error?: string;
}

export interface VersionInfo {
  current_version: string;
  commit?: string;
  latest_version: string;
  update_available: boolean;
  update_check_enabled?: boolean;
  release_url: string;
}
