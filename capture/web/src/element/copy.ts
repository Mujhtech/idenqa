import { CaptureCameraError, LIVE_CAMERA_METHOD } from "../camera.js";
import type {
  CaptureLocalizer,
  CaptureMessageCatalogue,
  CaptureMessageKey,
} from "../localisation.js";
import type { CaptureMethodAdapterProgress } from "../method-adapter.js";
import type { CaptureRuntimeFallbackReason } from "../planner.js";
import { FILE_UPLOAD_METHOD } from "../upload.js";

export function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(value);
}

export function adapterProgressMessage(
  progress: CaptureMethodAdapterProgress | undefined,
  method: string,
  localizer: CaptureLocalizer,
): string {
  if (progress === undefined || progress.phase === "preparing") {
    return localizer.text("adapterPreparing", { method });
  }
  if (progress.phase === "requesting_permission") {
    return localizer.text("adapterRequestingPermission", { method });
  }
  if (progress.phase === "ready") return localizer.text("adapterReady", { method });
  if (progress.phase === "submitting") {
    return localizer.text("adapterSubmitting", { method });
  }
  return localizer.text("adapterRunning", { method });
}

export function poseFeedbackKey(
  feedback: NonNullable<CaptureMethodAdapterProgress["poseFeedback"]>,
): CaptureMessageKey {
  const keys = {
    find_face: "poseFindFace",
    one_face: "poseOneFace",
    center_face: "poseCenterFace",
    move_closer: "poseMoveCloser",
    move_back: "poseMoveBack",
    face_forward: "poseFaceForward",
    follow_prompt: "poseFollowPrompt",
    hold_still: "poseHoldStill",
    open_eyes: "poseOpenEyes",
    quality: "poseQuality",
  } as const;
  return keys[feedback];
}

export function poseGuideStage(progress: CaptureMethodAdapterProgress | undefined) {
  if (progress?.poseStage !== undefined) return progress.poseStage;
  // Older host adapters can still render guidance without inventing measured
  // confirmation. Only an explicit centered stage makes the arcs green.
  if (
    progress?.prompt === undefined ||
    progress.prompt === "neutral" ||
    (progress.poseFeedback !== undefined &&
      progress.poseFeedback !== "follow_prompt" &&
      progress.poseFeedback !== "hold_still" &&
      progress.poseFeedback !== "open_eyes")
  ) {
    return "centering";
  }
  return "pose";
}

export function livenessPrompt(
  prompt: NonNullable<CaptureMethodAdapterProgress["prompt"]>,
  localizer: CaptureLocalizer,
): string {
  switch (prompt) {
    case "neutral":
      return localizer.text("livenessNeutral");
    case "turn_left":
      return localizer.text("livenessTurnLeft");
    case "turn_right":
      return localizer.text("livenessTurnRight");
    case "look_up":
      return localizer.text("livenessLookUp");
    case "look_down":
      return localizer.text("livenessLookDown");
    case "blink":
      return localizer.text("livenessBlink");
  }
}

export function fallbackMessage(
  condition: CaptureRuntimeFallbackReason,
  localizer: CaptureLocalizer,
): string {
  if (condition === "capability_unavailable") {
    return localizer.text("capabilityFallback");
  }
  if (condition === "capture_failed") {
    return localizer.text("captureFailureFallback");
  }
  return localizer.text("methodFallback");
}

export function cameraErrorMessage(error: unknown, localizer: CaptureLocalizer): string {
  if (error instanceof CaptureCameraError && error.code === "CAPTURE_CAMERA_PERMISSION_DENIED") {
    return localizer.text("cameraPermissionDenied");
  }
  if (error instanceof CaptureCameraError && error.code === "CAPTURE_CAMERA_FRAME_FAILED") {
    return localizer.text("cameraFrameFailed", { message: error.message });
  }
  return localizer.text("cameraUnavailable");
}

export function methodDescription(method: string, localizer: CaptureLocalizer): string {
  if (method === FILE_UPLOAD_METHOD) return localizer.text("fileMethodDescription");
  if (method === LIVE_CAMERA_METHOD) return localizer.text("cameraMethodDescription");
  return localizer.text("otherMethodDescription");
}

export function captureInstruction(method: string, localizer: CaptureLocalizer): string {
  if (method === FILE_UPLOAD_METHOD) return localizer.text("fileInstruction");
  if (method === LIVE_CAMERA_METHOD) return localizer.text("cameraInstruction");
  return localizer.text("otherInstruction");
}

export function friendlyArtefact(artefact: string, localizer: CaptureLocalizer): string {
  if (artefact === "idenqa.artefact.selfie_image") return localizer.text("selfie");
  if (artefact === "idenqa.artefact.document_front") return localizer.text("documentFront");
  if (artefact === "idenqa.artefact.document_back") return localizer.text("documentBack");
  return labelForIdentifier(artefact);
}

export function methodAction(method: string, localizer: CaptureLocalizer): string {
  if (method === "idenqa.method.file_upload") return localizer.text("uploadFileAction");
  if (method === "idenqa.method.live_camera") return localizer.text("useCameraAction");
  return localizer.text("useMethodAction", { method: methodLabel(method, localizer) });
}

export function methodLabel(method: string, localizer: CaptureLocalizer): string {
  if (method === "idenqa.method.file_upload") return localizer.text("fileUploadMethod");
  if (method === "idenqa.method.live_camera") return localizer.text("cameraMethod");
  return labelForIdentifier(method);
}

export function mediaTypeLabel(mediaType: string): string {
  return mediaType === "image/jpeg" ? "JPEG" : mediaType === "image/png" ? "PNG" : mediaType;
}

export function labelForIdentifier(identifier: string): string {
  const segment = identifier.split(".").at(-1) ?? identifier;
  return segment
    .split("_")
    .filter((word) => word.length > 0)
    .map((word) => word[0]?.toUpperCase() + word.slice(1))
    .join(" ");
}

export function browserLocale(): string {
  return globalThis.navigator?.languages?.[0] ?? globalThis.navigator?.language ?? "en";
}

export function mergeCaptureMessageCatalogues(
  base: CaptureMessageCatalogue | undefined,
  override: CaptureMessageCatalogue,
): CaptureMessageCatalogue {
  if (base === undefined) return override;
  const merged: Record<string, Partial<Record<CaptureMessageKey, string>>> = {};
  for (const locale of new Set([...Object.keys(base), ...Object.keys(override)])) {
    merged[locale] = { ...base[locale], ...override[locale] };
  }
  return merged;
}
