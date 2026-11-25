import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  ArrowUpToLine,
  Check,
  Clock,
  Download,
  GripVertical,
  Loader2,
  RefreshCw,
  RotateCcw,
  ShieldBan,
  ShieldCheck,
  SkipForward,
  Trash2,
  X as XIcon,
} from "lucide-react";
import { formatItemTitle, serverApi, serverPath, useDebouncedValue } from "../api";
import { ConfirmModal, InfoPopover, MultiSelectFilter, PaginationBar, SelectMenu } from "../components/Controls";
import { CAP_DEFS } from "../components/MediaExplorer";
import { useLocaleFormat } from "../utils/format";
import type { EventRow, HistoryRow, LibrarySummary, MediaItem, PendingArrTrigger } from "../types";

export type ActivityTab = "queue" | "history" | "blocklist" | "logs";

function sinceRangeToUnix(range: string): string {
  if (!range) return "";
  const now = Math.floor(Date.now() / 1000);
  switch (range) {
    case "1h":
      return String(now - 3600);
    case "24h":
      return String(now - 86400);
    case "7d":
      return String(now - 7 * 86400);
    case "30d":
      return String(now - 30 * 86400);
    default:
      return "";
  }
}

function formatDurationMs(ms?: number): string {
  if (!ms || ms <= 0) return "—";
  const secs = Math.max(1, Math.round(ms / 1000));
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  if (m < 60) return `${m}m ${String(s).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${String(m % 60).padStart(2, "0")}m`;
}

function formatClock(secs: number): string {
  const s = Math.max(0, Math.floor(secs));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const rem = s % 60;
  const pad = (x: number) => String(x).padStart(2, "0");
  return h > 0 ? `${pad(h)}:${pad(m)}:${pad(rem)}` : `${pad(m)}:${pad(rem)}`;
}

