import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, Globe, HelpCircle, Minus, Monitor, Moon, Plus, Server, Sun, X } from "lucide-react";
import { LANGUAGES, type Lang } from "../i18n";
import { parseLocalizedInt, useLocaleFormat } from "../utils/format";
import type { ThemeMode } from "../utils/theme";
import type { ServerSummary } from "../types";

/** Closes a popover/dropdown whenever the user clicks outside `ref` or presses Escape. */
export function useClickOutside(ref: RefObject<HTMLElement | null>, open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose();
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [ref, open, onClose]);
}

/** Accessible click-triggered info popover rendered in a portal so it is never clipped or faded by ancestors. */
export function InfoPopover({
  title,
  text,
  rows,
}: {
  title?: string;
  text?: string;
  rows?: { label: string; value: string }[];
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<CSSProperties | null>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const popId = useId();

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (btnRef.current?.contains(target) || boxRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onScroll = () => setOpen(false);
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onScroll);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onScroll);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open || !btnRef.current) return;
    const rect = btnRef.current.getBoundingClientRect();
    const boxW = Math.min(300, window.innerWidth - 24);
    let left = rect.left + rect.width / 2 - boxW / 2;
    left = Math.max(12, Math.min(left, window.innerWidth - boxW - 12));
    const showBelow = rect.top < 150;
    if (showBelow) {
      setPos({ position: "fixed", top: rect.bottom + 8, left, width: boxW, bottom: "auto", transform: "none" });
    } else {
      setPos({ position: "fixed", bottom: window.innerHeight - rect.top + 8, left, width: boxW, top: "auto", transform: "none" });
    }
  }, [open]);

  const ariaDesc = text || (rows ? rows.map((r) => `${r.label}: ${r.value}`).join("; ") : "");

  return (
    <span className="info-popover-wrap">
      <button
        ref={btnRef}
        type="button"
        className={`info-trigger ${open ? "active" : ""}`}
        aria-label={title ? `${title}: ${ariaDesc}` : ariaDesc}
        aria-expanded={open}
        aria-controls={open ? popId : undefined}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setOpen((v) => !v);
        }}
      >
        <HelpCircle size={14} strokeWidth={1.5} aria-hidden="true" />
      </button>
      {open &&
        pos &&
        createPortal(
          <div
            ref={boxRef}
            id={popId}
            role="dialog"
            aria-label={title || "Explanation"}
            className="info-popover-box"
            style={pos}
            onClick={(e) => e.stopPropagation()}
          >
            {title && <div className="info-popover-title">{title}</div>}
            {rows && rows.length > 0 ? (
              <dl className="info-popover-rows">
                {rows.map((row) => (
                  <div key={row.label} className="info-popover-row">
                    <dt>{row.label}</dt>
                    <dd>{row.value}</dd>
                  </div>
                ))}
              </dl>
            ) : (
              <div className="info-popover-body">{text}</div>
            )}
          </div>,
          document.body
        )}
    </span>
  );
}

