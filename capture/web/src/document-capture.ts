import type { EvidenceMediaType } from "@idenqa/sdk";

import { canvasBlob, type CameraFrame } from "./camera.js";
import {
  detectDocumentQuad,
  grayFrameDifference,
  normalizeDocumentCaptureGateOptions,
  normalizeDocumentDetectionOptions,
  rgbaToGrayFrame,
  varianceOfLaplacian,
  type DocumentCaptureGateOptions,
  type DocumentDetection,
  type DocumentDetectionOptions,
  type DocumentFrameObservation,
  type GrayFrame,
  type PixelBuffer,
} from "./document-detection.js";
import {
  correctDocumentFrame,
  normalizeDocumentCorrectionOptions,
  type DocumentCorrectionOptions,
} from "./document-correction.js";

export const DOCUMENT_ARTEFACT_FRONT = "idenqa.artefact.document_front";
export const DOCUMENT_ARTEFACT_BACK = "idenqa.artefact.document_back";
/** Aspect ratio of the fixed on-screen document guide. */
export const DOCUMENT_GUIDE_ASPECT_RATIO = 1.6;

const documentArtefacts = new Set([DOCUMENT_ARTEFACT_FRONT, DOCUMENT_ARTEFACT_BACK]);

export function isDocumentArtefact(artefact: string): boolean {
  return documentArtefacts.has(artefact);
}

export interface CaptureDocumentCaptureOptions {
  /** Enables document detection and the guide overlay. Defaults to true. */
  readonly enabled?: boolean;
  /** Enables automatic capture when the stability gate passes. Defaults to true. */
  readonly autoCapture?: boolean;
  /**
   * Delay between the stability gate turning ready and the automatic capture.
   * Gives the subject a visible confirmation before the shutter fires.
   */
  readonly autoCaptureDelayMs?: number;
  /** Detection cadence while the camera is streaming. */
  readonly observationIntervalMs?: number;
  /** Longest edge of the frame used for detection. */
  readonly observationMaxDimension?: number;
  readonly detection?: Partial<DocumentDetectionOptions>;
  readonly gate?: Partial<DocumentCaptureGateOptions>;
  readonly correction?: Partial<DocumentCorrectionOptions>;
}

export interface NormalizedCaptureDocumentCaptureOptions {
  readonly enabled: boolean;
  readonly autoCapture: boolean;
  readonly autoCaptureDelayMs: number;
  readonly observationIntervalMs: number;
  readonly observationMaxDimension: number;
  readonly detection: DocumentDetectionOptions;
  readonly gate: DocumentCaptureGateOptions;
  readonly correction: DocumentCorrectionOptions;
}

export interface DocumentFrameObserver {
  observe(video: HTMLVideoElement): DocumentFrameObservation | undefined;
  reset(): void;
  dispose(): void;
}

export function normalizeCaptureDocumentCaptureOptions(
  options: CaptureDocumentCaptureOptions = {},
): NormalizedCaptureDocumentCaptureOptions {
  return {
    enabled: booleanOption(options.enabled, "enabled", true),
    autoCapture: booleanOption(options.autoCapture, "autoCapture", true),
    autoCaptureDelayMs: boundedNumber(
      options.autoCaptureDelayMs,
      "autoCaptureDelayMs",
      0,
      2000,
      250,
    ),
    observationIntervalMs: boundedNumber(
      options.observationIntervalMs,
      "observationIntervalMs",
      80,
      2000,
      200,
    ),
    observationMaxDimension: boundedNumber(
      options.observationMaxDimension,
      "observationMaxDimension",
      64,
      480,
      240,
    ),
    detection: normalizeDocumentDetectionOptions(options.detection),
    gate: normalizeDocumentCaptureGateOptions(options.gate),
    correction: normalizeDocumentCorrectionOptions(options.correction),
  };
}

/**
 * Reads bounded grayscale observations from a live video element and detects the
 * largest document quadrilateral. Frames never leave the browser.
 */
