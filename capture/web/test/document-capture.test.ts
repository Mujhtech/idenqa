import { describe, expect, it } from "vitest";

import {
  DOCUMENT_ARTEFACT_BACK,
  DOCUMENT_ARTEFACT_FRONT,
  isDocumentArtefact,
  normalizeCaptureDocumentCaptureOptions,
} from "../src/index.js";

describe("document capture integration", () => {
  it("recognises only document front and back artefacts", () => {
    expect(isDocumentArtefact(DOCUMENT_ARTEFACT_FRONT)).toBe(true);
    expect(isDocumentArtefact(DOCUMENT_ARTEFACT_BACK)).toBe(true);
    expect(isDocumentArtefact("idenqa.artefact.selfie_image")).toBe(false);
    expect(isDocumentArtefact("idenqa.artefact.document_image")).toBe(false);
  });

  it("enables detection, automatic capture, and a visible confirmation delay by default", () => {
    const options = normalizeCaptureDocumentCaptureOptions();
    expect(options.enabled).toBe(true);
    expect(options.autoCapture).toBe(true);
    expect(options.autoCaptureDelayMs).toBe(250);
    expect(options.observationIntervalMs).toBe(200);
    expect(options.observationMaxDimension).toBe(240);
    expect(options.gate.requiredStableFrames).toBeGreaterThan(1);
    expect(options.correction.maxDimension).toBeLessThanOrEqual(4096);
  });

  it("merges partial overrides without dropping unrelated defaults", () => {
    const options = normalizeCaptureDocumentCaptureOptions({
      autoCapture: false,
      autoCaptureDelayMs: 0,
      gate: { requiredStableFrames: 3 },
      correction: { maxAspect: 1.7 },
    });
    expect(options.autoCapture).toBe(false);
    expect(options.autoCaptureDelayMs).toBe(0);
    expect(options.gate.requiredStableFrames).toBe(3);
    expect(options.gate.minCoverage).toBeGreaterThan(0);
    expect(options.correction.maxAspect).toBe(1.7);
    expect(options.correction.minAspect).toBeGreaterThanOrEqual(1);
  });

  it("fails closed on invalid option values", () => {
    expect(() => normalizeCaptureDocumentCaptureOptions({ observationIntervalMs: 10 })).toThrow(
      TypeError,
    );
    expect(() => normalizeCaptureDocumentCaptureOptions({ autoCapture: "yes" as never })).toThrow(
      TypeError,
    );
    expect(() => normalizeCaptureDocumentCaptureOptions({ gate: { minCoverage: 5 } })).toThrow(
      TypeError,
    );
  });
});
