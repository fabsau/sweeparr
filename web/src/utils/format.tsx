import { createContext, useContext, useMemo, type ReactNode } from "react";

export interface LocaleFormatContextValue {
  numberLocale: string;
  dateLocale: string;
  timeFormat: "24h" | "12h";
  fmtNum: (n: number, maxFrac?: number) => string;
  fmtFixed: (n: number, frac?: number) => string;
  fmtPct: (pct: number, frac?: number) => string;
  fmtBytes: (bytes: number) => string;
  fmtDate: (unixSec: number) => string;
  fmtDateTime: (unixSec: number) => string;
  fmtTime: (unixSec: number) => string;
}

const DEFAULT_LOCALE = "en-US";

/** Convert Eastern Arabic / Persian numerals to ASCII digits before parsing numbers. */
export function parseLocalizedInt(raw: string): number {
  const normalized = raw
    .replace(/[\u0660-\u0669]/g, (ch) => String(ch.charCodeAt(0) - 0x0660))
    .replace(/[\u06F0-\u06F9]/g, (ch) => String(ch.charCodeAt(0) - 0x06f0))
    .replace(/[^0-9-]/g, "");
  return Number.parseInt(normalized, 10);
}

export function createLocaleFormatter(
  numberLocale?: string,
  dateLocale?: string,
  timeFormat?: string
): LocaleFormatContextValue {
  const numLoc = numberLocale || DEFAULT_LOCALE;
  const dtLoc = dateLocale || DEFAULT_LOCALE;
  const tf: "24h" | "12h" = timeFormat === "12h" ? "12h" : "24h";
  const hour12 = tf === "12h";

  const fmtNum = (n: number, maxFrac = 0) => {
    const safe = Number.isFinite(n) ? n : 0;
    try {
      return new Intl.NumberFormat(numLoc, { maximumFractionDigits: maxFrac }).format(safe);
    } catch {
      return new Intl.NumberFormat(DEFAULT_LOCALE, { maximumFractionDigits: maxFrac }).format(safe);
    }
  };

  const fmtFixed = (n: number, frac = 1) => {
    const safe = Number.isFinite(n) ? n : 0;
    try {
      return new Intl.NumberFormat(numLoc, {
        minimumFractionDigits: frac,
        maximumFractionDigits: frac,
      }).format(safe);
    } catch {
      return new Intl.NumberFormat(DEFAULT_LOCALE, {
        minimumFractionDigits: frac,
        maximumFractionDigits: frac,
      }).format(safe);
    }
  };

  const fmtPct = (pct: number, frac = 1) => {
    const safe = Number.isFinite(pct) ? pct : 0;
    try {
      return new Intl.NumberFormat(numLoc, {
        style: "percent",
        minimumFractionDigits: frac,
        maximumFractionDigits: frac,
      }).format(safe / 100);
    } catch {
      return `${safe.toFixed(frac)}%`;
    }
  };

  const fmtBytes = (bytes: number) => {
    if (!bytes || bytes <= 0) return "—";
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let v = bytes;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${fmtFixed(v, i >= 3 ? 2 : 1)} ${units[i]}`;
  };

  const fmtDate = (unixSec: number) => {
    if (!unixSec) return "—";
    const d = new Date(unixSec * 1000);
    try {
      if (dtLoc === "sv-SE") {
        const y = d.getFullYear();
        const m = String(d.getMonth() + 1).padStart(2, "0");
        const day = String(d.getDate()).padStart(2, "0");
        return `${y}-${m}-${day}`;
      }
      return new Intl.DateTimeFormat(dtLoc, { year: "numeric", month: "2-digit", day: "2-digit" }).format(d);
    } catch {
      return d.toISOString().slice(0, 10);
    }
  };

  const fmtDateTime = (unixSec: number) => {
    if (!unixSec) return "—";
    const d = new Date(unixSec * 1000);
    try {
      if (dtLoc === "sv-SE") {
        const y = d.getFullYear();
        const m = String(d.getMonth() + 1).padStart(2, "0");
        const day = String(d.getDate()).padStart(2, "0");
        const timeStr = new Intl.DateTimeFormat("en-GB", {
          hour: "2-digit",
          minute: "2-digit",
          hour12,
        }).format(d);
        return `${y}-${m}-${day} ${timeStr}`;
      }
      return new Intl.DateTimeFormat(dtLoc, {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12,
      }).format(d);
    } catch {
      return d.toISOString().slice(0, 16).replace("T", " ");
    }
  };

  const fmtTime = (unixSec: number) => {
    if (!unixSec) return "—";
    const d = new Date(unixSec * 1000);
    try {
      return new Intl.DateTimeFormat(dtLoc === "sv-SE" ? "en-GB" : dtLoc, {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12,
      }).format(d);
    } catch {
      return d.toISOString().slice(11, 19);
    }
  };

  return {
    numberLocale: numLoc,
    dateLocale: dtLoc,
    timeFormat: tf,
    fmtNum,
    fmtFixed,
    fmtPct,
    fmtBytes,
    fmtDate,
    fmtDateTime,
    fmtTime,
  };
}

const LocaleFormatContext = createContext<LocaleFormatContextValue>(createLocaleFormatter());

export function LocaleFormatProvider({
  numberLocale,
  dateLocale,
  timeFormat,
  children,
}: {
  numberLocale?: string;
  dateLocale?: string;
  timeFormat?: string;
  children: ReactNode;
}) {
  const value = useMemo(
    () => createLocaleFormatter(numberLocale, dateLocale, timeFormat),
    [numberLocale, dateLocale, timeFormat]
  );
  return <LocaleFormatContext.Provider value={value}>{children}</LocaleFormatContext.Provider>;
}

export function useLocaleFormat(): LocaleFormatContextValue {
  return useContext(LocaleFormatContext);
}
