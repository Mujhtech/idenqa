import { CaptureClient, createIdempotencyKey } from "@idenqa/sdk";

import {
  createActiveLivenessMethodAdapter,
  defineIdenqaCapture,
  type CaptureActiveLivenessSubmission,
  type IdenqaCaptureElement,
} from "../src/index.js";

interface HostedBootstrap {
  readonly baseUrl: string;
  readonly verificationId: string;
  readonly captureToken: string;
  readonly outcomeToken: string;
  readonly sessionVersion: number;
  readonly region: string;
  readonly outcome:
    | "verified"
    | "not_verified"
    | "inconclusive"
    | "action_required"
    | "cancelled"
    | "expired"
    | "failed";
}

defineIdenqaCapture();

const capture = document.querySelector<IdenqaCaptureElement>("idenqa-capture");
if (capture === null) throw new Error("The hosted Capture Web element is missing.");

void startHostedJourney(capture).catch(() => {
  capture.hidden = true;
  const error = document.querySelector<HTMLElement>("#hosted-error");
  if (error !== null) error.hidden = false;
});

async function startHostedJourney(element: IdenqaCaptureElement): Promise<void> {
  const bootstrapURL = new URL("/__idenqa_demo/bootstrap", location.href);
  const requestedOutcome = new URLSearchParams(location.search).get("outcome");
  if (requestedOutcome !== null) bootstrapURL.searchParams.set("outcome", requestedOutcome);
  const response = await fetch(bootstrapURL, {
    method: "POST",
    cache: "no-store",
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error("The self-hosted Capture Web demo could not be prepared.");
  const bootstrap = (await response.json()) as HostedBootstrap;
  const baseUrl = new URL(bootstrap.baseUrl, location.href);
  if (bootstrap.outcome === "cancelled") {
    await new CaptureClient({ baseUrl, captureToken: bootstrap.captureToken }).cancel(
      bootstrap.sessionVersion,
      { idempotencyKey: createIdempotencyKey("demo_cancel") },
    );
  }
  const activeLiveness = new URLSearchParams(location.search).get("method") === "active-liveness";
  await element.start({
    baseUrl,
    captureToken: bootstrap.captureToken,
    outcomeToken: bootstrap.outcomeToken,
    expectedVerificationId: bootstrap.verificationId,
    capabilities: browserCapabilities(),
    region: bootstrap.region,
    ...(activeLiveness
      ? {
          methodAdapters: [
            createActiveLivenessMethodAdapter({
              plan: demoActiveLivenessPlan(bootstrap.verificationId),
              requirementKey: "selfie",
              submit: coreDemoSubmitter(baseUrl, bootstrap),
            }),
          ],
        }
      : {}),
  });
}

function demoActiveLivenessPlan(verificationId: string) {
  return {
    schema_version: "1.0",
    plan_id: "plan.capture_web_demo.active_liveness.v1",
    session_id: verificationId,
    requirements: [
      {
        id: "selfie.active_liveness",
        evidence_type: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image",
        acquisition_method: "idenqa.method.live_camera",
        camera: "front",
        quality: {
          minimum_width: 320,
          minimum_height: 320,
          maximum_bytes: 16_777_216,
        },
        challenges: [
          { id: "challenge.neutral", prompt: "neutral", maximum_duration_ms: 5_000 },
          { id: "challenge.turn_left", prompt: "turn_left", maximum_duration_ms: 5_000 },
          { id: "challenge.turn_right", prompt: "turn_right", maximum_duration_ms: 5_000 },
        ],
      },
    ],
  } as const;
}

function coreDemoSubmitter(baseUrl: URL, bootstrap: HostedBootstrap) {
  const client = new CaptureClient({ baseUrl, captureToken: bootstrap.captureToken });
  return async (submission: CaptureActiveLivenessSubmission, signal: AbortSignal) => {
    const representative = submission.frames[0];
    if (representative === undefined)
      throw new Error("The liveness sequence has no captured frame.");
    const digest = await sha256(representative.body);
    const issued = await client.createEvidenceUpload(
      {
        requirementKey: submission.requirementKey,
        artefact: submission.artefact,
        acquisitionMethod: submission.acquisitionMethod,
        ...(submission.fallbackCondition === undefined
          ? {}
          : { fallbackCondition: submission.fallbackCondition }),
        expectedBytes: representative.body.size,
        expectedDigest: digest,
        mediaType: evidenceMediaType(representative.body),
        region: bootstrap.region,
      },
      { idempotencyKey: createIdempotencyKey("demo_liveness"), signal },
    );
    if (issued.etag === undefined) throw new Error("Core did not return an upload precondition.");
    await client.uploadEvidence(issued.data.id, representative.body, {
      etag: issued.etag,
      digest,
      signal,
    });
  };
}

function evidenceMediaType(body: Blob): "image/jpeg" | "image/png" {
  if (body.type === "image/jpeg" || body.type === "image/png") return body.type;
  throw new Error("The liveness frame media type is not accepted by Core.");
}

async function sha256(body: Blob): Promise<string> {
  const bytes = await crypto.subtle.digest("SHA-256", await body.arrayBuffer());
  return `sha256:${[...new Uint8Array(bytes)]
    .map((value) => value.toString(16).padStart(2, "0"))
    .join("")}`;
}

function browserCapabilities() {
  const fileUpload = "idenqa.method.file_upload";
  const liveCamera = "idenqa.method.live_camera";
  const cameraAvailable =
    globalThis.isSecureContext && typeof navigator.mediaDevices?.getUserMedia === "function";
  return {
    supportedMethods: [liveCamera, fileUpload],
    availableMethods: cameraAvailable ? [liveCamera, fileUpload] : [fileUpload],
  };
}
