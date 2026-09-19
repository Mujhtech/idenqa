import type { VerificationSession } from "@idenqa/sdk";

import {
  createCapturePlan,
  defineIdenqaCapture,
  type CaptureMethodSelectDetail,
  type IdenqaCaptureElement,
} from "../src/index.js";

defineIdenqaCapture();

const session: VerificationSession = {
  id: "ver_01M11HEQG00000000000000000",
  state: "collecting",
  version: 1,
  profileId: "prf_01M11HEQG00000000000000000",
  profileRevision: 1,
  profileDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  policyId: "pol_01M11HEQG00000000000000000",
  region: "global",
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
        acquisition: {
          strategy: "any_of",
          methods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
        },
        required_assurances: [],
        constraints: [],
        fallbacks: [],
      },
      {
        key: "identity_document",
        purpose: "idenqa.purpose.identity_verification",
        evidence_type: "idenqa.evidence.identity_document",
        artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
        acquisition: {
          strategy: "any_of",
          methods: ["idenqa.method.file_upload"],
        },
        required_assurances: [],
        constraints: [],
        fallbacks: [],
      },
    ],
  },
  createdAt: "2026-08-30T00:00:00Z",
  updatedAt: "2026-08-30T00:00:00Z",
  expiresAt: "2026-08-30T01:00:00Z",
};

const capture = document.querySelector<IdenqaCaptureElement>("#capture");
const selection = document.querySelector<HTMLOutputElement>("#selection");
if (capture === null || selection === null) throw new Error("The capture demo is incomplete.");

capture.plan = createCapturePlan(session, {
  supportedMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
  availableMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
});
capture.addEventListener("idenqa-method-select", (event) => {
  const detail = (event as CustomEvent<CaptureMethodSelectDetail>).detail;
  selection.value = `${detail.method} selected for ${detail.artefact}.`;
});