/** Theme-styled custom accessible checkbox replacing default browser checkboxes. */
export function Checkbox({
  checked,
  indeterminate,
  onChange,
  onShiftChange,
  label,
  ariaLabel,
  disabled,
}: {
  checked: boolean;
  indeterminate?: boolean;
  onChange: (checked: boolean) => void;
  onShiftChange?: (checked: boolean, shiftKey: boolean) => void;
  label?: string;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  return (
    <label
      className={`custom-check-label ${disabled ? "disabled" : ""}`}
      onClick={(e) => {
        if (disabled) return;
        if (onShiftChange) {
          e.preventDefault();
          e.stopPropagation();
          onShiftChange(!checked, e.shiftKey);
        }
      }}
    >
      <input
        type="checkbox"
        className="sr-only"
        checked={checked}
        disabled={disabled}
        aria-checked={indeterminate ? "mixed" : checked}
        aria-label={ariaLabel || label}
        onChange={(e) => {
          if (!onShiftChange) {
            onChange(e.target.checked);
          }
        }}
      />
      <span className={`custom-check-box ${checked || indeterminate ? "checked" : ""}`} aria-hidden="true">
        {indeterminate ? <Minus size={12} strokeWidth={3} /> : checked ? <Check size={12} strokeWidth={3} /> : null}
      </span>
      {label && <span>{label}</span>}
    </label>
  );
}

/** Theme-styled numeric input with custom decrement/increment buttons instead of native browser spinners. */
export function NumberStepper({
  value,
  onChange,
  min = 0,
  max = 999999,
  step = 1,
  ariaLabel,
  disabled,
}: {
  value: number;
  onChange: (next: number) => void;
  min?: number;
  max?: number;
  step?: number;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  const { fmtNum } = useLocaleFormat();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(String(value));

  const clamp = (n: number) => {
    if (Number.isNaN(n)) return min;
    const rounded = step < 1 ? Math.round(n * 10) / 10 : Math.round(n);
    return Math.min(max, Math.max(min, rounded));
  };

  const commit = (raw: string) => {
    const normalized = raw
      .replace(/[\u0660-\u0669]/g, (ch) => String(ch.charCodeAt(0) - 0x0660))
      .replace(/[\u06F0-\u06F9]/g, (ch) => String(ch.charCodeAt(0) - 0x06f0))
      .replace(/,/g, ".");
    const parsed = step < 1 ? Number.parseFloat(normalized) : parseLocalizedInt(raw);
    onChange(clamp(parsed));
    setEditing(false);
  };

  const displayVal = editing ? draft : fmtNum(value, step < 1 ? 1 : 0);

  return (
    <div className="number-stepper" role="group" aria-label={ariaLabel} style={disabled ? { opacity: 0.6 } : undefined}>
      <button
        type="button"
        className="stepper-btn"
        aria-label={`Decrease ${ariaLabel || "value"}`}
        disabled={disabled || value <= min}
        onClick={() => onChange(clamp(value - step))}
      >
        <Minus size={13} aria-hidden="true" />
      </button>
      <input
        type="text"
        inputMode={step < 1 ? "decimal" : "numeric"}
        className="stepper-input"
        aria-label={ariaLabel}
        disabled={disabled}
        value={displayVal}
        onFocus={() => {
          setDraft(String(value));
          setEditing(true);
        }}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => commit(draft)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            commit(draft);
          } else if (e.key === "Escape") {
            setEditing(false);
          }
        }}
      />
      <button
        type="button"
        className="stepper-btn"
        aria-label={`Increase ${ariaLabel || "value"}`}
        disabled={disabled || value >= max}
        onClick={() => onChange(clamp(value + step))}
      >
        <Plus size={13} aria-hidden="true" />
      </button>
    </div>
  );
}

