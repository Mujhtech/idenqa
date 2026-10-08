import { DOCUMENT_GUIDE_ASPECT_RATIO } from "../document-capture.js";
import type { DocumentQuad } from "../document-detection.js";
import type { CaptureLocalizer } from "../localisation.js";
import type { StepCameraState, StepDocumentState } from "./state.js";

export function documentStateSignature(state: StepDocumentState): string {
  const quad = state.quad;
  const points =
    quad === undefined
      ? ""
      : [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft]
          .map((point) => `${point.x.toFixed(1)},${point.y.toFixed(1)}`)
          .join(";");
  return `${state.status}|${state.reason ?? ""}|${state.progress.toFixed(2)}|${points}|${state.frameWidth ?? 0}x${state.frameHeight ?? 0}`;
}

export function documentHintMessage(
  state: StepDocumentState | undefined,
  localizer: CaptureLocalizer,
): string {
  if (state === undefined || state.status === "searching") {
    return localizer.text("documentSearching");
  }
  if (state.status === "ready") return localizer.text("documentReady");
  if (state.status === "steadying") return localizer.text("documentHoldSteady");
  return state.reason === "low_coverage"
    ? localizer.text("documentMoveCloser")
    : localizer.text("documentAligning");
}

export function documentGuideRect(
  width: number,
  height: number,
): { readonly x: number; readonly y: number; readonly width: number; readonly height: number } {
  const maximumWidth = width * 0.86;
  const maximumHeight = height * 0.86;
  let guideWidth = maximumWidth;
  let guideHeight = guideWidth / DOCUMENT_GUIDE_ASPECT_RATIO;
  if (guideHeight > maximumHeight) {
    guideHeight = maximumHeight;
    guideWidth = guideHeight * DOCUMENT_GUIDE_ASPECT_RATIO;
  }
  return {
    x: (width - guideWidth) / 2,
    y: (height - guideHeight) / 2,
    width: guideWidth,
    height: guideHeight,
  };
}

export function quadPoints(quad: DocumentQuad): string {
  return [quad.topLeft, quad.topRight, quad.bottomRight, quad.bottomLeft]
    .map((point) => `${roundCoordinate(point.x)},${roundCoordinate(point.y)}`)
    .join(" ");
}

export function roundCoordinate(value: number): number {
  return Math.round(value * 10) / 10;
}

export function isCameraActive(state: StepCameraState | undefined): boolean {
  return (
    state?.status === "requesting" ||
    state?.status === "streaming" ||
    state?.status === "reviewing" ||
    state?.status === "uploading"
  );
}

export function isCameraBusy(state: StepCameraState | undefined): boolean {
  return (
    state?.status === "requesting" || state?.status === "streaming" || state?.status === "uploading"
  );
}
