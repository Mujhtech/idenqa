import type { EvidenceMediaType } from "@idenqa/sdk";

export const LIVE_CAMERA_METHOD = "idenqa.method.live_camera";

export type CameraFacingMode = "user" | "environment";

export interface CameraFrame {
  readonly body: Blob;
  readonly width: number;
  readonly height: number;
}

export class CaptureCameraError extends Error {
  readonly code:
    | "CAPTURE_CAMERA_PERMISSION_DENIED"
    | "CAPTURE_CAMERA_UNAVAILABLE"
    | "CAPTURE_CAMERA_FRAME_FAILED";

  constructor(code: CaptureCameraError["code"], message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "CaptureCameraError";
    this.code = code;
  }
}

export async function startCamera(
  facingMode: CameraFacingMode,
  signal?: AbortSignal,
  mediaDevices: Pick<MediaDevices, "getUserMedia"> | undefined = globalThis.navigator?.mediaDevices,
): Promise<MediaStream> {
  if (isAborted(signal)) throw abortReason(signal!);
  if (mediaDevices === undefined || typeof mediaDevices.getUserMedia !== "function") {
    throw new CaptureCameraError(
      "CAPTURE_CAMERA_UNAVAILABLE",
      "Camera capture is unavailable in this browser.",
    );
  }
  let stream: MediaStream;
  try {
    stream = await mediaDevices.getUserMedia({
      audio: false,
      video: { facingMode: { ideal: facingMode } },
    });
  } catch (error) {
    if (isPermissionError(error)) {
      throw new CaptureCameraError(
        "CAPTURE_CAMERA_PERMISSION_DENIED",
        "Camera permission was denied.",
        { cause: error },
      );
    }
    throw new CaptureCameraError("CAPTURE_CAMERA_UNAVAILABLE", "The camera could not be started.", {
      cause: error,
    });
  }
  if (isAborted(signal)) {
    stopCamera(stream);
    throw abortReason(signal!);
  }
  if (stream.getVideoTracks().length === 0) {
    stopCamera(stream);
    throw new CaptureCameraError(
      "CAPTURE_CAMERA_UNAVAILABLE",
      "The selected camera did not provide a video track.",
    );
  }
  return stream;
}

export async function captureCameraFrame(
  video: HTMLVideoElement,
  mediaType: EvidenceMediaType,
): Promise<CameraFrame> {
  const width = video.videoWidth;
  const height = video.videoHeight;
  if (!Number.isSafeInteger(width) || width <= 0 || !Number.isSafeInteger(height) || height <= 0) {
    throw new CaptureCameraError(
      "CAPTURE_CAMERA_FRAME_FAILED",
      "The camera is not ready to capture a photo.",
    );
  }
  const canvas = video.ownerDocument.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d", { alpha: false });
  if (context === null) {
    throw new CaptureCameraError(
      "CAPTURE_CAMERA_FRAME_FAILED",
      "The browser could not prepare the captured photo.",
    );
  }
  context.drawImage(video, 0, 0, width, height);
  const body = await canvasBlob(canvas, mediaType);
  if (body.size <= 0 || body.type !== mediaType) {
    throw new CaptureCameraError(
      "CAPTURE_CAMERA_FRAME_FAILED",
      "The browser produced an invalid captured photo.",
    );
  }
  return { body, width, height };
}

export function stopCamera(stream: MediaStream | undefined): void {
  for (const track of stream?.getTracks() ?? []) track.stop();
}

export function cameraFacingMode(artefact: string): CameraFacingMode {
  return artefact === "idenqa.artefact.selfie_image" ? "user" : "environment";
}

export function canvasBlob(canvas: HTMLCanvasElement, mediaType: EvidenceMediaType): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (body) => {
        if (body === null) {
          reject(
            new CaptureCameraError(
              "CAPTURE_CAMERA_FRAME_FAILED",
              "The browser could not encode the captured photo.",
            ),
          );
          return;
        }
        resolve(body);
      },
      mediaType,
      mediaType === "image/jpeg" ? 0.92 : undefined,
    );
  });
}

function isPermissionError(error: unknown): boolean {
  return (
    error instanceof DOMException &&
    (error.name === "NotAllowedError" || error.name === "SecurityError")
  );
}

function abortReason(signal: AbortSignal): unknown {
  return signal.reason ?? new DOMException("Camera capture was cancelled.", "AbortError");
}

function isAborted(signal: AbortSignal | undefined): boolean {
  return signal?.aborted === true;
}
