import { describe, expect, it } from "vitest";
import type { ExperienceResolution } from "@idenqa/sdk";
import { captureExperiencePresentation } from "../src/experience.js";

function resolution(): ExperienceResolution {
  return {
    manifest: {
      document: {
        schema_version: "1.0",
        experience_id: "exp_01J00000000000000000000000",
        version: 3,
        name: "Acme capture",
        copy: {
          version: "tc_acme_v1",
          locales: [
            {
              locale: "fr",
              entries: [
                { key: "ui.intro.title", value: "Vérifiez votre identité" },
                { key: "ui.get_started", value: "Commencer" },
                { key: "internal.tenant.only", value: "Ignored" },
              ],
            },
          ],
        },
        mandatory_copy_version: "mc-2026-09-01",
        default_locale: "en",
        targeting: [],
        links: {
          support: "https://acme.example/support",
          privacy: "https://acme.example/privacy",
          terms: "https://acme.example/terms",
        },
        theme: {
          primary_color: "#1f6feb",
          accent_color: "#0b3d91",
          background_color: "#ffffff",
          text_color: "#1b1f23",
        },
      },
      digest: "a".repeat(64),
      key_id: "expkey_v1",
      algorithm: "ed25519",
      signature: "0".repeat(128),
    },
    mandatory_copy: {
      version: "mc-2026-09-01",
      digest: "b".repeat(64),
      entries: [
        { key: "regulatory.processing_notice", value: "Your identity information is processed." },
      ],
    },
    pinned: {
      experience_id: "exp_01J00000000000000000000000",
      version: 3,
      locale: "fr",
      tenant_copy_version: "tc_acme_v1",
      mandatory_copy_version: "mc-2026-09-01",
      source: "pinned",
      digest: "a".repeat(64),
      key_id: "expkey_v1",
      pinned_at: "2026-09-20T12:00:00Z",
    },
    fallback: false,
  };
}

describe("capture experience presentation", () => {
  it("applies pinned-locale tenant copy only for allow-listed UI keys", () => {
    const presentation = captureExperiencePresentation(resolution());
    expect(presentation.locale).toBe("fr");
    expect(presentation.messageCatalogue).toEqual({
      fr: { introTitle: "Vérifiez votre identité", getStarted: "Commencer" },
    });
    expect(JSON.stringify(presentation.messageCatalogue)).not.toContain("internal.tenant.only");
  });

  it("maps safe theme tokens and exposes validated links and mandatory copy", () => {
    const presentation = captureExperiencePresentation(resolution());
    expect(presentation.theme).toEqual({
      "--idq-capture-accent": "#1f6feb",
      "--idq-capture-accent-strong": "#0b3d91",
      "--idq-capture-background": "#ffffff",
      "--idq-capture-text": "#1b1f23",
    });
    expect(presentation.links?.support).toBe("https://acme.example/support");
    expect(presentation.mandatoryCopy?.["regulatory.processing_notice"]).toContain("processed");
    expect(presentation.fallback).toBe(false);
    expect(presentation.pinned?.source).toBe("pinned");
  });

  it("keeps the mandatory copy separate from tenant copy", () => {
    const presentation = captureExperiencePresentation(resolution());
    const tenantValues = Object.values(presentation.messageCatalogue?.fr ?? {});
    expect(tenantValues).not.toContain("Your identity information is processed.");
  });
});
