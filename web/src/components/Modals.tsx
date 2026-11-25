import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tv, X } from "lucide-react";
import { serverPath } from "../api";
import { useLocaleFormat } from "../utils/format";
import type { ServerSummary, SessionInfo } from "../types";

/** Traps keyboard focus inside an open modal and closes on Escape. */
function useModalA11y(open: boolean, onClose: () => void) {
  const modalRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const prevActive = document.activeElement as HTMLElement | null;
    const el = modalRef.current;
    if (el) {
      const firstFocusable = el.querySelector<HTMLElement>(
        'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
      );
      firstFocusable?.focus();
    }

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key === "Tab" && modalRef.current) {
        const focusables = Array.from(
          modalRef.current.querySelectorAll<HTMLElement>(
            'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
          )
        );
        if (focusables.length === 0) return;
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };

    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      prevActive?.focus?.();
    };
  }, [open, onClose]);

  return modalRef;
}

function ModalShell({
  open,
  wide,
  ariaLabel,
  onClose,
  children,
}: {
  open: boolean;
  wide?: boolean;
  ariaLabel: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const modalRef = useModalA11y(open, onClose);
  if (!open) return null;
  return (
    <>
      <div className="modal-backdrop" onClick={onClose} aria-hidden="true" />
      <div
        ref={modalRef}
        className={`modal ${wide ? "modal-wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel}
      >
        {children}
      </div>
    </>
  );
}

export function ConfirmModal({
  open,
  title,
  message,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  message: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  return (
    <ModalShell open={open} ariaLabel={title} onClose={onCancel}>
      <h3 style={{ margin: 0 }}>{title}</h3>
      <p className="muted small" style={{ margin: 0 }}>{message}</p>
      <div className="modal-actions">
        <button type="button" className="btn btn-ghost" onClick={onCancel}>{t("modal.cancel")}</button>
        <button type="button" className="btn btn-primary" onClick={onConfirm}>{confirmLabel}</button>
      </div>
    </ModalShell>
  );
}

export function SyncModal({
  open,
  onDelta,
  onFull,
  onCancel,
}: {
  open: boolean;
  onDelta: () => void;
  onFull: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  return (
    <ModalShell open={open} ariaLabel={t("modal.sync_title")} onClose={onCancel}>
      <h3 style={{ margin: 0 }}>{t("modal.sync_title")}</h3>
      <p className="muted small" style={{ margin: 0 }}>{t("modal.sync_desc")}</p>
      <div className="modal-actions">
        <button type="button" className="btn btn-ghost" onClick={onCancel}>{t("modal.cancel")}</button>
        <button type="button" className="btn" onClick={onFull}>{t("modal.sync_full")}</button>
        <button type="button" className="btn btn-primary" onClick={onDelta}>{t("modal.sync_delta")}</button>
      </div>
    </ModalShell>
  );
}

function fmtMs(ms: number): string {
  const sec = Math.max(0, Math.floor((ms || 0) / 1000));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

function StreamThumb({ serverId, thumbPath, title }: { serverId: string; thumbPath: string; title: string }) {
  const [failed, setFailed] = useState(false);
  if (!thumbPath || failed) {
    return <Tv size={24} aria-hidden="true" />;
  }
  return (
    <img
      src={serverPath(serverId, `/plex/thumb?path=${encodeURIComponent(thumbPath)}`)}
      alt={title}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}

export function StreamsModal({
  open,
  serverId,
  server,
  sessions,
  transcodes,
  onClose,
}: {
  open: boolean;
  serverId: string;
  server?: ServerSummary;
  sessions: SessionInfo[];
  transcodes: number;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { fmtNum, fmtFixed, fmtPct } = useLocaleFormat();
  const serverKindLabel = (server?.kind || "plex").toUpperCase();
  const serverDisplayName = server?.server_name || server?.name || "Primary Server";

  return (
    <ModalShell open={open} wide ariaLabel={t("modal.streams_title")} onClose={onClose}>
      <div className="modal-head">
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <h3 style={{ margin: 0 }}>{t("modal.streams_title")}</h3>
            <span className="badge st-queued">
              {t("modal.streams_server", { kind: serverKindLabel, name: serverDisplayName })}
            </span>
          </div>
          <p className="muted small" style={{ marginTop: 4 }}>
            {sessions.length
              ? t("modal.streams_summary", {
                  count: fmtNum(sessions.length) as unknown as number,
                  transcodes: fmtNum(transcodes) as unknown as number,
                })
              : t("modal.streams_none_sub")}
          </p>
        </div>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label={t("modal.close")}>
          <X size={15} aria-hidden="true" /> {t("modal.close")}
        </button>
      </div>

      <div className="streams-list">
        {!sessions.length ? (
          <div className="muted small">{t("modal.streams_none")}</div>
        ) : (
          sessions.map((s, idx) => {
            const pct = Math.min(100, Math.max(0, s.progress_pct || 0));
            const title =
              s.kind === "episode" && s.grandparent_title
                ? `${s.grandparent_title} — S${String(s.parent_index).padStart(2, "0")}E${String(s.index).padStart(2, "0")} • ${s.raw_title || s.title}`
                : s.year
                  ? `${s.title} (${s.year})`
                  : s.title;
            const bw = s.bandwidth_kbps
              ? `${fmtFixed(s.bandwidth_kbps / 1000, 1)} Mbps`
              : s.bitrate_kbps
                ? `${fmtFixed(s.bitrate_kbps / 1000, 1)} Mbps`
                : "—";
            return (
              <div className="stream-card" key={idx}>
                <div className="stream-thumb">
                  <StreamThumb serverId={serverId} thumbPath={s.thumb_path} title={title} />
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0 }}>
                  <div style={{ display: "flex", justifyContent: "space-between", gap: 8, flexWrap: "wrap" }}>
                    <div>
                      <div className="strong">{title}</div>
                      <div className="small muted">
                        {s.user} • {[s.player, s.platform, s.product].filter(Boolean).join(" · ") || serverKindLabel}
                      </div>
                    </div>
                    <div className="action-group">
                      <span className="badge">{serverKindLabel}</span>
                      <span className={`badge ${s.state === "paused" ? "st-paused" : s.transcoding ? "st-running" : "st-complete"}`}>
                        {(s.state || "playing").toUpperCase()}
                      </span>
                    </div>
                  </div>
                  <div className="stream-meta-grid">
                    <div>
                      <span className="stream-meta-label">{t("modal.streams_video")}</span>
                      <span>{s.video_decision || "direct play"} {s.video_resolution ? `(${s.video_resolution}p ${s.video_codec.toUpperCase()})` : ""}</span>
                    </div>
                    <div>
                      <span className="stream-meta-label">{t("modal.streams_audio")}</span>
                      <span>{s.audio_decision || "direct play"} {s.audio_codec ? `(${s.audio_codec.toUpperCase()} ${s.audio_channels}ch)` : ""}</span>
                    </div>
                    <div>
                      <span className="stream-meta-label">{t("modal.streams_bandwidth")}</span>
                      <span>{bw} • {(s.location || (s.local ? "lan" : "wan")).toUpperCase()} {s.address ? `(${s.address})` : ""}</span>
                    </div>
                    <div>
                      <span className="stream-meta-label">{t("modal.streams_transcode")}</span>
                      <span>{s.transcoding ? t("modal.streams_transcode_yes") : t("modal.streams_transcode_no")}</span>
                    </div>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                    <div
                      className="progress-track"
                      role="progressbar"
                      aria-label={title}
                      aria-valuenow={Math.round(pct)}
                      aria-valuemin={0}
                      aria-valuemax={100}
                    >
                      <div className="progress-fill progress-amber" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="progress-caption mono small">
                      <span>{fmtMs(s.view_offset_ms)} / {fmtMs(s.duration_ms)}</span>
                      <span>{fmtPct(Math.round(pct), 0)}</span>
                    </div>
                  </div>
                </div>
              </div>
            );
          })
        )}
      </div>
    </ModalShell>
  );
}
