import { describe, expect, it } from "vitest";
import { createLocaleFormatter, parseLocalizedInt } from "../utils/format";

describe("locale formatting & parsing", () => {
  it("formats numbers in Western and Arabic-Indic locales", () => {
    const enFmt = createLocaleFormatter("en-US", "en-US", "12h");
    expect(enFmt.fmtNum(12345)).toBe("12,345");
    expect(enFmt.fmtPct(87, 0)).toContain("87");

    const arFmt = createLocaleFormatter("ar-EG", "ar-SA", "24h");
    const arStr = arFmt.fmtNum(123);
    expect(arStr).not.toBe("123"); // Uses Arabic-Indic digits
    expect(parseLocalizedInt(arStr)).toBe(123);
  });

  it("parses Western, Arabic-Indic, and Persian digits cleanly", () => {
    expect(parseLocalizedInt("42")).toBe(42);
    expect(parseLocalizedInt("١٢٣")).toBe(123);
    expect(parseLocalizedInt("۴۵۶")).toBe(456);
    expect(Number.isNaN(parseLocalizedInt("abc"))).toBe(true);
  });

  it("formats bytes cleanly across units", () => {
    const fmt = createLocaleFormatter("en-US", "en-US", "24h");
    expect(fmt.fmtBytes(0)).toBe("—");
    expect(fmt.fmtBytes(1024)).toContain("KiB");
    expect(fmt.fmtBytes(1024 * 1024 * 1024)).toContain("GiB");
  });
});
