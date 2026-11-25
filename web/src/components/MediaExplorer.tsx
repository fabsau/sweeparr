import { useEffect, useRef, useState, type MouseEvent, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { ArrowDown, ArrowUp, ArrowUpDown, Check, Play, RotateCcw, ShieldBan, ShieldCheck, X as XIcon } from "lucide-react";
import { formatItemTitle, serverApi, serverPath, useDebouncedValue } from "../api";
import { useLocaleFormat } from "../utils/format";
import { stripControlChars } from "../utils/sanitize";
import { Checkbox, InfoPopover, MultiSelectFilter, PaginationBar } from "./Controls";
import type { GroupRow, LibrarySummary, MediaItem } from "../types";

export const CAP_DEFS: { id: string; labelKey: string; tipKey: string; kinds: string[] }[] = [
  { id: "ads", labelKey: "cap.ads", tipKey: "cap.ads.tip", kinds: ["movie", "episode"] },
  { id: "chapters", labelKey: "cap.chapters", tipKey: "cap.chapters.tip", kinds: ["movie", "episode"] },
  { id: "credits", labelKey: "cap.credits", tipKey: "cap.credits.tip", kinds: ["movie", "episode"] },
  { id: "deep_analysis", labelKey: "cap.deep_analysis", tipKey: "cap.deep_analysis.tip", kinds: ["movie", "episode"] },
  { id: "intro", labelKey: "cap.intro", tipKey: "cap.intro.tip", kinds: ["episode"] },
  { id: "loudness", labelKey: "cap.loudness", tipKey: "cap.loudness.tip", kinds: ["movie", "episode", "track"] },
  { id: "previews", labelKey: "cap.previews", tipKey: "cap.previews.tip", kinds: ["movie", "episode"] },
  { id: "sonic", labelKey: "cap.sonic", tipKey: "cap.sonic.tip", kinds: ["track"] },
  { id: "voice", labelKey: "cap.voice", tipKey: "cap.voice.tip", kinds: ["movie", "episode"] },
];

const STATUS_DEFS: { value: string; labelKey: string }[] = [
  { value: "active", labelKey: "status.active" },
  { value: "pending", labelKey: "status.pending" },
  { value: "retry", labelKey: "status.retry" },
  { value: "exhausted", labelKey: "status.exhausted" },
  { value: "skipped", labelKey: "status.skipped" },
  { value: "complete", labelKey: "status.complete" },
];

export function MediaExplorer({
  serverId,
  libraries,
  libraryCapabilities,
  catalogVersion,
  defaultPageSize,
  dateLocale,
  timeFormat = "24h",
  canScan,
  explorerRef,
  statusFilters,
  setStatusFilters,
  viewMode,
  setViewMode,
  fmtNum,
  onRefreshStatus,
  toast,
}: {
  serverId: string;
  libraries: LibrarySummary[];
  libraryCapabilities?: Record<string, string[]>;
  catalogVersion: number;
  defaultPageSize: number;
  dateLocale: string;
  timeFormat?: string;
  canScan: boolean;
  explorerRef: RefObject<HTMLElement | null>;
  statusFilters: string[];
  setStatusFilters: (next: string[]) => void;
  viewMode: "flat" | "grouped";
  setViewMode: (next: "flat" | "grouped") => void;
  fmtNum: (n: number) => string;
  onRefreshStatus: () => void;
  toast: (msg: string) => void;
}) {
  const { t } = useTranslation();
  const { fmtBytes } = useLocaleFormat();
  const storageKey = `sweeparr_explorer_${serverId}`;
  const loadStored = () => {
    try {
      return JSON.parse(sessionStorage.getItem(storageKey) || "{}") as {
        search?: string;
        libFilters?: string[];
        capFilters?: string[];
        sort?: string;
        pageSize?: number;
      };
    } catch {
      return {};
    }
  };
  const initialStored = loadStored();
  const [search, setSearch] = useState(initialStored.search || "");
  const debouncedSearch = useDebouncedValue(search, 200);
  const [libFilters, setLibFilters] = useState<string[]>(initialStored.libFilters || []);
  const [capFilters, setCapFilters] = useState<string[]>(initialStored.capFilters || []);
  const [sort, setSort] = useState(initialStored.sort || "season_first");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(initialStored.pageSize || defaultPageSize || 50);

  useEffect(() => {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify({ search, libFilters, capFilters, sort, pageSize }));
    } catch {
      // ignore storage errors
    }
  }, [storageKey, search, libFilters, capFilters, sort, pageSize]);
  const [items, setItems] = useState<MediaItem[]>([]);
  const [groups, setGroups] = useState<GroupRow[]>([]);
  const [selectedKeys, setSelectedKeys] = useState<string[]>([]);
  const [allMatchingSelected, setAllMatchingSelected] = useState(false);
  const [anchorIndex, setAnchorIndex] = useState<number | null>(null);
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(1);

  const statusParam = statusFilters.join(",");
  const libParam = libFilters.join(",");
  const capParam = capFilters.join(",");

  const sortedCapDefs = [...CAP_DEFS].sort((a, b) => t(a.labelKey).localeCompare(t(b.labelKey)));

  async function loadExplorer() {
    const p = new URLSearchParams({
      search: debouncedSearch,
      status: statusParam,
      library: libParam,
      capability: capParam,
      sort,
      page: String(page),
      page_size: String(pageSize),
    });
    if (viewMode === "grouped") {
      const res = await serverApi<{ groups: GroupRow[]; total: number; total_pages: number }>(serverId, `/groups?${p}`);
      setGroups(res.groups || []);
      setTotal(res.total || 0);
      setTotalPages(res.total_pages || 1);
    } else {
      const res = await serverApi<{ items: MediaItem[]; total: number; total_pages: number }>(serverId, `/items?${p}`);
      setItems(res.items || []);
      setTotal(res.total || 0);
      setTotalPages(res.total_pages || 1);
    }
  }

  useEffect(() => {
    void loadExplorer();
  }, [serverId, viewMode, debouncedSearch, statusParam, libParam, capParam, sort, page, pageSize, catalogVersion]);

  function exportUrl(format: "csv" | "json") {
    const p = new URLSearchParams({
      format,
      search,
      status: statusParam,
      library: libParam,
      capability: capParam,
      sort,
    });
    return serverPath(serverId, `/export?${p}`);
  }

  const toggleColumnSort = (ascVal: string, descVal: string, defaultFirst: "asc" | "desc" = "asc") => {
    setPage(1);
    setSort((prev) => {
      if (defaultFirst === "asc") {
        if (prev === ascVal) return descVal;
        if (prev === descVal) return "season_first";
        return ascVal;
      }
      if (prev === descVal) return ascVal;
      if (prev === ascVal) return "season_first";
      return descVal;
    });
  };

  const renderSortIcon = (ascVal: string, descVal: string) => {
    if (sort === ascVal) return <ArrowUp size={12} className="col-sort-icon active" aria-hidden="true" />;
    if (sort === descVal) return <ArrowDown size={12} className="col-sort-icon active" aria-hidden="true" />;
    return <ArrowUpDown size={12} className="col-sort-icon" aria-hidden="true" />;
  };

  const formatDate = (ts?: number) => {
    if (!ts || ts <= 0) return "—";
    try {
      return new Date(ts * 1000).toLocaleDateString(dateLocale || "de-DE", {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
      });
    } catch {
      return "—";
    }
  };

  const formatDateTime = (ts?: number) => {
    if (!ts || ts <= 0) return "—";
    try {
      return new Date(ts * 1000).toLocaleString(dateLocale || "de-DE", {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: timeFormat === "12h",
      });
    } catch {
      return "—";
    }
  };

  const shiftPressedRef = useRef(false);

  const handleRowSelection = (index: number, key: string, shiftKey: boolean) => {
    if (!canScan) return;
    setAllMatchingSelected(false);
    if (shiftKey && anchorIndex !== null && anchorIndex >= 0 && anchorIndex < items.length) {
      const start = Math.min(anchorIndex, index);
      const end = Math.max(anchorIndex, index);
      const rangeKeys = items.slice(start, end + 1).map((it) => it.key);
      setSelectedKeys((prev) => Array.from(new Set([...prev, ...rangeKeys])));
    } else {
      setSelectedKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
      setAnchorIndex(index);
    }
  };

  const allVisibleSelected = items.length > 0 && items.every((it) => selectedKeys.includes(it.key));

  async function cycleHeaderSelection() {
    if (!canScan || items.length === 0) return;
    if (!allVisibleSelected && selectedKeys.length === 0) {
      setSelectedKeys(items.map((i) => i.key));
      setAllMatchingSelected(false);
    } else if (!allMatchingSelected && total > items.length) {
      const p = new URLSearchParams({
        search: debouncedSearch,
        status: statusParam,
        library: libParam,
        capability: capParam,
      });
      const res = await serverApi<{ keys: string[]; total: number }>(serverId, `/items/keys?${p}`);
      setSelectedKeys(res.keys || []);
      setAllMatchingSelected(true);
      toast(t("explorer.selected_all_count", { count: (res.keys || []).length }));
    } else {
      setSelectedKeys([]);
      setAllMatchingSelected(false);
    }
  }

  return (
    <section className="card" ref={explorerRef} aria-label="Media Explorer">
      <div className="explorer-toolbar">
        <input
          type="search"
          aria-label={t("explorer.search_placeholder")}
          placeholder={t("explorer.search_placeholder")}
          maxLength={256}
          value={search}
          onChange={(e) => {
            setSearch(stripControlChars(e.target.value, 256));
            setPage(1);
          }}
        />

        <div className="action-group">
          {(libFilters.length > 0 || statusFilters.length > 0 || capFilters.length > 0 || search !== "" || sort !== "season_first") && (
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={() => {
                setSearch("");
                setLibFilters([]);
                setStatusFilters([]);
                setCapFilters([]);
                setSort("season_first");
                setPage(1);
              }}
            >
              {t("explorer.reset_filters")}
            </button>
          )}
          <button
            type="button"
            className={`btn btn-sm ${viewMode === "flat" ? "btn-primary" : ""}`}
            onClick={() => {
              setViewMode("flat");
              setPage(1);
            }}
          >
            {t("explorer.flat")}
          </button>
          <button
            type="button"
            className={`btn btn-sm ${viewMode === "grouped" ? "btn-primary" : ""}`}
            onClick={() => {
              setViewMode("grouped");
              setPage(1);
            }}
          >
            {t("explorer.grouped")}
          </button>
          <InfoPopover title={t("explorer.view_tip.title")} text={t("explorer.view_tip.text")} />
          <a className="btn btn-sm btn-ghost" href={exportUrl("csv")} style={{ textDecoration: "none" }}>
            CSV
          </a>
          <a className="btn btn-sm btn-ghost" href={exportUrl("json")} style={{ textDecoration: "none" }}>
            JSON
          </a>
        </div>
      </div>

      {canScan && selectedKeys.length > 0 && viewMode === "flat" && (
        <div
          className="priority-row"
          role="region"
          aria-label={t("explorer.selected_count", { count: fmtNum(selectedKeys.length) as unknown as number })}
          style={{ marginBottom: 10, borderColor: "var(--accent)", background: "var(--accent-soft)" }}
        >
          <span className="mono small">
            {t("explorer.selected_count", { count: fmtNum(selectedKeys.length) as unknown as number })}
            {allMatchingSelected ? ` (${t("filter.all")})` : ""}
          </span>
          <div className="action-group">
            <button
              type="button"
              className="btn btn-xs btn-primary"
              onClick={async () => {
                await serverApi(serverId, "/items/enqueue", "POST", { keys: selectedKeys, front: true });
                toast(`Queued ${selectedKeys.length} item(s)`);
                setSelectedKeys([]);
                setAllMatchingSelected(false);
                void loadExplorer();
                onRefreshStatus();
              }}
            >
              <Play size={12} aria-hidden="true" />
              <span>{t("explorer.scan_selected")}</span>
            </button>
            <button
              type="button"
              className="btn btn-xs"
              onClick={async () => {
                await serverApi(serverId, "/items/bulk-skip", "POST", { keys: selectedKeys, skip: true });
                setSelectedKeys([]);
                setAllMatchingSelected(false);
                void loadExplorer();
                onRefreshStatus();
              }}
            >
              <ShieldBan size={12} aria-hidden="true" />
              <span>{t("explorer.skip_selected")}</span>
            </button>
            <button
              type="button"
              className="btn btn-xs"
              onClick={() => {
                setSelectedKeys([]);
                setAllMatchingSelected(false);
              }}
            >
              <XIcon size={12} aria-hidden="true" />
              <span>{t("explorer.clear_selection")}</span>
            </button>
          </div>
        </div>
      )}

      <div className="table-wrap table-wrap-filters table-wrap-paged">
        {viewMode === "grouped" ? (
          <table className="media-table media-table-fixed">
            <caption className="sr-only">{t("explorer.grouped")}</caption>
            <thead>
              <tr>
                <th scope="col" style={{ width: "30%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "alphabetical" || sort === "alphabetical_desc" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("alphabetical", "alphabetical_desc", "asc")}
                    >
                      <span>{t("explorer.col.title")}</span>
                      {renderSortIcon("alphabetical", "alphabetical_desc")}
                    </button>
                  </div>
                </th>
                <th scope="col" style={{ width: "20%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "sub_asc" || sort === "sub_desc" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("sub_asc", "sub_desc", "asc")}
                    >
                      <span>{t("explorer.col.season_album")}</span>
                      {renderSortIcon("sub_asc", "sub_desc")}
                    </button>
                  </div>
                </th>
                <th scope="col" style={{ width: "14%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "library_asc" || sort === "library_desc" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("library_asc", "library_desc", "asc")}
                    >
                      <span>{t("explorer.col.library")}</span>
                      {renderSortIcon("library_asc", "library_desc")}
                    </button>
                    <MultiSelectFilter
                      compact
                      label={t("explorer.libraries")}
                      options={libraries.map((l) => ({ value: l.title, label: l.title }))}
                      selected={libFilters}
                      onChange={(next) => {
                        setLibFilters(next);
                        setPage(1);
                      }}
                    />
                  </div>
                </th>
                <th scope="col" style={{ width: "16%" }}>
                  <div className="th-control-row">
                    <span>{t("explorer.col.progress")}</span>
                    <MultiSelectFilter
                      compact
                      align="right"
                      label={t("explorer.statuses")}
                      options={STATUS_DEFS.map((s) => ({ value: s.value, label: t(s.labelKey) }))}
                      selected={statusFilters}
                      onChange={(next) => {
                        setStatusFilters(next);
                        setPage(1);
                      }}
                    />
                  </div>
                </th>
                <th scope="col" style={{ width: "10%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "largest_first" || sort === "smallest_first" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("smallest_first", "largest_first", "desc")}
                    >
                      <span>{t("explorer.col.size")}</span>
                      {renderSortIcon("smallest_first", "largest_first")}
                    </button>
                  </div>
                </th>
                {canScan && <th scope="col" style={{ width: "130px" }}>{t("explorer.col.actions")}</th>}
              </tr>
            </thead>
            <tbody>
              {!groups.length ? (
                <tr>
                  <td colSpan={canScan ? 6 : 5} className="muted">
                    {t("explorer.no_groups")}
                  </td>
                </tr>
              ) : (
                groups.map((g, idx) => (
                  <tr key={`${g.library}:${g.group_title}:${g.sub_title}:${idx}`}>
                    <td className="strong cell-ellipsis">{g.group_title}</td>
                    <td className="cell-ellipsis">{g.sub_title || "—"}</td>
                    <td>{g.library}</td>
                    <td className="tabular-nums">
                      {fmtNum(g.complete)} / {fmtNum(g.total)} ({fmtNum(g.remaining)} {t("cap.left")})
                    </td>
                    <td className="tabular-nums">{fmtBytes(g.bytes)}</td>
                    {canScan && (
                      <td>
                        <button
                          type="button"
                          className="btn btn-xs btn-primary"
                          disabled={g.remaining === 0}
                          onClick={async () => {
                            const r = await serverApi<{ queued: number }>(serverId, "/items/enqueue-group", "POST", {
                              library: g.library,
                              grandparent_title: g.group_title,
                              parent_title: g.sub_title,
                            });
                            toast(`Queued ${r.queued} item(s)`);
                            onRefreshStatus();
                          }}
                        >
                          {g.kind === "track" ? t("explorer.add_album") : g.kind === "movie" ? t("explorer.scan") : t("explorer.add_season")}
                        </button>
                      </td>
                    )}
                  </tr>
                ))
              )}
            </tbody>
          </table>
        ) : (
          <table className="media-table media-table-fixed">
            <caption className="sr-only">{t("explorer.flat")}</caption>
            <thead>
              <tr>
                {canScan && (
                  <th scope="col" style={{ width: 40, textAlign: "center" }} title={t("explorer.select_cycle_tip")}>
                    <Checkbox
                      checked={allVisibleSelected || allMatchingSelected}
                      indeterminate={allVisibleSelected && !allMatchingSelected && total > items.length}
                      onChange={() => void cycleHeaderSelection()}
                      ariaLabel={t("explorer.select_cycle_tip")}
                    />
                  </th>
                )}
                <th scope="col" style={{ width: canScan ? "25%" : "28%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "alphabetical" || sort === "alphabetical_desc" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("alphabetical", "alphabetical_desc", "asc")}
                    >
                      <span>{t("explorer.col.title")}</span>
                      {renderSortIcon("alphabetical", "alphabetical_desc")}
                    </button>
                  </div>
                </th>
                <th scope="col" style={{ width: "10%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "library_asc" || sort === "library_desc" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("library_asc", "library_desc", "asc")}
                    >
                      <span>{t("explorer.col.library")}</span>
                      {renderSortIcon("library_asc", "library_desc")}
                    </button>
                    <MultiSelectFilter
                      compact
                      label={t("explorer.libraries")}
                      options={libraries.map((l) => ({ value: l.title, label: l.title }))}
                      selected={libFilters}
                      onChange={(next) => {
                        setLibFilters(next);
                        setPage(1);
                      }}
                    />
                  </div>
                </th>
                <th scope="col" style={{ width: "13%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "status_asc" || sort === "status_desc" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("status_asc", "status_desc", "asc")}
                    >
                      <span>{t("explorer.col.status")}</span>
                      {renderSortIcon("status_asc", "status_desc")}
                    </button>
                    <MultiSelectFilter
                      compact
                      align="right"
                      label={t("explorer.statuses")}
                      options={STATUS_DEFS.map((s) => ({ value: s.value, label: t(s.labelKey) }))}
                      selected={statusFilters}
                      onChange={(next) => {
                        setStatusFilters(next);
                        setPage(1);
                      }}
                    />
                  </div>
                </th>
                <th scope="col" style={{ width: "24%" }}>
                  <div className="th-control-row">
                    <span>{t("explorer.col.capabilities")}</span>
                    <MultiSelectFilter
                      compact
                      align="right"
                      label={t("explorer.capabilities")}
                      options={sortedCapDefs.map((c) => ({ value: c.id, label: t(c.labelKey) }))}
                      selected={capFilters}
                      onChange={(next) => {
                        setCapFilters(next);
                        setPage(1);
                      }}
                    />
                  </div>
                </th>
                <th scope="col" style={{ width: "9%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "largest_first" || sort === "smallest_first" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("smallest_first", "largest_first", "desc")}
                    >
                      <span>{t("explorer.col.size")}</span>
                      {renderSortIcon("smallest_first", "largest_first")}
                    </button>
                  </div>
                </th>
                <th scope="col" style={{ width: "9%" }}>
                  <div className="th-control-row">
                    <button
                      type="button"
                      className={`th-sort-btn ${sort === "newest_first" || sort === "oldest_first" ? "active" : ""}`}
                      onClick={() => toggleColumnSort("oldest_first", "newest_first", "desc")}
                    >
                      <span>{t("explorer.col.added")}</span>
                      {renderSortIcon("oldest_first", "newest_first")}
                    </button>
                  </div>
                </th>
                {canScan && <th scope="col" style={{ width: "130px" }}>{t("explorer.col.actions")}</th>}
              </tr>
            </thead>
            <tbody>
              {!items.length ? (
                <tr>
                  <td colSpan={canScan ? 8 : 6} className="muted">
                    {t("explorer.no_items")}
                  </td>
                </tr>
              ) : (
                items.map((it, rowIndex) => {
                  const isSelected = selectedKeys.includes(it.key);
                  const onRowClick = (e: MouseEvent<HTMLTableRowElement>) => {
                    if (e.shiftKey) {
                      window.getSelection()?.removeAllRanges();
                    }
                    handleRowSelection(rowIndex, it.key, e.shiftKey);
                  };
                  const statusKey = it.queued ? "status.queued" : `status.${it.status}`;
                  const missingSet = new Set(it.missing || []);
                  const libMeta = libraries.find((l) => l.title === it.library);
                  const disabledCapsSet = new Set(libMeta?.disabled_caps || []);
                  const configuredLibCaps = libraryCapabilities?.[it.library];
                  const applicableCaps = sortedCapDefs.filter((c) => {
                    if (!c.kinds.includes(it.kind || "movie")) return false;
                    if (disabledCapsSet.has(c.id)) return false;
                    if (Array.isArray(configuredLibCaps) && !configuredLibCaps.includes(c.id)) return false;
                    return true;
                  });
                  const hasRetryInfo = (it.status === "retry" || it.status === "exhausted") && (it.attempts > 0 || it.last_error);
                  const cleanLabel = (s: string) => s.replace(/[:：]\s*$/, "").trim();
                  const retryRows: { label: string; value: string }[] = [];
                  if (it.last_error) {
                    retryRows.push({ label: cleanLabel(t("explorer.reason")), value: it.last_error });
                  }
                  if (it.updated_at) {
                    retryRows.push({ label: cleanLabel(t("explorer.last_run")), value: formatDateTime(it.updated_at) });
                  }
                  if (it.next_retry_at) {
                    retryRows.push({ label: cleanLabel(t("explorer.next_retry")), value: formatDateTime(it.next_retry_at) });
                  }

                  return (
                    <tr
                      key={it.key}
                      className={`selectable-row ${isSelected ? "row-selected" : ""}`}
                      aria-selected={canScan ? isSelected : undefined}
                      tabIndex={canScan ? 0 : undefined}
                      onClick={onRowClick}
                      onKeyDown={(e) => {
                        if (canScan && (e.key === " " || e.key === "Enter")) {
                          e.preventDefault();
                          handleRowSelection(rowIndex, it.key, e.shiftKey);
                        }
                      }}
                    >
                      {canScan && (
                        <td
                          style={{ textAlign: "center" }}
                          onClick={(e) => {
                            e.stopPropagation();
                          }}
                        >
                          <Checkbox
                            checked={isSelected}
                            onChange={() => {}}
                            onShiftChange={(_, shiftKey) => handleRowSelection(rowIndex, it.key, shiftKey)}
                            ariaLabel={formatItemTitle(it)}
                          />
                        </td>
                      )}
                      <td className="row-title-cell cell-ellipsis">
                        <div className="strong cell-ellipsis" title={formatItemTitle(it)}>
                          {formatItemTitle(it)}
                        </div>
                      </td>
                      <td className="cell-ellipsis">{it.library}</td>
                      <td>
                        <div className="action-group" style={{ gap: 5 }}>
                          <span className={`badge st-${it.queued ? "queued" : it.status}`}>
                            {t(statusKey)}
                          </span>
                          {it.attempts > 0 && (it.status === "retry" || it.status === "exhausted") && (
                            <span className="tabular-nums small">×{fmtNum(it.attempts)}</span>
                          )}
                          {hasRetryInfo && <InfoPopover title={t(statusKey)} rows={retryRows} />}
                        </div>
                      </td>
                      <td>
                        <div className="cap-chip-list">
                          {applicableCaps.map((c) => {
                            const isMissing = missingSet.has(c.id);
                            return (
                              <span
                                key={c.id}
                                className={`cap-chip ${isMissing ? "cap-chip-miss" : "cap-chip-ok"}`}
                                title={`${t(c.labelKey)}: ${isMissing ? t("cap.left") : t("cap.done")}`}
                              >
                                {isMissing ? <XIcon size={10} aria-hidden="true" /> : <Check size={10} aria-hidden="true" />}
                                <span>{t(c.labelKey)}</span>
                              </span>
                            );
                          })}
                        </div>
                      </td>
                      <td className="tabular-nums">{fmtBytes(it.bytes)}</td>
                      <td className="tabular-nums">{formatDate(it.added_at)}</td>
                      {canScan && (
                        <td onClick={(e) => e.stopPropagation()}>
                          {it.status === "complete" ? (
                            <span className="small muted">—</span>
                          ) : it.status === "exhausted" ? (
                            <div className="action-group">
                              <button
                                type="button"
                                className="btn btn-xs btn-primary"
                                onClick={async () => {
                                  await serverApi(serverId, "/control/reset-retries", "POST", { keys: [it.key] });
                                  await serverApi(serverId, "/items/enqueue", "POST", { keys: [it.key], front: true });
                                  toast(`Retrying ${it.title}`);
                                  void loadExplorer();
                                  onRefreshStatus();
                                }}
                              >
                                <RotateCcw size={12} aria-hidden="true" />
                                <span>{t("explorer.retry")}</span>
                              </button>
                              <button
                                type="button"
                                className="btn btn-xs btn-primary"
                                onClick={async () => {
                                  await serverApi(serverId, "/items/skip", "POST", { key: it.key, skip: true });
                                  toast(`Blocklisted ${it.title}`);
                                  void loadExplorer();
                                  onRefreshStatus();
                                }}
                              >
                                <ShieldBan size={12} aria-hidden="true" />
                                <span>{t("explorer.skip")}</span>
                              </button>
                            </div>
                          ) : (
                            <div className="action-group">
                              <button
                                type="button"
                                className="btn btn-xs btn-primary"
                                onClick={async () => {
                                  await serverApi(serverId, "/items/enqueue", "POST", { keys: [it.key], front: true });
                                  toast(`Queued ${it.title}`);
                                  void loadExplorer();
                                  onRefreshStatus();
                                }}
                              >
                                <Play size={12} aria-hidden="true" />
                                <span>{t("explorer.scan")}</span>
                              </button>
                              <button
                                type="button"
                                className="btn btn-xs btn-primary"
                                onClick={async () => {
                                  await serverApi(serverId, "/items/skip", "POST", { key: it.key, skip: !it.skipped });
                                  toast(it.skipped ? `Unblocked ${it.title}` : `Blocklisted ${it.title}`);
                                  void loadExplorer();
                                  onRefreshStatus();
                                }}
                              >
                                {it.skipped ? <ShieldCheck size={12} aria-hidden="true" /> : <ShieldBan size={12} aria-hidden="true" />}
                                <span>{it.skipped ? t("explorer.unskip") : t("explorer.skip")}</span>
                              </button>
                            </div>
                          )}
                        </td>
                      )}
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        )}
      </div>

      <PaginationBar
        page={page}
        totalPages={totalPages}
        pageSize={pageSize}
        total={total}
        unitLabel={viewMode === "grouped" ? t("pagination.groups") : t("pagination.items")}
        fmtNum={fmtNum}
        onPageChange={setPage}
        onPageSizeChange={setPageSize}
      />
    </section>
  );
}
