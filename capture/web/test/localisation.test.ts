import { describe, expect, it } from "vitest";

import { CaptureLocalisationError, createCaptureLocalizer } from "../src/localisation.js";

describe("createCaptureLocalizer", () => {
  it("resolves exact, base-language, and English fallbacks in order", () => {
    const localizer = createCaptureLocalizer("fr-CA", {
      fr: { secureCapture: "Capture sécurisée", chooseFile: "Choisir un fichier" },
      "fr-CA": { secureCapture: "Capture sécurisée canadienne" },
    });

    expect(localizer.locale).toBe("fr-CA");
    expect(localizer.text("secureCapture")).toBe("Capture sécurisée canadienne");
    expect(localizer.text("chooseFile")).toBe("Choisir un fichier");
    expect(localizer.text("refuse")).toBe("Refuse");
  });

  it("formats message values and numbers with the resolved locale", () => {
    const localizer = createCaptureLocalizer("de-DE", {
      de: { progressSummary: "{completed} von {total} Schritten abgeschlossen." },
    });

    expect(
      localizer.text("progressSummary", {
        completed: localizer.formatNumber(1_000),
        total: localizer.formatNumber(2_000),
      }),
    ).toBe("1.000 von 2.000 Schritten abgeschlossen.");
  });

  it("derives right-to-left presentation from the locale", () => {
    expect(createCaptureLocalizer("ar").direction).toBe("rtl");
  });

  it.each([
    ["invalid locale", "not_a_locale", {}, /locale/],
    ["duplicate canonical locale", "en", { en: {}, EN: {} }, /repeats locale/],
    ["unknown key", "en", { en: { unknown: "value" } }, /unknown key/],
    ["empty value", "en", { en: { refuse: "  " } }, /must not be empty/],
    [
      "missing placeholder",
      "en",
      { en: { progressSummary: "Capture complete." } },
      /preserve placeholders/,
    ],
    [
      "unexpected placeholder",
      "en",
      { en: { refuse: "Refuse {subject}." } },
      /preserve placeholders/,
    ],
  ])("rejects %s", (_case, locale, catalogue, message) => {
    expect(() =>
      createCaptureLocalizer(locale, catalogue as Parameters<typeof createCaptureLocalizer>[1]),
    ).toThrowError(CaptureLocalisationError);
    expect(() =>
      createCaptureLocalizer(locale, catalogue as Parameters<typeof createCaptureLocalizer>[1]),
    ).toThrow(message);
  });
});
