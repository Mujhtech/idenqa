import { expect, test } from "@playwright/test";

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
