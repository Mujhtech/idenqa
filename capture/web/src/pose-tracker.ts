import type { CameraFrame } from "./camera.js";
import type { CaptureFacePose } from "./pose.js";

export interface CapturePoseTracker {
  measure(frame: CameraFrame, signal: AbortSignal): Promise<CaptureFacePose>;
  close(): void;
}

/** Loads the package-owned worker and model from the host, never a public CDN.
 * The worker receives transient local frames; no frames are sent to a network. */
export async function createBrowserPoseTracker(
  signal: AbortSignal,
  assetBaseUrl = "/idenqa-liveness/",
): Promise<CapturePoseTracker> {
  const base = new URL(
    assetBaseUrl.endsWith("/") ? assetBaseUrl : `${assetBaseUrl}/`,
    location.href,
  );
  if (base.origin !== location.origin)
    throw new Error("Liveness assets must be hosted on the page origin.");
  const worker = new Worker(new URL("pose-worker.js", base));
  let closed = false;
  let pending:
    { resolve: (value: CaptureFacePose) => void; reject: (error: unknown) => void } | undefined;
  const close = () => {
    if (closed) return;
    closed = true;
    worker.terminate();
    pending?.reject(new DOMException("Pose tracker closed.", "AbortError"));
    pending = undefined;
    signal.removeEventListener("abort", close);
  };
  const request = (message: unknown, transfer: Transferable[] = []) =>
    new Promise<CaptureFacePose>((resolve, reject) => {
      if (closed || pending) {
        reject(new Error("Pose tracker is unavailable."));
        return;
      }
      pending = { resolve, reject };
      worker.postMessage(message, transfer);
    });
  worker.onmessage = ({ data }) => {
    const current = pending;
    pending = undefined;
    if (data.error)
      current?.reject(new Error("Face tracking could not run. Check the local model assets."));
    else current?.resolve(data.pose);
  };
  worker.onerror = () => {
    pending?.reject(new Error("Face tracking worker failed."));
    pending = undefined;
    close();
  };
  signal.addEventListener("abort", close, { once: true });
  if (signal.aborted) close();
  const timeout = setTimeout(close, 30_000);
  try {
    await request({ type: "init", base: base.href });
  } catch (error) {
    close();
    throw error;
  } finally {
    clearTimeout(timeout);
  }
  return {
    async measure(frame, frameSignal) {
      if (frameSignal.aborted || closed)
        throw frameSignal.reason ?? new Error("Face tracking stopped.");
      const bitmap = await createImageBitmap(frame.body);
      if (frameSignal.aborted || closed) {
        bitmap.close();
        throw frameSignal.reason ?? new Error("Face tracking stopped.");
      }
      const aborted = () => close();
      frameSignal.addEventListener("abort", aborted, { once: true });
      try {
        return await request({ type: "frame", bitmap }, [bitmap]);
      } finally {
        frameSignal.removeEventListener("abort", aborted);
      }
    },
    close,
  };
}
