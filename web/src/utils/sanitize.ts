/**
 * Client-side input sanitization helpers (defense-in-depth alongside backend validation).
 */

const CONTROL_CHARS_RE = /[\u0000-\u001F\u007F]/g;

/** Strips ASCII/C0 control characters (including NUL, CR, LF) and bounds length. */
export function stripControlChars(raw: string, maxLen = 256): string {
  const cleaned = raw.replace(CONTROL_CHARS_RE, "");
  return maxLen > 0 && cleaned.length > maxLen ? cleaned.slice(0, maxLen) : cleaned;
}

/** Keeps only digits (e.g. for 6-digit TOTP codes). */
export function sanitizeDigits(raw: string, maxLen = 6): string {
  return raw.replace(/\D/g, "").slice(0, maxLen);
}

/** Keeps only numeric decimal characters (digits, dot, comma) for budget/float inputs. */
export function sanitizeDecimalInput(raw: string, maxLen = 12): string {
  return raw.replace(/[^0-9.,]/g, "").slice(0, maxLen);
}

/** Validates an external URL and ensures it strictly uses https:// (prevents javascript:/data: injection). */
export function safeExternalURL(raw?: string | null): string | null {
  if (!raw) return null;
  try {
    const u = new URL(raw.trim());
    if (u.protocol !== "https:") return null;
    if (u.username || u.password) return null;
    return u.toString();
  } catch {
    return null;
  }
}
