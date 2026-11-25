import { describe, expect, it } from "vitest";
import { en } from "../locales/en";
import { de } from "../locales/de";
import { es } from "../locales/es";
import { fr } from "../locales/fr";
import { it as itLocale } from "../locales/it";

const LOCALES: Record<string, Record<string, string>> = { en, de, es, fr, it: itLocale };
const PLACEHOLDER_RE = /\{\{([a-zA-Z0-9_]+)\}\}/g;

function extractPlaceholders(s: string): string[] {
  const out: string[] = [];
  let m: RegExpExecArray | null;
  while ((m = PLACEHOLDER_RE.exec(s)) !== null) {
    out.push(m[1]);
  }
  return out.sort();
}

describe("i18n 5-locale parity & quality", () => {
  const enKeys = Object.keys(en).sort();

  it("all 5 locales have 100% key parity with en", () => {
    for (const [lang, dict] of Object.entries(LOCALES)) {
      const keys = Object.keys(dict).sort();
      expect(keys, `${lang} keys must match en`).toEqual(enKeys);
    }
  });

  it("no locale contains empty values or double-colon bugs", () => {
    for (const [lang, dict] of Object.entries(LOCALES)) {
      for (const [k, v] of Object.entries(dict)) {
        expect(v.trim().length, `${lang}.${k} must be non-empty`).toBeGreaterThan(0);
        expect(v.includes("::"), `${lang}.${k} must not contain '::'`).toBe(false);
      }
    }
  });

  it("interpolation placeholders match across all 5 locales", () => {
    for (const key of enKeys) {
      const expected = extractPlaceholders(en[key]);
      for (const [lang, dict] of Object.entries(LOCALES)) {
        expect(extractPlaceholders(dict[key]), `placeholders mismatch on ${lang}.${key}`).toEqual(expected);
      }
    }
  });
});
