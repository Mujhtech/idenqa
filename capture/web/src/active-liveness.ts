import {
  findCaptureAcquisitionRequirement,
  parseCaptureAcquisitionPlan,
  type CaptureAcquisitionCamera,
  type CaptureAcquisitionPlan,
  type CaptureAcquisitionQualityPolicy,
  type CaptureAcquisitionRequirement,
  type CaptureLivenessChallenge,
} from "./acquisition.js";
import { captureCameraFrame, startCamera, stopCamera, type CameraFrame } from "./camera.js";
import type { CaptureMethodAdapter, CaptureMethodAdapterCopyResolver } from "./method-adapter.js";
import type { CaptureRuntimeFallbackReason } from "./planner.js";

export interface CaptureActiveLivenessQuality {
  readonly width: number;
  readonly height: number;
  readonly byteCount: number;
  readonly brightness?: number;
  readonly contrast?: number;
  readonly sharpness?: number;
  readonly glare?: number;
  readonly faceCount?: number;
}

export interface CaptureActiveLivenessFrame {
  readonly challengeId: string;
  readonly prompt: CaptureLivenessChallenge["prompt"];
  readonly capturedAt: string;
  readonly body: Blob;
  readonly quality: CaptureActiveLivenessQuality;
}

export interface CaptureActiveLivenessSubmission {
  readonly schemaVersion: "1.0";
  readonly planId: string;
  readonly sessionId: string;
  readonly requirementId: string;
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly acquisitionMethod: "idenqa.method.live_camera";
  readonly fallbackCondition?: CaptureRuntimeFallbackReason;
  readonly frames: readonly CaptureActiveLivenessFrame[];
}

export interface CaptureActiveLivenessCameraSession {
  readonly stream: MediaStream;
  capture(mediaType: "image/jpeg" | "image/png", signal: AbortSignal): Promise<CameraFrame>;
  close(): void;
}

export interface CaptureActiveLivenessAdapterOptions {
  readonly plan: CaptureAcquisitionPlan | unknown;
  readonly requirementKey: string;
  readonly acquisitionRequirementId?: string;
  readonly copy?: CaptureMethodAdapterCopyResolver;
  readonly mediaType?: "image/jpeg" | "image/png";
  readonly settleDurationMs?: number;
  readonly clock?: () => Date;
  readonly cameraSessionFactory?: (
    camera: CaptureAcquisitionCamera,
    signal: AbortSignal,
  ) => Promise<CaptureActiveLivenessCameraSession>;
  readonly assess?: (
    frame: CameraFrame,
    requirement: CaptureAcquisitionRequirement,
    challenge: CaptureLivenessChallenge,
    signal: AbortSignal,
  ) => Promise<CaptureActiveLivenessQuality>;
  /**
   * Submits the frame set through a host-owned Core/provider integration. It
   * must make authoritative Core progress visible before resolving.
   */
  readonly submit: (
    submission: CaptureActiveLivenessSubmission,
    signal: AbortSignal,
  ) => Promise<void>;
}

export type CaptureActiveLivenessErrorCode =
  | "CAPTURE_ACTIVE_LIVENESS_INVALID"
  | "CAPTURE_ACTIVE_LIVENESS_TIMEOUT"
  | "CAPTURE_ACTIVE_LIVENESS_QUALITY";

export class CaptureActiveLivenessError extends Error {
  readonly code: CaptureActiveLivenessErrorCode;
  readonly failures: readonly string[];

  constructor(
    code: CaptureActiveLivenessErrorCode,
    message: string,
    failures: readonly string[] = [],
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "CaptureActiveLivenessError";
    this.code = code;
    this.failures = [...failures];
  }
}

function defaultCopy(challengeCount: number) {
  const promptCount = challengeCount === 1 ? "one quick prompt" : `${challengeCount} quick prompts`;
  return {
    label: "Liveness Check",
    action: "Start Liveness Check",
    description: "Follow a short series of camera prompts",
    title: "Let’s Make Sure You’re You",
    preparation: `Center your face in the camera, then follow ${promptCount}.`,
    instruction: "Keep your face in view while the camera captures each requested movement.",
    tips: [
      "Usually takes only a few seconds.",
      "Photos are captured automatically.",
      "Use bright, even lighting and remove anything covering your face.",
    ],
  } as const;
}