/** Multi-select column filter dropdown rendered in a portal so 1-row tables never clip or scroll awkwardly. */
export function MultiSelectFilter({
  label,
  options,
  selected,
  onChange,
  compact = false,
  align = "left",
}: {
  label: string;
  options: { value: string; label: string }[];
  selected: string[];
  onChange: (next: string[]) => void;
  compact?: boolean;
  align?: "left" | "right";
}) {
  const { t } = useTranslation();
  const { fmtNum: fmtSel } = useLocaleFormat();
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<CSSProperties | null>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (btnRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onResize = () => setOpen(false);
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    window.addEventListener("resize", onResize);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("resize", onResize);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open || !btnRef.current) return;
    const rect = btnRef.current.getBoundingClientRect();
    const menuW = Math.min(240, window.innerWidth - 24);
    let left = align === "right" ? rect.right - menuW : rect.left;
    left = Math.max(12, Math.min(left, window.innerWidth - menuW - 12));
    const spaceBelow = window.innerHeight - rect.bottom - 16;
    const openUp = spaceBelow < 220 && rect.top > spaceBelow;
    if (openUp) {
      setPos({ position: "fixed", bottom: window.innerHeight - rect.top + 6, left, width: menuW, top: "auto", right: "auto" });
    } else {
      setPos({ position: "fixed", top: rect.bottom + 6, left, width: menuW, bottom: "auto", right: "auto" });
    }
  }, [open, align]);

  const toggle = (val: string) => {
    onChange(selected.includes(val) ? selected.filter((v) => v !== val) : [...selected, val]);
  };

  return (
    <div className="multiselect-wrap">
      <button
        ref={btnRef}
        type="button"
        className={`multiselect-btn ${compact ? "multiselect-btn-compact" : ""} ${selected.length > 0 ? "has-selection" : ""}`}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={`${label}${selected.length > 0 ? ` (${fmtSel(selected.length)})` : ""}`}
        onClick={(e) => {
          e.stopPropagation();
          setOpen((v) => !v);
        }}
      >
        {!compact && (
          <span>
            {label}
            {selected.length > 0 ? ` (${fmtSel(selected.length)})` : ""}
          </span>
        )}
        {compact && selected.length > 0 && <span className="mono">{selected.length}</span>}
        {selected.length > 0 ? (
          <span
            role="button"
            tabIndex={0}
            className="multiselect-clear-pill"
            aria-label={`${t("filter.clear")} ${label}`}
            onClick={(e) => {
              e.stopPropagation();
              onChange([]);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                e.stopPropagation();
                onChange([]);
              }
            }}
          >
            <X size={11} aria-hidden="true" />
          </span>
        ) : (
          <ChevronDown size={13} className="muted" aria-hidden="true" />
        )}
      </button>
      {open &&
        pos &&
        createPortal(
          <div
            ref={menuRef}
            className="dropdown-menu multiselect-menu"
            style={pos}
            role="listbox"
            aria-multiselectable="true"
            aria-label={label}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="multiselect-head">
              <span className="dropdown-title">{label}</span>
              <div className="action-group">
                <button
                  type="button"
                  className="btn-link-xs"
                  onClick={() => onChange(options.map((o) => o.value))}
                >
                  {t("filter.all")}
                </button>
                <button
                  type="button"
                  className="btn-link-xs"
                  disabled={selected.length === 0}
                  onClick={() => onChange([])}
                >
                  {t("filter.clear")}
                </button>
              </div>
            </div>
            <div className="multiselect-list">
              {options.map((opt) => (
                <div key={opt.value} className="multiselect-option" role="option" aria-selected={selected.includes(opt.value)}>
                  <Checkbox
                    checked={selected.includes(opt.value)}
                    onChange={() => toggle(opt.value)}
                    label={opt.label}
                  />
                </div>
              ))}
            </div>
          </div>,
          document.body
        )}
    </div>
  );
}

