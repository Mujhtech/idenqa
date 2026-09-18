import { describe, expect, it, vi } from "vitest";

import {
  CaptureMethodAdapterError,
  captureMethodAdapterCopy,
  findCaptureMethodAdapter,
  normalizeCaptureMethodProgress,
  requireCaptureMethodAdapter,
  validateCaptureMethodAdapters,
  type CaptureMethodAdapter,
} from "../src/index.js";

const context = {
  verificationId: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
  requirementKey: "chip",
  evidenceType: "com.example.evidence.document_chip",
  artefact: "com.example.artefact.document_chip",
  acquisitionMethod: "com.example.method.secure_nfc",
};

describe("capture method adapters", () => {
  it("selects an exact namespaced adapter and resolves locale copy", () => {
    const adapter = extensionAdapter({
      copy: (locale) => ({
        label: locale === "fr" ? "Puce sécurisée" : "Secure Chip",
        action: "Start Secure Chip",
        description: "Read the document chip",
        title: "Read Your Document Chip",
        preparation: "Hold the document near this device.",
        instruction: "Keep the document still until the secure read completes.",
      }),
    });
    const adapters = validateCaptureMethodAdapters([adapter]);

    expect(requireCaptureMethodAdapter(adapters, context)).toBe(adapter);
    expect(captureMethodAdapterCopy(adapter, "fr").label).toBe("Puce sécurisée");
    expect(captureMethodAdapterCopy(adapter, "fr").title).toBe("Read Your Document Chip");
  });

  it("rejects missing, duplicate, ambiguous, and non-namespaced adapters", () => {
    expect(() => requireCaptureMethodAdapter([], context)).toThrowError(
      expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_MISSING" }),
    );
    const duplicate = extensionAdapter();
    expect(() => validateCaptureMethodAdapters([duplicate, duplicate])).toThrowError(
      expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_INVALID" }),
    );
    const broad = extensionAdapter();
    const exact = extensionAdapter({ requirementKey: "chip" });
    expect(() => findCaptureMethodAdapter([broad, exact], context)).toThrowError(
      expect.objectContaining({
        code: "CAPTURE_METHOD_ADAPTER_AMBIGUOUS",
      } satisfies Partial<CaptureMethodAdapterError>),
    );
    expect(() =>
      validateCaptureMethodAdapters([extensionAdapter({ method: "secure_nfc" })]),
    ).toThrowError(expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_INVALID" }));
    expect(() =>
      validateCaptureMethodAdapters([extensionAdapter({ requirementKey: "Invalid-Key" })]),
    ).toThrowError(expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_INVALID" }));
    expect(() =>
      validateCaptureMethodAdapters([extensionAdapter({ artefact: "document_chip" })]),
    ).toThrowError(expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_INVALID" }));
    expect(() =>
      validateCaptureMethodAdapters([
        extensionAdapter({ presentation: "unknown" as "active_liveness" }),
      ]),
    ).toThrowError(expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_INVALID" }));
  });

  it("accepts only closed identifier-free progress", () => {
    expect(
      normalizeCaptureMethodProgress({
        phase: "challenge",
        current: 2,
        total: 3,
        prompt: "turn_left",
      }),
    ).toEqual({ phase: "challenge", current: 2, total: 3, prompt: "turn_left" });
    expect(() => normalizeCaptureMethodProgress({ phase: "challenge" })).toThrowError(
      expect.objectContaining({ code: "CAPTURE_METHOD_ADAPTER_INVALID" }),
    );
  });
});

function extensionAdapter(overrides: Partial<CaptureMethodAdapter> = {}): CaptureMethodAdapter {
  return {
    method: "com.example.method.secure_nfc",
    copy: {
      label: "Secure Chip",
      action: "Start Secure Chip",
      description: "Read the document chip",
      preparation: "Hold the document near this device.",
      instruction: "Keep the document still until the secure read completes.",
    },
    acquire: vi.fn(),
    ...overrides,
  };
}
