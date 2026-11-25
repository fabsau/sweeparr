export type ThemeMode = "system" | "light" | "dark";
export type AccentColor = "amber" | "blue" | "emerald" | "rose" | "violet" | "orange";

export const ACCENT_OPTIONS: { id: AccentColor; labelKey: string; swatch: string }[] = [
  { id: "amber", labelKey: "settings.accent_amber", swatch: "#d99100" },
  { id: "blue", labelKey: "settings.accent_blue", swatch: "#2563eb" },
  { id: "emerald", labelKey: "settings.accent_emerald", swatch: "#059669" },
  { id: "rose", labelKey: "settings.accent_rose", swatch: "#e11d48" },
  { id: "violet", labelKey: "settings.accent_violet", swatch: "#7c3aed" },
  { id: "orange", labelKey: "settings.accent_orange", swatch: "#ea580c" },
];

const VALID_THEMES = new Set<ThemeMode>(["system", "light", "dark"]);
const VALID_ACCENTS = new Set<AccentColor>(["amber", "blue", "emerald", "rose", "violet", "orange"]);

export function normalizeThemeMode(raw?: string | null): ThemeMode {
  if (raw && VALID_THEMES.has(raw as ThemeMode)) return raw as ThemeMode;
  return "system";
}

export function normalizeAccentColor(raw?: string | null): AccentColor {
  if (raw && VALID_ACCENTS.has(raw as AccentColor)) return raw as AccentColor;
  return "amber";
}

export function getStoredThemeMode(): ThemeMode {
  try {
    return normalizeThemeMode(localStorage.getItem("sweeparr_theme_mode"));
  } catch {
    return "system";
  }
}

export function setStoredThemeMode(mode: ThemeMode) {
  try {
    localStorage.setItem("sweeparr_theme_mode", mode);
  } catch {
    // ignore storage errors
  }
}

export function getStoredAccentColor(): AccentColor {
  try {
    return normalizeAccentColor(localStorage.getItem("sweeparr_accent_color"));
  } catch {
    return "amber";
  }
}

export function setStoredAccentColor(accent: AccentColor) {
  try {
    localStorage.setItem("sweeparr_accent_color", accent);
  } catch {
    // ignore storage errors
  }
}

export function resolveTheme(mode: ThemeMode): "light" | "dark" {
  if (mode === "light" || mode === "dark") return mode;
  if (typeof window !== "undefined" && window.matchMedia) {
    return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  }
  return "dark";
}

export function applyTheme(mode: ThemeMode, accent: AccentColor) {
  const resolved = resolveTheme(mode);
  document.documentElement.setAttribute("data-theme", resolved);
  document.documentElement.setAttribute("data-accent", accent);
  document.documentElement.style.colorScheme = resolved;
}
