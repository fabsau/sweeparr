import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { FolderSearch, Plus, Save, Trash2 } from "lucide-react";
import { api, serverApi } from "../api";
import { useLocaleFormat } from "../utils/format";
import { sanitizeDecimalInput } from "../utils/sanitize";
import { ACCENT_OPTIONS, type AccentColor, type ThemeMode } from "../utils/theme";
import { LANGUAGES, type Lang } from "../i18n";
import { Checkbox, ConfirmModal, InfoPopover, NumberStepper, SelectMenu } from "../components/Controls";
import { LibrariesAndCapsCard, MediaServersCard } from "../components/ServerSettingsSection";
import type { PathCheckResult, PathMapping, ScannerConfig, SecuritySettings, ServerStatus, ServerSummary } from "../types";

function stripInstantFields(c: ScannerConfig): Omit<ScannerConfig, "theme" | "accent_color"> {
  const { theme: _t, accent_color: _a, ...rest } = c;
  return {
    ...rest,
    path_mappings: (rest.path_mappings || []).map((m) => ({ remote: m.remote, local: m.local })),
    arr_path_mappings: (rest.arr_path_mappings || []).map((m) => ({ remote: m.remote, local: m.local })),
  };
}

export function SettingsView({
  serverId,
  servers,
  status,
  isAdmin,
  canScan,
  themeMode,
  accentColor,
  onThemeModeChange,
  onAccentColorChange,
  onDirtyChange,
  onSelectServer,
  onServersChanged,
  toast,
}: {
  serverId: string;
  servers: ServerSummary[];
  status: ServerStatus;
  isAdmin: boolean;
  canScan: boolean;
  themeMode: ThemeMode;
  accentColor: AccentColor;
  onThemeModeChange: (next: ThemeMode) => void;
  onAccentColorChange: (next: AccentColor) => void;
  onDirtyChange: (dirty: boolean) => void;
  onSelectServer: (id: string) => void;
  onServersChanged: () => Promise<void>;
  onOpenSync: () => void;
  toast: (msg: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const { fmtNum } = useLocaleFormat();
  const lang = ((i18n.resolvedLanguage || i18n.language || "en").slice(0, 2) as Lang) || "en";

  const [savedCfg, setSavedCfg] = useState<ScannerConfig>(status.config);
  const [cfg, setCfg] = useState<ScannerConfig>(status.config);
  const [budgetInput, setBudgetInput] = useState<string>(String(status.config.drive_budget_gib ?? 450));
  const [budgetFocused, setBudgetFocused] = useState(false);
  const [discoveredLibs, setDiscoveredLibs] = useState<typeof status.libraries>([]);

  // Admin security settings (Session IP Binding & Offline-by-default Update Check)
  const [secSaved, setSecSaved] = useState<SecuritySettings>({ session_ip_binding: true, update_check_enabled: false });
  const [secDraft, setSecDraft] = useState<SecuritySettings>({ session_ip_binding: true, update_check_enabled: false });

  // Path access check state
  const [testPath, setTestPath] = useState("");
  const [pathCheckBusy, setPathCheckBusy] = useState(false);
  const [pathCheckRes, setPathCheckRes] = useState<PathCheckResult | null>(null);

  // Cleanup confirmation modal
  const [cleanupConfirmOpen, setCleanupConfirmOpen] = useState(false);
  const [cleanupBusy, setCleanupBusy] = useState(false);

  useEffect(() => {
    setSavedCfg(status.config);
    setCfg(status.config);
    setBudgetInput(String(status.config.drive_budget_gib ?? 450));
    setDiscoveredLibs([]);
    setPathCheckRes(null);
  }, [serverId]);

  useEffect(() => {
    if (!isAdmin) return;
    void api<SecuritySettings>("/api/admin/security-settings")
      .then((res) => {
        setSecSaved(res);
        setSecDraft(res);
      })
      .catch(() => {});
  }, [isAdmin]);

  const parsedBudgetNow = Number(budgetInput.replace(",", "."));
  const effectiveCfg: ScannerConfig = {
    ...cfg,
    drive_budget_gib:
      Number.isFinite(parsedBudgetNow) && parsedBudgetNow >= 1 ? parsedBudgetNow : cfg.drive_budget_gib,
  };

  const cfgDirty = JSON.stringify(stripInstantFields(effectiveCfg)) !== JSON.stringify(stripInstantFields(savedCfg));
  const secDirty =
    isAdmin &&
    (secDraft.session_ip_binding !== secSaved.session_ip_binding ||
      secDraft.update_check_enabled !== secSaved.update_check_enabled);
  const dirty = cfgDirty || secDirty;

  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  const updateCfg = (updater: (prev: ScannerConfig) => ScannerConfig) => {
    setCfg((prev) => updater(prev));
  };

  async function save() {
    const parsedBudget = Number(budgetInput.replace(",", "."));
    const safeBudget = Number.isFinite(parsedBudget) && parsedBudget >= 1 ? parsedBudget : cfg.drive_budget_gib || 450;
    const payload: ScannerConfig = {
      ...cfg,
      drive_budget_gib: safeBudget,
      path_mappings: (cfg.path_mappings || []).filter((m) => m.remote.trim() && m.local.trim()),
      arr_path_mappings: (cfg.arr_path_mappings || []).filter((m) => m.remote.trim() && m.local.trim()),
    };
    if (cfgDirty) {
      await serverApi(serverId, "/config", "PUT", payload);
      setSavedCfg(payload);
      setCfg(payload);
    }
    if (secDirty && isAdmin) {
      await api("/api/admin/security-settings", "PUT", secDraft);
      setSecSaved(secDraft);
      window.dispatchEvent(new CustomEvent("sweeparr:version-refresh"));
    }
    onDirtyChange(false);
    await onServersChanged();
    toast("Settings saved");
  }

  async function runCleanup() {
    setCleanupBusy(true);
    try {
      const r = await serverApi<{ events_deleted: number; history_deleted: number }>(
        serverId,
        "/maintenance/cleanup",
        "POST"
      );
      toast(
        t("settings.cleanup_done", {
          logs: fmtNum(r.events_deleted || 0),
          history: fmtNum(r.history_deleted || 0),
        })
      );
      setCleanupConfirmOpen(false);
    } catch (err) {
      toast((err as Error).message);
    } finally {
      setCleanupBusy(false);
    }
  }

  async function runPathCheck() {
    if (!testPath.trim()) return;
    setPathCheckBusy(true);
    try {
      const r = await serverApi<PathCheckResult>(serverId, "/maintenance/check-path", "POST", {
        path: testPath.trim(),
        path_mappings: cfg.path_mappings || [],
      });
      setPathCheckRes(r);
    } catch (err) {
      toast((err as Error).message);
    } finally {
      setPathCheckBusy(false);
    }
  }

  const formatHourSample = (hour: number) => {
    try {
      const d = new Date(2026, 0, 1, hour, 0, 0);
      return d.toLocaleTimeString(cfg.datetime_locale || "de-DE", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: (cfg.time_format || "24h") === "12h",
      });
    } catch {
      return `${String(hour).padStart(2, "0")}:00`;
    }
  };

  const iacLocked = Boolean(status.config_managed);
  const canEditScanner = canScan && !iacLocked;

  const labelWithTip = (label: string, tip: string) => (
    <span className="field-label-row">
      <span>{label}</span>
      <InfoPopover title={label} text={tip} />
    </span>
  );

  const pathMappings: PathMapping[] = cfg.path_mappings || [];

  return (
    <div className="page-view unified-page">
      {iacLocked && (
        <div
          className="card"
          role="status"
          style={{
            borderColor: "var(--accent)",
            background: "rgba(245, 166, 35, 0.08)",
            display: "flex",
            flexDirection: "column",
            gap: 4,
            padding: "12px 16px",
          }}
        >
          <strong className="small">{t("settings.iac_banner_title")}</strong>
          <span className="small muted">{t("settings.iac_banner_desc")}</span>
        </div>
      )}

      {/* 1. Localization & Display */}
      <div className="card form-section">
        <h3>{t("settings.localization_display")}</h3>
        <div className="field-grid">
          <div className="field">
            {labelWithTip(t("sidebar.language"), t("settings.language.tip"))}
            <SelectMenu
              ariaLabel={t("sidebar.language")}
              value={lang}
              options={LANGUAGES.map((l) => ({ value: l.code, label: l.native }))}
              onChange={(v) => void i18n.changeLanguage(v as Lang)}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.datetime_locale"), t("settings.datetime_locale.tip"))}
            <SelectMenu
              ariaLabel={t("settings.datetime_locale")}
              value={cfg.datetime_locale || "de-DE"}
              disabled={!canScan}
              options={[
                { value: "sv-SE", label: "YYYY-MM-DD" },
                { value: "ja-JP", label: "YYYY/MM/DD" },
                { value: "ko-KR", label: "YYYY. MM. DD." },
                { value: "de-DE", label: "DD.MM.YYYY" },
                { value: "en-GB", label: "DD/MM/YYYY" },
                { value: "nl-NL", label: "DD-MM-YYYY" },
                { value: "en-US", label: "MM/DD/YYYY" },
                { value: "ar-SA", label: "٢٠٢٦/١١/٢٥" },
              ]}
              onChange={(v) => updateCfg((p) => ({ ...p, datetime_locale: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.time_format"), t("settings.time_format.tip"))}
            <SelectMenu
              ariaLabel={t("settings.time_format")}
              value={cfg.time_format || "24h"}
              disabled={!canScan}
              options={[
                { value: "24h", label: t("settings.time_24h") },
                { value: "12h", label: t("settings.time_12h") },
              ]}
              onChange={(v) => updateCfg((p) => ({ ...p, time_format: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.number_locale"), t("settings.number_locale.tip"))}
            <SelectMenu
              ariaLabel={t("settings.number_locale")}
              value={cfg.number_locale}
              disabled={!canScan}
              options={[
                { value: "en-US", label: "1,234.56" },
                { value: "en-IN", label: "1,23,456.78" },
                { value: "de-DE", label: "1.234,56" },
                { value: "fr-FR", label: "1 234,56" },
                { value: "de-CH", label: "1'234.56" },
                { value: "ar-EG", label: "١٬٢٣٤٫٥٦" },
              ]}
              onChange={(v) => updateCfg((p) => ({ ...p, number_locale: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.theme"), t("settings.theme.tip"))}
            <SelectMenu
              ariaLabel={t("settings.theme")}
              value={themeMode}
              options={[
                { value: "system", label: t("theme.system") },
                { value: "light", label: t("theme.light") },
                { value: "dark", label: t("theme.dark") },
              ]}
              onChange={(v) => onThemeModeChange(v as ThemeMode)}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.default_page_size"), t("settings.default_page_size.tip"))}
            <SelectMenu
              ariaLabel={t("settings.default_page_size")}
              value={String(cfg.default_page_size)}
              disabled={!canScan}
              options={[
                { value: "50", label: t("pagination.rows_count", { count: fmtNum(50) as unknown as number }) },
                { value: "100", label: t("pagination.rows_count", { count: fmtNum(100) as unknown as number }) },
                { value: "250", label: t("pagination.rows_count", { count: fmtNum(250) as unknown as number }) },
              ]}
              onChange={(v) => updateCfg((p) => ({ ...p, default_page_size: Number(v) }))}
            />
          </div>
        </div>
        <div className="field" style={{ marginTop: 4 }}>
          {labelWithTip(t("settings.accent_color"), t("settings.accent_color.tip"))}
          <div className="accent-swatch-row" role="radiogroup" aria-label={t("settings.accent_color")}>
            {ACCENT_OPTIONS.map((opt) => (
              <button
                key={opt.id}
                type="button"
                role="radio"
                aria-checked={accentColor === opt.id}
                className={`accent-swatch-btn ${accentColor === opt.id ? "active" : ""}`}
                onClick={() => onAccentColorChange(opt.id)}
              >
                <span className="accent-swatch-dot" style={{ backgroundColor: opt.swatch }} aria-hidden="true" />
                <span>{t(opt.labelKey)}</span>
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* 2. Media Servers */}
      <MediaServersCard
        serverId={serverId}
        servers={servers}
        canScan={canScan || isAdmin}
        iacLocked={iacLocked}
        onSelectServer={onSelectServer}
        onChangeCfg={updateCfg}
        onLibrariesDiscovered={(libs) => setDiscoveredLibs(libs)}
      />

      {/* 3. Libraries & Capabilities */}
      <LibrariesAndCapsCard
        cfg={cfg}
        libraries={discoveredLibs.length > 0 ? discoveredLibs : status.libraries}
        canScan={canEditScanner}
        onChangeCfg={updateCfg}
      />

      {/* 4. Behavior & Debug */}
      <div className="card form-section">
        <h3>{t("settings.behavior_debug")}</h3>
        <div className="field-grid">
          <div className="field">
            {labelWithTip(t("settings.order_strategy"), t("settings.order_strategy.tip"))}
            <SelectMenu
              ariaLabel={t("settings.order_strategy")}
              value={cfg.order_strategy}
              disabled={!canEditScanner}
              options={[
                { value: "season_first", label: t("sort.season_first_rec") },
                { value: "alphabetical", label: t("sort.alphabetical") },
                { value: "alphabetical_desc", label: t("sort.alphabetical_desc") },
                { value: "newest_first", label: t("sort.newest_first") },
                { value: "oldest_first", label: t("sort.oldest_first") },
                { value: "smallest_first", label: t("sort.smallest_first") },
                { value: "largest_first", label: t("sort.largest_first") },
                { value: "fewest_attempts_first", label: t("sort.fewest_attempts_first") },
              ]}
              onChange={(v) => updateCfg((p) => ({ ...p, order_strategy: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.drive_budget_gib"), t("settings.drive_budget_gib.tip"))}
            <input
              type="text"
              aria-label={t("settings.drive_budget_gib")}
              inputMode="decimal"
              maxLength={12}
              disabled={!canEditScanner}
              value={budgetFocused ? budgetInput : fmtNum(cfg.drive_budget_gib ?? 450, 1)}
              onFocus={() => {
                setBudgetInput(String(cfg.drive_budget_gib ?? 450));
                setBudgetFocused(true);
              }}
              onChange={(e) => setBudgetInput(sanitizeDecimalInput(e.target.value, 12))}
              onBlur={() => {
                setBudgetFocused(false);
                const n = Number(budgetInput.replace(",", "."));
                if (Number.isFinite(n) && n >= 1) {
                  setCfg((p) => ({ ...p, drive_budget_gib: n }));
                  setBudgetInput(String(n));
                }
              }}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.budget_window"), t("settings.budget_window.tip"))}
            <NumberStepper
              ariaLabel={t("settings.budget_window")}
              min={1}
              max={720}
              step={1}
              disabled={!canEditScanner}
              value={cfg.drive_window_hours}
              onChange={(v) => updateCfg((p) => ({ ...p, drive_window_hours: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.cooldown"), t("settings.cooldown.tip"))}
            <NumberStepper
              ariaLabel={t("settings.cooldown")}
              min={0}
              max={3600}
              disabled={!canEditScanner}
              value={cfg.inter_item_delay_seconds}
              onChange={(v) => updateCfg((p) => ({ ...p, inter_item_delay_seconds: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.max_timeout"), t("settings.max_timeout.tip"))}
            <NumberStepper
              ariaLabel={t("settings.max_timeout")}
              min={1}
              max={360}
              disabled={!canEditScanner}
              value={cfg.max_item_scan_minutes}
              onChange={(v) => updateCfg((p) => ({ ...p, max_item_scan_minutes: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.delta_sync_interval"), t("settings.delta_sync_interval.tip"))}
            <NumberStepper
              ariaLabel={t("settings.delta_sync_interval")}
              min={0}
              max={1440}
              step={5}
              disabled={!canEditScanner}
              value={cfg.catalog_refresh_minutes}
              onChange={(v) => updateCfg((p) => ({ ...p, catalog_refresh_minutes: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.full_sync_interval"), t("settings.full_sync_interval.tip"))}
            <NumberStepper
              ariaLabel={t("settings.full_sync_interval")}
              min={0}
              max={720}
              step={1}
              disabled={!canEditScanner}
              value={cfg.full_sync_interval_hours ?? 0}
              onChange={(v) => updateCfg((p) => ({ ...p, full_sync_interval_hours: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.bulk_page_size"), t("settings.bulk_page_size.tip"))}
            <NumberStepper
              ariaLabel={t("settings.bulk_page_size")}
              min={50}
              max={500}
              step={25}
              disabled={!canEditScanner}
              value={cfg.bulk_page_size}
              onChange={(v) => updateCfg((p) => ({ ...p, bulk_page_size: v }))}
            />
          </div>
        </div>
        <div className="switch-row">
          <Checkbox
            checked={cfg.respect_plex_library_settings}
            disabled={!canEditScanner}
            onChange={(checked) => updateCfg((p) => ({ ...p, respect_plex_library_settings: checked }))}
            label={t("settings.respect_lib")}
          />
          <InfoPopover title={t("settings.respect_lib")} text={t("settings.respect_lib.tip")} />
        </div>
        <div className="switch-row">
          <Checkbox
            checked={cfg.dry_run}
            disabled={!canEditScanner}
            onChange={(checked) => updateCfg((p) => ({ ...p, dry_run: checked }))}
            label={t("settings.dry_run")}
          />
          <InfoPopover title={t("settings.dry_run")} text={t("settings.dry_run.tip")} />
        </div>
        {isAdmin && (
          <>
            <div className="switch-row">
              <Checkbox
                checked={secDraft.session_ip_binding}
                onChange={(checked) => setSecDraft((prev) => ({ ...prev, session_ip_binding: checked }))}
                label={t("settings.session_ip_binding")}
              />
              <InfoPopover title={t("settings.session_ip_binding")} text={t("settings.session_ip_binding.tip")} />
            </div>
            <div className="switch-row">
              <Checkbox
                checked={secDraft.update_check_enabled}
                onChange={(checked) => setSecDraft((prev) => ({ ...prev, update_check_enabled: checked }))}
                label={t("settings.update_check_enabled")}
              />
              {!secDraft.update_check_enabled && (
                <span className="badge st-complete">{t("settings.update_check_offline_badge")}</span>
              )}
              <InfoPopover
                title={t("settings.update_check_enabled")}
                text={secDraft.update_check_enabled ? t("settings.update_check_enabled.tip") : t("settings.update_check_offline.tip")}
              />
            </div>
          </>
        )}
      </div>

      {/* 5. Playback Protection & Quiet Hours */}
      <div className="card form-section">
        <h3>{t("settings.playback_quiet")}</h3>
        <div className="switch-row">
          <Checkbox
            checked={cfg.pause_on_active_streams}
            disabled={!canEditScanner}
            onChange={(checked) => updateCfg((p) => ({ ...p, pause_on_active_streams: checked }))}
            label={t("settings.pause_streams")}
          />
          <InfoPopover title={t("settings.pause_streams")} text={t("settings.pause_streams.tip")} />
        </div>
        <div className="switch-row">
          <Checkbox
            checked={cfg.pause_on_transcodes_only}
            disabled={!canEditScanner}
            onChange={(checked) => updateCfg((p) => ({ ...p, pause_on_transcodes_only: checked }))}
            label={t("settings.pause_transcodes")}
          />
          <InfoPopover title={t("settings.pause_transcodes")} text={t("settings.pause_transcodes.tip")} />
        </div>
        <div className="field-grid">
          <div className="field">
            {labelWithTip(t("settings.stream_threshold"), t("settings.stream_threshold.tip"))}
            <NumberStepper
              ariaLabel={t("settings.stream_threshold")}
              min={1}
              max={50}
              disabled={!canEditScanner}
              value={cfg.max_active_streams}
              onChange={(v) => updateCfg((p) => ({ ...p, max_active_streams: v }))}
            />
          </div>
        </div>
        <div className="switch-row">
          <Checkbox
            checked={cfg.quiet_hours_enabled}
            disabled={!canEditScanner}
            onChange={(checked) => updateCfg((p) => ({ ...p, quiet_hours_enabled: checked }))}
            label={t("settings.quiet_enable")}
          />
          <InfoPopover title={t("settings.quiet_enable")} text={t("settings.quiet_enable.tip")} />
        </div>
        {cfg.quiet_hours_enabled && (
          <div className="field-grid">
            <div className="field">
              {labelWithTip(`${t("settings.quiet_start")} (${formatHourSample(cfg.quiet_hours_start)})`, t("settings.quiet_start.tip"))}
              <NumberStepper
                ariaLabel={t("settings.quiet_start")}
                min={0}
                max={23}
                disabled={!canEditScanner}
                value={cfg.quiet_hours_start}
                onChange={(v) => updateCfg((p) => ({ ...p, quiet_hours_start: v }))}
              />
            </div>
            <div className="field">
              {labelWithTip(`${t("settings.quiet_end")} (${formatHourSample(cfg.quiet_hours_end)})`, t("settings.quiet_end.tip"))}
              <NumberStepper
                ariaLabel={t("settings.quiet_end")}
                min={0}
                max={23}
                disabled={!canEditScanner}
                value={cfg.quiet_hours_end}
                onChange={(v) => updateCfg((p) => ({ ...p, quiet_hours_end: v }))}
              />
            </div>
          </div>
        )}
      </div>

      {/* 6. Retry Policy */}
      <div className="card form-section">
        <h3>{t("settings.retry_policy")}</h3>
        <div className="field-grid">
          <div className="field">
            {labelWithTip(t("settings.max_attempts"), t("settings.max_attempts.tip"))}
            <NumberStepper
              ariaLabel={t("settings.max_attempts")}
              min={1}
              max={20}
              disabled={!canEditScanner}
              value={cfg.max_attempts}
              onChange={(v) => updateCfg((p) => ({ ...p, max_attempts: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.circuit_breaker"), t("settings.circuit_breaker.tip"))}
            <NumberStepper
              ariaLabel={t("settings.circuit_breaker")}
              min={2}
              max={50}
              disabled={!canEditScanner}
              value={cfg.circuit_breaker_threshold}
              onChange={(v) => updateCfg((p) => ({ ...p, circuit_breaker_threshold: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.retry_initial"), t("settings.retry_initial.tip"))}
            <NumberStepper
              ariaLabel={t("settings.retry_initial")}
              min={10}
              max={86400}
              step={30}
              disabled={!canEditScanner}
              value={cfg.retry_initial_seconds}
              onChange={(v) => updateCfg((p) => ({ ...p, retry_initial_seconds: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.retry_max"), t("settings.retry_max.tip"))}
            <NumberStepper
              ariaLabel={t("settings.retry_max")}
              min={30}
              max={604800}
              step={60}
              disabled={!canEditScanner}
              value={cfg.retry_max_seconds}
              onChange={(v) => updateCfg((p) => ({ ...p, retry_max_seconds: v }))}
            />
          </div>
        </div>
      </div>

      {/* 7. Webhooks & File Protection */}
      <div className="card form-section">
        <h3>{t("settings.webhooks_protection")}</h3>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <div className="field-label-row">
            <Checkbox
              checked={cfg.arr_autoscan_enabled ?? true}
              disabled={!canEditScanner}
              onChange={(checked) => updateCfg((p) => ({ ...p, arr_autoscan_enabled: checked }))}
              label={t("settings.arr_autoscan_enabled")}
            />
            <InfoPopover title={t("settings.arr_autoscan_enabled")} text={t("settings.arr_autoscan_enabled.tip")} />
          </div>
          <div className="field-label-row">
            <Checkbox
              checked={cfg.webhook_prioritize_top ?? true}
              disabled={!canEditScanner}
              onChange={(checked) => updateCfg((p) => ({ ...p, webhook_prioritize_top: checked }))}
              label={t("settings.webhook_prioritize_top")}
            />
            <InfoPopover title={t("settings.webhook_prioritize_top")} text={t("settings.webhook_prioritize_top.tip")} />
          </div>
          <div className="field-label-row">
            <Checkbox
              checked={cfg.hold_playing_items ?? true}
              disabled={!canEditScanner}
              onChange={(checked) => updateCfg((p) => ({ ...p, hold_playing_items: checked }))}
              label={t("settings.hold_playing_items")}
            />
            <InfoPopover title={t("settings.hold_playing_items")} text={t("settings.hold_playing_items.tip")} />
          </div>
          <div className="field-label-row">
            <Checkbox
              checked={cfg.hold_modified_files ?? true}
              disabled={!canEditScanner}
              onChange={(checked) => updateCfg((p) => ({ ...p, hold_modified_files: checked }))}
              label={t("settings.hold_modified_files")}
            />
            <InfoPopover title={t("settings.hold_modified_files")} text={t("settings.hold_modified_files.tip")} />
          </div>
        </div>
        <div className="field-grid" style={{ marginTop: 8 }}>
          <div className="field">
            {labelWithTip(t("settings.webhook_debounce_seconds"), t("settings.webhook_debounce_seconds.tip"))}
            <NumberStepper
              ariaLabel={t("settings.webhook_debounce_seconds")}
              min={0}
              max={600}
              step={5}
              disabled={!canEditScanner}
              value={cfg.webhook_debounce_seconds ?? 30}
              onChange={(v) => updateCfg((p) => ({ ...p, webhook_debounce_seconds: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.webhook_max_wait_seconds"), t("settings.webhook_max_wait_seconds.tip"))}
            <NumberStepper
              ariaLabel={t("settings.webhook_max_wait_seconds")}
              min={10}
              max={1800}
              step={15}
              disabled={!canEditScanner}
              value={cfg.webhook_max_wait_seconds ?? 180}
              onChange={(v) => updateCfg((p) => ({ ...p, webhook_max_wait_seconds: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.webhook_pickup_wait_seconds"), t("settings.webhook_pickup_wait_seconds.tip"))}
            <NumberStepper
              ariaLabel={t("settings.webhook_pickup_wait_seconds")}
              min={2}
              max={120}
              step={5}
              disabled={!canEditScanner}
              value={cfg.webhook_pickup_wait_seconds ?? 15}
              onChange={(v) => updateCfg((p) => ({ ...p, webhook_pickup_wait_seconds: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.webhook_hold_seconds"), t("settings.webhook_hold_seconds.tip"))}
            <NumberStepper
              ariaLabel={t("settings.webhook_hold_seconds")}
              min={0}
              max={3600}
              step={15}
              disabled={!canEditScanner}
              value={cfg.webhook_hold_seconds ?? 60}
              onChange={(v) => updateCfg((p) => ({ ...p, webhook_hold_seconds: v }))}
            />
          </div>
        </div>

        {/* *arr -> Plex Path Mappings */}
        <div className="ro-info-card" style={{ gap: 10, marginTop: 4 }}>
          <div className="section-head-row">
            <span className="small strong field-label-row">
              <span>{t("settings.arr_path_mappings")}</span>
              <InfoPopover title={t("settings.arr_path_mappings")} text={t("settings.arr_path_mappings.tip")} />
            </span>
            {canEditScanner && (
              <button
                type="button"
                className="btn btn-xs btn-ghost"
                onClick={() => updateCfg((p) => ({ ...p, arr_path_mappings: [...(p.arr_path_mappings || []), { remote: "", local: "" }] }))}
              >
                <Plus size={12} aria-hidden="true" />
                <span>{t("settings.add_mapping")}</span>
              </button>
            )}
          </div>
          {(cfg.arr_path_mappings || []).length === 0 ? (
            <div className="small muted">{t("settings.no_arr_path_mappings")}</div>
          ) : (
            (cfg.arr_path_mappings || []).map((m, idx) => (
              <div key={idx} className="action-group" style={{ gap: 8, flexWrap: "nowrap" }}>
                <input
                  style={{ flex: 1 }}
                  placeholder={t("settings.arr_path_placeholder")}
                  aria-label={t("settings.arr_path_placeholder")}
                  disabled={!canEditScanner}
                  value={m.remote}
                  onChange={(e) => {
                    const next = [...(cfg.arr_path_mappings || [])];
                    next[idx] = { ...next[idx], remote: e.target.value };
                    updateCfg((p) => ({ ...p, arr_path_mappings: next }));
                  }}
                />
                <span className="muted">→</span>
                <input
                  style={{ flex: 1 }}
                  placeholder={t("settings.plex_path_placeholder")}
                  aria-label={t("settings.plex_path_placeholder")}
                  disabled={!canEditScanner}
                  value={m.local}
                  onChange={(e) => {
                    const next = [...(cfg.arr_path_mappings || [])];
                    next[idx] = { ...next[idx], local: e.target.value };
                    updateCfg((p) => ({ ...p, arr_path_mappings: next }));
                  }}
                />
                {canEditScanner && (
                  <button
                    type="button"
                    className="btn btn-xs btn-danger"
                    aria-label={t("queue.remove")}
                    onClick={() => {
                      const next = (cfg.arr_path_mappings || []).filter((_, i) => i !== idx);
                      updateCfg((p) => ({ ...p, arr_path_mappings: next }));
                    }}
                  >
                    <Trash2 size={12} aria-hidden="true" />
                  </button>
                )}
              </div>
            ))
          )}
        </div>

        {/* Remote -> Local Path Mappings */}
        <div className="ro-info-card" style={{ gap: 10, marginTop: 4 }}>
          <div className="section-head-row">
            <span className="small strong field-label-row">
              <span>{t("settings.path_mappings")}</span>
              <InfoPopover title={t("settings.path_mappings")} text={t("settings.path_mappings.tip")} />
            </span>
            {isAdmin && canEditScanner && (
              <button
                type="button"
                className="btn btn-xs btn-ghost"
                onClick={() => updateCfg((p) => ({ ...p, path_mappings: [...(p.path_mappings || []), { remote: "", local: "" }] }))}
              >
                <Plus size={12} aria-hidden="true" />
                <span>{t("settings.add_mapping")}</span>
              </button>
            )}
          </div>
          {pathMappings.length === 0 ? (
            <div className="small muted">{t("settings.no_path_mappings")}</div>
          ) : (
            pathMappings.map((m, idx) => (
              <div key={idx} className="action-group" style={{ gap: 8, flexWrap: "nowrap" }}>
                <input
                  style={{ flex: 1 }}
                  placeholder={t("settings.remote_path_placeholder")}
                  aria-label={t("settings.remote_path_placeholder")}
                  disabled={!isAdmin || !canEditScanner}
                  value={m.remote}
                  onChange={(e) => {
                    const next = [...pathMappings];
                    next[idx] = { ...next[idx], remote: e.target.value };
                    updateCfg((p) => ({ ...p, path_mappings: next }));
                  }}
                />
                <span className="muted">→</span>
                <input
                  style={{ flex: 1 }}
                  placeholder={t("settings.local_path_placeholder")}
                  aria-label={t("settings.local_path_placeholder")}
                  disabled={!isAdmin || !canEditScanner}
                  value={m.local}
                  onChange={(e) => {
                    const next = [...pathMappings];
                    next[idx] = { ...next[idx], local: e.target.value };
                    updateCfg((p) => ({ ...p, path_mappings: next }));
                  }}
                />
                {isAdmin && canEditScanner && (
                  <button
                    type="button"
                    className="btn btn-xs btn-danger"
                    aria-label={t("queue.remove")}
                    onClick={() => {
                      const next = pathMappings.filter((_, i) => i !== idx);
                      updateCfg((p) => ({ ...p, path_mappings: next }));
                    }}
                  >
                    <Trash2 size={12} aria-hidden="true" />
                  </button>
                )}
              </div>
            ))
          )}
          <div className="action-group" style={{ gap: 8, paddingTop: 8, borderTop: "1px solid var(--border)" }}>
            <input
              style={{ flex: 1 }}
              placeholder={t("settings.check_path_placeholder")}
              aria-label={t("settings.check_path_placeholder")}
              value={testPath}
              onChange={(e) => setTestPath(e.target.value)}
            />
            <button
              type="button"
              className="btn btn-xs btn-primary"
              disabled={pathCheckBusy || !testPath.trim()}
              onClick={() => void runPathCheck()}
            >
              <FolderSearch size={12} aria-hidden="true" />
              <span>{t("settings.check_file_access")}</span>
            </button>
          </div>
          {pathCheckRes && (
            <div className="small" style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
              <span className={`badge ${pathCheckRes.readable ? "st-complete" : "st-exhausted"}`}>
                {pathCheckRes.readable ? t("settings.path_accessible") : t("settings.path_inaccessible")}
              </span>
              <span className="muted">
                {pathCheckRes.local_path} (UID {pathCheckRes.uid} / GID {pathCheckRes.gid})
                {pathCheckRes.error ? ` — ${pathCheckRes.error}` : ""}
              </span>
            </div>
          )}
        </div>
      </div>

      {/* 8. Log & History Retention */}
      <div className="card form-section">
        <div className="section-head-row">
          <h3>{t("settings.retention_heading")}</h3>
          {canScan && (
            <button
              type="button"
              className="btn btn-xs btn-danger"
              onClick={() => setCleanupConfirmOpen(true)}
            >
              <Trash2 size={12} aria-hidden="true" />
              <span>{t("settings.cleanup_now")}</span>
            </button>
          )}
        </div>
        <div className="field-grid">
          <div className="field">
            {labelWithTip(t("settings.log_retention_days"), t("settings.log_retention_days.tip"))}
            <NumberStepper
              ariaLabel={t("settings.log_retention_days")}
              min={1}
              max={365}
              disabled={!canEditScanner}
              value={cfg.log_retention_days ?? 14}
              onChange={(v) => updateCfg((p) => ({ ...p, log_retention_days: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.history_retention_days"), t("settings.history_retention_days.tip"))}
            <NumberStepper
              ariaLabel={t("settings.history_retention_days")}
              min={1}
              max={365}
              disabled={!canEditScanner}
              value={cfg.history_retention_days ?? 30}
              onChange={(v) => updateCfg((p) => ({ ...p, history_retention_days: v }))}
            />
          </div>
          <div className="field">
            {labelWithTip(t("settings.max_log_rows"), t("settings.max_log_rows.tip"))}
            <NumberStepper
              ariaLabel={t("settings.max_log_rows")}
              min={500}
              max={100000}
              step={500}
              disabled={!canEditScanner}
              value={cfg.max_log_rows ?? 5000}
              onChange={(v) => updateCfg((p) => ({ ...p, max_log_rows: v }))}
            />
          </div>
        </div>
      </div>

      {canScan && (
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 10 }}>
          {dirty && (
            <span className="small" style={{ alignSelf: "center", color: "var(--accent-ink)" }}>
              {t("settings.unsaved_badge")}
            </span>
          )}
          <button
            type="button"
            className={`btn ${dirty ? "btn-primary" : "btn-ghost"}`}
            disabled={!dirty}
            onClick={() => void save()}
          >
            <Save size={14} aria-hidden="true" />
            <span>{t("settings.save")}</span>
          </button>
        </div>
      )}

      <ConfirmModal
        open={cleanupConfirmOpen}
        title={t("confirm.cleanup_title")}
        message={t("confirm.cleanup_msg")}
        confirmLabel={t("settings.cleanup_now")}
        danger
        busy={cleanupBusy}
        onConfirm={() => void runCleanup()}
        onCancel={() => setCleanupConfirmOpen(false)}
      />
    </div>
  );
}