export function QueueView({
  serverId,
  libraries = [],
  catalogVersion = 0,
  canScan,
  activeTab,
  onTabChange,
  initialHistoryStatus = "",
  onConsumeHistoryStatus,
  currentItemStartedAt = 0,
  pendingTriggers = [],
  onConfirmSkip,
  toast,
}: {
  serverId: string;
  libraries?: LibrarySummary[];
  catalogVersion?: number;
  canScan: boolean;
  dateLocale: string;
  timeFormat?: string;
  activeTab: ActivityTab;
  onTabChange: (tab: ActivityTab) => void;
  initialHistoryStatus?: string;
  onConsumeHistoryStatus?: () => void;
  currentItemStartedAt?: number;
  pendingTriggers?: PendingArrTrigger[];
  onConfirmSkip: () => void;
  toast: (msg: string) => void;
}) {
  const { t } = useTranslation();
  const tab = activeTab;
  const setTab = onTabChange;

  const storageKey = `sweeparr_activity_${serverId}`;
  const loadStored = () => {
    try {
      return JSON.parse(sessionStorage.getItem(storageKey) || "{}") as Record<string, unknown>;
    } catch {
      return {};
    }
  };
  const stored = loadStored();

  // Queue tab state
  const [prio, setPrio] = useState<MediaItem[]>([]);
  const [auto, setAuto] = useState<MediaItem[]>([]);
  const [currentItem, setCurrentItem] = useState<MediaItem | null>(null);
  const [dragKey, setDragKey] = useState<string | null>(null);
  const [dragOverKey, setDragOverKey] = useState<string | null>(null);
  const [queueSearch, setQueueSearch] = useState(String(stored.queueSearch || ""));
  const [queueLibs, setQueueLibs] = useState<string[]>((stored.queueLibs as string[]) || []);
  const [autoSort, setAutoSort] = useState(String(stored.autoSort || ""));

  // History tab state
  const [history, setHistory] = useState<HistoryRow[]>([]);
  const [histSearch, setHistSearch] = useState(String(stored.histSearch || ""));
  const debouncedHistSearch = useDebouncedValue(histSearch, 200);
  const [histLibs, setHistLibs] = useState<string[]>((stored.histLibs as string[]) || []);
  const [histStatuses, setHistStatuses] = useState<string[]>((stored.histStatuses as string[]) || []);
  const [histInfo, setHistInfo] = useState<string[]>((stored.histInfo as string[]) || []);
  const [histSince, setHistSince] = useState(String(stored.histSince || ""));
  const [histSort, setHistSort] = useState(String(stored.histSort || "finished_desc"));
  const [histPage, setHistPage] = useState(1);
  const [histPageSize, setHistPageSize] = useState(Number(stored.histPageSize) || 50);
  const [histTotal, setHistTotal] = useState(0);
  const [histTotalPages, setHistTotalPages] = useState(1);

  // Blocklist tab state
  const [blocklist, setBlocklist] = useState<MediaItem[]>([]);
  const [blockSearch, setBlockSearch] = useState(String(stored.blockSearch || ""));
  const debouncedBlockSearch = useDebouncedValue(blockSearch, 200);
  const [blockLibs, setBlockLibs] = useState<string[]>((stored.blockLibs as string[]) || []);
  const [blockSince, setBlockSince] = useState(String(stored.blockSince || ""));
  const [blockSort, setBlockSort] = useState(String(stored.blockSort || "blocklisted_desc"));
  const [blockPage, setBlockPage] = useState(1);
  const [blockPageSize, setBlockPageSize] = useState(Number(stored.blockPageSize) || 50);
  const [blockTotal, setBlockTotal] = useState(0);
  const [blockTotalPages, setBlockTotalPages] = useState(1);

  // Logs tab state
  const [events, setEvents] = useState<EventRow[]>([]);
  const [logSearch, setLogSearch] = useState(String(stored.logSearch || ""));
  const debouncedLogSearch = useDebouncedValue(logSearch, 200);
  const [logLevels, setLogLevels] = useState<string[]>((stored.logLevels as string[]) || []);
  const [logCategories, setLogCategories] = useState<string[]>((stored.logCategories as string[]) || []);
  const [logSince, setLogSince] = useState(String(stored.logSince || ""));
  const [logSort, setLogSort] = useState(String(stored.logSort || "ts_desc"));
  const [logPage, setLogPage] = useState(1);
  const [logPageSize, setLogPageSize] = useState(Number(stored.logPageSize) || 50);
  const [logTotal, setLogTotal] = useState(0);
  const [logTotalPages, setLogTotalPages] = useState(1);

  // Confirmation modal state
  const [confirmAction, setConfirmAction] = useState<null | "clear_history" | "retry_all" | "clear_blocklist" | "clear_logs">(null);
  const [confirmBusy, setConfirmBusy] = useState(false);

  // Live elapsed timer for active scan or debouncing *arr triggers
  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000));
  const needsTick = Boolean((currentItem && currentItemStartedAt) || pendingTriggers.length > 0);
  useEffect(() => {
    if (!needsTick) return;
    setNowSec(Math.floor(Date.now() / 1000));
    const iv = window.setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(iv);
  }, [needsTick]);

  // Consume deep-link status filter from Overview Scanner Engine stat pills
  useEffect(() => {
    if (!initialHistoryStatus) return;
    const mapped = initialHistoryStatus === "failed" ? ["exhausted"] : [initialHistoryStatus];
    setHistStatuses(mapped);
    setHistPage(1);
    onConsumeHistoryStatus?.();
  }, [initialHistoryStatus, onConsumeHistoryStatus]);

  // Persist filters across navigation
  useEffect(() => {
    try {
      sessionStorage.setItem(
        storageKey,
        JSON.stringify({
          queueSearch,
          queueLibs,
          autoSort,
          histSearch,
          histLibs,
          histStatuses,
          histInfo,
          histSince,
          histSort,
          histPageSize,
          blockSearch,
          blockLibs,
          blockSince,
          blockSort,
          blockPageSize,
          logSearch,
          logLevels,
          logCategories,
          logSince,
          logSort,
          logPageSize,
        })
      );
    } catch {
      // ignore storage errors
    }
  }, [
    storageKey,
    queueSearch,
    queueLibs,
    autoSort,
    histSearch,
    histLibs,
    histStatuses,
    histInfo,
    histSince,
    histSort,
    histPageSize,
    blockSearch,
    blockLibs,
    blockSince,
    blockSort,
    blockPageSize,
    logSearch,
    logLevels,
    logCategories,
    logSince,
    logSort,
    logPageSize,
  ]);

  const { fmtNum, fmtBytes, fmtDateTime } = useLocaleFormat();
  const fmtTime = (ts?: number) => (ts && ts > 0 ? fmtDateTime(ts) : "—");
  const sortedCapDefs = [...CAP_DEFS].sort((a, b) => t(a.labelKey).localeCompare(t(b.labelKey)));

  const timeRangeOptions = [
    { value: "", label: t("filter.time_all") },
    { value: "1h", label: t("filter.time_1h") },
    { value: "24h", label: t("filter.time_24h") },
    { value: "7d", label: t("filter.time_7d") },
    { value: "30d", label: t("filter.time_30d") },
  ];

  async function loadQueue() {
    const r = await serverApi<{
      prioritized: MediaItem[];
      automatic: MediaItem[];
      current_item: MediaItem | null;
    }>(serverId, "/queue?limit=60");
    setPrio(r.prioritized || []);
    setAuto(r.automatic || []);
    setCurrentItem(r.current_item || null);
  }

  async function loadHistory() {
    const p = new URLSearchParams({
      page: String(histPage),
      page_size: String(histPageSize),
      search: debouncedHistSearch,
      library: histLibs.join(","),
      status: histStatuses.join(","),
      info: histInfo.join(","),
      since: sinceRangeToUnix(histSince),
      sort: histSort,
    });
    const r = await serverApi<{
      items: HistoryRow[];
      total: number;
      total_pages: number;
    }>(serverId, `/history?${p}`);
    setHistory(r.items || []);
    setHistTotal(r.total || 0);
    setHistTotalPages(r.total_pages || 1);
  }

  async function loadBlocklist() {
    const p = new URLSearchParams({
      page: String(blockPage),
      page_size: String(blockPageSize),
      search: debouncedBlockSearch,
      library: blockLibs.join(","),
      since: sinceRangeToUnix(blockSince),
      sort: blockSort,
    });
    const r = await serverApi<{
      items: MediaItem[];
      total: number;
      total_pages: number;
    }>(serverId, `/blocklist?${p}`);
    setBlocklist(r.items || []);
    setBlockTotal(r.total || 0);
    setBlockTotalPages(r.total_pages || 1);
  }

  async function loadLogs() {
    const p = new URLSearchParams({
      page: String(logPage),
      page_size: String(logPageSize),
      level: logLevels.length === 1 ? logLevels[0] : "",
      category: logCategories.length === 1 ? logCategories[0] : "",
      search: debouncedLogSearch,
      since: sinceRangeToUnix(logSince),
      sort: logSort,
    });
    const res = await serverApi<{ events: EventRow[]; total: number; total_pages: number }>(serverId, `/events?${p}`);
    const filtered = (res.events || []).filter((ev) => {
      if (logLevels.length > 1 && !logLevels.includes(ev.level)) return false;
      if (logCategories.length > 1 && !logCategories.includes(ev.category)) return false;
      return true;
    });
    setEvents(filtered);
    setLogTotal(res.total || 0);
    setLogTotalPages(res.total_pages || 1);
  }

  useEffect(() => {
    void loadQueue();
  }, [serverId, catalogVersion, tab]);

  useEffect(() => {
    void loadLogs();
  }, [serverId, catalogVersion, tab, debouncedLogSearch, logLevels, logCategories, logSince, logSort, logPage, logPageSize]);

  useEffect(() => {
    void loadHistory();
  }, [serverId, catalogVersion, tab, debouncedHistSearch, histLibs, histStatuses, histInfo, histSince, histSort, histPage, histPageSize]);

  useEffect(() => {
    void loadBlocklist();
  }, [serverId, catalogVersion, tab, debouncedBlockSearch, blockLibs, blockSince, blockSort, blockPage, blockPageSize]);

  async function saveOrder(next: MediaItem[]) {
    setPrio(next);
    await serverApi(serverId, "/queue/reorder", "POST", { keys: next.map((x) => x.key) });
    toast("Updated queue order");
  }

  async function executeConfirmAction() {
    if (!confirmAction) return;
    setConfirmBusy(true);
    try {
      if (confirmAction === "clear_history") {
        await serverApi(serverId, "/history/clear", "POST");
        toast("Cleared scan history");
        setHistPage(1);
        await loadHistory();
      } else if (confirmAction === "retry_all") {
        const r = await serverApi<{ reset: number }>(serverId, "/control/reset-retries", "POST", {});
        toast(`Reset ${r.reset} failed/retry item(s)`);
        await Promise.all([loadHistory(), loadQueue()]);
      } else if (confirmAction === "clear_blocklist") {
        await serverApi(serverId, "/blocklist/clear", "POST");
        toast("Cleared blocklist");
        setBlockPage(1);
        await loadBlocklist();
      } else if (confirmAction === "clear_logs") {
        await serverApi(serverId, "/events/clear", "POST");
        toast("Cleared logs");
        setLogPage(1);
        await loadLogs();
      }
      setConfirmAction(null);
    } finally {
      setConfirmBusy(false);
    }
  }

  const renderSortIcon = (current: string, asc: string, desc: string) => {
    if (current === asc) return <ArrowUp size={12} className="col-sort-icon active" aria-hidden="true" />;
    if (current === desc) return <ArrowDown size={12} className="col-sort-icon active" aria-hidden="true" />;
    return <ArrowUpDown size={12} className="col-sort-icon" aria-hidden="true" />;
  };

  const toggleSort = (
    current: string,
    setter: (s: string) => void,
    asc: string,
    desc: string,
    defaultDir: "asc" | "desc" = "asc"
  ) => {
    if (current === asc) setter(desc);
    else if (current === desc) setter(asc);
    else setter(defaultDir === "asc" ? asc : desc);
  };

  const capLabel = (c: string) => {
    const k = `cap.${c}`;
    const tr = t(k);
    return tr === k ? c : tr;
  };

  const renderItemCapChips = (it: MediaItem, analyzing = false) => {
    const missingSet = new Set(it.missing || []);
    const libMeta = libraries.find((l) => l.title === it.library);
    const disabledSet = new Set(libMeta?.disabled_caps || []);
    const applicable = sortedCapDefs.filter(
      (c) => c.kinds.includes(it.kind || "movie") && !disabledSet.has(c.id)
    );
    if (!applicable.length) return <span className="muted">—</span>;
    return (
      <div className="cap-chip-list">
        {applicable.map((c) => {
          const isMissing = missingSet.has(c.id);
          const chipClass = isMissing ? (analyzing ? "cap-chip-scanning" : "cap-chip-miss") : "cap-chip-ok";
          return (
            <span
              key={c.id}
              className={`cap-chip ${chipClass}`}
              title={`${t(c.labelKey)}: ${isMissing ? (analyzing ? t("status.active") : t("cap.left")) : t("cap.done")}`}
            >
              {isMissing ? (
                analyzing ? <Loader2 size={10} aria-hidden="true" /> : <XIcon size={10} aria-hidden="true" />
              ) : (
                <Check size={10} aria-hidden="true" />
              )}
              <span>{t(c.labelKey)}</span>
            </span>
          );
        })}
      </div>
    );
  };

  const formatSource = (by?: string, reason?: string) => {
    const actor = (by || "").trim() || t("queue.source_system");
    const r = (reason || "").trim();
    if (!r) return actor;
    return `${actor} (${r})`;
  };

  const libOptions = libraries.map((l) => ({ value: l.title, label: l.title }));

  // Unified queue: single ordered list
  const unifiedQueue: (MediaItem & { queuePos: number })[] = [];
  const seenQueueKeys = new Set<string>();
  for (const it of [...prio, ...auto]) {
    if (seenQueueKeys.has(it.key)) continue;
    seenQueueKeys.add(it.key);
    unifiedQueue.push({ ...it, queuePos: unifiedQueue.length + 1 });
  }

  const qTokens = queueSearch.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const matchesQueueFilter = (it: MediaItem) => {
    if (queueLibs.length > 0 && !queueLibs.includes(it.library)) return false;
    if (qTokens.length === 0) return true;
    const hay = `${formatItemTitle(it)} ${it.library} ${it.status} ${it.queued_by || ""} ${it.queued_reason || ""}`.toLowerCase();
    return qTokens.every((tok) => hay.includes(tok));
  };

  const visibleQueue = [...unifiedQueue.filter(matchesQueueFilter)].sort((a, b) => {
    switch (autoSort) {
      case "title_asc":
        return formatItemTitle(a).localeCompare(formatItemTitle(b));
      case "title_desc":
        return formatItemTitle(b).localeCompare(formatItemTitle(a));
      case "library_asc":
        return a.library.localeCompare(b.library);
      case "library_desc":
        return b.library.localeCompare(a.library);
      case "status_asc":
        return a.status.localeCompare(b.status);
      case "status_desc":
        return b.status.localeCompare(a.status);
      case "size_asc":
        return a.bytes - b.bytes;
      case "size_desc":
        return b.bytes - a.bytes;
      default:
        return a.queuePos - b.queuePos;
    }
  });

  const parseHistoryParts = (h: HistoryRow) => {
    const added = [...(h.added_caps || [])];
    const missing = [...(h.missing_caps || [])];
    const errors: string[] = [];
    const raw = (h.detail || "").trim();
    if (raw) {
      const segments = raw.split("|").map((s) => s.trim()).filter(Boolean);
      for (const seg of segments) {
        if (seg.startsWith("added:")) {
          if (added.length === 0) {
            added.push(...seg.slice(6).split(",").map((c) => c.trim()).filter(Boolean));
          }
        } else if (seg.startsWith("missing:")) {
          if (missing.length === 0) {
            missing.push(...seg.slice(8).split(",").map((c) => c.trim()).filter(Boolean));
          }
        } else if (seg.toLowerCase().startsWith("still missing capabilities after verification:")) {
          if (missing.length === 0) {
            missing.push(...seg.slice(seg.indexOf(":") + 1).split(",").map((c) => c.trim()).filter(Boolean));
          }
        } else if (seg.startsWith("error:")) {
          errors.push(seg.slice(6).trim());
        } else {
          errors.push(seg);
        }
      }
    }
    return { added, missing, errors };
  };

  const canDragQueue = canScan && qTokens.length === 0 && queueLibs.length === 0 && autoSort === "";

  const historyExportUrl = (format: "csv" | "json") => {
    const p = new URLSearchParams({
      format,
      search: debouncedHistSearch,
      library: histLibs.join(","),
      status: histStatuses.join(","),
      info: histInfo.join(","),
      since: sinceRangeToUnix(histSince),
      sort: histSort,
    });
    return serverPath(serverId, `/history/export?${p}`);
  };

  const blocklistExportUrl = (format: "csv" | "json") => {
    const p = new URLSearchParams({
      format,
      search: debouncedBlockSearch,
      library: blockLibs.join(","),
      since: sinceRangeToUnix(blockSince),
      sort: blockSort,
    });
    return serverPath(serverId, `/blocklist/export?${p}`);
  };

  const logsExportUrl = (format: "csv" | "json") => {
    const p = new URLSearchParams({
      format,
      level: logLevels.length === 1 ? logLevels[0] : "",
      category: logCategories.length === 1 ? logCategories[0] : "",
      search: debouncedLogSearch,
      since: sinceRangeToUnix(logSince),
      sort: logSort,
    });
    return serverPath(serverId, `/events/export?${p}`);
  };

  const activeElapsed =
    currentItem && currentItemStartedAt > 0 ? formatClock(Math.max(0, nowSec - currentItemStartedAt)) : "";

  const confirmCopy = (() => {
    switch (confirmAction) {
      case "clear_history":
        return {
          title: t("confirm.clear_history_title"),
          message: t("confirm.clear_history_msg"),
          label: t("queue.clear_history"),
          danger: true,
        };
      case "retry_all":
        return {
          title: t("confirm.retry_all_title"),
          message: t("confirm.retry_all_msg"),
          label: t("queue.retry_all_failed"),
          danger: false,
        };
      case "clear_blocklist":
        return {
          title: t("confirm.clear_blocklist_title"),
          message: t("confirm.clear_blocklist_msg"),
          label: t("queue.clear_blocklist"),
          danger: true,
        };
      case "clear_logs":
        return {
          title: t("confirm.clear_logs_title"),
          message: t("confirm.clear_logs_msg"),
          label: t("logs.clear"),
          danger: true,
        };
      default:
        return { title: "", message: "", label: "", danger: true };
    }
  })();

  return (
    <div className="page-view unified-page">
      <div className="section-head-row">
        <div className="tab-bar" role="tablist" aria-label={t("nav.queue")}>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "queue"}
            className={`tab-btn ${tab === "queue" ? "active" : ""}`}
            onClick={() => setTab("queue")}
          >
            <span>{t("queue.tab_queue")}</span>
            <span className="badge st-queued">{fmtNum(unifiedQueue.length)}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "history"}
            className={`tab-btn ${tab === "history" ? "active" : ""}`}
            onClick={() => setTab("history")}
          >
            <span>{t("queue.tab_history")}</span>
            <span className="badge st-pending">{fmtNum(histTotal)}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "blocklist"}
            className={`tab-btn ${tab === "blocklist" ? "active" : ""}`}
            onClick={() => setTab("blocklist")}
          >
            <span>{t("queue.tab_blocklist")}</span>
            <span className="badge st-skipped">{fmtNum(blockTotal)}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "logs"}
            className={`tab-btn ${tab === "logs" ? "active" : ""}`}
            onClick={() => setTab("logs")}
          >
            <span>{t("nav.logs")}</span>
            <span className="badge st-pending">{fmtNum(logTotal)}</span>
          </button>
        </div>
      </div>

      {tab === "queue" && (
        <>
          {/* Currently Scanning */}
          <div className="card form-section">
            <div className="section-head-row">
              <div className="field-label-row">
                <h3>{t("queue.active_heading")}</h3>
                <InfoPopover title={t("queue.active_heading")} text={t("queue.active.tip")} />
              </div>
              {canScan && currentItem && (
                <button type="button" className="btn btn-sm btn-primary" onClick={onConfirmSkip}>
                  <SkipForward size={13} aria-hidden="true" />
                  <span>{t("overview.skip_current")}</span>
                </button>
              )}
            </div>
            {!currentItem ? (
              <div className="small muted">{t("queue.no_active")}</div>
            ) : (
              <div className="priority-row" style={{ borderColor: "var(--cyan)", background: "var(--cyan-soft)", flexDirection: "column", alignItems: "stretch", gap: 10 }}>
                <div className="section-head-row">
                  <div className="action-group" style={{ gap: 10 }}>
                    <span className="badge st-active">{t("status.active").toUpperCase()}</span>
                    <div>
                      <div className="strong">{formatItemTitle(currentItem)}</div>
                      <div className="small muted">
                        {currentItem.library} • {fmtBytes(currentItem.bytes)} • {formatSource(currentItem.queued_by, currentItem.queued_reason)}
                      </div>
                    </div>
                  </div>
                  {activeElapsed && (
                    <span className="badge st-active tabular-nums">
                      <Clock size={12} aria-hidden="true" />
                      <span>{activeElapsed}</span>
                    </span>
                  )}
                </div>
                {renderItemCapChips(currentItem, true)}
              </div>
            )}
          </div>

          {/* Incoming *arr Auto-Scan Triggers (Season / Album Coalescing) */}
          {pendingTriggers.length > 0 && (
            <div className="card form-section">
              <div className="section-head-row">
                <div className="field-label-row">
                  <h3>{t("arr.pending_heading")} ({fmtNum(pendingTriggers.length)})</h3>
                  <InfoPopover title={t("arr.pending_heading")} text={t("arr.pending.tip")} />
                </div>
                {canScan && (
                  <button
                    type="button"
                    className="btn btn-sm btn-primary"
                    onClick={async () => {
                      await serverApi(serverId, "/arr-triggers/flush", "POST");
                      toast(t("arr.flushed_toast"));
                    }}
                  >
                    <RefreshCw size={13} aria-hidden="true" />
                    <span>{t("arr.flush_now")}</span>
                  </button>
                )}
              </div>
              {pendingTriggers.map((tr) => {
                const rem = Math.max(0, tr.fires_at - nowSec);
                return (
                  <div key={tr.id} className="priority-row">
                    <div className="action-group" style={{ gap: 8, flexWrap: "wrap" }}>
                      <span className="badge st-queued">{tr.source}</span>
                      <strong>{tr.library}</strong>
                      <span className="small muted">{tr.dir}</span>
                      <span className="badge st-pending">
                        {fmtNum(tr.file_count)} {tr.file_count === 1 ? t("arr.file_one") : t("arr.file_many")}
                      </span>
                    </div>
                    <span className={`badge tabular-nums ${tr.state === "debouncing" ? "st-paused" : "st-active"}`}>
                      {tr.state === "debouncing"
                        ? `${t("arr.state_debouncing")} ${formatClock(rem)}`
                        : tr.state === "refreshing_plex"
                          ? t("arr.state_refreshing")
                          : t("arr.state_syncing")}
                    </span>
                  </div>
                );
              })}
            </div>
          )}

          {/* Unified Queue */}
          <div className="card form-section">
            <div className="section-head-row">
              <div className="field-label-row">
                <h3>{t("queue.tab_queue")} ({fmtNum(visibleQueue.length)})</h3>
                <InfoPopover title={t("queue.tab_queue")} text={t("queue.list.tip")} />
              </div>
            </div>
            <div className="explorer-toolbar" style={{ marginBottom: 4 }}>
              <input
                type="search"
                aria-label={t("explorer.search_placeholder")}
                placeholder={t("explorer.search_placeholder")}
                value={queueSearch}
                onChange={(e) => setQueueSearch(e.target.value)}
              />
              {(queueSearch !== "" || queueLibs.length > 0 || autoSort !== "") && (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => {
                    setQueueSearch("");
                    setQueueLibs([]);
                    setAutoSort("");
                  }}
                >
                  {t("explorer.reset_filters")}
                </button>
              )}
            </div>
            <div className="table-wrap table-wrap-filters table-wrap-paged">
              <table className="media-table media-table-fixed">
                <thead>
                  <tr>
                    <th scope="col" style={{ width: "56px" }}>#</th>
                    <th scope="col" style={{ width: "24%" }}>
                      <button
                        type="button"
                        className={`th-sort-btn ${autoSort === "title_asc" || autoSort === "title_desc" ? "active" : ""}`}
                        onClick={() => toggleSort(autoSort, setAutoSort, "title_asc", "title_desc")}
                      >
                        <span>{t("explorer.col.title")}</span>
                        {renderSortIcon(autoSort, "title_asc", "title_desc")}
                      </button>
                    </th>
                    <th scope="col" style={{ width: "11%" }}>
                      <div className="th-control-row">
                        <button
                          type="button"
                          className={`th-sort-btn ${autoSort === "library_asc" || autoSort === "library_desc" ? "active" : ""}`}
                          onClick={() => toggleSort(autoSort, setAutoSort, "library_asc", "library_desc")}
                        >
                          <span>{t("explorer.col.library")}</span>
                          {renderSortIcon(autoSort, "library_asc", "library_desc")}
                        </button>
                        <MultiSelectFilter compact label={t("explorer.libraries")} options={libOptions} selected={queueLibs} onChange={setQueueLibs} />
                      </div>
                    </th>
                    <th scope="col" style={{ width: "10%" }}>
                      <button
                        type="button"
                        className={`th-sort-btn ${autoSort === "status_asc" || autoSort === "status_desc" ? "active" : ""}`}
                        onClick={() => toggleSort(autoSort, setAutoSort, "status_asc", "status_desc")}
                      >
                        <span>{t("explorer.col.status")}</span>
                        {renderSortIcon(autoSort, "status_asc", "status_desc")}
                      </button>
                    </th>
                    <th scope="col" style={{ width: "22%" }}>{t("explorer.col.capabilities")}</th>
                    <th scope="col" style={{ width: "12%" }}>{t("queue.col_source")}</th>
                    <th scope="col" style={{ width: "9%" }}>
                      <button
                        type="button"
                        className={`th-sort-btn ${autoSort === "size_asc" || autoSort === "size_desc" ? "active" : ""}`}
                        onClick={() => toggleSort(autoSort, setAutoSort, "size_asc", "size_desc", "desc")}
                      >
                        <span>{t("explorer.col.size")}</span>
                        {renderSortIcon(autoSort, "size_asc", "size_desc")}
                      </button>
                    </th>
                    {canScan && <th scope="col" style={{ width: "184px" }}>{t("explorer.col.actions")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {!visibleQueue.length ? (
                    <tr>
                      <td colSpan={canScan ? 8 : 7} className="muted">
                        {t("queue.auto_empty")}
                      </td>
                    </tr>
                  ) : (
                    visibleQueue.map((it) => {
                      const fromIdx = dragKey ? unifiedQueue.findIndex((x) => x.key === dragKey) : -1;
                      const overIdx = dragOverKey === it.key ? unifiedQueue.findIndex((x) => x.key === it.key) : -1;
                      const dropClass =
                        dragKey && dragKey !== it.key && overIdx >= 0
                          ? fromIdx < overIdx
                            ? "drop-after"
                            : "drop-before"
                          : "";
                      return (
                        <tr
                          key={it.key}
                          draggable={canDragQueue}
                          className={`${dragKey === it.key ? "dragging" : ""} ${dropClass} ${canDragQueue ? "row-draggable" : ""}`}
                          onDragStart={(e) => {
                            setDragKey(it.key);
                            e.dataTransfer.effectAllowed = "move";
                            e.dataTransfer.setData("text/plain", it.key);
                          }}
                          onDragOver={(e) => {
                            if (!canDragQueue || !dragKey) return;
                            e.preventDefault();
                            e.dataTransfer.dropEffect = "move";
                            if (dragOverKey !== it.key) setDragOverKey(it.key);
                          }}
                          onDrop={(e) => {
                            e.preventDefault();
                            setDragOverKey(null);
                            if (!dragKey || dragKey === it.key) {
                              setDragKey(null);
                              return;
                            }
                            const from = unifiedQueue.findIndex((x) => x.key === dragKey);
                            const to = unifiedQueue.findIndex((x) => x.key === it.key);
                            if (from < 0 || to < 0) {
                              setDragKey(null);
                              return;
                            }
                            const next = [...unifiedQueue];
                            next.splice(to, 0, next.splice(from, 1)[0]);
                            setDragKey(null);
                            void saveOrder(next);
                          }}
                          onDragEnd={() => {
                            setDragKey(null);
                            setDragOverKey(null);
                          }}
                        >
                          <td className="tabular-nums">
                            <span className="action-group" style={{ gap: 4 }}>
                              {canDragQueue && <GripVertical size={13} className="muted" aria-hidden="true" />}
                              <span>{fmtNum(it.queuePos)}</span>
                            </span>
                          </td>
                          <td className="strong cell-ellipsis" title={formatItemTitle(it)}>
                            {formatItemTitle(it)}
                          </td>
                          <td className="cell-ellipsis">{it.library}</td>
                          <td>
                            <span className={`badge st-${it.status}`}>{t(`status.${it.status}`)}</span>
                          </td>
                          <td>{renderItemCapChips(it)}</td>
                          <td className="cell-ellipsis" title={formatSource(it.queued_by, it.queued_reason)}>
                            {formatSource(it.queued_by, it.queued_reason)}
                          </td>
                          <td className="tabular-nums">{fmtBytes(it.bytes)}</td>
                          {canScan && (
                            <td>
                              <div className="action-group">
                                {it.queuePos > 1 && (
                                  <button
                                    type="button"
                                    className="btn btn-xs btn-primary"
                                    title={t("queue.move_to_top")}
                                    onClick={async () => {
                                      await serverApi(serverId, "/queue/move-top", "POST", { key: it.key });
                                      toast(`Moved ${it.title} to top`);
                                      void loadQueue();
                                    }}
                                  >
                                    <ArrowUpToLine size={12} aria-hidden="true" />
                                    <span>{t("queue.move_to_top")}</span>
                                  </button>
                                )}
                                <button
                                  type="button"
                                  className="btn btn-xs btn-primary"
                                  onClick={async () => {
                                    await serverApi(serverId, "/items/skip", "POST", { key: it.key, skip: true });
                                    toast(`Blocklisted ${it.title}`);
                                    void loadQueue();
                                    void loadBlocklist();
                                  }}
                                >
                                  <ShieldBan size={12} aria-hidden="true" />
                                  <span>{t("explorer.skip")}</span>
                                </button>
                              </div>
                            </td>
                          )}
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}

      {tab === "history" && (
        <div className="card form-section">
          <div className="section-head-row">
            <div className="field-label-row">
              <h3>{t("queue.history_heading", { count: fmtNum(histTotal) })}</h3>
              <InfoPopover title={t("queue.tab_history")} text={t("queue.history.tip")} />
            </div>
            {canScan && (
              <div className="action-group">
                <button
                  type="button"
                  className="btn btn-sm btn-primary"
                  onClick={() => setConfirmAction("retry_all")}
                >
                  <RotateCcw size={13} aria-hidden="true" />
                  <span>{t("queue.retry_all_failed")}</span>
                </button>
                <button
                  type="button"
                  className="btn btn-sm btn-danger"
                  disabled={histTotal === 0}
                  onClick={() => setConfirmAction("clear_history")}
                >
                  <Trash2 size={13} aria-hidden="true" />
                  <span>{t("queue.clear_history")}</span>
                </button>
              </div>
            )}
          </div>
          <div className="explorer-toolbar" style={{ marginBottom: 4 }}>
            <input
              type="search"
              aria-label={t("explorer.search_placeholder")}
              placeholder={t("explorer.search_placeholder")}
              value={histSearch}
              onChange={(e) => {
                setHistSearch(e.target.value);
                setHistPage(1);
              }}
            />
            <div style={{ width: 150 }}>
              <SelectMenu
                compact
                ariaLabel={t("filter.time_range")}
                value={histSince}
                options={timeRangeOptions}
                onChange={(v) => {
                  setHistSince(v);
                  setHistPage(1);
                }}
              />
            </div>
            <div className="action-group">
              {(histSearch !== "" || histLibs.length > 0 || histStatuses.length > 0 || histInfo.length > 0 || histSince !== "" || histSort !== "finished_desc") && (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => {
                    setHistSearch("");
                    setHistLibs([]);
                    setHistStatuses([]);
                    setHistInfo([]);
                    setHistSince("");
                    setHistSort("finished_desc");
                    setHistPage(1);
                  }}
                >
                  {t("explorer.reset_filters")}
                </button>
              )}
              <a className="btn btn-sm btn-ghost" href={historyExportUrl("csv")} style={{ textDecoration: "none" }}>
                <Download size={13} aria-hidden="true" />
                <span>CSV</span>
              </a>
              <a className="btn btn-sm btn-ghost" href={historyExportUrl("json")} style={{ textDecoration: "none" }}>
                <Download size={13} aria-hidden="true" />
                <span>JSON</span>
              </a>
            </div>
          </div>
          <div className="table-wrap table-wrap-filters table-wrap-paged">
            <table className="media-table media-table-fixed">
              <thead>
                <tr>
                  <th scope="col" style={{ width: "12%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${histSort === "finished_asc" || histSort === "finished_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(histSort, setHistSort, "finished_asc", "finished_desc", "desc");
                        setHistPage(1);
                      }}
                    >
                      <span>{t("logs.col_time")}</span>
                      {renderSortIcon(histSort, "finished_asc", "finished_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "18%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${histSort === "title_asc" || histSort === "title_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(histSort, setHistSort, "title_asc", "title_desc");
                        setHistPage(1);
                      }}
                    >
                      <span>{t("explorer.col.title")}</span>
                      {renderSortIcon(histSort, "title_asc", "title_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "9%" }}>
                    <div className="th-control-row">
                      <button
                        type="button"
                        className={`th-sort-btn ${histSort === "library_asc" || histSort === "library_desc" ? "active" : ""}`}
                        onClick={() => {
                          toggleSort(histSort, setHistSort, "library_asc", "library_desc");
                          setHistPage(1);
                        }}
                      >
                        <span>{t("explorer.col.library")}</span>
                        {renderSortIcon(histSort, "library_asc", "library_desc")}
                      </button>
                      <MultiSelectFilter
                        compact
                        label={t("explorer.libraries")}
                        options={libOptions}
                        selected={histLibs}
                        onChange={(next) => {
                          setHistLibs(next);
                          setHistPage(1);
                        }}
                      />
                    </div>
                  </th>
                  <th scope="col" style={{ width: "9%" }}>
                    <div className="th-control-row">
                      <button
                        type="button"
                        className={`th-sort-btn ${histSort === "status_asc" || histSort === "status_desc" ? "active" : ""}`}
                        onClick={() => {
                          toggleSort(histSort, setHistSort, "status_asc", "status_desc");
                          setHistPage(1);
                        }}
                      >
                        <span>{t("explorer.col.status")}</span>
                        {renderSortIcon(histSort, "status_asc", "status_desc")}
                      </button>
                      <MultiSelectFilter
                        compact
                        align="right"
                        label={t("explorer.statuses")}
                        options={[
                          { value: "complete", label: t("status.complete") },
                          { value: "retry", label: t("status.retry") },
                          { value: "exhausted", label: t("status.exhausted") },
                        ]}
                        selected={histStatuses}
                        onChange={(next) => {
                          setHistStatuses(next);
                          setHistPage(1);
                        }}
                      />
                    </div>
                  </th>
                  <th scope="col" style={{ width: "13%" }}>{t("queue.col_added_caps")}</th>
                  <th scope="col" style={{ width: "14%" }}>
                    <div className="th-control-row">
                      <span>{t("queue.col_missing_error")}</span>
                      <MultiSelectFilter
                        compact
                        align="right"
                        label={t("queue.col_info")}
                        options={[
                          { value: "added", label: t("queue.info_added") },
                          { value: "missing", label: t("queue.info_missing") },
                          { value: "error", label: t("queue.info_error") },
                        ]}
                        selected={histInfo}
                        onChange={(next) => {
                          setHistInfo(next);
                          setHistPage(1);
                        }}
                      />
                    </div>
                  </th>
                  <th scope="col" style={{ width: "7%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${histSort === "duration_asc" || histSort === "duration_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(histSort, setHistSort, "duration_asc", "duration_desc", "desc");
                        setHistPage(1);
                      }}
                    >
                      <span>{t("queue.col_duration")}</span>
                      {renderSortIcon(histSort, "duration_asc", "duration_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "7%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${histSort === "size_asc" || histSort === "size_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(histSort, setHistSort, "size_asc", "size_desc", "desc");
                        setHistPage(1);
                      }}
                    >
                      <span>{t("explorer.col.size")}</span>
                      {renderSortIcon(histSort, "size_asc", "size_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "10%" }}>{t("queue.col_source")}</th>
                  {canScan && <th scope="col" style={{ width: "152px" }}>{t("explorer.col.actions")}</th>}
                </tr>
              </thead>
              <tbody>
                {!history.length ? (
                  <tr>
                    <td colSpan={canScan ? 10 : 9} className="muted">
                      {t("queue.history_empty")}
                    </td>
                  </tr>
                ) : (
                  history.map((h) => {
                    const canRetry = h.status === "exhausted" || h.status === "retry" || h.status === "incomplete";
                    const { added, missing, errors } = parseHistoryParts(h);
                    return (
                      <tr key={h.id}>
                        <td className="tabular-nums">{fmtTime(h.finished_at)}</td>
                        <td className="strong cell-ellipsis" title={h.title}>
                          {h.title}
                        </td>
                        <td className="cell-ellipsis">{h.library}</td>
                        <td>
                          <span className={`badge st-${h.status === "complete" ? "complete" : h.status === "retry" ? "retry" : "exhausted"}`}>
                            {t(`status.${h.status}`, { defaultValue: h.status })}
                          </span>
                        </td>
                        <td>
                          {added.length > 0 ? (
                            <div className="cap-chip-list">
                              {added.map((c) => (
                                <span key={`a-${c}`} className="cap-chip cap-chip-ok">
                                  <Check size={10} aria-hidden="true" />
                                  <span>{capLabel(c)}</span>
                                </span>
                              ))}
                            </div>
                          ) : h.status === "complete" ? (
                            <span className="badge st-complete">{t("queue.info_all_verified")}</span>
                          ) : (
                            <span className="muted">—</span>
                          )}
                        </td>
                        <td>
                          {missing.length > 0 || errors.length > 0 ? (
                            <div className="cap-chip-list">
                              {missing.map((c) => (
                                <span key={`m-${c}`} className="cap-chip cap-chip-miss">
                                  <XIcon size={10} aria-hidden="true" />
                                  <span>{capLabel(c)}</span>
                                </span>
                              ))}
                              {errors.map((err, i) => (
                                <span key={`e-${i}`} className="action-group" style={{ gap: 4 }}>
                                  <span className="badge st-exhausted">{t("queue.info_error")}</span>
                                  <InfoPopover title={t("queue.info_error")} text={err} />
                                </span>
                              ))}
                            </div>
                          ) : (
                            <span className="muted">—</span>
                          )}
                        </td>
                        <td className="tabular-nums">{formatDurationMs(h.duration_ms)}</td>
                        <td className="tabular-nums">{h.bytes ? fmtBytes(h.bytes) : "—"}</td>
                        <td className="cell-ellipsis" title={formatSource(h.queued_by, h.queued_reason)}>
                          {formatSource(h.queued_by, h.queued_reason)}
                        </td>
                        {canScan && (
                          <td>
                            <div className="action-group" style={{ gap: 6 }}>
                              {canRetry && (
                                <button
                                  type="button"
                                  className="btn btn-xs btn-primary"
                                  onClick={async () => {
                                    await serverApi(serverId, "/control/reset-retries", "POST", { keys: [h.item_key] });
                                    await serverApi(serverId, "/items/enqueue", "POST", { keys: [h.item_key], front: true });
                                    toast(`Retrying ${h.title}`);
                                    void loadQueue();
                                  }}
                                >
                                  <RotateCcw size={12} aria-hidden="true" />
                                  <span>{t("explorer.retry")}</span>
                                </button>
                              )}
                              <button
                                type="button"
                                className="btn btn-xs btn-primary"
                                onClick={async () => {
                                  await serverApi(serverId, "/items/skip", "POST", { key: h.item_key, skip: true });
                                  toast(`Blocklisted ${h.title}`);
                                  void loadBlocklist();
                                  void loadQueue();
                                }}
                              >
                                <ShieldBan size={12} aria-hidden="true" />
                                <span>{t("explorer.skip")}</span>
                              </button>
                            </div>
                          </td>
                        )}
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
          <PaginationBar
            page={histPage}
            totalPages={histTotalPages}
            pageSize={histPageSize}
            total={histTotal}
            unitLabel={t("pagination.items")}
            fmtNum={fmtNum}
            onPageChange={setHistPage}
            onPageSizeChange={setHistPageSize}
          />
        </div>
      )}

      {tab === "blocklist" && (
        <div className="card form-section">
          <div className="section-head-row">
            <div className="field-label-row">
              <h3>{t("queue.blocklist_heading", { count: fmtNum(blockTotal) })}</h3>
              <InfoPopover title={t("queue.tab_blocklist")} text={t("queue.blocklist.tip")} />
            </div>
            {canScan && blockTotal > 0 && (
              <button
                type="button"
                className="btn btn-sm btn-danger"
                onClick={() => setConfirmAction("clear_blocklist")}
              >
                <Trash2 size={13} aria-hidden="true" />
                <span>{t("queue.clear_blocklist")}</span>
              </button>
            )}
          </div>
          <div className="explorer-toolbar" style={{ marginBottom: 4 }}>
            <input
              type="search"
              aria-label={t("explorer.search_placeholder")}
              placeholder={t("explorer.search_placeholder")}
              value={blockSearch}
              onChange={(e) => {
                setBlockSearch(e.target.value);
                setBlockPage(1);
              }}
            />
            <div style={{ width: 150 }}>
              <SelectMenu
                compact
                ariaLabel={t("filter.time_range")}
                value={blockSince}
                options={timeRangeOptions}
                onChange={(v) => {
                  setBlockSince(v);
                  setBlockPage(1);
                }}
              />
            </div>
            <div className="action-group">
              {(blockSearch !== "" || blockLibs.length > 0 || blockSince !== "" || blockSort !== "blocklisted_desc") && (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => {
                    setBlockSearch("");
                    setBlockLibs([]);
                    setBlockSince("");
                    setBlockSort("blocklisted_desc");
                    setBlockPage(1);
                  }}
                >
                  {t("explorer.reset_filters")}
                </button>
              )}
              <a className="btn btn-sm btn-ghost" href={blocklistExportUrl("csv")} style={{ textDecoration: "none" }}>
                <Download size={13} aria-hidden="true" />
                <span>CSV</span>
              </a>
              <a className="btn btn-sm btn-ghost" href={blocklistExportUrl("json")} style={{ textDecoration: "none" }}>
                <Download size={13} aria-hidden="true" />
                <span>JSON</span>
              </a>
            </div>
          </div>
          <div className="table-wrap table-wrap-filters table-wrap-paged">
            <table className="media-table media-table-fixed">
              <thead>
                <tr>
                  <th scope="col" style={{ width: "14%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${blockSort === "blocklisted_asc" || blockSort === "blocklisted_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(blockSort, setBlockSort, "blocklisted_asc", "blocklisted_desc", "desc");
                        setBlockPage(1);
                      }}
                    >
                      <span>{t("queue.col_blocklisted_at")}</span>
                      {renderSortIcon(blockSort, "blocklisted_asc", "blocklisted_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "26%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${blockSort === "title_asc" || blockSort === "title_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(blockSort, setBlockSort, "title_asc", "title_desc");
                        setBlockPage(1);
                      }}
                    >
                      <span>{t("explorer.col.title")}</span>
                      {renderSortIcon(blockSort, "title_asc", "title_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "13%" }}>
                    <div className="th-control-row">
                      <button
                        type="button"
                        className={`th-sort-btn ${blockSort === "library_asc" || blockSort === "library_desc" ? "active" : ""}`}
                        onClick={() => {
                          toggleSort(blockSort, setBlockSort, "library_asc", "library_desc");
                          setBlockPage(1);
                        }}
                      >
                        <span>{t("explorer.col.library")}</span>
                        {renderSortIcon(blockSort, "library_asc", "library_desc")}
                      </button>
                      <MultiSelectFilter
                        compact
                        label={t("explorer.libraries")}
                        options={libOptions}
                        selected={blockLibs}
                        onChange={(next) => {
                          setBlockLibs(next);
                          setBlockPage(1);
                        }}
                      />
                    </div>
                  </th>
                  <th scope="col" style={{ width: "24%" }}>{t("explorer.col.capabilities")}</th>
                  <th scope="col" style={{ width: "12%" }}>{t("queue.col_blocklisted_by")}</th>
                  {canScan && <th scope="col" style={{ width: "120px" }}>{t("explorer.col.actions")}</th>}
                </tr>
              </thead>
              <tbody>
                {!blocklist.length ? (
                  <tr>
                    <td colSpan={canScan ? 6 : 5} className="muted">
                      {t("queue.blocklist_empty")}
                    </td>
                  </tr>
                ) : (
                  blocklist.map((it) => (
                    <tr key={it.key}>
                      <td className="tabular-nums">{fmtTime(it.blocklisted_at || it.updated_at || it.added_at)}</td>
                      <td className="strong cell-ellipsis" title={formatItemTitle(it)}>
                        {formatItemTitle(it)}
                      </td>
                      <td className="cell-ellipsis">{it.library}</td>
                      <td>{renderItemCapChips(it)}</td>
                      <td className="cell-ellipsis">{it.blocklisted_by || "—"}</td>
                      {canScan && (
                        <td>
                          <button
                            type="button"
                            className="btn btn-xs btn-primary"
                            onClick={async () => {
                              await serverApi(serverId, "/items/skip", "POST", { key: it.key, skip: false });
                              toast(`Unblocked ${it.title}`);
                              void loadBlocklist();
                              void loadQueue();
                            }}
                          >
                            <ShieldCheck size={12} aria-hidden="true" />
                            <span>{t("explorer.unskip")}</span>
                          </button>
                        </td>
                      )}
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
          <PaginationBar
            page={blockPage}
            totalPages={blockTotalPages}
            pageSize={blockPageSize}
            total={blockTotal}
            unitLabel={t("pagination.items")}
            fmtNum={fmtNum}
            onPageChange={setBlockPage}
            onPageSizeChange={setBlockPageSize}
          />
        </div>
      )}

      {tab === "logs" && (
        <div className="card form-section">
          <div className="section-head-row">
            <div className="field-label-row">
              <h3>{t("logs.entries_heading", { count: fmtNum(logTotal) })}</h3>
              <InfoPopover title={t("nav.logs")} text={t("logs.entries.tip")} />
            </div>
            <div className="action-group">
              <button type="button" className="btn btn-sm btn-primary" onClick={() => void loadLogs()}>
                <RefreshCw size={13} aria-hidden="true" />
                <span>{t("logs.refresh")}</span>
              </button>
              {canScan && logTotal > 0 && (
                <button
                  type="button"
                  className="btn btn-sm btn-danger"
                  onClick={() => setConfirmAction("clear_logs")}
                >
                  <Trash2 size={13} aria-hidden="true" />
                  <span>{t("logs.clear")}</span>
                </button>
              )}
            </div>
          </div>
          <div className="explorer-toolbar" style={{ marginBottom: 4 }}>
            <input
              type="search"
              aria-label={t("logs.search_placeholder")}
              placeholder={t("logs.search_placeholder")}
              value={logSearch}
              onChange={(e) => {
                setLogSearch(e.target.value);
                setLogPage(1);
              }}
            />
            <div style={{ width: 150 }}>
              <SelectMenu
                compact
                ariaLabel={t("filter.time_range")}
                value={logSince}
                options={timeRangeOptions}
                onChange={(v) => {
                  setLogSince(v);
                  setLogPage(1);
                }}
              />
            </div>
            <div className="action-group">
              {(logSearch !== "" || logLevels.length > 0 || logCategories.length > 0 || logSince !== "" || logSort !== "ts_desc") && (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => {
                    setLogSearch("");
                    setLogLevels([]);
                    setLogCategories([]);
                    setLogSince("");
                    setLogSort("ts_desc");
                    setLogPage(1);
                  }}
                >
                  {t("explorer.reset_filters")}
                </button>
              )}
              <a className="btn btn-sm btn-ghost" href={logsExportUrl("csv")} style={{ textDecoration: "none" }}>
                <Download size={13} aria-hidden="true" />
                <span>CSV</span>
              </a>
              <a className="btn btn-sm btn-ghost" href={logsExportUrl("json")} style={{ textDecoration: "none" }}>
                <Download size={13} aria-hidden="true" />
                <span>JSON</span>
              </a>
            </div>
          </div>
          <div className="table-wrap table-wrap-filters table-wrap-paged">
            <table className="media-table media-table-fixed">
              <thead>
                <tr>
                  <th scope="col" style={{ width: "14%" }}>
                    <button
                      type="button"
                      className={`th-sort-btn ${logSort === "ts_asc" || logSort === "ts_desc" ? "active" : ""}`}
                      onClick={() => {
                        toggleSort(logSort, setLogSort, "ts_asc", "ts_desc", "desc");
                        setLogPage(1);
                      }}
                    >
                      <span>{t("logs.col_time")}</span>
                      {renderSortIcon(logSort, "ts_asc", "ts_desc")}
                    </button>
                  </th>
                  <th scope="col" style={{ width: "12%" }}>
                    <div className="th-control-row">
                      <span>{t("logs.col_level")}</span>
                      <MultiSelectFilter
                        compact
                        label={t("logs.col_level")}
                        options={[
                          { value: "info", label: "INFO" },
                          { value: "warning", label: "WARNING" },
                          { value: "error", label: "ERROR" },
                        ]}
                        selected={logLevels}
                        onChange={(next) => {
                          setLogLevels(next);
                          setLogPage(1);
                        }}
                      />
                    </div>
                  </th>
                  <th scope="col" style={{ width: "13%" }}>
                    <div className="th-control-row">
                      <span>{t("logs.col_category")}</span>
                      <MultiSelectFilter
                        compact
                        label={t("logs.col_category")}
                        options={[
                          { value: "scan", label: t("logs.cat.scan") },
                          { value: "sync", label: t("logs.cat.sync") },
                          { value: "webhook", label: t("logs.cat.webhook") },
                          { value: "budget", label: t("logs.cat.budget") },
                          { value: "control", label: t("logs.cat.control") },
                          { value: "config", label: t("logs.cat.config") },
                          { value: "auth", label: t("logs.cat.auth") },
                        ]}
                        selected={logCategories}
                        onChange={(next) => {
                          setLogCategories(next);
                          setLogPage(1);
                        }}
                      />
                    </div>
                  </th>
                  <th scope="col" style={{ width: "11%" }}>{t("logs.col_user")}</th>
                  <th scope="col" style={{ width: "50%" }}>{t("queue.col_info")}</th>
                </tr>
              </thead>
              <tbody>
                {!events.length ? (
                  <tr>
                    <td colSpan={5} className="muted">
                      {t("logs.no_events")}
                    </td>
                  </tr>
                ) : (
                  events.map((ev) => {
                    const lvlClass =
                      ev.level === "error" ? "st-exhausted" : ev.level === "warning" ? "st-paused" : "st-active";
                    return (
                      <tr key={ev.id}>
                        <td className="tabular-nums">{fmtTime(ev.ts)}</td>
                        <td>
                          <span className={`badge ${lvlClass}`}>{ev.level.toUpperCase()}</span>
                        </td>
                        <td>{ev.category}</td>
                        <td className="cell-ellipsis">{ev.username || t("queue.source_system")}</td>
                        <td style={{ wordBreak: "break-word" }}>{ev.message}</td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
          <PaginationBar
            page={logPage}
            totalPages={logTotalPages}
            pageSize={logPageSize}
            total={logTotal}
            unitLabel={t("pagination.events")}
            fmtNum={fmtNum}
            onPageChange={setLogPage}
            onPageSizeChange={setLogPageSize}
          />
        </div>
      )}

      <ConfirmModal
        open={confirmAction !== null}
        title={confirmCopy.title}
        message={confirmCopy.message}
        confirmLabel={confirmCopy.label}
        danger={confirmCopy.danger}
        busy={confirmBusy}
        onConfirm={() => void executeConfirmAction()}
        onCancel={() => setConfirmAction(null)}
      />
    </div>
  );
}