/** Shared table/log pagination bar with custom page-size dropdown and page stepper incrementer. */
export function PaginationBar({
  page,
  totalPages,
  pageSize,
  total,
  unitLabel = "items",
  fmtNum: fmtNumProp,
  onPageChange,
  onPageSizeChange,
}: {
  page: number;
  totalPages: number;
  pageSize: number;
  total: number;
  unitLabel?: string;
  fmtNum?: (n: number) => string;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: number) => void;
}) {
  const { t } = useTranslation();
  const localeFmt = useLocaleFormat();
  const fmtNum = fmtNumProp || localeFmt.fmtNum;
  const [editingPage, setEditingPage] = useState(false);
  const [pageInput, setPageInput] = useState(String(page));
  const [sizeOpen, setSizeOpen] = useState(false);
  const sizeRef = useRef<HTMLDivElement>(null);
  useClickOutside(sizeRef, sizeOpen, () => setSizeOpen(false));

  useEffect(() => {
    setPageInput(String(page));
  }, [page]);

  const commitPage = (raw: string) => {
    const parsed = parseLocalizedInt(raw);
    const n = Math.min(totalPages, Math.max(1, Number.isNaN(parsed) ? 1 : parsed));
    setPageInput(String(n));
    setEditingPage(false);
    onPageChange(n);
  };

  return (
    <nav className="pagination-bar" aria-label="Pagination">
      <div className="page-size-picker" ref={sizeRef}>
        <span className="small muted">{t("pagination.rows_per_page")}</span>
        <div className="multiselect-wrap">
          <button
            type="button"
            className="multiselect-btn page-size-btn"
            aria-label={t("pagination.rows_per_page")}
            aria-expanded={sizeOpen}
            aria-haspopup="listbox"
            onClick={() => setSizeOpen((v) => !v)}
          >
            <span className="mono strong">{fmtNum(pageSize)}</span>
            <ChevronDown size={13} className="muted" aria-hidden="true" />
          </button>
          {sizeOpen && (
            <div className="dropdown-menu page-size-menu" role="listbox" aria-label={t("pagination.rows_per_page")}>
              {[50, 100, 250].map((sz) => (
                <button
                  key={sz}
                  type="button"
                  role="option"
                  aria-selected={pageSize === sz}
                  className={`dropdown-item mono ${pageSize === sz ? "active" : ""}`}
                  onClick={() => {
                    setSizeOpen(false);
                    onPageSizeChange(sz);
                    onPageChange(1);
                  }}
                >
                  {t("pagination.rows_count", { count: fmtNum(sz) as unknown as number })}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="page-stepper-group" role="group" aria-label={t("pagination.current")}>
        <button
          type="button"
          className="page-nav-btn"
          aria-label={t("pagination.first")}
          disabled={page <= 1}
          onClick={() => onPageChange(1)}
        >
          «
        </button>
        <button
          type="button"
          className="page-nav-btn"
          aria-label={t("pagination.prev")}
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <Minus size={12} aria-hidden="true" />
        </button>
        <form
          className="page-stepper-form"
          onSubmit={(e) => {
            e.preventDefault();
            commitPage(pageInput);
          }}
        >
          <input
            type="text"
            inputMode="numeric"
            aria-label={t("pagination.current")}
            className="page-jump-input"
            value={editingPage ? pageInput : fmtNum(page)}
            onFocus={() => {
              setPageInput(String(page));
              setEditingPage(true);
            }}
            onChange={(e) => setPageInput(e.target.value)}
            onBlur={() => commitPage(pageInput)}
          />
          <span className="mono small muted">/ {fmtNum(totalPages)}</span>
        </form>
        <button
          type="button"
          className="page-nav-btn"
          aria-label={t("pagination.next")}
          disabled={page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          <Plus size={12} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="page-nav-btn"
          aria-label={t("pagination.last")}
          disabled={page >= totalPages}
          onClick={() => onPageChange(totalPages)}
        >
          »
        </button>
      </div>

      <span className="mono small muted" aria-live="polite">
        {fmtNum(total)} {unitLabel}
      </span>
    </nav>
  );
}

/** Reusable theme-styled single-select dropdown replacing native browser <select>. */
export function SelectMenu({
  value,
  options,
  onChange,
  ariaLabel,
  placeholder,
  disabled = false,
  compact = false,
  className = "",
}: {
  value: string;
  options: { value: string; label: string; disabled?: boolean }[];
  onChange: (next: string) => void;
  ariaLabel?: string;
  placeholder?: string;
  disabled?: boolean;
  compact?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<CSSProperties | null>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (btnRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onResize = () => setOpen(false);
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    window.addEventListener("resize", onResize);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("resize", onResize);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open || !btnRef.current) return;
    const rect = btnRef.current.getBoundingClientRect();
    const menuW = Math.min(Math.max(rect.width, 180), window.innerWidth - 24);
    let left = rect.left;
    left = Math.max(12, Math.min(left, window.innerWidth - menuW - 12));
    const spaceBelow = window.innerHeight - rect.bottom - 16;
    const openUp = spaceBelow < 220 && rect.top > spaceBelow;
    if (openUp) {
      setPos({ position: "fixed", bottom: window.innerHeight - rect.top + 6, left, width: menuW, top: "auto", right: "auto" });
    } else {
      setPos({ position: "fixed", top: rect.bottom + 6, left, width: menuW, bottom: "auto", right: "auto" });
    }
  }, [open]);

  const current = options.find((o) => o.value === value);

  return (
    <div className={`select-menu-wrap ${className}`}>
      <button
        ref={btnRef}
        type="button"
        className={`select-menu-btn ${compact ? "select-menu-btn-compact" : ""} ${open ? "open" : ""}`}
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-haspopup="listbox"
        disabled={disabled}
        onClick={(e) => {
          e.stopPropagation();
          if (!disabled) setOpen((v) => !v);
        }}
      >
        <span className="select-menu-value">{current?.label ?? placeholder ?? value}</span>
        <ChevronDown size={14} className="muted select-menu-chevron" aria-hidden="true" />
      </button>
      {open &&
        pos &&
        createPortal(
          <div
            ref={menuRef}
            className="dropdown-menu select-menu-list"
            style={pos}
            role="listbox"
            aria-label={ariaLabel}
            onClick={(e) => e.stopPropagation()}
          >
            {options.map((opt) => (
              <button
                key={opt.value}
                type="button"
                role="option"
                disabled={opt.disabled}
                aria-selected={opt.value === value}
                className={`dropdown-item sidebar-lang-item ${opt.value === value ? "active" : ""}`}
                onClick={() => {
                  onChange(opt.value);
                  setOpen(false);
                }}
              >
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{opt.label}</span>
                {opt.value === value && <Check size={14} style={{ flexShrink: 0 }} aria-hidden="true" />}
              </button>
            ))}
          </div>,
          document.body
        )}
    </div>
  );
}

/** Accessible confirmation modal for destructive or high-impact actions. */
export function ConfirmModal({
  open,
  title,
  message,
  confirmLabel,
  cancelLabel,
  danger = true,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  message: string;
  confirmLabel: string;
  cancelLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onCancel]);

  if (!open) return null;

  return createPortal(
    <>
      <div className="modal-backdrop" onClick={() => !busy && onCancel()} />
      <div className="modal" role="alertdialog" aria-modal="true" aria-label={title}>
        <div className="modal-head">
          <div>
            <h3>{title}</h3>
          </div>
          <button type="button" className="btn btn-xs btn-ghost" disabled={busy} onClick={onCancel} aria-label={cancelLabel || t("common.cancel")}>
            <X size={15} aria-hidden="true" />
          </button>
        </div>
        <p className="muted" style={{ margin: 0 }}>{message}</p>
        <div className="modal-actions">
          <button type="button" className="btn btn-ghost" disabled={busy} onClick={onCancel}>
            {cancelLabel || t("common.cancel")}
          </button>
          <button type="button" className={`btn ${danger ? "btn-danger" : "btn-primary"}`} disabled={busy} onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </>,
    document.body
  );
}

/** Custom theme-styled language switcher popover. */
export function LanguageMenu({ collapsed = false, direction = "down" }: { collapsed?: boolean; direction?: "up" | "down" }) {
  const { t, i18n } = useTranslation();
  const lang = ((i18n.resolvedLanguage || i18n.language || "en").slice(0, 2) as Lang) || "en";
  const current = LANGUAGES.find((l) => l.code === lang) || LANGUAGES[0];
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  useClickOutside(wrapRef, open, () => setOpen(false));

  return (
    <div className="sidebar-lang-wrap" ref={wrapRef}>
      <button
        type="button"
        className={`nav-item sidebar-lang-btn ${open ? "active" : ""}`}
        aria-label={t("a11y.language_selector")}
        aria-expanded={open}
        aria-haspopup="listbox"
        title={current.native}
        onClick={() => setOpen((v) => !v)}
      >
        <Globe size={17} aria-hidden="true" />
        {!collapsed && (
          <>
            <span className="sidebar-item-label">{current.native}</span>
            <span className="lang-code-pill">{current.code.toUpperCase()}</span>
            <ChevronDown size={13} className="muted sidebar-item-chevron" aria-hidden="true" />
          </>
        )}
      </button>
      {open && (
        <div className={`dropdown-menu sidebar-lang-menu ${direction === "down" ? "sidebar-lang-menu-down" : ""}`} role="listbox" aria-label={t("a11y.language_selector")}>
          {LANGUAGES.map((l) => (
            <button
              key={l.code}
              type="button"
              role="option"
              aria-selected={lang === l.code}
              className={`dropdown-item sidebar-lang-item ${lang === l.code ? "active" : ""}`}
              onClick={() => {
                void i18n.changeLanguage(l.code);
                setOpen(false);
              }}
            >
              <span>{l.native}</span>
              <span className="small muted">{l.code.toUpperCase()}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** Sidebar theme switcher supporting System (auto), Light, and Dark modes. */
export function ThemeMenu({
  mode,
  collapsed,
  direction = "down",
  onChange,
}: {
  mode: ThemeMode;
  collapsed: boolean;
  direction?: "up" | "down";
  onChange: (next: ThemeMode) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  useClickOutside(wrapRef, open, () => setOpen(false));

  const options: { id: ThemeMode; label: string; icon: typeof Sun }[] = [
    { id: "system", label: t("theme.system"), icon: Monitor },
    { id: "light", label: t("theme.light"), icon: Sun },
    { id: "dark", label: t("theme.dark"), icon: Moon },
  ];
  const current = options.find((o) => o.id === mode) || options[0];
  const CurrentIcon = current.icon;

  return (
    <div className="sidebar-lang-wrap" ref={wrapRef}>
      <button
        type="button"
        className={`nav-item sidebar-lang-btn ${open ? "active" : ""}`}
        aria-label={t("settings.theme")}
        aria-expanded={open}
        aria-haspopup="listbox"
        title={collapsed ? `${t("settings.theme")}: ${current.label}` : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        <CurrentIcon size={17} strokeWidth={1.6} aria-hidden="true" />
        {!collapsed && (
          <>
            <span className="sidebar-item-label">{t("settings.theme")}</span>
            <span className="lang-code-pill">{current.label}</span>
            <ChevronDown size={13} className="muted sidebar-item-chevron" aria-hidden="true" />
          </>
        )}
      </button>
      {open && (
        <div className={`dropdown-menu sidebar-lang-menu ${direction === "down" ? "sidebar-lang-menu-down" : ""}`} role="listbox" aria-label={t("settings.theme")}>
          {options.map((opt) => {
            const Icon = opt.icon;
            return (
              <button
                key={opt.id}
                type="button"
                role="option"
                aria-selected={mode === opt.id}
                className={`dropdown-item sidebar-lang-item ${mode === opt.id ? "active" : ""}`}
                onClick={() => {
                  onChange(opt.id);
                  setOpen(false);
                }}
              >
                <span className="action-group" style={{ gap: 8 }}>
                  <Icon size={15} strokeWidth={1.5} aria-hidden="true" />
                  <span>{opt.label}</span>
                </span>
                {mode === opt.id && <Check size={14} aria-hidden="true" />}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Custom theme-styled media server switcher replacing browser native select. */
export function ServerMenu({
  servers,
  serverId,
  onSelect,
}: {
  servers: ServerSummary[];
  serverId: string;
  onSelect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  useClickOutside(wrapRef, open, () => setOpen(false));
  const active = servers.find((s) => s.id === serverId) || servers[0];
  if (!active) return null;

  return (
    <div className="server-switcher" ref={wrapRef}>
      <div className="server-switcher-head">
        <span className="server-switcher-label">{t("sidebar.active_server")}</span>
        <span className={`dot ${active.connected ? "live" : ""}`} aria-hidden="true" />
      </div>
      <div className="multiselect-wrap">
        <button
          type="button"
          className="server-picker-btn"
          aria-label={t("a11y.active_server")}
          aria-expanded={open}
          aria-haspopup="listbox"
          onClick={() => setOpen((v) => !v)}
        >
          <span className="action-group" style={{ gap: 7, minWidth: 0 }}>
            <Server size={13} className="muted" aria-hidden="true" />
            <span className="strong" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {active.server_name || active.name}
            </span>
          </span>
          <ChevronDown size={14} className="muted" aria-hidden="true" />
        </button>
        {open && (
          <div className="dropdown-menu server-picker-menu" role="listbox" aria-label={t("a11y.active_server")}>
            {servers.map((s) => (
              <button
                key={s.id}
                type="button"
                role="option"
                aria-selected={s.id === serverId}
                className={`dropdown-item sidebar-lang-item ${s.id === serverId ? "active" : ""}`}
                onClick={() => {
                  onSelect(s.id);
                  setOpen(false);
                }}
              >
                <span className="action-group" style={{ gap: 7 }}>
                  <span className={`dot ${s.connected ? "live" : ""}`} aria-hidden="true" />
                  <span>{s.server_name || s.name}</span>
                </span>
                <span className="small muted">{s.connected ? t("sidebar.online") : t("sidebar.offline")}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
