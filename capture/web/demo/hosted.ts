import { CaptureClient, createIdempotencyKey, type ExperienceResolution } from "@idenqa/sdk";

import {
  createActiveLivenessMethodAdapter,
  defineIdenqaCapture,
  type CaptureElementStartOptions,
  type CaptureActiveLivenessSubmission,
  type CaptureCountryOption,
  type IdenqaCaptureElement,
} from "../src/index.js";

interface HostedBootstrap {
  readonly countrySelectionRequired: false;
  readonly baseUrl: string;
  readonly verificationId: string;
  readonly captureToken: string;
  readonly outcomeToken: string;
  readonly sessionVersion: number;
  readonly region: string;
  readonly selfieRequirementKey?: string;
  readonly experience?: ExperienceResolution;
  readonly outcome:
    | "verified"
    | "not_verified"
    | "inconclusive"
    | "action_required"
    | "cancelled"
    | "expired"
    | "failed";
}

interface HostedCountrySelection {
  readonly countrySelectionRequired: true;
  readonly captureItemCount: number;
}

defineIdenqaCapture();

const capture = document.querySelector<IdenqaCaptureElement>("idenqa-capture");
if (capture === null) throw new Error("The hosted Capture Web element is missing.");

void startHostedJourney(capture).catch(() => {
  capture.cancel();
  capture.hidden = true;
  const error = document.querySelector<HTMLElement>("#hosted-error");
  if (error !== null) error.hidden = false;
});

async function startHostedJourney(element: IdenqaCaptureElement): Promise<void> {
  const launch = new URLSearchParams(location.hash.slice(1));
  const requestedOutcome = launch.get("outcome");
  const requestedProfile = launch.get("profile");
  const controller = launch.get("controller");
  const recipient = launch.get("recipient");
  const documentJourney = launch.get("journey") === "document";
  const activeLiveness = true;
  const options = {
    requestedOutcome,
    requestedProfile,
    controller,
    recipient,
    documentJourney,
    activeLiveness,
  };
  const bootstrap = await requestHostedBootstrap(options);
  if (!bootstrap.countrySelectionRequired) {
    await element.start(await hostedStartOptions(bootstrap, activeLiveness));
    return;
  }
  element.startCountryJourney({
    countries: documentCountries(),
    captureItemCount: bootstrap.captureItemCount,
    notice: demoPrivacyNotice(controller, recipient),
    resolve: (country, signal) => {
      return prepareHostedJourney(
        {
          ...options,
          country: country.code,
        },
        signal,
      );
    },
  });
}

function demoPrivacyNotice(controller: string | null, recipient: string | null) {
  if (controller === null || controller.trim().length < 2) {
    throw new Error("The controller display identity is required.");
  }
  if (recipient === null || recipient.trim().length < 2) {
    throw new Error("The recipient display identity is required.");
  }
  return {
    locale: "en",
    controller: controller.trim(),
    recipient: recipient.trim(),
    consentRequired: true,
    copy: {
      title: "Identity Verification Notice",
      summary: "We need identity evidence to demonstrate this capture journey.",
      purpose: "This evidence is used only for this local identity-capture demonstration.",
      consequences: "You may refuse. Capture will stop and no evidence will be collected.",
    },
  } as const;
}

interface HostedLaunch {
  readonly requestedOutcome: string | null;
  readonly requestedProfile: string | null;
  readonly controller: string | null;
  readonly recipient: string | null;
  readonly documentJourney: boolean;
  readonly country?: string;
  readonly activeLiveness: boolean;
}

async function prepareHostedJourney(
  launch: HostedLaunch,
  signal?: AbortSignal,
): Promise<CaptureElementStartOptions> {
  const bootstrap = await requestHostedBootstrap(launch, signal);
  if (bootstrap.countrySelectionRequired) {
    throw new Error("The document capture journey requires country selection.");
  }
  return hostedStartOptions(bootstrap, launch.activeLiveness);
}

async function requestHostedBootstrap(
  launch: HostedLaunch,
  signal?: AbortSignal,
): Promise<HostedBootstrap | HostedCountrySelection> {
  const bootstrapURL = new URL("/__idenqa_demo/bootstrap", location.href);
  const response = await fetch(bootstrapURL, {
    method: "POST",
    cache: "no-store",
    credentials: "same-origin",
    ...(signal === undefined ? {} : { signal }),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({
      ...(launch.requestedOutcome === null ? {} : { outcome: launch.requestedOutcome }),
      ...(launch.requestedProfile === null ? {} : { profile: launch.requestedProfile }),
      ...(launch.controller === null ? {} : { controller: launch.controller }),
      ...(launch.recipient === null ? {} : { recipient: launch.recipient }),
      ...(launch.documentJourney ? { journey: "document" } : {}),
      ...(launch.country === undefined ? {} : { country: launch.country }),
    }),
  });
  if (!response.ok) throw new Error("The self-hosted Capture Web demo could not be prepared.");
  return response.json();
}