export function createActiveLivenessMethodAdapter(
  options: CaptureActiveLivenessAdapterOptions,
): CaptureMethodAdapter {
  const plan = parseCaptureAcquisitionPlan(options.plan);
  const requirement = selectRequirement(
    plan,
    options.acquisitionRequirementId,
    options.requirementKey,
  );
  if (requirement.challenges.length === 0) {
    throw invalid("An active-liveness adapter requires at least one challenge.");
  }
  const settleDurationMs = options.settleDurationMs ?? 750;
  if (!Number.isSafeInteger(settleDurationMs) || settleDurationMs < 0 || settleDurationMs > 5_000) {
    throw invalid("settleDurationMs must be an integer from 0 through 5000.");
  }
  if (typeof options.submit !== "function") throw invalid("submit must be a function.");
  const mediaType = options.mediaType ?? "image/jpeg";
  if (mediaType !== "image/jpeg" && mediaType !== "image/png") {
    throw invalid("mediaType must be image/jpeg or image/png.");
  }
  if (options.clock !== undefined && typeof options.clock !== "function") {
    throw invalid("clock must be a function.");
  }
  if (
    options.cameraSessionFactory !== undefined &&
    typeof options.cameraSessionFactory !== "function"
  ) {
    throw invalid("cameraSessionFactory must be a function.");
  }
  if (options.assess !== undefined && typeof options.assess !== "function") {
    throw invalid("assess must be a function.");
  }
  const clock = options.clock ?? (() => new Date());
  const cameraSessionFactory = options.cameraSessionFactory ?? createBrowserCameraSession;
  const assess = options.assess ?? defaultAssessment;

  return {
    method: requirement.acquisition_method,
    requirementKey: options.requirementKey,
    artefact: requirement.artefact,
    presentation: "active_liveness",
    copy: options.copy ?? defaultCopy(requirement.challenges.length),
    async acquire(context, controls): Promise<void> {
      if (context.signal.aborted) throw abortReason(context.signal);
      if (
        context.verificationId !== plan.session_id ||
        context.evidenceType !== requirement.evidence_type ||
        context.artefact !== requirement.artefact ||
        context.acquisitionMethod !== requirement.acquisition_method
      ) {
        throw invalid("The active-liveness plan does not match the capture step.");
      }
      controls.update({ phase: "requesting_permission" });
      let session: CaptureActiveLivenessCameraSession | undefined;
      try {
        session = await cameraSessionFactory(requirement.camera, context.signal);
        await controls.setPreview(session.stream);
        controls.update({ phase: "ready" });
        const frames: CaptureActiveLivenessFrame[] = [];
        for (const [index, challenge] of requirement.challenges.entries()) {
          const frame = await withinChallengeDeadline(challenge, context.signal, async (signal) => {
            controls.update({
              phase: "challenge",
              current: index + 1,
              total: requirement.challenges.length,
              prompt: challenge.prompt,
            });
            await abortableDelay(
              Math.min(settleDurationMs, Math.floor(challenge.maximum_duration_ms / 2)),
              signal,
            );
            const captured = await session!.capture(mediaType, signal);
            const quality = await assess(captured, requirement, challenge, signal);
            const failures = qualityFailures(captured, quality, requirement.quality, mediaType);
            if (failures.length > 0) {
              throw new CaptureActiveLivenessError(
                "CAPTURE_ACTIVE_LIVENESS_QUALITY",
                "The liveness frame did not meet the required capture quality. Adjust the camera and retry.",
                failures,
              );
            }
            const capturedAt = clock().toISOString();
            return {
              challengeId: challenge.id,
              prompt: challenge.prompt,
              capturedAt,
              body: captured.body,
              quality,
            } satisfies CaptureActiveLivenessFrame;
          });
          frames.push(frame);
        }
        controls.update({ phase: "submitting" });
        await options.submit(
          {
            schemaVersion: "1.0",
            planId: plan.plan_id,
            sessionId: plan.session_id,
            requirementId: requirement.id,
            requirementKey: context.requirementKey,
            evidenceType: requirement.evidence_type,
            artefact: requirement.artefact,
            acquisitionMethod: requirement.acquisition_method,
            ...(context.fallbackCondition === undefined
              ? {}
              : { fallbackCondition: context.fallbackCondition }),
            frames,
          },
          context.signal,
        );
      } finally {
        session?.close();
        await controls.setPreview(undefined);
      }
    },
  };
}

