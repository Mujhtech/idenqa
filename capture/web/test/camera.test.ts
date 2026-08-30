import { describe, expect, it, vi } from "vitest";

import { CaptureCameraError, startCamera, stopCamera } from "../src/index.js";

describe("camera acquisition", () => {
  it("requests video-only media with the selected facing mode", async () => {
    const stream = mediaStream();
    const getUserMedia = vi.fn().mockResolvedValue(stream);

    await expect(startCamera("environment", undefined, { getUserMedia })).resolves.toBe(stream);
    expect(getUserMedia).toHaveBeenCalledWith({
      audio: false,
      video: { facingMode: { ideal: "environment" } },
    });
  });

  it("classifies permission denial without exposing the browser error", async () => {
    const getUserMedia = vi
      .fn()
      .mockRejectedValue(new DOMException("synthetic private detail", "NotAllowedError"));

    await expect(startCamera("user", undefined, { getUserMedia })).rejects.toMatchObject({
      code: "CAPTURE_CAMERA_PERMISSION_DENIED",
      message: "Camera permission was denied.",
    } satisfies Partial<CaptureCameraError>);
  });

  it("stops every media track during cleanup", () => {
    const first = { stop: vi.fn() };
    const second = { stop: vi.fn() };
    stopCamera({ getTracks: () => [first, second] } as unknown as MediaStream);
    expect(first.stop).toHaveBeenCalledOnce();
    expect(second.stop).toHaveBeenCalledOnce();
  });
});

function mediaStream(): MediaStream {
  return {
    getVideoTracks: () => [{}],
    getTracks: () => [],
  } as unknown as MediaStream;
}
