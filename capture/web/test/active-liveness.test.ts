import { describe, expect, it, vi } from "vitest";

import {
  CaptureActiveLivenessError,
  createActiveLivenessMethodAdapter,
  type CaptureActiveLivenessCameraSession,
  type CaptureMethodAdapterControls,
  type CaptureMethodAdapterContext,
} from "../src/index.js";

describe("active liveness adapter", () => {
  it("describes the server-issued challenge count on the one-action preparation page", () => {
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      submit: vi.fn(),
    });

    expect(adapter.copy).toMatchObject({
      title: "Let’s Make Sure You’re You",
      action: "Start Liveness Check",
      preparation: "Center your face in the camera, then follow 3 quick prompts.",
    });
  });

  it("captures ordered challenged frames and submits without asserting assurance", async () => {
    const close = vi.fn();
    const capture = vi.fn().mockResolvedValue({
      body: new Blob([new Uint8Array([0xff, 0xd8, 0xff])], { type: "image/jpeg" }),
      width: 720,
      height: 720,
    });
    const stream = { getTracks: () => [] } as unknown as MediaStream;
    const submit = vi.fn();
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      settleDurationMs: 0,
      clock: () => new Date("2026-09-14T12:00:00Z"),
      cameraSessionFactory: vi.fn().mockResolvedValue({ stream, capture, close }),
      assess: async (frame) => ({
        width: frame.width,
        height: frame.height,
        byteCount: frame.body.size,
        brightness: 0.5,
        contrast: 0.5,
        sharpness: 0.5,
        glare: 0.05,
        faceCount: 1,
      }),
      submit,
    });
    const updates: unknown[] = [];
    const previews: Array<MediaStream | undefined> = [];

    await adapter.acquire(
      { ...context(), fallbackCondition: "capture_failed" },
      controls(updates, previews),
    );

    expect(capture).toHaveBeenCalledTimes(3);
    expect(updates).toEqual([
      { phase: "requesting_permission" },
      { phase: "ready" },
      { phase: "challenge", current: 1, total: 3, prompt: "neutral" },
      { phase: "challenge", current: 2, total: 3, prompt: "turn_left" },
      { phase: "challenge", current: 3, total: 3, prompt: "turn_right" },
      { phase: "submitting" },
    ]);
    expect(previews).toEqual([stream, undefined]);
    expect(submit).toHaveBeenCalledOnce();
    expect(submit.mock.calls[0]?.[0]).toMatchObject({
      schemaVersion: "1.0",
      planId: "plan.pan_african_individual.v1",
      sessionId: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
      requirementId: "selfie.live",
      requirementKey: "selfie",
      acquisitionMethod: "idenqa.method.live_camera",
      fallbackCondition: "capture_failed",
      frames: [
        { challengeId: "challenge.neutral", prompt: "neutral" },
        { challengeId: "challenge.turn_left", prompt: "turn_left" },
        { challengeId: "challenge.turn_right", prompt: "turn_right" },
      ],
    });
    expect(submit.mock.calls[0]?.[0]).not.toHaveProperty("assurances");
    expect(close).toHaveBeenCalledOnce();
  });

  it("fails closed when a required quality measurement is unavailable", async () => {
    const close = vi.fn();
    const camera: CaptureActiveLivenessCameraSession = {
      stream: { getTracks: () => [] } as unknown as MediaStream,
      capture: vi.fn().mockResolvedValue({
        body: new Blob([new Uint8Array([0xff, 0xd8, 0xff])], { type: "image/jpeg" }),
        width: 720,
        height: 720,
      }),
      close,
    };
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      settleDurationMs: 0,
      cameraSessionFactory: vi.fn().mockResolvedValue(camera),
      submit: vi.fn(),
    });

    await expect(adapter.acquire(context(), controls([], []))).rejects.toMatchObject({
      code: "CAPTURE_ACTIVE_LIVENESS_QUALITY",
      failures: expect.arrayContaining(["brightness_unavailable", "face_count_unavailable"]),
    } satisfies Partial<CaptureActiveLivenessError>);
    expect(close).toHaveBeenCalledOnce();
  });

  it("rejects a plan bound to another verification", async () => {
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      cameraSessionFactory: vi.fn(),
      submit: vi.fn(),
    });

    await expect(
      adapter.acquire(
        { ...context(), verificationId: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JZZ" },
        controls([], []),
      ),
    ).rejects.toMatchObject({ code: "CAPTURE_ACTIVE_LIVENESS_INVALID" });
  });

  it("enforces each server-authored challenge deadline and releases the camera", async () => {
    vi.useFakeTimers();
    try {
      const close = vi.fn();
      const camera: CaptureActiveLivenessCameraSession = {
        stream: { getTracks: () => [] } as unknown as MediaStream,
        capture: (_mediaType, signal) =>
          new Promise((_, reject) => {
            signal.addEventListener("abort", () => reject(signal.reason), { once: true });
          }),
        close,
      };
      const adapter = createActiveLivenessMethodAdapter({
        plan: plan(),
        requirementKey: "selfie",
        settleDurationMs: 0,
        cameraSessionFactory: vi.fn().mockResolvedValue(camera),
        submit: vi.fn(),
      });

      const pending = adapter.acquire(context(), controls([], []));
      const rejection = expect(pending).rejects.toMatchObject({
        code: "CAPTURE_ACTIVE_LIVENESS_TIMEOUT",
      });
      await vi.advanceTimersByTimeAsync(5_000);

      await rejection;
      expect(close).toHaveBeenCalledOnce();
    } finally {
      vi.useRealTimers();
    }
  });

  it("propagates subject cancellation and clears the preview", async () => {
    const close = vi.fn();
    const controller = new AbortController();
    const previews: Array<MediaStream | undefined> = [];
    const camera: CaptureActiveLivenessCameraSession = {
      stream: { getTracks: () => [] } as unknown as MediaStream,
      capture: (_mediaType, signal) =>
        new Promise((_, reject) => {
          signal.addEventListener("abort", () => reject(signal.reason), { once: true });
        }),
      close,
    };
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      settleDurationMs: 0,
      cameraSessionFactory: vi.fn().mockResolvedValue(camera),
      submit: vi.fn(),
    });
    const pending = adapter.acquire(
      { ...context(), signal: controller.signal },
      controls([], previews),
    );
    await vi.waitFor(() => expect(previews).toHaveLength(1));

    const reason = new DOMException("Subject cancelled.", "AbortError");
    controller.abort(reason);

    await expect(pending).rejects.toBe(reason);
    expect(close).toHaveBeenCalledOnce();
    expect(previews).toEqual([camera.stream, undefined]);
  });

  it("does not request the camera when acquisition is already cancelled", async () => {
    const controller = new AbortController();
    const reason = new DOMException("Subject already cancelled.", "AbortError");
    controller.abort(reason);
    const cameraSessionFactory = vi.fn();
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      cameraSessionFactory,
      submit: vi.fn(),
    });

    await expect(
      adapter.acquire({ ...context(), signal: controller.signal }, controls([], [])),
    ).rejects.toBe(reason);
    expect(cameraSessionFactory).not.toHaveBeenCalled();
  });

  it("rejects non-finite or contradictory quality results", async () => {
    const close = vi.fn();
    const frame = {
      body: new Blob([new Uint8Array([0xff, 0xd8, 0xff])], { type: "image/jpeg" }),
      width: 720,
      height: 720,
    };
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      settleDurationMs: 0,
      cameraSessionFactory: vi.fn().mockResolvedValue({
        stream: { getTracks: () => [] } as unknown as MediaStream,
        capture: vi.fn().mockResolvedValue(frame),
        close,
      }),
      assess: async () => ({
        width: 721,
        height: 720,
        byteCount: frame.body.size + 1,
        brightness: Number.NaN,
        contrast: 0.5,
        sharpness: 0.5,
        glare: 0.1,
        faceCount: 1.5,
      }),
      submit: vi.fn(),
    });

    await expect(adapter.acquire(context(), controls([], []))).rejects.toMatchObject({
      code: "CAPTURE_ACTIVE_LIVENESS_QUALITY",
      failures: expect.arrayContaining([
        "dimensions_invalid",
        "size_invalid",
        "brightness_invalid",
        "face_count_invalid",
      ]),
    } satisfies Partial<CaptureActiveLivenessError>);
    expect(close).toHaveBeenCalledOnce();
  });

  it("cleans up when submission fails", async () => {
    const close = vi.fn();
    const stream = { getTracks: () => [] } as unknown as MediaStream;
    const frame = {
      body: new Blob([new Uint8Array([0xff, 0xd8, 0xff])], { type: "image/jpeg" }),
      width: 720,
      height: 720,
    };
    const failure = new Error("provider unavailable");
    const adapter = createActiveLivenessMethodAdapter({
      plan: plan(),
      requirementKey: "selfie",
      settleDurationMs: 0,
      cameraSessionFactory: vi.fn().mockResolvedValue({
        stream,
        capture: vi.fn().mockResolvedValue(frame),
        close,
      }),
      assess: async () => ({
        width: frame.width,
        height: frame.height,
        byteCount: frame.body.size,
        brightness: 0.5,
        contrast: 0.5,
        sharpness: 0.5,
        glare: 0.1,
        faceCount: 1,
      }),
      submit: vi.fn().mockRejectedValue(failure),
    });
    const previews: Array<MediaStream | undefined> = [];

    await expect(adapter.acquire(context(), controls([], previews))).rejects.toBe(failure);
    expect(close).toHaveBeenCalledOnce();
    expect(previews).toEqual([stream, undefined]);
  });

  it("requires an actual active challenge", () => {
    const input = plan();
    input.requirements[0]!.challenges = [];

    expect(() =>
      createActiveLivenessMethodAdapter({
        plan: input,
        requirementKey: "selfie",
        submit: vi.fn(),
      }),
    ).toThrowError(expect.objectContaining({ code: "CAPTURE_ACTIVE_LIVENESS_INVALID" }));
  });
});

