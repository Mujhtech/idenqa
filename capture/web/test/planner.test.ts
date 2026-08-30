import { describe, expect, it } from "vitest";

import type { CaptureRequirement, VerificationSession } from "@idenqa/sdk";

import { CapturePlanError, applyCaptureFailureFallback, createCapturePlan } from "../src/index.js";

const fileUpload = "idenqa.method.file_upload";
const liveCamera = "idenqa.method.live_camera";

describe("createCapturePlan", () => {
  it("keeps tenant ordering while intersecting an any_of choice with host capabilities", () => {
    const plan = createCapturePlan(session(requirement({ methods: [liveCamera, fileUpload] })), {
      supportedMethods: [fileUpload, liveCamera],
      availableMethods: [fileUpload, liveCamera],
    });

    expect(plan.requirements[0]?.steps).toEqual([
      expect.objectContaining({
        artefact: "idenqa.artefact.selfie_image",
        methodOptions: [liveCamera, fileUpload],
      }),
    ]);
  });

  it("creates a required step for every document artefact", () => {
    const plan = createCapturePlan(
      session(
        requirement({
          artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
          methods: [fileUpload],
        }),
      ),
      { supportedMethods: [fileUpload, liveCamera], availableMethods: [fileUpload] },
    );

    expect(plan.requirements[0]?.steps.map((step) => step.artefact)).toEqual([
      "idenqa.artefact.document_front",
      "idenqa.artefact.document_back",
    ]);
  });

  it("expands all_of acquisition into separately required method legs", () => {
    const plan = createCapturePlan(
      session(requirement({ strategy: "all_of", methods: [liveCamera, fileUpload] })),
      { supportedMethods: [fileUpload, liveCamera], availableMethods: [fileUpload, liveCamera] },
    );

    expect(plan.requirements[0]?.steps.map((step) => step.methodOptions)).toEqual([
      [liveCamera],
      [fileUpload],
    ]);
  });

  it("uses only a policy-approved capability fallback", () => {
    const input = requirement({ methods: [liveCamera] });
    const plan = createCapturePlan(
      session({
        ...input,
        fallbacks: [
          {
            on: ["capture_failed", "capability_unavailable"],
            acquisition: { strategy: "any_of", methods: [fileUpload] },
          },
        ],
      }),
      { supportedMethods: [fileUpload, liveCamera], availableMethods: [fileUpload] },
    );

    expect(plan.requirements[0]?.steps[0]).toMatchObject({
      methodOptions: [fileUpload],
      fallbackCondition: "capability_unavailable",
    });
  });

  it("fails closed when neither the primary method nor an approved fallback is available", () => {
    expect(() =>
      createCapturePlan(session(requirement({ methods: [liveCamera] })), {
        supportedMethods: [fileUpload, liveCamera],
        availableMethods: [fileUpload],
      }),
    ).toThrowError(
      expect.objectContaining<Partial<CapturePlanError>>({
        code: "CAPTURE_PLAN_NO_COMPATIBLE_METHOD",
        requirementKey: "selfie",
      }),
    );
  });

  it("rejects duplicate host capability advertisements", () => {
    expect(() =>
      createCapturePlan(session(requirement()), {
        supportedMethods: [fileUpload],
        availableMethods: [fileUpload, fileUpload],
      }),
    ).toThrowError(
      expect.objectContaining<Partial<CapturePlanError>>({ code: "CAPTURE_PLAN_INVALID_SESSION" }),
    );
  });

  it("does not use a fallback approved for a different unavailability reason", () => {
    const input = requirement({ methods: [liveCamera] });
    expect(() =>
      createCapturePlan(
        session({
          ...input,
          fallbacks: [
            {
              on: ["method_unavailable"],
              acquisition: { strategy: "any_of", methods: [fileUpload] },
            },
          ],
        }),
        { supportedMethods: [fileUpload, liveCamera], availableMethods: [fileUpload] },
      ),
    ).toThrowError(
      expect.objectContaining<Partial<CapturePlanError>>({
        code: "CAPTURE_PLAN_NO_COMPATIBLE_METHOD",
      }),
    );
  });

  it("uses method_unavailable only when the host does not implement the primary method", () => {
    const input = requirement({ methods: [liveCamera] });
    const plan = createCapturePlan(
      session({
        ...input,
        fallbacks: [
          {
            on: ["method_unavailable"],
            acquisition: { strategy: "any_of", methods: [fileUpload] },
          },
        ],
      }),
      { supportedMethods: [fileUpload], availableMethods: [fileUpload] },
    );

    expect(plan.requirements[0]?.steps[0]).toMatchObject({
      methodOptions: [fileUpload],
      fallbackCondition: "method_unavailable",
    });
  });

  it("activates capture_failed fallback only after the planned attempt fails", () => {
    const input = requirement({ methods: [liveCamera] });
    const snapshot = session({
      ...input,
      fallbacks: [
        {
          on: ["capture_failed"],
          acquisition: { strategy: "any_of", methods: [fileUpload] },
        },
      ],
    });
    const capabilities = {
      supportedMethods: [liveCamera, fileUpload],
      availableMethods: [liveCamera, fileUpload],
    };
    const initial = createCapturePlan(snapshot, capabilities);

    expect(initial.requirements[0]?.steps[0]).toMatchObject({
      methodOptions: [liveCamera],
    });
    expect(initial.requirements[0]?.steps[0]?.fallbackCondition).toBeUndefined();
    expect(
      applyCaptureFailureFallback(
        initial,
        snapshot,
        initial.requirements[0]!.steps[0]!,
        capabilities,
      ).requirements[0]?.steps[0],
    ).toMatchObject({
      methodOptions: [fileUpload],
      fallbackCondition: "capture_failed",
    });
  });

  it("fails closed when capture_failed fallback is absent or unavailable", () => {
    const snapshot = session(requirement({ methods: [liveCamera] }));
    const capabilities = {
      supportedMethods: [liveCamera, fileUpload],
      availableMethods: [liveCamera, fileUpload],
    };
    const initial = createCapturePlan(snapshot, capabilities);

    expect(() =>
      applyCaptureFailureFallback(
        initial,
        snapshot,
        initial.requirements[0]!.steps[0]!,
        capabilities,
      ),
    ).toThrowError(
      expect.objectContaining<Partial<CapturePlanError>>({
        code: "CAPTURE_PLAN_NO_COMPATIBLE_METHOD",
      }),
    );
  });
});

