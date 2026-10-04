import { expect, test } from "@playwright/test";

test("keeps the real tracker open across no-face attempt deadlines until cancellation", async ({
  page,
}) => {
  await page.goto("/");
  await page.evaluate(async () => {
    const moduleUrl = performance
      .getEntriesByType("resource")
      .map((entry) => entry.name)
      .find((name) => name.includes("/src/index.ts"));
    if (!moduleUrl) throw new Error("Capture module not loaded");
    const { createActiveLivenessMethodAdapter, createBrowserPoseTracker } = await import(moduleUrl);
    const controller = new AbortController();
    const canvas = document.createElement("canvas");
    canvas.width = 640;
    canvas.height = 480;
    const drawing = canvas.getContext("2d")!;
    drawing.fillStyle = "#888";
    drawing.fillRect(0, 0, 640, 480);
    const body = await new Promise<Blob>((resolve) =>
      canvas.toBlob((blob) => resolve(blob!), "image/jpeg"),
    );
    const stream = canvas.captureStream(1);
    const state = {
      attempts: 0,
      measurements: 0,
      submissions: 0,
      cameraClosed: false,
      trackerClosed: false,
      result: "running",
      cancel: () => controller.abort(new DOMException("Subject cancelled.", "AbortError")),
      pending: Promise.resolve(),
    };
    const adapter = createActiveLivenessMethodAdapter({
      requirementKey: "selfie",
      plan: {
        schema_version: "1.0",
        plan_id: "plan.browser.waiting",
        session_id: "ver_01M11HEQG00000000000000000",
        requirements: [
          {
            id: "selfie.live",
            evidence_type: "idenqa.evidence.selfie_image",
            artefact: "idenqa.artefact.selfie_image",
            acquisition_method: "idenqa.method.live_camera",
            camera: "front",
            quality: {
              minimum_width: 640,
              minimum_height: 480,
              maximum_bytes: 8388608,
              required_face_count: 1,
            },
            challenges: [{ id: "neutral", prompt: "neutral", maximum_duration_ms: 1000 }],
          },
        ],
      },
      cameraSessionFactory: async () => ({
        stream,
        capture: async () => ({ body, width: 640, height: 480 }),
        close: () => {
          state.cameraClosed = true;
          stream.getTracks().forEach((track) => track.stop());
        },
      }),
      poseTrackerFactory: async (signal: AbortSignal) => {
        const tracker = await createBrowserPoseTracker(signal);
        return {
          measure: async (
            frame: { body: Blob; width: number; height: number },
            frameSignal: AbortSignal,
          ) => {
            const pose = await tracker.measure(frame, frameSignal);
            state.measurements++;
            return pose;
          },
          close: () => {
            state.trackerClosed = true;
            tracker.close();
          },
        };
      },
      submit: async () => {
        state.submissions++;
      },
    });
    state.pending = adapter
      .acquire(
        {
          verificationId: "ver_01M11HEQG00000000000000000",
          requirementKey: "selfie",
          evidenceType: "idenqa.evidence.selfie_image",
          artefact: "idenqa.artefact.selfie_image",
          acquisitionMethod: "idenqa.method.live_camera",
          locale: "en",
          signal: controller.signal,
        },
        {
          setPreview: async () => {},
          update: (progress: { phase: string; poseStage?: string }) => {
            if (progress.phase === "challenge" && progress.poseStage === undefined)
              state.attempts++;
          },
        },
      )
      .then(
        () => {
          state.result = "submitted";
        },
        (error: Error) => {
          state.result = controller.signal.aborted ? "cancelled" : error.message;
        },
      );
    Reflect.set(globalThis, "waitingLiveness", state);
  });
  await expect
    .poll(() => page.evaluate(() => Reflect.get(globalThis, "waitingLiveness").attempts), {
      timeout: 15000,
    })
    .toBeGreaterThanOrEqual(3);
  const running = await page.evaluate(() => {
    const state = Reflect.get(globalThis, "waitingLiveness");
    return {
      result: state.result,
      submissions: state.submissions,
      cameraClosed: state.cameraClosed,
      trackerClosed: state.trackerClosed,
      measurements: state.measurements,
    };
  });
  expect(running).toMatchObject({
    result: "running",
    submissions: 0,
    cameraClosed: false,
    trackerClosed: false,
  });
  expect(running.measurements).toBeGreaterThan(1);
  const stopped = await page.evaluate(async () => {
    const state = Reflect.get(globalThis, "waitingLiveness");
    state.cancel();
    await state.pending;
    return {
      result: state.result,
      submissions: state.submissions,
      cameraClosed: state.cameraClosed,
      trackerClosed: state.trackerClosed,
    };
  });
  expect(stopped).toEqual({
    result: "cancelled",
    submissions: 0,
    cameraClosed: true,
    trackerClosed: true,
  });
});

test("loads the self-hosted landmark worker and rejects a frame without a face", async ({
  page,
}) => {
  await page.goto("/");
  const result = await page.evaluate(async () => {
    const moduleUrl = performance
      .getEntriesByType("resource")
      .map((entry) => entry.name)
      .find((name) => name.includes("/src/index.ts"));
    if (!moduleUrl) throw new Error("Capture module not loaded");
    const { createBrowserPoseTracker } = await import(moduleUrl);
    const controller = new AbortController();
    const tracker = await createBrowserPoseTracker(controller.signal);
    try {
      const canvas = document.createElement("canvas");
      canvas.width = 640;
      canvas.height = 480;
      const context = canvas.getContext("2d")!;
      context.fillStyle = "#888";
      context.fillRect(0, 0, 640, 480);
      const body = await new Promise<Blob>((resolve) =>
        canvas.toBlob((blob) => resolve(blob!), "image/jpeg"),
      );
      return await tracker.measure({ body, width: 640, height: 480 }, controller.signal);
    } finally {
      tracker.close();
    }
  });
  expect(result.faceCount).toBe(0);
});