export function createDocumentFrameObserver(
  options: NormalizedCaptureDocumentCaptureOptions,
): DocumentFrameObserver {
  let canvas: HTMLCanvasElement | undefined;
  let context: CanvasRenderingContext2D | undefined;
  let previous: GrayFrame | undefined;

  function reset(): void {
    previous = undefined;
  }

  return {
    reset,
    dispose() {
      reset();
      canvas = undefined;
      context = undefined;
    },
    observe(video) {
      const size = boundedVideoSize(video, options.observationMaxDimension);
      if (size === undefined) return undefined;
      if (canvas === undefined || context === undefined) {
        canvas = video.ownerDocument.createElement("canvas");
        context = canvas.getContext("2d", { willReadFrequently: true }) ?? undefined;
      }
      if (canvas === undefined || context === undefined) return undefined;
      if (canvas.width !== size.width || canvas.height !== size.height) {
        canvas.width = size.width;
        canvas.height = size.height;
      }
      context.drawImage(video, 0, 0, size.width, size.height);
      const image = context.getImageData(0, 0, size.width, size.height);
      const gray = rgbaToGrayFrame(image, options.observationMaxDimension);
      const detection = detectDocumentQuad(gray, options.detection);
      const sharpness = varianceOfLaplacian(gray);
      const difference = previous === undefined ? 0 : grayFrameDifference(previous, gray);
      previous = gray;
      return { detection, sharpness, difference };
    },
  };
}

/**
 * Warps the detected document quad out of the captured frame. Returns `undefined`
 * for any degenerate quad or encoding failure so the caller can fall back to the
 * uncorrected frame.
 */
export async function captureCorrectedDocumentFrame(
  video: HTMLVideoElement,
  mediaType: EvidenceMediaType,
  detection: DocumentDetection,
  options: NormalizedCaptureDocumentCaptureOptions,
): Promise<CameraFrame | undefined> {
  try {
    const source = readVideoPixels(video, options.correction.maxDimension);
    if (source === undefined) return undefined;
    const corrected = correctDocumentFrame(source, detection, options.correction);
    if (corrected === undefined) return undefined;
    const body = await encodeCorrectedFrame(video.ownerDocument, corrected, mediaType);
    if (body === null || body.size <= 0 || body.type !== mediaType) return undefined;
    return { body, width: corrected.width, height: corrected.height };
  } catch {
    return undefined;
  }
}

function readVideoPixels(video: HTMLVideoElement, maxDimension: number): PixelBuffer | undefined {
  const size = boundedVideoSize(video, maxDimension);
  if (size === undefined) return undefined;
  const canvas = video.ownerDocument.createElement("canvas");
  canvas.width = size.width;
  canvas.height = size.height;
  const context = canvas.getContext("2d", { alpha: false });
  if (context === null) return undefined;
  context.drawImage(video, 0, 0, size.width, size.height);
  const image = context.getImageData(0, 0, size.width, size.height);
  return { data: image.data, width: size.width, height: size.height };
}

async function encodeCorrectedFrame(
  document: Document,
  corrected: {
    readonly image: {
      readonly data: Uint8ClampedArray<ArrayBuffer>;
      readonly width: number;
      readonly height: number;
    };
    readonly width: number;
    readonly height: number;
  },
  mediaType: EvidenceMediaType,
): Promise<Blob | null> {
  const canvas = document.createElement("canvas");
  canvas.width = corrected.width;
  canvas.height = corrected.height;
  const context = canvas.getContext("2d", { alpha: false });
  if (context === null) return null;
  context.putImageData(
    new ImageData(corrected.image.data, corrected.width, corrected.height),
    0,
    0,
  );
  return canvasBlob(canvas, mediaType);
}

function boundedVideoSize(
  video: HTMLVideoElement,
  maxDimension: number,
): { readonly width: number; readonly height: number } | undefined {
  const width = video.videoWidth;
  const height = video.videoHeight;
  if (!Number.isSafeInteger(width) || width <= 0 || !Number.isSafeInteger(height) || height <= 0) {
    return undefined;
  }
  const scale = Math.min(1, maxDimension / Math.max(width, height));
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
  };
}

function booleanOption(value: boolean | undefined, name: string, fallback: boolean): boolean {
  if (value === undefined) return fallback;
  if (typeof value !== "boolean") {
    throw new TypeError(`${name} must be a boolean.`);
  }
  return value;
}

function boundedNumber(
  value: number | undefined,
  name: string,
  minimum: number,
  maximum: number,
  fallback: number,
): number {
  if (value === undefined) return fallback;
  if (!Number.isFinite(value) || value < minimum || value > maximum) {
    throw new TypeError(`${name} must be a finite number between ${minimum} and ${maximum}.`);
  }
  return value;
}