async function hostedStartOptions(
  bootstrap: HostedBootstrap,
  activeLiveness: boolean,
): Promise<CaptureElementStartOptions> {
  const baseUrl = new URL(bootstrap.baseUrl, location.href);
  if (bootstrap.outcome === "cancelled") {
    await new CaptureClient({ baseUrl, captureToken: bootstrap.captureToken }).cancel(
      bootstrap.sessionVersion,
      { idempotencyKey: createIdempotencyKey("demo_cancel") },
    );
  }
  return {
    baseUrl,
    captureToken: bootstrap.captureToken,
    outcomeToken: bootstrap.outcomeToken,
    expectedVerificationId: bootstrap.verificationId,
    capabilities: browserCapabilities(),
    region: bootstrap.region,
    ...(bootstrap.experience === undefined ? {} : { experience: bootstrap.experience }),
    ...(activeLiveness && bootstrap.selfieRequirementKey !== undefined
      ? {
          methodAdapters: [
            createActiveLivenessMethodAdapter({
              plan: demoActiveLivenessPlan(bootstrap.verificationId),
              requirementKey: bootstrap.selfieRequirementKey,
              copy: {
                label: "Liveness Check",
                action: "Continue to Capture",
                description: "Follow a short series of camera prompts",
                title: "Get Your Selfie Ready",
                preparation: "Before opening the camera, take a moment to set up a clear shot.",
                instruction:
                  "Keep your face in view while the camera captures each requested movement.",
                tips: [
                  "Use bright, even lighting and avoid glare.",
                  "Keep your full face visible inside the guide.",
                  "Follow each prompt and hold still while it captures automatically.",
                ],
              },
              submit: coreDemoSubmitter(baseUrl, bootstrap),
            }),
          ],
        }
      : {}),
  };
}

function documentCountries(): readonly CaptureCountryOption[] {
  return [
    { code: "NG", label: "Nigeria" },
    { code: "GH", label: "Ghana" },
    { code: "GB", label: "United Kingdom" },
  ];
}

function demoActiveLivenessPlan(verificationId: string) {
  return {
    schema_version: "1.1",
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
          { id: "challenge.neutral", prompt: "neutral", maximum_duration_ms: 15_000 },
          { id: "challenge.turn_left", prompt: "turn_left", maximum_duration_ms: 15_000 },
          { id: "challenge.turn_right", prompt: "turn_right", maximum_duration_ms: 15_000 },
        ],
      },
    ],
  } as const;
}

function coreDemoSubmitter(baseUrl: URL, bootstrap: HostedBootstrap) {
  const client = new CaptureClient({ baseUrl, captureToken: bootstrap.captureToken });
  return async (submission: CaptureActiveLivenessSubmission, signal: AbortSignal) => {
    if (submission.frames.length < 2)
      throw new Error("The liveness sequence has no captured frame.");
    const sequenceDigest = await sha256Text(
      JSON.stringify({
        schemaVersion: submission.schemaVersion,
        planId: submission.planId,
        requirementId: submission.requirementId,
        challenges: submission.frames.map((frame) => frame.challengeId),
      }),
    );
    let previousDigest: string | undefined;
    for (const [index, frame] of submission.frames.entries()) {
      const digest = await sha256(frame.body);
      const issued = await client.createEvidenceUpload(
        {
          requirementKey: submission.requirementKey,
          artefact: submission.artefact,
          acquisitionMethod: submission.acquisitionMethod,
          ...(submission.fallbackCondition === undefined
            ? {}
            : { fallbackCondition: submission.fallbackCondition }),
          sequence: {
            sequenceDigest,
            index,
            count: submission.frames.length,
            challengeId: frame.challengeId,
            capturedAt: frame.capturedAt,
            ...(previousDigest === undefined ? {} : { previousDigest }),
          },
          expectedBytes: frame.body.size,
          expectedDigest: digest,
          mediaType: evidenceMediaType(frame.body),
          region: bootstrap.region,
        },
        { idempotencyKey: createIdempotencyKey(`demo_liveness_${index}`), signal },
      );
      if (issued.etag === undefined) throw new Error("Core did not return an upload precondition.");
      await client.uploadEvidence(issued.data.id, frame.body, {
        etag: issued.etag,
        digest,
        signal,
      });
      previousDigest = digest;
    }
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

async function sha256Text(value: string): Promise<string> {
  return sha256(new Blob([value], { type: "text/plain" }));
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