function requirement(
  overrides: {
    readonly artefacts?: readonly string[];
    readonly methods?: readonly string[];
    readonly strategy?: "any_of" | "all_of";
  } = {},
): CaptureRequirement {
  return {
    key: "selfie",
    purpose: "idenqa.purpose.identity_verification",
    evidence_type: "idenqa.evidence.selfie_image",
    artefacts: overrides.artefacts ?? ["idenqa.artefact.selfie_image"],
    acquisition: {
      strategy: overrides.strategy ?? "any_of",
      methods: overrides.methods ?? [fileUpload],
    },
    required_assurances: [],
    constraints: [],
    fallbacks: [],
  };
}

function session(
  requirements: CaptureRequirement | readonly CaptureRequirement[],
): VerificationSession {
  return {
    id: "ver_01M11HEQG00000000000000000",
    state: "collecting",
    version: 1,
    profileId: "prf_01M11HEQG00000000000000000",
    profileRevision: 1,
    profileDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    policyId: "pol_01M11HEQG00000000000000000",
    requirements: {
      schema_version: 1,
      registry: {
        schema_version: 1,
        revision: 1,
        digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      },
      requirements: Array.isArray(requirements) ? requirements : [requirements],
    },
    createdAt: "2026-08-30T00:00:00Z",
    updatedAt: "2026-08-30T00:00:00Z",
    expiresAt: "2026-08-30T01:00:00Z",
  };
}