function selectRequirement(
  plan: CaptureAcquisitionPlan,
  identifier: string | undefined,
  requirementKey: string,
): CaptureAcquisitionRequirement {
  if (requirementKey.length === 0 || requirementKey.trim() !== requirementKey) {
    throw invalid("requirementKey is invalid.");
  }
  if (identifier !== undefined) {
    const matches = plan.requirements.filter((requirement) => requirement.id === identifier);
    if (matches.length !== 1) throw invalid("acquisitionRequirementId is not unique in the plan.");
    return matches[0]!;
  }
  const challenged = plan.requirements.filter((requirement) => requirement.challenges.length > 0);
  if (challenged.length !== 1) {
    throw invalid("The plan must identify exactly one challenged requirement.");
  }
  return findCaptureAcquisitionRequirement(plan, {
    evidenceType: challenged[0]!.evidence_type,
    artefact: challenged[0]!.artefact,
    acquisitionMethod: challenged[0]!.acquisition_method,
  });
}

async function createBrowserCameraSession(
  camera: CaptureAcquisitionCamera,
  signal: AbortSignal,
): Promise<CaptureActiveLivenessCameraSession> {
  if (typeof document === "undefined") throw invalid("Browser camera capture is unavailable.");
  const stream = await startCamera(camera === "front" ? "user" : "environment", signal);
  const video = document.createElement("video");
  video.autoplay = true;
  video.muted = true;
  video.playsInline = true;
  video.srcObject = stream;
  try {
    await video.play();
    await waitForVideo(video, signal);
  } catch (error) {
    video.srcObject = null;
    stopCamera(stream);
    throw error;
  }
  let closed = false;
  return {
    stream,
    capture: async (mediaType, captureSignal) => {
      if (closed || captureSignal.aborted) throw abortReason(captureSignal);
      return captureCameraFrame(video, mediaType);
    },
    close: () => {
      if (closed) return;
      closed = true;
      video.srcObject = null;
      stopCamera(stream);
    },
  };
}

async function waitForVideo(video: HTMLVideoElement, signal: AbortSignal): Promise<void> {
  if (video.videoWidth > 0 && video.videoHeight > 0) return;
  await new Promise<void>((resolve, reject) => {
    const ready = () => {
      cleanup();
      if (video.videoWidth > 0 && video.videoHeight > 0) resolve();
      else reject(invalid("The camera did not provide a usable frame."));
    };
    const aborted = () => {
      cleanup();
      reject(abortReason(signal));
    };
    const cleanup = () => {
      video.removeEventListener("loadedmetadata", ready);
      signal.removeEventListener("abort", aborted);
    };
    video.addEventListener("loadedmetadata", ready, { once: true });
    signal.addEventListener("abort", aborted, { once: true });
  });
}

async function defaultAssessment(frame: CameraFrame): Promise<CaptureActiveLivenessQuality> {
  return {
    width: frame.width,
    height: frame.height,
    byteCount: frame.body.size,
  };
}

