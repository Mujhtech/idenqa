import { createHash } from "node:crypto";

import { describe, expect, it } from "vitest";

import type { VerificationSession } from "@idenqa/sdk";

import {
  CaptureUploadError,
  fileUploadPolicy,
  prepareFileUpload,
  type CapturePlanStep,
} from "../src/index.js";

const step: CapturePlanStep = {
  requirementKey: "selfie",
  evidenceType: "idenqa.evidence.selfie_image",
  artefact: "idenqa.artefact.selfie_image",
  methodOptions: ["idenqa.method.file_upload"],
};

describe("file upload preparation", () => {
  it("applies profile media and size constraints and computes the exact digest", async () => {
    const body = new Blob([pngBytes()], { type: "image/png" });
    const policy = fileUploadPolicy(session(["image/png"], body.size), step);

    await expect(prepareFileUpload(body, policy)).resolves.toMatchObject({
      body,
      mediaType: "image/png",
      digest: `sha256:${createHash("sha256").update(new Uint8Array(pngBytes())).digest("hex")}`,
    });
    expect(policy).toEqual({
      allowedMediaTypes: ["image/png"],
      maximumBytes: body.size,
      hasExplicitMaximum: true,
    });
  });

  it("rejects an oversized or signature-mismatched file before hashing", async () => {
    const policy = fileUploadPolicy(session(["image/png"], 8), step);
    await expect(
      prepareFileUpload(
        new Blob([pngBytes(), new Uint8Array([1]).buffer], { type: "image/png" }),
        policy,
      ),
    ).rejects.toMatchObject({
      code: "CAPTURE_UPLOAD_INVALID_FILE",
    } satisfies Partial<CaptureUploadError>);
    await expect(
      prepareFileUpload(new Blob([new Uint8Array(8)], { type: "image/png" }), policy),
    ).rejects.toThrow("does not match");
  });
});

function session(allowedMediaTypes: readonly string[], maximumBytes: number): VerificationSession {
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
      requirements: [
        {
          key: "selfie",
          purpose: "idenqa.purpose.identity_verification",
          evidence_type: "idenqa.evidence.selfie_image",
          artefacts: ["idenqa.artefact.selfie_image"],
          acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
          required_assurances: [],
          constraints: [
            { name: "idenqa.constraint.allowed_media_types", value: allowedMediaTypes },
            { name: "idenqa.constraint.maximum_bytes", value: maximumBytes },
          ],
          fallbacks: [],
        },
      ],
    },
    createdAt: "2026-08-30T00:00:00Z",
    updatedAt: "2026-08-30T00:00:00Z",
    expiresAt: "2026-08-30T01:00:00Z",
  };
}

function pngBytes(): ArrayBuffer {
  return new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]).buffer;
}
