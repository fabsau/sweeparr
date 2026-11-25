import { describe, expect, it } from "vitest";
import { safeExternalURL, sanitizeDecimalInput, sanitizeDigits, stripControlChars } from "../utils/sanitize";

describe("input & URL sanitization", () => {
  it("safeExternalURL allows https:// and blocks dangerous schemes", () => {
    expect(safeExternalURL("https://github.com/fabsau/sweeparr")).toBe("https://github.com/fabsau/sweeparr");
    expect(safeExternalURL("javascript:alert(1)")).toBeNull();
    expect(safeExternalURL("data:text/html,<script>alert(1)</script>")).toBeNull();
    expect(safeExternalURL("http://insecure.example.com")).toBeNull();
    expect(safeExternalURL("")).toBeNull();
  });

  it("stripControlChars removes control characters and enforces maxLen", () => {
    expect(stripControlChars("hello\x00\x07world", 20)).toBe("helloworld");
    expect(stripControlChars("abcdef", 3)).toBe("abc");
  });

  it("sanitizeDigits and sanitizeDecimalInput filter invalid characters", () => {
    expect(sanitizeDigits("12a3b4", 6)).toBe("1234");
    expect(sanitizeDecimalInput("450.5abc", 10)).toBe("450.5");
  });
});
