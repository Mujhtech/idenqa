import { describe, expect, it } from "vitest";
import { auditCaptureThemeContrast, captureContrastRatio } from "../src/theme-contrast.js";

const palette = {
  background: "#ffffff",
  surface: "#f3f7f5",
  surfaceStrong: "#e4f2ed",
  text: "#14201d",
  muted: "#52605c",
  accent: "#087f68",
  accentStrong: "#075345",
  accentForeground: "#ffffff",
};

describe("theme contrast diagnostics", () => {
  it("matches reference ratios and treats the threshold without rounding", () => {
    expect(captureContrastRatio("#000", "#fff")).toBe(21);
    expect(captureContrastRatio("rgb(255, 255, 255)", "#ffffff")).toBe(1);
    expect(captureContrastRatio("#767676", "#fff")).toBeGreaterThan(4.5);
    expect(captureContrastRatio("#777777", "#fff")).toBeLessThan(4.5);
    expect(captureContrastRatio("#0000ff", "#fff")).toBeCloseTo(8.59, 2);
  });

  it("accepts the default light palette", () => {
    expect(auditCaptureThemeContrast(palette)).toEqual([]);
  });

  it("reports low-contrast tenant body, button, hover and focus colours", () => {
    const issues = auditCaptureThemeContrast({
      ...palette,
      text: "#eeeeee",
      accent: "#ffffff",
      accentStrong: "#ffffff",
    });
    expect(issues).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ foreground: "text", background: "background", minimum: 4.5 }),
        expect.objectContaining({ foreground: "accentForeground", background: "accent", ratio: 1 }),
        expect.objectContaining({
          foreground: "accentForeground",
          background: "accentStrong",
          ratio: 1,
        }),
        expect.objectContaining({ foreground: "accent", background: "background", minimum: 3 }),
      ]),
    );
  });

  it.each([
    "transparent",
    "rgba(0, 0, 0, 0.5)",
    "var(--brand)",
    "rgb(256, 0, 0)",
    "#12345",
    "color(display-p3 1 0 0)",
  ])("does not report an unsupported colour as passing: %s", (colour) => {
    expect(captureContrastRatio(colour, "#fff")).toBeUndefined();
    expect(auditCaptureThemeContrast({ ...palette, text: colour })).toContainEqual({
      foreground: "text",
      background: "background",
      minimum: 4.5,
      reason: "unsupported_colour",
    });
  });
});