function context(): CaptureMethodAdapterContext {
  return {
    verificationId: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
    requirementKey: "selfie",
    evidenceType: "idenqa.evidence.selfie_image",
    artefact: "idenqa.artefact.selfie_image",
    acquisitionMethod: "idenqa.method.live_camera",
    locale: "en",
    signal: new AbortController().signal,
  };
}

function controls(
  updates: unknown[],
  previews: Array<MediaStream | undefined>,
): CaptureMethodAdapterControls {
  return {
    update: (progress) => updates.push(progress),
    setPreview: async (stream) => {
      previews.push(stream);
    },
  };
}

function plan() {
  return {
    schema_version: "1.0",
    plan_id: "plan.pan_african_individual.v1",
    session_id: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
    requirements: [
      {
        id: "selfie.live",
        evidence_type: "idenqa.evidence.selfie_image",
        artefact: "idenqa.artefact.selfie_image",
        acquisition_method: "idenqa.method.live_camera",
        camera: "front",
        quality: {
          minimum_width: 720,
          minimum_height: 720,
          maximum_bytes: 8_388_608,
          minimum_brightness: 0.18,
          maximum_brightness: 0.9,
          minimum_contrast: 0.08,
          minimum_sharpness: 0.1,
          maximum_glare: 0.2,
          required_face_count: 1,
        },
        challenges: [
          { id: "challenge.neutral", prompt: "neutral", maximum_duration_ms: 5_000 },
          { id: "challenge.turn_left", prompt: "turn_left", maximum_duration_ms: 5_000 },
          { id: "challenge.turn_right", prompt: "turn_right", maximum_duration_ms: 5_000 },
        ],
      },
    ],
  };
}
