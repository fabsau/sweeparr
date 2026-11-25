import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, CheckCircle2, ChevronDown, Clock, Pause, Play, RefreshCw, RotateCcw, ShieldBan, SkipForward, SlidersHorizontal, Square } from "lucide-react";
import { formatItemTitle, serverApi } from "../api";
import { InfoPopover, useClickOutside } from "../components/Controls";
import { CAP_DEFS, MediaExplorer } from "../components/MediaExplorer";
import { useLocaleFormat } from "../utils/format";
import type { CapStat, ServerStatus } from "../types";

function CapBreakdown({
  capStats,
  kind,
}: {
  capStats: Record<string, CapStat>;
  kind?: string;
}) {
  const { t } = useTranslation();
  const { fmtNum, fmtPct } = useLocaleFormat();
  const active = CAP_DEFS.filter((c) => {
    if (kind && !c.kinds.includes(kind)) return false;
    return (capStats?.[c.id]?.total || 0) > 0;
  }).sort((a, b) => t(a.labelKey).localeCompare(t(b.labelKey)));
  if (!active.length) return <span className="small muted">{t("cap.all_complete")}</span>;
  return (
    <div className="cap-breakdown-grid">
      {active.map((c) => {
        const s = capStats[c.id];
        const pct = Math.round(s.pct);
        const label = t(c.labelKey);
        return (
          <div className="cap-stat-row" key={c.id}>
            <div className="cap-stat-top">
              <span className="cap-stat-name">
                <span>{label}</span>
                <InfoPopover title={label} text={t(c.tipKey)} />
              </span>
              <span className="cap-stat-pct">{fmtPct(pct, 0)}</span>
            </div>
            <div
              className="cap-mini-bar"
              role="progressbar"
              aria-label={label}
              aria-valuenow={pct}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="cap-mini-fill" style={{ width: `${pct}%` }} />
            </div>
            <div className="cap-stat-vals">
              <span className="cap-stat-ok">{fmtNum(s.have)}</span> {t("cap.done")} •{" "}
              <span className={s.missing > 0 ? "cap-stat-miss" : "cap-stat-ok"}>{fmtNum(s.missing)}</span>{" "}
              {t("cap.left")} • {fmtNum(s.total)} {t("cap.total")}
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function OverviewView({
  serverId,
  status,
  canScan,
  onOpenSync,
  onConfirmSkip,
  onConfirmIgnoreCurrent,
  onConfirmInterrupt,
  onConfirmScanLib,
  onNavigateHistory,
  onRefreshStatus,
  toast,
}: {
  serverId: string;
  status: ServerStatus;
  canScan: boolean;
  onOpenSync: () => void;
  onConfirmSkip: () => void;
  onConfirmIgnoreCurrent: () => void;
  onConfirmInterrupt: () => void;
  onConfirmScanLib: (lib: string) => void;
  onNavigateHistory: (statusFilter: string) => void;
  onRefreshStatus: () => void;
  toast: (msg: string) => void;
}) {
  const { t } = useTranslation();
  const [actionsMenu, setActionsMenu] = useState(false);
  const actionsWrapRef = useRef<HTMLDivElement>(null);
  useClickOutside(actionsWrapRef, actionsMenu, () => setActionsMenu(false));

  const [viewMode, setViewMode] = useState<"flat" | "grouped">("flat");
  const [statusFilters, setStatusFilters] = useState<string[]>([]);
  const explorerRef = useRef<HTMLElement>(null);

  const { fmtNum, fmtFixed, fmtPct } = useLocaleFormat();

  const { daemon, plex, drive_budget, totals, libraries } = status;
  const parked = totals.exhausted + totals.retry + totals.blocked;
  const scanSummary = daemon.scan_summary || { completed: 0, retries: 0, failed: 0 };
  const pendingTriggers = daemon.pending_triggers || [];

  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000));
  const needsTick = Boolean(
    (daemon.paused && daemon.paused_until && daemon.paused_until > 0) ||
    (daemon.current_item && daemon.current_item_started_at && daemon.current_item_started_at > 0) ||
    pendingTriggers.length > 0
  );
  useEffect(() => {
    if (!needsTick) return;
    setNowSec(Math.floor(Date.now() / 1000));
    const iv = window.setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(iv);
  }, [needsTick]);

  const formatDurationClock = (secs: number) => {
    const s = Math.max(0, Math.floor(secs));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    const pad = (x: number) => String(x).padStart(2, "0");
    return h > 0 ? `${pad(h)}:${pad(m)}:${pad(rem)}` : `${pad(m)}:${pad(rem)}`;
  };

  const pauseCountdown = (() => {
    if (!daemon.paused || !daemon.paused_until || daemon.paused_until <= 0) return "";
    const rem = Math.max(0, Math.floor(daemon.paused_until - nowSec));
    return rem > 0 ? formatDurationClock(rem) : "";
  })();

  const activeElapsed = (() => {
    if (!daemon.current_item || !daemon.current_item_started_at || daemon.current_item_started_at <= 0) return "";
    return formatDurationClock(Math.max(0, nowSec - daemon.current_item_started_at));
  })();

  async function pauseFor(mins: number) {
    setActionsMenu(false);
    await serverApi(serverId, "/control/pause", "POST", { duration_minutes: mins });
    toast(mins > 0 ? `Paused for ${mins} min` : "Paused indefinitely");
    onRefreshStatus();
  }

  async function resume() {
    setActionsMenu(false);
    await serverApi(serverId, "/control/resume", "POST");
    toast("Scanner resumed");
    onRefreshStatus();
  }

  return (
    <div className="page-view">
      <section className="hero-grid" aria-label={t("overview.scanner_engine")}>
        {/* Scanner Engine */}
        <article className="card hero-card">
          <div className="card-head">
            <div className="engine-head-left">
              <span className="card-label">{t("overview.scanner_engine")}</span>
              <span className={`status-pill st-${daemon.state}`}>{daemon.state.toUpperCase()}</span>
              {pauseCountdown && (
                <span className="badge st-paused mono" title={t("overview.pause_duration")}>
                  {pauseCountdown}
                </span>
              )}
            </div>
            {canScan && (
              <div className="pause-menu-wrap" ref={actionsWrapRef}>
                <button
                  type="button"
                  className="btn btn-sm btn-primary"
                  aria-expanded={actionsMenu}
                  aria-haspopup="menu"
                  onClick={() => setActionsMenu((v) => !v)}
                >
                  <SlidersHorizontal size={13} aria-hidden="true" />
                  <span>{t("overview.actions_btn")}</span>
                  <ChevronDown size={13} aria-hidden="true" />
                </button>
                {actionsMenu && (
                  <div className="dropdown-menu scanner-actions-menu" role="menu" aria-label={t("overview.actions_btn")}>
                    {plex.activities > 0 && (
                      <>
                        <button
                          type="button"
                          role="menuitem"
                          className="dropdown-item menu-icon-item"
                          onClick={() => {
                            setActionsMenu(false);
                            onConfirmInterrupt();
                          }}
                        >
                          <Square size={13} aria-hidden="true" />
                          <span>{t("overview.interrupt")}</span>
                        </button>
                        <div className="menu-divider" />
                      </>
                    )}
                    {daemon.paused ? (
                      <button type="button" role="menuitem" className="dropdown-item menu-icon-item" onClick={() => void resume()}>
                        <Play size={13} aria-hidden="true" />
                        <span>{t("overview.resume")}</span>
                      </button>
                    ) : (
                      <>
                        <button type="button" role="menuitem" className="dropdown-item menu-icon-item" onClick={() => void pauseFor(0)}>
                          <Pause size={13} aria-hidden="true" />
                          <span>{t("overview.pause")}</span>
                          <span className="badge st-paused" style={{ marginLeft: "auto", padding: "0 6px" }}>∞</span>
                        </button>
                        <div className="dropdown-title">{t("overview.pause_duration")}</div>
                        <div className="pause-quick-row">
                          {[15, 60, 240, 1440].map((m) => (
                            <button
                              key={m}
                              type="button"
                              role="menuitem"
                              className="btn btn-xs btn-ghost"
                              onClick={() => void pauseFor(m)}
                            >
                              {m >= 60 ? `${fmtNum(m / 60)}h` : `${fmtNum(m)}m`}
                            </button>
                          ))}
                        </div>
                      </>
                    )}
                    <div className="menu-divider" />
                    <button
                      type="button"
                      role="menuitem"
                      className="dropdown-item menu-icon-item"
                      onClick={() => {
                        setActionsMenu(false);
                        onConfirmSkip();
                      }}
                    >
                      <SkipForward size={13} aria-hidden="true" />
                      <span>{t("overview.skip_current")}</span>
                    </button>
                    <button
                      type="button"
                      role="menuitem"
                      className="dropdown-item menu-icon-item"
                      disabled={!daemon.current_item}
                      onClick={() => {
                        setActionsMenu(false);
                        onConfirmIgnoreCurrent();
                      }}
                    >
                      <ShieldBan size={13} aria-hidden="true" />
                      <span>{t("overview.ignore_current")}</span>
                    </button>
                    {parked > 0 && (
                      <button
                        type="button"
                        role="menuitem"
                        className="dropdown-item menu-icon-item"
                        onClick={async () => {
                          setActionsMenu(false);
                          await serverApi(serverId, "/control/reset-retries", "POST", {});
                          toast("Reset failed/retry items");
                          onRefreshStatus();
                        }}
                      >
                        <RotateCcw size={13} aria-hidden="true" />
                        <span>{t("overview.retry_failed", { count: fmtNum(parked) as unknown as number })}</span>
                      </button>
                    )}
                    <div className="menu-divider" />
                    <button
                      type="button"
                      role="menuitem"
                      className="dropdown-item menu-icon-item"
                      onClick={() => {
                        setActionsMenu(false);
                        onOpenSync();
                      }}
                    >
                      <RefreshCw size={13} aria-hidden="true" />
                      <span>{t("overview.refresh_libs")}</span>
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="engine-stat-pills" aria-label={t("overview.scan_stats")}>
            <button
              type="button"
              className={`engine-stat-pill ${scanSummary.completed > 0 ? "stat-ok" : ""}`}
              onClick={() => onNavigateHistory("complete")}
            >
              <CheckCircle2 size={12} aria-hidden="true" />
              <span>{fmtNum(scanSummary.completed)} {t("overview.stat_completed")}</span>
            </button>
            <button
              type="button"
              className={`engine-stat-pill ${scanSummary.retries > 0 ? "stat-warn" : ""}`}
              onClick={() => onNavigateHistory("retry")}
            >
              <RotateCcw size={12} aria-hidden="true" />
              <span>{fmtNum(scanSummary.retries)} {t("overview.stat_retries")}</span>
            </button>
            <button
              type="button"
              className={`engine-stat-pill ${scanSummary.failed > 0 ? "stat-fail" : ""}`}
              onClick={() => onNavigateHistory("failed")}
            >
              <AlertTriangle size={12} aria-hidden="true" />
              <span>{fmtNum(scanSummary.failed)} {t("overview.stat_failed")}</span>
            </button>
          </div>

          {pendingTriggers.length > 0 && (
            <div className="engine-subbox">
              <div className="engine-subhead">
                <span className="subbox-label">{t("arr.pending_heading")}</span>
                {canScan && (
                  <button
                    type="button"
                    className="btn btn-xs btn-ghost"
                    onClick={async () => {
                      await serverApi(serverId, "/arr-triggers/flush", "POST");
                      toast(t("arr.flushed_toast"));
                      onRefreshStatus();
                    }}
                  >
                    <Play size={11} aria-hidden="true" />
                    <span>{t("arr.flush_now")}</span>
                  </button>
                )}
              </div>
              {pendingTriggers.map((tr) => {
                const rem = Math.max(0, tr.fires_at - nowSec);
                return (
                  <div key={tr.id} className="small" style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8, flexWrap: "wrap" }}>
                    <span className="action-group" style={{ gap: 6, minWidth: 0 }}>
                      <span className="badge st-queued">{tr.source}</span>
                      <strong className="cell-ellipsis" title={tr.dir}>{tr.library}</strong>
                      <span className="muted">({fmtNum(tr.file_count)} {tr.file_count === 1 ? t("arr.file_one") : t("arr.file_many")})</span>
                    </span>
                    <span className={`badge tabular-nums ${tr.state === "debouncing" ? "st-paused" : "st-active"}`}>
                      {tr.state === "debouncing"
                        ? formatDurationClock(rem)
                        : tr.state === "refreshing_plex"
                          ? t("arr.state_refreshing")
                          : t("arr.state_syncing")}
                    </span>
                  </div>
                );
              })}
            </div>
          )}

          {daemon.current_item && (
            <div className="engine-subbox">
              <div className="engine-subhead">
                <span className="subbox-label">{t("queue.currently_scanning")}</span>
                {activeElapsed && (
                  <span className="badge st-active tabular-nums">
                    <Clock size={11} aria-hidden="true" />
                    <span>{activeElapsed}</span>
                  </span>
                )}
              </div>
              <div className="small">
                <strong>{formatItemTitle(daemon.current_item)}</strong>{" "}
                <span className="muted">({daemon.current_item.library})</span>
              </div>
            </div>
          )}

          {daemon.indexing && (
            <div className="progress-wrap">
              <div
                className="progress-track"
                role="progressbar"
                aria-label={t("overview.indexing_lib", { library: daemon.index_progress.library })}
                aria-valuenow={
                  daemon.index_progress.total
                    ? Math.round((daemon.index_progress.done / daemon.index_progress.total) * 100)
                    : 0
                }
                aria-valuemin={0}
                aria-valuemax={100}
              >
                <div
                  className="progress-fill progress-green"
                  style={{
                    width: `${daemon.index_progress.total ? (daemon.index_progress.done / daemon.index_progress.total) * 100 : 0}%`,
                  }}
                />
              </div>
              <div className="progress-caption">
                <span>{t("overview.indexing_lib", { library: daemon.index_progress.library })}</span>
                <span>
                  {fmtNum(daemon.index_progress.done)} / {fmtNum(daemon.index_progress.total)}
                </span>
              </div>
            </div>
          )}

          {plex.activity_list?.length > 0 && (
            <div className="engine-subbox">
              <div className="engine-subhead">
                <span className="subbox-label">{t("overview.active_tasks")}</span>
                <span className="badge st-running">
                  {t("overview.active_count", { count: fmtNum(plex.activity_list.length) as unknown as number })}
                </span>
              </div>
              {plex.activity_list.map((act) => {
                const indeterminate = (act.progress ?? -1) < 0;
                const pct = indeterminate ? 100 : Math.min(100, Math.max(0, Math.round(act.progress || 0)));
                return (
                <div key={act.uuid} style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  <div style={{ display: "flex", justifyContent: "space-between", fontSize: 12 }}>
                    <span>
                      <strong>{act.title}</strong> {act.subtitle ? `— ${act.subtitle}` : ""}
                    </span>
                    <span className="tabular-nums muted">{indeterminate ? t("status.active") : fmtPct(pct, 0)}</span>
                  </div>
                  <div
                    className="progress-track"
                    role="progressbar"
                    aria-label={act.title}
                    aria-valuenow={indeterminate ? undefined : pct}
                    aria-valuemin={0}
                    aria-valuemax={100}
                  >
                    <div className="progress-fill progress-amber" style={{ width: `${pct}%`, opacity: indeterminate ? 0.55 : 1 }} />
                  </div>
                </div>
                );
              })}
            </div>
          )}

          <div className="engine-subbox">
            <div className="engine-subhead">
              <span className="subbox-label">{t("overview.queue")}</span>
            </div>
            <div className="small">
              {daemon.next_up ? (
                <>
                  {t("overview.up_next")} <strong>{daemon.next_up.title}</strong>{" "}
                  <span className="muted">({daemon.next_up.library})</span>
                </>
              ) : (
                <span className="muted">{t("overview.all_caught_up")}</span>
              )}
            </div>
          </div>

          <div className="engine-subbox">
            <div className="engine-subhead">
              <span className="subbox-label field-label-row">
                <span>{t("overview.drive_budget")}</span>
                <InfoPopover title={t("overview.drive_budget")} text={t("overview.drive_budget.tip")} />
              </span>
              <span className="mono small muted">
                {t("overview.window_hours", { hours: fmtNum(drive_budget.window_hours) as unknown as number })}
              </span>
            </div>
            <div className="small">
              <strong className="mono">{fmtFixed(drive_budget.used_gib, 1)} GiB</strong>{" "}
              <span className="mono muted">
                / {fmtNum(drive_budget.budget_gib)} GiB (
                {t("overview.active_parts", { count: fmtNum(drive_budget.active_parts) as unknown as number })})
              </span>
            </div>
            <div
              className="progress-track"
              role="progressbar"
              aria-label={t("overview.drive_budget")}
              aria-valuenow={Math.round(Math.min(100, drive_budget.percent))}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="progress-fill progress-amber" style={{ width: `${Math.min(100, drive_budget.percent)}%` }} />
            </div>
          </div>
        </article>

        {/* All Libraries */}
        <article className="card hero-card">
          <div className="card-head">
            <div className="engine-head-left">
              <span className="card-label">{t("overview.all_libraries")}</span>
              <InfoPopover title={t("overview.completeness.title")} text={t("overview.completeness.tip")} />
            </div>
            {canScan && (
              <div className="action-group">
                <button type="button" className="btn btn-xs btn-primary" onClick={() => onConfirmScanLib("")}>
                  <Play size={12} aria-hidden="true" />
                  <span>{t("overview.scan_all_libs")}</span>
                </button>
              </div>
            )}
          </div>

          <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
            <span style={{ fontSize: 28, fontWeight: 700 }}>{fmtPct(Math.round(totals.pct), 0)}</span>
            <span className="small muted">
              {t("overview.complete_summary", { complete: fmtNum(totals.complete), total: fmtNum(totals.total) })}
            </span>
          </div>

          <div className="progress-wrap">
            <div
              className="progress-track"
              role="progressbar"
              aria-label={t("overview.all_libraries")}
              aria-valuenow={Math.round(totals.pct)}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="progress-fill progress-green" style={{ width: `${totals.pct}%` }} />
            </div>
            <div className="progress-caption">
              <span>{t("overview.pending_summary", { pending: fmtNum(totals.pending) })}</span>
            </div>
          </div>

          <CapBreakdown capStats={totals.cap_stats} />
        </article>
      </section>

      {/* Per-Library Cards */}
      <section className="libraries-grid" aria-label={t("explorer.libraries")}>
        {libraries.map((lib) => (
          <article className="card lib-card" key={lib.title}>
            <div className="card-head">
              <div className="field-label-row">
                <strong>{lib.title}</strong>
                <InfoPopover
                  title={lib.title}
                  text={
                    lib.kind === "track"
                      ? t("overview.lib_tip.track")
                      : lib.kind === "episode"
                        ? t("overview.lib_tip.episode")
                        : t("overview.lib_tip.movie")
                  }
                />
              </div>
              {canScan && (
                <button type="button" className="btn btn-xs btn-primary" onClick={() => onConfirmScanLib(lib.title)}>
                  <Play size={12} aria-hidden="true" />
                  <span>{t("overview.scan_lib")}</span>
                </button>
              )}
            </div>
            <div
              className="progress-track"
              role="progressbar"
              aria-label={lib.title}
              aria-valuenow={Math.round(lib.pct)}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="progress-fill progress-green" style={{ width: `${lib.pct}%` }} />
            </div>
            <div className="small muted">
              {fmtPct(Math.round(lib.pct), 0)} ({t("overview.complete_summary", { complete: fmtNum(lib.complete), total: fmtNum(lib.total) })} •{" "}
              {t("overview.pending_summary", { pending: fmtNum(lib.pending) })})
            </div>
            <CapBreakdown capStats={lib.cap_stats} kind={lib.kind} />
          </article>
        ))}
      </section>

      <MediaExplorer
        serverId={serverId}
        libraries={libraries}
        libraryCapabilities={status.config.library_capabilities}
        catalogVersion={status.catalog_version}
        defaultPageSize={status.config.default_page_size || 50}
        dateLocale={status.config.datetime_locale || "de-DE"}
        timeFormat={status.config.time_format || "24h"}
        canScan={canScan}
        explorerRef={explorerRef}
        statusFilters={statusFilters}
        setStatusFilters={setStatusFilters}
        viewMode={viewMode}
        setViewMode={setViewMode}
        fmtNum={fmtNum}
        onRefreshStatus={onRefreshStatus}
        toast={toast}
      />
    </div>
  );
}