function qualityFailures(
  frame: CameraFrame,
  measurement: CaptureActiveLivenessQuality,
  policy: CaptureAcquisitionQualityPolicy,
  mediaType: "image/jpeg" | "image/png",
): readonly string[] {
  if (typeof frame !== "object" || frame === null) return ["frame_invalid"];
  if (typeof measurement !== "object" || measurement === null) {
    return ["quality_measurement_invalid"];
  }
  const failures: string[] = [];
  const frameSize = frame.body instanceof Blob ? frame.body.size : -1;
  if (!(frame.body instanceof Blob) || frame.body.type !== mediaType) {
    failures.push("media_type");
  }
  const hasValidFrame =
    frameSize > 0 &&
    Number.isSafeInteger(frame.width) &&
    frame.width > 0 &&
    Number.isSafeInteger(frame.height) &&
    frame.height > 0;
  const hasValidDimensions =
    Number.isSafeInteger(measurement.width) &&
    measurement.width > 0 &&
    Number.isSafeInteger(measurement.height) &&
    measurement.height > 0 &&
    measurement.width === frame.width &&
    measurement.height === frame.height;
  if (!hasValidFrame || !hasValidDimensions) {
    failures.push("dimensions_invalid");
  } else if (
    measurement.width < policy.minimum_width ||
    measurement.height < policy.minimum_height
  ) {
    failures.push("dimensions");
  }
  if (
    !Number.isSafeInteger(measurement.byteCount) ||
    measurement.byteCount < 1 ||
    measurement.byteCount !== frameSize
  ) {
    failures.push("size_invalid");
  } else if (measurement.byteCount > policy.maximum_bytes) {
    failures.push("size");
  }
  for (const [name, value] of [
    ["brightness", measurement.brightness],
    ["contrast", measurement.contrast],
    ["sharpness", measurement.sharpness],
    ["glare", measurement.glare],
  ] as const) {
    if (value !== undefined && !validUnitMeasurement(value)) failures.push(`${name}_invalid`);
  }
  compareMinimum(failures, "brightness", measurement.brightness, policy.minimum_brightness);
  compareMaximum(failures, "brightness", measurement.brightness, policy.maximum_brightness);
  compareMinimum(failures, "contrast", measurement.contrast, policy.minimum_contrast);
  compareMinimum(failures, "sharpness", measurement.sharpness, policy.minimum_sharpness);
  compareMaximum(failures, "glare", measurement.glare, policy.maximum_glare);
  if (
    policy.required_face_count !== undefined &&
    measurement.faceCount !== policy.required_face_count
  ) {
    failures.push(
      measurement.faceCount === undefined
        ? "face_count_unavailable"
        : validFaceCount(measurement.faceCount)
          ? "face_count"
          : "face_count_invalid",
    );
  } else if (measurement.faceCount !== undefined && !validFaceCount(measurement.faceCount)) {
    failures.push("face_count_invalid");
  }
  return [...new Set(failures)];
}

function compareMinimum(
  failures: string[],
  name: string,
  measured: number | undefined,
  required: number | undefined,
): void {
  if (required === undefined) return;
  if (measured === undefined) failures.push(`${name}_unavailable`);
  else if (!validUnitMeasurement(measured)) failures.push(`${name}_invalid`);
  else if (measured < required) failures.push(name);
}

function compareMaximum(
  failures: string[],
  name: string,
  measured: number | undefined,
  required: number | undefined,
): void {
  if (required === undefined) return;
  if (measured === undefined) failures.push(`${name}_unavailable`);
  else if (!validUnitMeasurement(measured)) failures.push(`${name}_invalid`);
  else if (measured > required) failures.push(name);
}

function validUnitMeasurement(value: number): boolean {
  return Number.isFinite(value) && value >= 0 && value <= 1;
}

function validFaceCount(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0 && value <= 4;
}

async function withinChallengeDeadline<T>(
  challenge: CaptureLivenessChallenge,
  parent: AbortSignal,
  operation: (signal: AbortSignal) => Promise<T>,
): Promise<T> {
  if (parent.aborted) throw abortReason(parent);
  const controller = new AbortController();
  const parentAborted = () => controller.abort(parent.reason);
  parent.addEventListener("abort", parentAborted, { once: true });
  const timeout = setTimeout(
    () =>
      controller.abort(
        new CaptureActiveLivenessError(
          "CAPTURE_ACTIVE_LIVENESS_TIMEOUT",
          "The liveness prompt timed out. Keep your face in view and retry.",
        ),
      ),
    challenge.maximum_duration_ms,
  );
  try {
    return await operation(controller.signal);
  } catch (error) {
    if (parent.aborted) throw abortReason(parent);
    if (controller.signal.aborted) {
      const reason = controller.signal.reason;
      if (reason instanceof CaptureActiveLivenessError) throw reason;
    }
    throw error;
  } finally {
    clearTimeout(timeout);
    parent.removeEventListener("abort", parentAborted);
  }
}

function abortableDelay(milliseconds: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.reject(abortReason(signal));
  if (milliseconds === 0) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      cleanup();
      resolve();
    }, milliseconds);
    const aborted = () => {
      cleanup();
      reject(abortReason(signal));
    };
    const cleanup = () => {
      clearTimeout(timeout);
      signal.removeEventListener("abort", aborted);
    };
    signal.addEventListener("abort", aborted, { once: true });
  });
}

function abortReason(signal: AbortSignal): unknown {
  return signal.reason ?? new DOMException("The operation was aborted.", "AbortError");
}

function invalid(message: string): CaptureActiveLivenessError {
  return new CaptureActiveLivenessError("CAPTURE_ACTIVE_LIVENESS_INVALID", message);
}
