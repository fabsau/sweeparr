import { useEffect, useState } from "react";
import type { MediaItem } from "./types";

export function isSlowConnection(): boolean {
  const nav = navigator as Navigator & { connection?: { effectiveType?: string; saveData?: boolean } };
  const conn = nav.connection;
  if (!conn) return false;
  return Boolean(conn.saveData || conn.effectiveType === "slow-2g" || conn.effectiveType === "2g" || conn.effectiveType === "3g");
}

export async function api<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const upper = method.toUpperCase();
  const maxAttempts = upper === "GET" ? 3 : 1;
  const timeoutMs = isSlowConnection() ? 45000 : 25000;
  let lastErr: Error | null = null;

  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    if (attempt > 0) {
      await new Promise((r) => setTimeout(r, attempt * 1000));
    }
    const ctrl = new AbortController();
    const timer = window.setTimeout(() => ctrl.abort(), timeoutMs);
    try {
      const res = await fetch(path, {
        method,
        credentials: "same-origin",
        signal: ctrl.signal,
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "sweeparr",
        },
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      window.clearTimeout(timer);
      if (res.status >= 502 && res.status <= 504 && attempt + 1 < maxAttempts) {
        lastErr = new Error(`HTTP ${res.status}`);
        continue;
      }
      const data = await res.json().catch(() => ({}));
      if (res.status === 401 && !path.startsWith("/api/auth/login") && !path.startsWith("/api/auth/setup") && !path.startsWith("/api/auth/passkey/login") && !path.startsWith("/api/auth/mfa/verify")) {
        window.dispatchEvent(new CustomEvent("sweeparr:unauthorized"));
      }
      if (!res.ok) {
        throw new Error((data as { detail?: string }).detail || `HTTP ${res.status}`);
      }
      return data as T;
    } catch (err) {
      window.clearTimeout(timer);
      const e = err instanceof Error ? err : new Error(String(err));
      if (e.name === "AbortError") {
        lastErr = new Error("Request timed out (slow connection)");
      } else {
        lastErr = e;
      }
      if (attempt + 1 < maxAttempts && (e.name === "AbortError" || e.name === "TypeError")) {
        continue;
      }
      throw lastErr;
    }
  }
  throw lastErr || new Error("Network error");
}

export function serverPath(serverId: string, subPath: string): string {
  return `/api/servers/${encodeURIComponent(serverId)}${subPath}`;
}

export function serverApi<T>(serverId: string, subPath: string, method = "GET", body?: unknown): Promise<T> {
  return api<T>(serverPath(serverId, subPath), method, body);
}

export function useDebouncedValue<T>(value: T, delayMs = 200): T {
  const [debounced, setDebounced] = useState<T>(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}

export function formatItemTitle(it: Pick<MediaItem, "grandparent_title" | "title">): string {
  return it.grandparent_title ? `${it.grandparent_title} — ${it.title}` : it.title;
}

export function fmtBytes(bytes: number): string {
  if (!bytes) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}
