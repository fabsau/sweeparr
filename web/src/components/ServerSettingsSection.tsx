import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowLeftRight, ClipboardCheck, FolderSearch, GripVertical, Wifi, X } from "lucide-react";
import { serverApi } from "../api";
import { useLocaleFormat } from "../utils/format";
import { Checkbox, InfoPopover, useClickOutside } from "./Controls";
import { CAP_DEFS } from "./MediaExplorer";
import type { LibrarySummary, ScannerConfig, ServerSummary } from "../types";

export interface SettingsCheckResult {
  ok: boolean;
  error?: string;
  checks?: { id: string; label: string; value: string; status: string; recommendation: string }[];
}

type DiagnosticPanel =
  | { kind: "test"; ok: boolean; title: string; subtitle: string; detail?: string }
  | { kind: "check"; result: SettingsCheckResult }
  | { kind: "discover"; ok: boolean; title: string; libraries: { title: string; kind: string; isNew: boolean }[]; error?: string };

function defaultCapsForKind(kind: string): string[] {
  return [...CAP_DEFS]
    .filter((c) => c.kinds.includes(kind) && ["loudness", "voice", "chapters", "intro", "credits", "sonic"].includes(c.id))
    .map((c) => c.id);
}

export function MediaServersCard({
  serverId,
  servers,
  canScan,
  iacLocked,
  onSelectServer,
  onChangeCfg,
  onLibrariesDiscovered,
}: {
  serverId: string;
  servers: ServerSummary[];
  canScan: boolean;
  iacLocked?: boolean;
  onSelectServer: (id: string) => void;
  onChangeCfg: (updater: (prev: ScannerConfig) => ScannerConfig) => void;
  onLibrariesDiscovered: (libs: LibrarySummary[]) => void;
}) {
  const { t } = useTranslation();
  const { fmtNum } = useLocaleFormat();
  const [panel, setPanel] = useState<DiagnosticPanel | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  useClickOutside(panelRef, Boolean(panel), () => setPanel(null));

  return (
    <div className="card form-section">
      <div className="section-head-row">
        <div className="field-label-row">
          <h3>{t("settings.media_servers", { count: fmtNum(servers.length) as unknown as number })}</h3>
          <InfoPopover
            title={t("settings.media_servers", { count: fmtNum(servers.length) as unknown as number })}
            text={t("settings.media_servers.tip")}
          />
        </div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {servers.map((s) => {
          const isCurrent = s.id === serverId;
          const displayName = s.server_name || s.name || t("settings.primary_server");
          return (
            <div key={s.id} className="priority-row">
              <div>
                <div className="action-group" style={{ gap: 8 }}>
                  <strong>{displayName}</strong>
                  <span className="badge st-paused">{s.kind.toUpperCase()}</span>
                  <span className={`badge ${s.connected ? "st-complete" : "st-disconnected"}`}>
                    {s.connected ? t("settings.connected") : t("settings.offline")}
                  </span>
                </div>
                <div className="small muted mono" style={{ marginTop: 2 }}>
                  {s.id} · {s.url}
                </div>
              </div>
              <div className="action-group">
                {!isCurrent && (
                  <button type="button" className="btn btn-xs btn-primary" onClick={() => onSelectServer(s.id)}>
                    <ArrowLeftRight size={12} aria-hidden="true" />
                    <span>{t("settings.switch")}</span>
                  </button>
                )}
                {isCurrent && canScan && (
                  <>
                    <button
                      type="button"
                      className="btn btn-xs btn-primary"
                      onClick={async () => {
                        try {
                          const r = await serverApi<{
                            ok: boolean;
                            friendly_name?: string;
                            version?: string;
                            libraries?: { title: string; type: string }[];
                            error?: string;
                          }>(serverId, "/connection/test", "POST");
                          if (!r.ok) {
                            setPanel({
                              kind: "test",
                              ok: false,
                              title: t("settings.test_conn_title"),
                              subtitle: r.error || "Connection failed",
                            });
                            return;
                          }
                          setPanel({
                            kind: "test",
                            ok: true,
                            title: t("settings.test_conn_title"),
                            subtitle: `${r.friendly_name || displayName}${r.version ? ` (v${r.version})` : ""}`,
                            detail: `${(r.libraries || []).length} libraries reachable`,
                          });
                        } catch (err) {
                          setPanel({
                            kind: "test",
                            ok: false,
                            title: t("settings.test_conn_title"),
                            subtitle: (err as Error).message,
                          });
                        }
                      }}
                    >
                      <Wifi size={12} aria-hidden="true" />
                      <span>{t("settings.test_conn")}</span>
                    </button>
                    <button
                      type="button"
                      className="btn btn-xs btn-primary"
                      onClick={async () => {
                        const r = await serverApi<SettingsCheckResult>(serverId, "/plex/settings-check");
                        setPanel({ kind: "check", result: r });
                      }}
                    >
                      <ClipboardCheck size={12} aria-hidden="true" />
                      <span>{t("settings.check_server")}</span>
                    </button>
                    {!iacLocked && (
                    <button
                      type="button"
                      className="btn btn-xs btn-primary"
                      onClick={async () => {
                        try {
                          const r = await serverApi<{
                            ok: boolean;
                            server_name?: string;
                            libraries?: { title: string; type: string }[];
                            error?: string;
                          }>(serverId, "/libraries/discover", "POST");
                          if (!r.ok) {
                            setPanel({
                              kind: "discover",
                              ok: false,
                              title: t("settings.discover_title"),
                              libraries: [],
                              error: r.error || "Discovery failed",
                            });
                            return;
                          }
                          const rawLibs = r.libraries || [];
                          const mapKind = (tp: string) =>
                            tp === "show" ? "episode" : tp === "artist" ? "track" : "movie";
                          const discovered: { title: string; kind: string; isNew: boolean }[] = [];
                          onChangeCfg((prev) => {
                            const nextPriority = [...prev.library_priority];
                            const nextEnabled = [...prev.enabled_libraries];
                            const nextCaps = { ...(prev.library_capabilities || {}) };
                            for (const lib of rawLibs) {
                              const k = mapKind(lib.type);
                              const isNew = !nextPriority.includes(lib.title);
                              if (isNew) {
                                nextPriority.push(lib.title);
                                nextEnabled.push(lib.title);
                              }
                              if (nextCaps[lib.title] === undefined) {
                                nextCaps[lib.title] = defaultCapsForKind(k);
                              }
                              discovered.push({ title: lib.title, kind: k, isNew });
                            }
                            return {
                              ...prev,
                              library_priority: nextPriority,
                              enabled_libraries: nextEnabled,
                              library_capabilities: nextCaps,
                            };
                          });
                          onLibrariesDiscovered(
                            rawLibs.map((lib) => ({
                              title: lib.title,
                              kind: mapKind(lib.type),
                              total: 0,
                              complete: 0,
                              pending: 0,
                              retry: 0,
                              exhausted: 0,
                              skipped: 0,
                              queued: 0,
                              pct: 0,
                              disabled_caps: [],
                              cap_stats: {},
                              capabilities: {},
                            }))
                          );
                          setPanel({
                            kind: "discover",
                            ok: true,
                            title: t("settings.discover_title"),
                            libraries: discovered,
                          });
                        } catch (err) {
                          setPanel({
                            kind: "discover",
                            ok: false,
                            title: t("settings.discover_title"),
                            libraries: [],
                            error: (err as Error).message,
                          });
                        }
                      }}
                    >
                      <FolderSearch size={12} aria-hidden="true" />
                      <span>{t("settings.find_new_libs")}</span>
                    </button>
                    )}
                  </>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {panel && (
        <div
          ref={panelRef}
          className="engine-subbox"
          style={{ marginTop: 8, gap: 8, borderColor: "var(--border-strong)" }}
          role="region"
          aria-label="Server diagnostics"
        >
          <div className="section-head-row">
            <div className="field-label-row">
              <span className="subbox-label">
                {panel.kind === "check" ? t("settings.check_server_title") : panel.title}
              </span>
              <InfoPopover
                title={panel.kind === "check" ? t("settings.check_server_title") : panel.title}
                text={
                  panel.kind === "check"
                    ? t("settings.check_server.tip")
                    : panel.kind === "test"
                      ? t("settings.test_conn.tip")
                      : t("settings.discover.tip")
                }
              />
            </div>
            <button
              type="button"
              className="btn btn-xs btn-ghost"
              aria-label={t("modal.close")}
              onClick={() => setPanel(null)}
            >
              <X size={13} aria-hidden="true" />
            </button>
          </div>

          {panel.kind === "test" && (
            <div className="priority-row">
              <div>
                <div className="strong small">{panel.subtitle}</div>
                {panel.detail && <div className="small muted">{panel.detail}</div>}
              </div>
              <span className={`badge ${panel.ok ? "st-complete" : "st-disconnected"}`}>
                {panel.ok ? t("settings.connected") : t("settings.offline")}
              </span>
            </div>
          )}

          {panel.kind === "discover" &&
            (!panel.ok ? (
              <div className="err-banner">{panel.error}</div>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <div className="small muted">{t("settings.discover_summary", { count: panel.libraries.length })}</div>
                <div className="action-group" style={{ gap: 6, flexWrap: "wrap" }}>
                  {panel.libraries.map((l) => (
                    <span key={l.title} className={`badge ${l.isNew ? "st-active" : "st-complete"}`}>
                      {l.title} {l.isNew ? "(New)" : ""}
                    </span>
                  ))}
                </div>
              </div>
            ))}

          {panel.kind === "check" &&
            (!panel.result.ok ? (
              <div className="err-banner">{panel.result.error || "Check failed"}</div>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                {(panel.result.checks || []).map((c) => (
                  <div key={c.id} className="priority-row">
                    <div>
                      <div className="field-label-row">
                        <span className="strong small">{c.label}</span>
                        <InfoPopover title={c.label} text={c.recommendation} />
                      </div>
                      <div className="small muted">{c.recommendation}</div>
                    </div>
                    <span className={`badge ${c.status === "ok" ? "st-complete" : "st-paused"}`}>{c.value}</span>
                  </div>
                ))}
              </div>
            ))}
        </div>
      )}
    </div>
  );
}

export function LibrariesAndCapsCard({
  cfg,
  libraries,
  canScan,
  onChangeCfg,
}: {
  cfg: ScannerConfig;
  libraries: LibrarySummary[];
  canScan: boolean;
  onChangeCfg: (updater: (prev: ScannerConfig) => ScannerConfig) => void;
}) {
  const { t } = useTranslation();
  const { fmtNum } = useLocaleFormat();
  const [dragLib, setDragLib] = useState<string | null>(null);
  const [dragOverLib, setDragOverLib] = useState<string | null>(null);

  const sortedCapDefs = [...CAP_DEFS].sort((a, b) => t(a.labelKey).localeCompare(t(b.labelKey)));

  const orderedLibs = [...libraries].sort((a, b) => {
    const ia = cfg.library_priority.indexOf(a.title);
    const ib = cfg.library_priority.indexOf(b.title);
    return (ia === -1 ? 999 : ia) - (ib === -1 ? 999 : ib);
  });

  function toggleLib(title: string) {
    onChangeCfg((prev) => {
      const has = prev.enabled_libraries.includes(title);
      const next = has ? prev.enabled_libraries.filter((l) => l !== title) : [...prev.enabled_libraries, title];
      return { ...prev, enabled_libraries: next };
    });
  }

  function reorderLib(targetTitle: string) {
    if (!dragLib || dragLib === targetTitle) return;
    const names = orderedLibs.map((l) => l.title);
    const from = names.indexOf(dragLib);
    const to = names.indexOf(targetTitle);
    if (from < 0 || to < 0) return;
    names.splice(to, 0, names.splice(from, 1)[0]);
    onChangeCfg((prev) => ({ ...prev, library_priority: names }));
    setDragLib(null);
  }

  function getLibCaps(lib: LibrarySummary): string[] {
    const configured = cfg.library_capabilities?.[lib.title];
    if (Array.isArray(configured)) return configured;
    const kind = lib.kind || "movie";
    return defaultCapsForKind(kind);
  }

  function toggleLibCap(lib: LibrarySummary, capId: string) {
    const current = getLibCaps(lib);
    const has = current.includes(capId);
    const next = has ? current.filter((x) => x !== capId) : [...current, capId];
    onChangeCfg((prev) => ({
      ...prev,
      library_capabilities: { ...(prev.library_capabilities || {}), [lib.title]: next },
    }));
  }

  return (
    <div className="card form-section">
      <div className="section-head-row">
        <div className="field-label-row">
          <h3>{t("settings.libs_caps")}</h3>
          <InfoPopover title={t("settings.libs_caps")} text={t("settings.libs_caps.tip")} />
        </div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {orderedLibs.map((lib, idx) => {
          const enabled = cfg.enabled_libraries.includes(lib.title);
          const kind = lib.kind || "movie";
          const applicableCaps = sortedCapDefs.filter((c) => c.kinds.includes(kind));
          const activeCaps = getLibCaps(lib);
          return (
            <div
              key={lib.title}
              className={`priority-row ${dragLib === lib.title ? "dragging" : ""} ${dragOverLib === lib.title ? "drag-over" : ""}`}
              style={{ flexDirection: "column", alignItems: "stretch", gap: 8, cursor: canScan ? "grab" : "default" }}
              draggable={canScan}
              onDragStart={(e) => {
                setDragLib(lib.title);
                e.dataTransfer.effectAllowed = "move";
                e.dataTransfer.setData("text/plain", lib.title);
              }}
              onDragOver={(e) => {
                if (!canScan) return;
                e.preventDefault();
                e.dataTransfer.dropEffect = "move";
                if (dragOverLib !== lib.title) setDragOverLib(lib.title);
              }}
              onDragLeave={() => {
                if (dragOverLib === lib.title) setDragOverLib(null);
              }}
              onDrop={(e) => {
                e.preventDefault();
                setDragOverLib(null);
                reorderLib(lib.title);
              }}
              onDragEnd={() => {
                setDragLib(null);
                setDragOverLib(null);
              }}
            >
              <div className="section-head-row">
                <div className="action-group" style={{ gap: 10 }}>
                  {canScan && <GripVertical size={14} className="muted" aria-hidden="true" />}
                  <Checkbox
                    checked={enabled}
                    disabled={!canScan}
                    onChange={() => toggleLib(lib.title)}
                    label={lib.title}
                  />
                  <span className="small muted">({fmtNum(lib.total)})</span>
                </div>
                <div className="action-group">
                  <span className="badge st-paused">#{fmtNum(idx + 1)}</span>
                </div>
              </div>
              {enabled && (
                <div className="action-group" style={{ paddingLeft: 24, gap: 12, flexWrap: "wrap" }}>
                  {applicableCaps.map((c) => {
                    const mode = lib.cap_modes?.[c.id];
                    const disabledOnServer = mode === "disabled" || (lib.disabled_caps || []).includes(c.id);
                    const recordedOnly = mode === "recorded_only";
                    return (
                      <span key={c.id} className="field-label-row">
                        <span style={disabledOnServer ? { opacity: 0.48 } : undefined}>
                          <Checkbox
                            checked={!disabledOnServer && activeCaps.includes(c.id)}
                            disabled={!canScan || disabledOnServer}
                            onChange={() => toggleLibCap(lib, c.id)}
                            label={t(c.labelKey)}
                          />
                        </span>
                        {disabledOnServer && (
                          <InfoPopover
                            title={t(c.labelKey)}
                            text={t("settings.cap_disabled_server")}
                          />
                        )}
                        {!disabledOnServer && recordedOnly && (
                          <>
                            <span className="badge st-paused" style={{ fontSize: 10 }}>
                              {t("settings.cap_recorded_only")}
                            </span>
                            <InfoPopover
                              title={t("settings.cap_recorded_only")}
                              text={t("settings.cap_recorded_only.tip")}
                            />
                          </>
                        )}
                      </span>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
