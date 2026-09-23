import { describe, expect, it } from "vitest";

import {
  CaptureAcquisitionPlanError,
  findCaptureAcquisitionRequirement,
  parseCaptureAcquisitionPlan,
} from "../src/index.js";

describe("capture acquisition plan", () => {
  it("accepts bounded 1.1 pose policies without changing legacy 1.0 plans", () => {
    const input = plan();
    const pose = { target_degrees: 20, tolerance_degrees: 7, hold_duration_ms: 450 };
    Object.assign(input.requirements[0]!.challenges[0]!, { pose });
    expect(() => parseCaptureAcquisitionPlan(input)).toThrow();
    input.schema_version = "1.1";
    expect(parseCaptureAcquisitionPlan(input).requirements[0]!.challenges[0]!.pose).toEqual(pose);
    expect(parseCaptureAcquisitionPlan(plan()).schema_version).toBe("1.0");
  });

  it.each([
    { target_degrees: 0 },
    { tolerance_degrees: 20 },
    { hold_duration_ms: 0 },
    { hold_duration_ms: NaN },
    { bypass: true },
  ])("rejects weakening or malformed pose policies: %j", (override) => {
    const input = plan();
    input.schema_version = "1.1";
    Object.assign(input.requirements[0]!.challenges[0]!, {
      pose: { target_degrees: 20, tolerance_degrees: 7, hold_duration_ms: 450, ...override },
    });
    expect(() => parseCaptureAcquisitionPlan(input)).toThrow();
  });

  it("rejects a pose policy whose holds cannot fit the deadline", () => {
    const input = plan();
    input.schema_version = "1.1";
    Object.assign(input.requirements[0]!.challenges[0]!, {
      maximum_duration_ms: 500,
      pose: { target_degrees: 20, tolerance_degrees: 7, hold_duration_ms: 450 },
    });
    expect(() => parseCaptureAcquisitionPlan(input)).toThrow();
  });
  it("strictly parses the ordered active-liveness contract", () => {
    const parsed = parseCaptureAcquisitionPlan(plan());

    expect(parsed.requirements[0]?.challenges.map((challenge) => challenge.prompt)).toEqual([
      "neutral",
      "turn_left",
      "turn_right",
    ]);
    expect(
      findCaptureAcquisitionRequirement(parsed, {
        evidenceType: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image",
        acquisitionMethod: "idenqa.method.live_camera",
      }).id,
    ).toBe("selfie.live");
  });

  it("rejects unknown fields and duplicate challenge identities", () => {
    const unknown = { ...plan(), token: "must-not-enter-the-plan" };
    expect(() => parseCaptureAcquisitionPlan(unknown)).toThrowError(
      expect.objectContaining({ code: "CAPTURE_ACQUISITION_PLAN_INVALID" }),
    );

    const duplicate = plan();
    duplicate.requirements[0]!.challenges[1]!.id = "challenge.neutral";
    expect(() => parseCaptureAcquisitionPlan(duplicate)).toThrowError(
      expect.objectContaining({ code: "CAPTURE_ACQUISITION_PLAN_INVALID" }),
    );
  });

  it("rejects inconsistent quality ranges and ambiguous bindings", () => {
    const invalidRange = plan();
    invalidRange.requirements[0]!.quality.minimum_brightness = 0.9;
    invalidRange.requirements[0]!.quality.maximum_brightness = 0.2;
    expect(() => parseCaptureAcquisitionPlan(invalidRange)).toThrowError(
      expect.objectContaining({ code: "CAPTURE_ACQUISITION_PLAN_INVALID" }),
    );

    const ambiguous = plan();
    ambiguous.requirements.push({
      ...ambiguous.requirements[0]!,
      id: "selfie.second",
      challenges: [],
    });
    expect(() => parseCaptureAcquisitionPlan(ambiguous)).toThrowError(
      expect.objectContaining({ code: "CAPTURE_ACQUISITION_PLAN_INVALID" }),
    );
  });

  it("fails closed when a requested acquisition binding is absent", () => {
    const parsed = parseCaptureAcquisitionPlan(plan());
    expect(() =>
      findCaptureAcquisitionRequirement(parsed, {
        evidenceType: "idenqa.evidence.document_image",
        artefact: "idenqa.artefact.document_front",
        acquisitionMethod: "idenqa.method.live_camera",
      }),
    ).toThrowError(
      expect.objectContaining({
        code: "CAPTURE_ACQUISITION_REQUIREMENT_NOT_FOUND",
      } satisfies Partial<CaptureAcquisitionPlanError>),
    );
  });
});

function plan() {
  return {
    schema_version: "1.0",
    plan_id: "plan.pan_african_individual.v1",
    session_id: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
    requirements: [
      {
        id: "selfie.live",
        evidence_type: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image",
        acquisition_method: "idenqa.method.live_camera",
        camera: "front",
        quality: {
          minimum_width: 720,
          minimum_height: 720,
          maximum_bytes: 8_388_608,
          minimum_brightness: 0.18,
          maximum_brightness: 0.9,
        },
        challenges: [
          { id: "challenge.neutral", prompt: "neutral", maximum_duration_ms: 5_000 },
          { id: "challenge.turn_left", prompt: "turn_left", maximum_duration_ms: 5_000 },
          { id: "challenge.turn_right", prompt: "turn_right", maximum_duration_ms: 5_000 },
        ],
      },
    ],
  };
}
