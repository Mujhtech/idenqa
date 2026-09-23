import { expect, test } from "@playwright/test";
import { auditCaptureThemeContrast, captureContrastRatio } from "../../src/theme-contrast.js";

import type { CaptureDocumentCaptureOptions, IdenqaCaptureElement } from "../../src/index.js";

test("renders the plain-HTML capture plan with semantic, keyboard-operable choices", async ({
  page,
}) => {
  await page.goto("/");

  await expect(
    page.getByRole("heading", { level: 2, name: "Identity Verification" }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { level: 3, name: "Selfie Image" })).toBeVisible();
  await expect(page.getByRole("heading", { level: 4, name: "Document Front" })).toBeVisible();
  await expect(page.getByRole("heading", { level: 4, name: "Document Back" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Capture methods for Selfie Image" })).toBeVisible();

  const camera = page.getByRole("button", { name: "Use Camera" });
  await camera.focus();
  await expect(camera).toBeFocused();
  await page.keyboard.press("Enter");

  await expect(camera).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("status").filter({ hasText: "Camera selected." })).toBeVisible();
  await expect(page.locator("#selection")).toHaveText(
    "idenqa.method.live_camera selected for idenqa.artefact.selfie_image.",
  );
});

test("keeps the component within a narrow mobile viewport", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 720 });
  await page.goto("/");

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await expect(page.getByRole("button", { name: "Upload File" }).first()).toBeVisible();
});

test("presents one keyboard-operable primary task at a time", async ({ page }) => {
  await mockCaptureFlow(page, { consentRequired: true });
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-guided-token");

  await expect(page.getByRole("heading", { name: "Let’s Verify Your Identity" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Identity Verification Notice" })).toHaveCount(0);
  await expect(page.getByLabel("Choose File")).toHaveCount(0);

  await page.keyboard.press("Tab");
  const start = page.getByRole("button", { name: "Get Started" });
  await expect(start).toBeFocused();
  await page.keyboard.press("Enter");

  await expect(page.getByRole("heading", { name: "Review Before You Continue" })).toBeVisible();
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
});

test("applies public styling variables across the component boundary", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await mockCaptureFlow(page, { consentRequired: false });
  await page.goto("/");
  const capture = page.locator("idenqa-capture");
  await capture.evaluate((element) => {
    element.style.setProperty("--idq-capture-accent", "#7c3aed");
    element.style.setProperty("--idq-capture-accent-foreground", "#fff7ed");
    element.style.setProperty("--idq-capture-background", "#fefce8");
    element.style.setProperty("--idq-capture-control-radius", "1.25rem");
    element.style.setProperty("--idq-capture-shell-max-width", "48rem");
    element.style.setProperty("--idq-capture-shell-radius", "0.5rem");
    element.style.setProperty("--idq-capture-shell-shadow", "none");
  });
  await loadCaptureFlow(page, "synthetic-theme-token");

  const shell = capture.locator("section.shell");
  const primary = page.getByRole("button", { name: "Get Started" });
  await expect(shell).toHaveCSS("background-color", "rgb(254, 252, 232)");
  await expect(shell).toHaveCSS("border-radius", "8px");
  await expect(shell).toHaveCSS("max-width", "768px");
  await expect(shell).toHaveCSS("box-shadow", "none");
  await expect(primary).toHaveCSS("background-color", "rgb(124, 58, 237)");
  await expect(primary).toHaveCSS("color", "rgb(255, 247, 237)");
  await expect(primary).toHaveCSS("border-radius", "20px");

  await page.emulateMedia({ colorScheme: "dark" });
  await expect(shell).toHaveCSS("background-color", "rgb(254, 252, 232)");
  await expect(primary).toHaveCSS("background-color", "rgb(124, 58, 237)");
});

test("audits computed light, dark and host theme contrast including hover and focus", async ({
  page,
}) => {
  await page.goto("/");
  for (const mode of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: mode });
    const palette = await readThemePalette(page);
    expect(auditCaptureThemeContrast(palette)).toEqual([]);
  }
  await page.locator("idenqa-capture").evaluate((element) => {
    element.style.setProperty("--idq-capture-background", "#ffffff");
    element.style.setProperty("--idq-capture-text", "#eeeeee");
  });
  expect(auditCaptureThemeContrast(await readThemePalette(page))).toContainEqual(
    expect.objectContaining({
      foreground: "text",
      background: "background",
      reason: "insufficient_contrast",
    }),
  );
});

test("applies portable themes beneath host overrides and clears previous experience colours", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  const moduleURL = `/@fs${new URL("../../src/experience.ts", import.meta.url).pathname}`;
  await page.locator("idenqa-capture").evaluate(async (element, moduleURL) => {
    const { applyCaptureExperienceTheme } = await import(moduleURL);
    applyCaptureExperienceTheme(element, {
      "--idq-capture-background": "#fff7ed",
      "--idq-capture-text": "#1b1f23",
    });
  }, moduleURL);
  await expect(page.locator("idenqa-capture .shell")).toHaveCSS(
    "background-color",
    "rgb(255, 247, 237)",
  );
  await page.addStyleTag({ content: "idenqa-capture { --idq-capture-background: #fefce8; }" });
  await expect(page.locator("idenqa-capture .shell")).toHaveCSS(
    "background-color",
    "rgb(254, 252, 232)",
  );
  await page.locator("idenqa-capture").evaluate(async (element, moduleURL) => {
    const { applyCaptureExperienceTheme } = await import(moduleURL);
    applyCaptureExperienceTheme(element, undefined);
  }, moduleURL);
  await expect(page.locator("idenqa-capture .shell")).toHaveCSS("color", "rgb(20, 32, 29)");
  await expect(page.locator("idenqa-capture .shell")).toHaveCSS(
    "background-color",
    "rgb(254, 252, 232)",
  );
});

async function readThemePalette(page: import("@playwright/test").Page) {
  return page.locator("idenqa-capture .shell").evaluate((element) => {
    const style = getComputedStyle(element);
    const token = (name: string) => style.getPropertyValue(`--idq-capture-${name}`).trim();
    return {
      background: token("background"),
      surface: token("surface"),
      surfaceStrong: token("surface-strong"),
      text: token("text"),
      muted: token("muted"),
      accent: token("accent"),
      accentStrong: token("accent-strong"),
      accentForeground: token("accent-foreground"),
    };
  });
}

test("supports RTL direction, text enlargement, reduced motion, and forced colours", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce", forcedColors: "active" });
  await page.setViewportSize({ width: 320, height: 720 });
  await mockCaptureFlow(page, { consentRequired: false, noticeLocale: "ar" });
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-accessibility-token");
  await page.evaluate(() => {
    document.documentElement.style.fontSize = "200%";
  });

  await expect(page.locator("idenqa-capture").locator("section.shell")).toHaveAttribute(
    "dir",
    "rtl",
  );
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);

  const start = page.getByRole("button", { name: "Get Started" });
  await start.focus();
  expect(await start.evaluate((button) => getComputedStyle(button).outlineStyle)).not.toBe("none");
});

test("matches the safe-default responsive visual captures", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  const methods = ["idenqa.method.live_camera", "idenqa.method.file_upload"];
  await mockCaptureFlow(page, { consentRequired: true, primaryMethods: methods });
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-visual-token", methods);

  const shell = page.locator("idenqa-capture").locator("section.shell");
  await expect(shell).toHaveScreenshot("guided-intro-mobile-light.png", {
    animations: "disabled",
    maxDiffPixels: 20,
  });

  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.getByRole("button", { name: "Get Started" }).click();
  await expect(shell).toHaveScreenshot("guided-notice-tablet-dark.png", {
    animations: "disabled",
    maxDiffPixels: 20,
  });

  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole("button", { name: "Agree & Continue" }).click();
  await expect(page.getByRole("heading", { name: "Get Your Selfie Ready" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Use Another Method" })).toBeVisible();
  await expect(shell).toHaveScreenshot("guided-method-desktop-light.png", {
    animations: "disabled",
    maxDiffPixels: 20,
  });
});

test("shows the exact notice and records explicit consent before revealing capture", async ({
  page,
}) => {
  const captureToken = "synthetic-capture-token-that-must-not-enter-the-dom";
  const requests = await mockCaptureFlow(page, { consentRequired: true });
  await page.goto("/");

  await startCaptureFlow(page, captureToken);
  expect(requests.authorization).toEqual([
    `Bearer ${captureToken}-outcome`,
    `Bearer ${captureToken}`,
    `Bearer ${captureToken}`,
    `Bearer ${captureToken}`,
  ]);

  await expect(
    page.getByRole("heading", { level: 3, name: "Identity Verification Notice" }),
  ).toBeVisible();
  await expect(page.getByText("We need to verify your identity.", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Your evidence is used only for identity verification.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("You may refuse and collection will not continue.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Example Controller", { exact: true })).toBeVisible();
  await expect(page.getByText("Example Recipient", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Upload File" })).toHaveCount(0);

  await page.getByRole("button", { name: "Agree & Continue" }).click();

  await expect(page.getByRole("heading", { name: "Get Your Selfie Ready" })).toBeVisible();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await expect(page.getByLabel("Choose File")).toBeAttached();
  expect(requests.responses).toEqual([
    {
      action: "consent",
      locale: "en",
      rendered_experience_version: "idenqa.capture.web.v1",
    },
  ]);
  const outerHTML = await page.locator("idenqa-capture").evaluate((element) => element.outerHTML);
  expect(outerHTML).not.toContain(captureToken);
});

test("records refusal without exposing capture methods", async ({ page }) => {
  const captureToken = "synthetic-refusal-token-that-must-not-enter-the-dom";
  const requests = await mockCaptureFlow(page, { consentRequired: false });
  await page.goto("/");

  await startCaptureFlow(page, captureToken);
  await expect(page.getByRole("button", { name: "Acknowledge & Continue" })).toBeVisible();
  await page.getByRole("button", { name: "Refuse" }).click();

  await expect(
    page.getByText("You chose not to continue. No evidence will be collected in this experience."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Upload File" })).toHaveCount(0);
  expect(requests.responses).toEqual([
    {
      action: "refuse",
      locale: "en",
      rendered_experience_version: "idenqa.capture.web.v1",
    },
  ]);
});

test("validates and uploads a requirement-bound file without sending its filename", async ({
  page,
}) => {
  const requests = await mockCaptureFlow(page, { consentRequired: false });
  await page.goto("/");
  await startCaptureFlow(page, "synthetic-upload-token");
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();

  const input = page.getByLabel("Choose File");
  await expect(input).toHaveAttribute("accept", "image/jpeg,image/png");
  await input.setInputFiles({
    name: "private-subject-name.png",
    mimeType: "image/png",
    buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  });
  await expect(page.getByAltText("Preview of Your Selfie")).toBeVisible();
  await page.getByRole("button", { name: "Use This File" }).click();

  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadIntents).toHaveLength(1);
  expect(requests.uploadIntents[0]).toMatchObject({
    requirement_key: "selfie",
    artefact: "idenqa.artefact.selfie_image",
    acquisition_method: "idenqa.method.file_upload",
    expected_bytes: 8,
    media_type: "image/png",
    region: "idenqa.region.synthetic",
  });
  expect(JSON.stringify(requests.uploadIntents[0])).not.toContain("private-subject-name.png");
  expect(requests.uploadBodies).toEqual(["89504e470d0a1a0a"]);
  expect(requests.uploadHeaders[0]).toMatchObject({
    contentType: "image/png",
    ifMatch: '"1"',
  });
  expect(requests.uploadHeaders[0]?.contentDigest).toMatch(
    /^sha-256=:[A-Za-z0-9+/]+=*:$|^sha-256=:[A-Za-z0-9+/]+:$/,
  );
});

test("locks an any_of step after one method succeeds and reports capture completion", async ({
  page,
}) => {
  await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.file_upload", "idenqa.method.live_camera"],
  });
  await page.goto("/");
  await startCaptureFlow(page, "synthetic-choice-token", [
    "idenqa.method.file_upload",
    "idenqa.method.live_camera",
  ]);
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.locator("idenqa-capture").evaluate((element) => {
    const state = globalThis as typeof globalThis & { captureCompleteDetail?: unknown };
    element.addEventListener("idenqa-capture-complete", (event) => {
      state.captureCompleteDetail = (event as CustomEvent).detail;
    });
  });

  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await expect(page.getByLabel("Choose File")).toBeAttached();
  await page.getByLabel("Choose File").setInputFiles(pngFile("choice.png"));
  await page.getByRole("button", { name: "Use This File" }).click();

  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Start Camera" })).toHaveCount(0);
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (globalThis as typeof globalThis & { captureCompleteDetail?: unknown })
            .captureCompleteDetail,
      ),
    )
    .toEqual({
      verificationId: sessionResponse.id,
      completedSteps: 1,
      totalSteps: 1,
      captureComplete: true,
    });
});

test("recovers accepted progress after a fresh page load without uploading again", async ({
  page,
}) => {
  const requests = await mockCaptureFlow(page, { consentRequired: false });
  await page.goto("/");
  await page.reload();
  await startCaptureFlow(page, "synthetic-recovery-token");
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByLabel("Choose File").setInputFiles(pngFile("first-load.png"));
  await page.getByRole("button", { name: "Use This File" }).click();
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();

  await startCaptureFlow(page, "synthetic-recovery-token");

  await expect(page.getByRole("heading", { name: "Welcome Back" })).toBeVisible();
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
  await page.getByRole("button", { name: "Review and Finish" }).click();
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadIntents).toHaveLength(1);
  expect(requests.uploadBodies).toHaveLength(1);
});

test("tracks document front and back independently before capture completion", async ({ page }) => {
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
  });
  await page.goto("/");
  await startCaptureFlow(page, "synthetic-document-token");
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();

  const progress = page.getByRole("progressbar", { name: "Evidence capture progress" });
  await expect(progress).toHaveJSProperty("max", 2);
  await expect(progress).toHaveJSProperty("value", 0);
  const inputs = page.getByLabel("Choose File");
  await expect(inputs).toHaveCount(1);

  await inputs.setInputFiles(pngFile("front.png"));
  await page.getByRole("button", { name: "Use This File" }).click();
  await expect(progress).toHaveJSProperty("value", 1);
  await expect(
    page.getByText("Required evidence capture is complete. Verification may continue."),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Continue to Next Step" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByLabel("Choose File").setInputFiles(pngFile("back.png"));
  await page.getByRole("button", { name: "Use This File" }).click();
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadIntents.map((input) => input.artefact)).toEqual([
    "idenqa.artefact.document_front",
    "idenqa.artefact.document_back",
  ]);
});

test("captures, reviews, and uploads a live-camera photo", async ({ page }) => {
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
  });
  await page.goto("/");
  await startCaptureFlow(page, "synthetic-camera-token", [
    "idenqa.method.live_camera",
    "idenqa.method.file_upload",
  ]);
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();

  await page.getByRole("button", { name: "Start Camera" }).click();
  await expect(page.getByLabel("Live camera preview for Your Selfie")).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Camera ready." })).toBeVisible();
  await page.getByRole("button", { name: "Capture Photo" }).click();
  await expect(page.getByAltText("Captured Preview of Your Selfie")).toBeVisible();
  await page.getByRole("button", { name: "Use Photo" }).click();

  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadIntents[0]).toMatchObject({
    acquisition_method: "idenqa.method.live_camera",
    requirement_key: "selfie",
    artefact: "idenqa.artefact.selfie_image",
  });
  expect(requests.uploadBodies[0]).not.toBe("");
});

test("uses capture_failed fallback only after a real camera failure", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator.mediaDevices, "getUserMedia", {
      configurable: true,
      value: () => Promise.reject(new DOMException("synthetic denial", "NotAllowedError")),
    });
  });
  await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    fallbacks: [
      {
        on: ["capture_failed"],
        acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
      },
    ],
  });
  await page.goto("/");
  await startCaptureFlow(page, "synthetic-fallback-token", [
    "idenqa.method.live_camera",
    "idenqa.method.file_upload",
  ]);
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByRole("button", { name: "Start Camera" }).click();

  await expect(
    page.getByText("The camera attempt failed. This policy-approved alternative is available."),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "Get Your Selfie Ready" })).toBeVisible();
});

test("camera cancellation stops the preview without activating fallback", async ({ page }) => {
  await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    fallbacks: [
      {
        on: ["capture_failed"],
        acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
      },
    ],
  });
  await page.goto("/");
  await startCaptureFlow(page, "synthetic-cancel-token", [
    "idenqa.method.live_camera",
    "idenqa.method.file_upload",
  ]);
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByRole("button", { name: "Start Camera" }).click();
  await expect(page.getByLabel("Live camera preview for Your Selfie")).toBeVisible();
  await page.getByRole("button", { name: "Cancel Camera" }).click();

  await expect(page.getByRole("button", { name: "Start Camera" })).toBeVisible();
  await expect(page.getByText(/camera attempt failed/i)).toHaveCount(0);
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
});

test("records a sole pinned document option before showing preparation", async ({ page }) => {
  await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front"],
      documentOptions: [
        { id: "passport", label: "passport", artefacts: ["idenqa.artefact.document_front"] },
      ],
    },
  });
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-single-document", ["idenqa.method.live_camera"]);
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await expect(
    page.getByRole("heading", { name: "Take a clear photo of the front of your passport." }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "Which document will you use?" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Change document" })).toHaveCount(0);
});

test("keeps document selection visible when Core rejects the choice", async ({ page }) => {
  const observed = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front"],
      documentOptions: [
        { id: "passport", label: "passport", artefacts: ["idenqa.artefact.document_front"] },
        {
          id: "national_id",
          label: "national identity card",
          artefacts: ["idenqa.artefact.document_front"],
        },
      ],
    },
  });
  await page.route("**/core/v1/capture/document-selection", (route) =>
    route.fulfill({
      status: 409,
      json: {
        error: {
          code: "SESSION_CONFLICT",
          message: "The session changed.",
          request_id: "req_selection_conflict",
        },
      },
    }),
  );
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-document-selection-conflict", [
    "idenqa.method.live_camera",
  ]);
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "passport", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText(
    "We couldn’t save that choice. Select your document to try again.",
  );
  await expect(page.getByRole("heading", { name: "Which document will you use?" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to Capture" })).toHaveCount(0);
  expect(observed.uploadIntents).toHaveLength(0);
});

test("document selection drives front/back capture and survives review and retake", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera", "idenqa.method.file_upload"],
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
      documentOptions: [
        {
          id: "driver_license",
          label: "driver license",
          artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
        },
        {
          id: "national_id",
          label: "national identity card",
          artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
        },
      ],
    },
  });
  await page.goto("/");
  await loadCaptureFlow(
    page,
    "synthetic-document-selection",
    ["idenqa.method.live_camera", "idenqa.method.file_upload"],
    { autoCapture: false },
  );
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await expect(page.getByRole("heading", { name: "Which document will you use?" })).toBeVisible();
  await page.getByRole("button", { name: "driver license" }).click();
  await page.getByRole("button", { name: "Change document" }).click();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "Change document" }).click();
  await page.getByRole("button", { name: "national identity card" }).click();
  await page.getByRole("button", { name: "Use Another Method" }).click();
  await page.getByRole("button", { name: "Upload File" }).click();
  await page.getByRole("button", { name: "Use Another Method" }).click();
  await page.getByRole("button", { name: "Use Camera" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await expect(
    page.getByRole("heading", {
      name: "Take a clear photo of the front of your national identity card.",
    }),
  ).toBeVisible();
  await expect(page.locator("idenqa-capture .shell")).toHaveCSS("background-color", "rgb(8, 8, 8)");
  await page.locator("idenqa-capture summary").click();
  await expect(page.getByText(/Place your document on a flat surface/)).toBeVisible();
  await page.locator("idenqa-capture summary").click();
  await page.getByRole("button", { name: "Start Camera" }).click();
  await expect(page.getByRole("button", { name: "Capture Photo", exact: true })).toBeEnabled();
  await page.screenshot({
    path: test.info().outputPath("document-capture-mobile.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Capture Photo", exact: true }).click();
  await expect(page.getByRole("heading", { name: /Make sure the lighting is good/ })).toBeVisible();
  const reviewColours = await page
    .locator("idenqa-capture .document-camera .primary")
    .evaluate((element) => {
      const style = getComputedStyle(element);
      return { foreground: style.color, background: style.backgroundColor };
    });
  expect(
    captureContrastRatio(reviewColours.foreground, reviewColours.background),
  ).toBeGreaterThanOrEqual(4.5);
  await page.screenshot({
    path: test.info().outputPath("document-review-mobile.png"),
    fullPage: true,
  });
  expect(requests.uploadBodies).toHaveLength(0);
  await page.getByRole("button", { name: "Retake Photo" }).click();
  await expect(
    page.getByRole("heading", { name: /front of your national identity card/ }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Capture Photo", exact: true }).click();
  await page.getByRole("button", { name: "Use Photo" }).click();
  await page.getByRole("button", { name: "Continue to Next Step" }).click();
  await expect(page.getByRole("button", { name: "Change document" })).toHaveCount(0);
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await expect(
    page.getByRole("heading", { name: /back of your national identity card/ }),
  ).toBeVisible();
  await expect(page.getByText("Back of ID", { exact: true })).toBeVisible();
  expect(requests.uploadIntents.map((input) => input.artefact)).toEqual([
    "idenqa.artefact.document_front",
  ]);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
  ).toBeLessThanOrEqual(0);
  await page.reload();
  await loadCaptureFlow(page, "synthetic-document-selection", ["idenqa.method.live_camera"], {
    autoCapture: false,
  });
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Resume Capture" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await expect(
    page.getByRole("heading", { name: /back of your national identity card/ }),
  ).toBeVisible();
  expect(requests.uploadBodies).toHaveLength(1);
});

test("auto-captures a stable document, corrects perspective, and uploads the corrected frame", async ({
  page,
}) => {
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front"],
    },
  });
  await installSyntheticDocumentCamera(page);
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-document-auto-token", ["idenqa.method.live_camera"], {
    autoCaptureDelayMs: 1000,
    observationIntervalMs: 120,
    gate: { requiredStableFrames: 3 },
  });
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByRole("button", { name: "Start Camera" }).click();

  await expect(
    page.getByLabel("Live camera preview for Front of Your Identity Document"),
  ).toBeVisible();
  await expect(page.locator("idenqa-capture .document-guide")).toBeVisible();
  await expect(page.getByText("Automatic document capture is on")).toBeVisible();

  await expect(page.getByText("Hold the camera steady.")).toBeVisible();
  await page.waitForTimeout(1200);
  await expect(
    page.getByAltText("Captured Preview of Front of Your Identity Document"),
  ).toHaveCount(0);
  expect(requests.uploadBodies).toHaveLength(0);

  await page.evaluate(() => {
    (
      globalThis as typeof globalThis & { __setDocumentMotion?: (value: number) => void }
    ).__setDocumentMotion?.(0);
  });
  await expect(page.getByText("Document found. Hold still while the photo is taken.")).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    page.getByAltText("Captured Preview of Front of Your Identity Document"),
  ).toBeVisible({ timeout: 10_000 });

  const preview = page.getByAltText("Captured Preview of Front of Your Identity Document");
  const dimensions = await preview.evaluate((image) => ({
    width: Number(image.getAttribute("width")),
    height: Number(image.getAttribute("height")),
    naturalWidth: (image as HTMLImageElement).naturalWidth,
  }));
  expect(dimensions.width).toBeLessThan(640);
  expect(dimensions.height).toBeLessThan(480);
  expect(dimensions.width / dimensions.height).toBeGreaterThan(1.2);
  expect(dimensions.width / dimensions.height).toBeLessThan(1.9);
  expect(dimensions.naturalWidth).toBe(dimensions.width);

  await page.getByRole("button", { name: "Use Photo" }).click();
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadIntents[0]).toMatchObject({
    requirement_key: "identity_document",
    artefact: "idenqa.artefact.document_front",
    acquisition_method: "idenqa.method.live_camera",
  });
  expect(requests.uploadBodies[0]).toMatch(/^ffd8ff/);
  expect(jpegDimensions(requests.uploadBodies[0]!)).toEqual({
    width: dimensions.width,
    height: dimensions.height,
  });
});

test("retakes a document photo and replaces the corrected artefact", async ({ page }) => {
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front"],
    },
  });
  await installSyntheticDocumentCamera(page, { motion: false });
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-document-retake-token", ["idenqa.method.live_camera"], {
    observationIntervalMs: 120,
    gate: { requiredStableFrames: 3 },
  });
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByRole("button", { name: "Start Camera" }).click();

  const preview = page.getByAltText("Captured Preview of Front of Your Identity Document");
  await expect(preview).toBeVisible({ timeout: 10_000 });
  const firstPreview = await preview.getAttribute("src");
  await page.getByRole("button", { name: "Retake Photo" }).click();
  await expect(preview).toBeVisible({ timeout: 10_000 });
  const secondPreview = await preview.getAttribute("src");
  expect(secondPreview).not.toBe(firstPreview);

  await page.getByRole("button", { name: "Use Photo" }).click();
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadBodies).toHaveLength(1);
  expect(requests.uploadBodies[0]).toMatch(/^ffd8ff/);
});

test("never auto-captures without a document and keeps the manual shutter working", async ({
  page,
}) => {
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: ["idenqa.method.live_camera"],
    requirement: {
      key: "identity_document",
      evidenceType: "idenqa.evidence.document_image",
      artefacts: ["idenqa.artefact.document_front"],
    },
  });
  await installSyntheticDocumentCamera(page, { document: false });
  await page.goto("/");
  await loadCaptureFlow(page, "synthetic-document-manual-token", ["idenqa.method.live_camera"], {
    observationIntervalMs: 120,
    gate: { requiredStableFrames: 3 },
  });
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByRole("button", { name: "Start Camera" }).click();

  await expect(page.getByText("Point the camera at the document.")).toBeVisible();
  await page.waitForTimeout(1200);
  await expect(
    page.getByAltText("Captured Preview of Front of Your Identity Document"),
  ).toHaveCount(0);
  expect(requests.uploadBodies).toHaveLength(0);

  await page.getByRole("button", { name: "Capture Photo" }).click();
  const preview = page.getByAltText("Captured Preview of Front of Your Identity Document");
  await expect(preview).toBeVisible();
  const dimensions = await preview.evaluate((image) => ({
    width: Number(image.getAttribute("width")),
    height: Number(image.getAttribute("height")),
  }));
  expect(dimensions).toEqual({ width: 640, height: 480 });

  await page.getByRole("button", { name: "Use Photo" }).click();
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.uploadIntents).toHaveLength(1);
  expect(requests.uploadBodies[0]).toMatch(/^ffd8ff/);
  expect(jpegDimensions(requests.uploadBodies[0]!)).toEqual({ width: 640, height: 480 });
});

function jpegDimensions(hex: string): { readonly width: number; readonly height: number } {
  const bytes = Buffer.from(hex, "hex");
  let offset = 2;
  while (offset + 9 < bytes.length) {
    if (bytes[offset] !== 0xff) {
      offset += 1;
      continue;
    }
    const marker = bytes[offset + 1]!;
    if (marker === 0xd8 || marker === 0x01 || (marker >= 0xd0 && marker <= 0xd7)) {
      offset += 2;
      continue;
    }
    const length = bytes.readUInt16BE(offset + 2);
    const startOfFrame =
      (marker >= 0xc0 && marker <= 0xc3) ||
      (marker >= 0xc5 && marker <= 0xc7) ||
      (marker >= 0xc9 && marker <= 0xcb) ||
      (marker >= 0xcd && marker <= 0xcf);
    if (startOfFrame) {
      return { height: bytes.readUInt16BE(offset + 5), width: bytes.readUInt16BE(offset + 7) };
    }
    offset += 2 + length;
  }
  throw new Error("The captured JPEG does not expose a start-of-frame marker.");
}

async function installSyntheticDocumentCamera(
  page: import("@playwright/test").Page,
  options: { readonly document?: boolean; readonly motion?: boolean } = {},
): Promise<void> {
  await page.addInitScript(
    (input: { readonly document: boolean; readonly motion: boolean }) => {
      const canvas = document.createElement("canvas");
      canvas.width = 640;
      canvas.height = 480;
      const context = canvas.getContext("2d");
      const state = { motion: input.motion ? 1 : 0, frame: 0 };
      Object.defineProperty(globalThis, "__setDocumentMotion", {
        configurable: true,
        value: (value: number) => {
          state.motion = value;
        },
      });
      const draw = () => {
        if (context === null) return;
        state.frame += 1;
        if (!input.document) {
          const gradient = context.createLinearGradient(0, 0, canvas.width, canvas.height);
          gradient.addColorStop(0, "#23282e");
          gradient.addColorStop(1, "#313841");
          context.fillStyle = gradient;
          context.fillRect(0, 0, canvas.width, canvas.height);
          return;
        }
        context.fillStyle = "#1b2026";
        context.fillRect(0, 0, canvas.width, canvas.height);
        const phase = state.motion === 0 ? 0 : ((state.frame * 20) % 140) - 70;
        const corners = [
          { x: 90 + phase, y: 110 },
          { x: 552 + phase, y: 96 },
          { x: 566 + phase, y: 392 },
          { x: 74 + phase, y: 376 },
        ];
        context.beginPath();
        context.moveTo(corners[0]!.x, corners[0]!.y);
        context.lineTo(corners[1]!.x, corners[1]!.y);
        context.lineTo(corners[2]!.x, corners[2]!.y);
        context.lineTo(corners[3]!.x, corners[3]!.y);
        context.closePath();
        context.fillStyle = "#f5f1e6";
        context.fill();
        context.save();
        context.clip();
        context.fillStyle = "#2f5d50";
        context.fillRect(110 + phase, 140, 430, 52);
        context.fillStyle = "#9aa1a9";
        for (let line = 0; line < 5; line += 1) {
          context.fillRect(110 + phase, 230 + line * 28, 380 - line * 30, 10);
        }
        context.fillStyle = "#3c4752";
        context.fillRect(120 + ((state.frame * 3) % 40), 396, 18, 18);
        context.restore();
        context.lineWidth = 8;
        context.strokeStyle = "#0f1419";
        context.stroke();
      };
      const mediaDevices = navigator.mediaDevices;
      Object.defineProperty(navigator, "mediaDevices", {
        configurable: true,
        value: {
          getUserMedia: async () => canvas.captureStream(12),
          enumerateDevices: mediaDevices?.enumerateDevices?.bind(mediaDevices) ?? (async () => []),
          getSupportedConstraints:
            mediaDevices?.getSupportedConstraints?.bind(mediaDevices) ?? (() => ({})),
        },
      });
      draw();
      setInterval(draw, 40);
    },
    { document: options.document ?? true, motion: options.motion ?? true },
  );
}

test("runs ordered active-liveness prompts and completes only after Core confirmation", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const method = "idenqa.method.live_camera";
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: [method],
  });
  await page.goto("/");
  await loadCaptureFlowWithSyntheticAdapter(page, "synthetic-liveness-token", method, "liveness");
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();

  await expect(
    page.getByText("Center your face in the camera, then follow 3 quick prompts."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to Capture" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Start Liveness Check" })).toHaveCount(1);
  const animatedHead = page.locator("idenqa-capture").locator(".liveness-head");
  await expect(animatedHead).toHaveCSS("animation-name", "idq-liveness-head-demo");
  await page.locator("idenqa-capture").evaluate((element) => {
    element.style.setProperty("--idq-capture-liveness-duration", "4s");
  });
  await expect(animatedHead).toHaveCSS("animation-duration", "4s");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect(animatedHead).toHaveCSS("animation-name", "none");
  await expect(page.locator("idenqa-capture")).toHaveScreenshot(
    "guided-liveness-preparation-mobile-light.png",
    { animations: "disabled", maxDiffPixels: 20 },
  );
  await page.getByRole("button", { name: "Start Liveness Check" }).click();

  const progress = page.getByRole("progressbar", { name: "Liveness challenge progress" });
  await expect(page.getByText("Look Straight at the Camera")).toBeVisible();
  await expect(progress).toHaveJSProperty("value", 0);
  await advanceSyntheticAdapter(page);
  await expect(page.getByText("Slowly Turn Your Head Left")).toBeVisible();
  await expect(progress).toHaveJSProperty("value", 1);
  await advanceSyntheticAdapter(page);
  await expect(page.getByText("Slowly Turn Your Head Right")).toBeVisible();
  await expect(progress).toHaveJSProperty("value", 2);

  expect(requests.adapterCompletions).toHaveLength(0);
  await advanceSyntheticAdapter(page);
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.adapterCompletions).toEqual([
    expect.objectContaining({
      requirementKey: "selfie",
      evidenceType: "idenqa.evidence.selfie_image",
      artefact: "idenqa.artefact.selfie_image",
      acquisitionMethod: method,
    }),
  ]);
});

test("cancels an active-liveness adapter without completing or activating fallback", async ({
  page,
}) => {
  const method = "idenqa.method.live_camera";
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: [method],
    fallbacks: [
      {
        on: ["capture_failed"],
        acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
      },
    ],
  });
  await page.goto("/");
  await loadCaptureFlowWithSyntheticAdapter(
    page,
    "synthetic-liveness-cancel-token",
    method,
    "liveness",
  );
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();
  await page.getByRole("button", { name: "Start Liveness Check" }).click();
  await expect(page.getByText("Look Straight at the Camera")).toBeVisible();

  await page.getByRole("button", { name: "Cancel Liveness Check" }).click();

  await expect(page.getByRole("button", { name: "Start Liveness Check" })).toBeVisible();
  await expect(page.getByText("Look Straight at the Camera")).toHaveCount(0);
  await expect(page.getByText(/attempt failed/i)).toHaveCount(0);
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
  expect(requests.adapterCompletions).toHaveLength(0);
});

test("supports an arbitrary namespaced acquisition adapter with safe host-provided copy", async ({
  page,
}) => {
  const method = "com.example.method.secure_nfc";
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: [method],
  });
  await page.goto("/");
  await loadCaptureFlowWithSyntheticAdapter(page, "synthetic-nfc-token", method, "extension");
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();

  await expect(page.getByText("Hold your passport near this device.")).toBeVisible();
  await page.getByRole("button", { name: "Read Passport Chip" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Secure NFC is ready." })).toBeVisible();
  expect(requests.adapterCompletions).toHaveLength(0);

  await advanceSyntheticAdapter(page);
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.adapterCompletions[0]).toMatchObject({ acquisitionMethod: method });
});

test("does not complete when an adapter returns before Core confirms, then permits retry", async ({
  page,
}) => {
  const method = "com.example.method.secure_nfc";
  const requests = await mockCaptureFlow(page, {
    consentRequired: false,
    primaryMethods: [method],
  });
  await page.goto("/");
  await loadCaptureFlowWithSyntheticAdapter(page, "synthetic-unconfirmed-token", method, "retry");
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Acknowledge & Continue" }).click();

  const start = page.getByRole("button", { name: "Read Passport Chip" });
  await start.click();
  await advanceSyntheticAdapter(page);
  await expect(
    page.getByRole("alert").filter({ hasText: "not confirmed by the verification service" }),
  ).toBeVisible();
  expect(requests.adapterCompletions).toHaveLength(0);

  await start.click();
  await advanceSyntheticAdapter(page);
  await expect(page.getByRole("heading", { name: "Verifying Your Identity" })).toBeVisible();
  expect(requests.adapterCompletions).toHaveLength(1);
});

test("fails closed when a planned extension method has no registered adapter", async ({ page }) => {
  const method = "com.example.method.secure_nfc";
  await mockCaptureFlow(page, { consentRequired: false, primaryMethods: [method] });
  await page.goto("/");

  await expect(loadCaptureFlow(page, "synthetic-missing-adapter-token", [method])).rejects.toThrow(
    `No acquisition adapter is registered for ${method}.`,
  );
  await expect(
    page.getByRole("heading", { name: "We Couldn’t Open This Verification" }),
  ).toBeVisible();
});

test("runs in a React host with locale fallback and composed completion events", async ({
  page,
}) => {
  await mockCaptureFlow(page, { consentRequired: false, noticeLocale: "fr-CA" });
  await page.goto("/react.html");
  await page.waitForFunction(() => typeof window.startIdenqaReactDemo === "function");
  await page.evaluate(() =>
    window.startIdenqaReactDemo({
      captureToken: "synthetic-react-host-token",
      outcomeToken: "synthetic-react-host-outcome-token",
      messageCatalogue: {
        fr: {
          secureCapture: "Capture sécurisée",
          acknowledgeAndContinue: "Reconnaître et continuer",
          chooseFile: "Choisir un fichier",
          captureComplete: "Collecte des preuves terminée.",
          processingTitle: "Vérification de votre identité",
        },
        "fr-CA": { secureCapture: "Capture sécurisée canadienne" },
      },
    }),
  );

  const shell = page.locator("idenqa-capture").locator("section.shell");
  await expect(shell).toHaveAttribute("lang", "fr-CA");
  await expect(shell).toHaveAttribute("dir", "ltr");
  await expect(page.getByText("Capture sécurisée canadienne", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("heading", { level: 2, name: "Identity Verification" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Reconnaître et continuer" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByLabel("Choisir un fichier").setInputFiles(pngFile("react.png"));
  await page.getByRole("button", { name: "Use This File" }).click();

  await expect(page.getByRole("heading", { name: "Vérification de votre identité" })).toBeVisible();
  await expect(page.locator("#react-status")).toHaveText("React host observed completion: 1/1.");
});

for (const outcome of [
  { state: "processing", title: "Verifying Your Identity" },
  { state: "action_required", title: "Check the Next Step" },
  { state: "verified", title: "Identity Verified" },
  { state: "not_verified", title: "We Couldn’t Verify Your Identity" },
  { state: "inconclusive", title: "We Couldn’t Complete the Verification" },
  { state: "cancelled", title: "Capture Cancelled" },
  { state: "expired", title: "This Verification Has Expired" },
  { state: "failed", title: "We Couldn’t Complete the Verification" },
] as const) {
  test(`renders authoritative ${outcome.state} without exposing capture controls`, async ({
    page,
  }) => {
    await mockCaptureFlow(page, { consentRequired: false, outcomeState: outcome.state });
    await page.goto("/");
    await loadCaptureFlow(page, `synthetic-${outcome.state}-token`);

    await expect(page.getByRole("heading", { name: outcome.title })).toBeVisible();
    await expect(page.getByRole("button", { name: "Get Started" })).toHaveCount(0);
    await expect(page.getByLabel("Choose File")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Finish Capture" })).toHaveCount(0);
  });
}

async function startCaptureFlow(
  page: Parameters<typeof mockCaptureFlow>[0],
  captureToken: string,
  methods: readonly string[] = ["idenqa.method.file_upload"],
) {
  await loadCaptureFlow(page, captureToken, methods);
  await expect(page.getByRole("heading", { name: "Let’s Verify Your Identity" })).toBeVisible();
  await page.getByRole("button", { name: "Get Started" }).click();
}

async function loadCaptureFlow(
  page: Parameters<typeof mockCaptureFlow>[0],
  captureToken: string,
  methods: readonly string[] = ["idenqa.method.file_upload"],
  documentCapture?: CaptureDocumentCaptureOptions,
) {
  await page.locator("idenqa-capture").evaluate(
    (element, input) =>
      (element as IdenqaCaptureElement).start({
        baseUrl: new URL("/core/", location.href),
        captureToken: input.token,
        outcomeToken: `${input.token}-outcome`,
        capabilities: {
          supportedMethods: [...input.selectedMethods],
          availableMethods: [...input.selectedMethods],
        },
        ...(input.documentCapture === undefined ? {} : { documentCapture: input.documentCapture }),
      }),
    { token: captureToken, selectedMethods: methods, documentCapture },
  );
}

async function loadCaptureFlowWithSyntheticAdapter(
  page: Parameters<typeof mockCaptureFlow>[0],
  captureToken: string,
  method: string,
  variant: "liveness" | "extension" | "retry",
) {
  await page.locator("idenqa-capture").evaluate(
    (element, input) => {
      const testState = globalThis as typeof globalThis & {
        advanceCaptureAdapter?: () => void;
      };
      const waitForAdvance = (signal: AbortSignal) =>
        new Promise<void>((resolve, reject) => {
          const abort = () => {
            delete testState.advanceCaptureAdapter;
            reject(signal.reason);
          };
          testState.advanceCaptureAdapter = () => {
            signal.removeEventListener("abort", abort);
            resolve();
          };
          signal.addEventListener("abort", abort, { once: true });
        });
      let attempts = 0;
      return (element as IdenqaCaptureElement).start({
        baseUrl: new URL("/core/", location.href),
        captureToken: input.token,
        outcomeToken: `${input.token}-outcome`,
        capabilities: {
          supportedMethods: [input.method],
          availableMethods: [input.method],
        },
        methodAdapters: [
          {
            method: input.method,
            ...(input.variant === "liveness" ? { presentation: "active_liveness" as const } : {}),
            copy:
              input.variant === "liveness"
                ? {
                    label: "Liveness Check",
                    action: "Start Liveness Check",
                    description: "Follow a short series of camera prompts",
                    title: "Let’s Make Sure You’re You",
                    preparation: "Center your face in the camera, then follow 3 quick prompts.",
                    instruction: "Keep your face in view while you follow each prompt.",
                    tips: [
                      "Usually takes only a few seconds.",
                      "Photos are captured automatically.",
                      "Use bright, even lighting and remove anything covering your face.",
                    ],
                  }
                : {
                    label: "Secure NFC",
                    action: "Read Passport Chip",
                    description: "Read the secure chip in your passport",
                    preparation: "Hold your passport near this device.",
                    instruction: "Keep the passport still while its chip is read.",
                  },
            async acquire(context, controls) {
              attempts += 1;
              if (input.variant === "liveness") {
                for (const [index, prompt] of ["neutral", "turn_left", "turn_right"].entries()) {
                  controls.update({
                    phase: "challenge",
                    current: index + 1,
                    total: 3,
                    prompt: prompt as "neutral" | "turn_left" | "turn_right",
                  });
                  await waitForAdvance(context.signal);
                }
              } else {
                controls.update({ phase: "ready" });
                await waitForAdvance(context.signal);
                if (input.variant === "retry" && attempts === 1) return;
              }
              controls.update({ phase: "submitting" });
              const response = await fetch("/__capture_adapter_complete", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  requirement_key: context.requirementKey,
                  evidence_type: context.evidenceType,
                  artefact: context.artefact,
                  acquisition_method: context.acquisitionMethod,
                  fallback_condition: context.fallbackCondition,
                }),
                signal: context.signal,
              });
              if (!response.ok) throw new Error("Synthetic adapter submission failed.");
            },
          },
        ],
      });
    },
    { token: captureToken, method, variant },
  );
}

async function advanceSyntheticAdapter(page: Parameters<typeof mockCaptureFlow>[0]) {
  await page.evaluate(() => {
    const state = globalThis as typeof globalThis & { advanceCaptureAdapter?: () => void };
    const advance = state.advanceCaptureAdapter;
    delete state.advanceCaptureAdapter;
    if (advance === undefined) throw new Error("Synthetic acquisition adapter is not waiting.");
    advance();
  });
}

async function mockCaptureFlow(
  page: import("@playwright/test").Page,
  options: {
    readonly consentRequired: boolean;
    readonly noticeLocale?: string;
    readonly primaryMethods?: readonly string[];
    readonly fallbacks?: readonly Record<string, unknown>[];
    readonly requirement?: {
      readonly key: string;
      readonly evidenceType: string;
      readonly artefacts: readonly string[];
      readonly documentOptions?: readonly {
        readonly id: string;
        readonly label: string;
        readonly artefacts: readonly string[];
      }[];
    };
    readonly outcomeState?:
      | "capture_required"
      | "processing"
      | "action_required"
      | "verified"
      | "not_verified"
      | "inconclusive"
      | "cancelled"
      | "expired"
      | "failed";
  },
) {
  const observed: {
    readonly authorization: string[];
    readonly responses: Array<{
      readonly action: "acknowledge" | "consent" | "refuse";
      readonly locale: string;
      readonly rendered_experience_version: string;
    }>;
    readonly uploadIntents: Record<string, unknown>[];
    readonly uploadBodies: string[];
    readonly uploadHeaders: Array<{
      readonly contentType: string;
      readonly contentDigest: string;
      readonly ifMatch: string;
    }>;
    readonly adapterCompletions: BrowserUploadBinding[];
  } = {
    authorization: [],
    responses: [],
    uploadIntents: [],
    uploadBodies: [],
    uploadHeaders: [],
    adapterCompletions: [],
  };
  const uploadBindings = new Map<string, BrowserUploadBinding>();
  const acceptedUploads = new Set<string>();
  const documentSelections: Record<string, string> = {};
  let sessionVersion = 1;
  await page.route("**/core/v1/capture/document-selection", async (route) => {
    const input = route.request().postDataJSON();
    expect(input.expected_version).toBe(sessionVersion);
    expect(
      options.requirement?.documentOptions?.some((option) => option.id === input.document_type),
    ).toBe(true);
    documentSelections[input.requirement_key] = input.document_type;
    sessionVersion++;
    await route.fulfill({
      json: {
        ...captureSessionResponse(options),
        version: sessionVersion,
        document_selections: documentSelections,
      },
      headers: { "X-Request-ID": "req_document_selection" },
    });
  });
  await page.route("**/core/v1/capture/outcome", async (route) => {
    observed.authorization.push(route.request().headers().authorization ?? "");
    await route.fulfill({
      json: {
        verification_id: sessionResponse.id,
        state: options.outcomeState ?? "capture_required",
        session_version: options.outcomeState === undefined ? 1 : 4,
        updated_at: "2026-08-30T00:00:04Z",
      },
      headers: { "X-Request-ID": "req_outcome" },
    });
  });
  await page.route("**/core/v1/capture/session", async (route) => {
    observed.authorization.push(route.request().headers().authorization ?? "");
    await route.fulfill({
      json: {
        ...captureSessionResponse(options),
        version: sessionVersion,
        document_selections: documentSelections,
      },
      headers: { "X-Request-ID": "req_session" },
    });
  });
  await page.route("**/core/v1/capture/authority", async (route) => {
    observed.authorization.push(route.request().headers().authorization ?? "");
    await route.fulfill({
      json: {
        ...authorityResponse(options),
        ...(observed.responses.length === 0
          ? {}
          : {
              latest_response: subjectResponse(
                observed.responses.at(-1)!.action,
                observed.responses.at(-1)!.locale,
              ),
            }),
      },
      headers: { "X-Request-ID": "req_authority" },
    });
  });
  await page.route("**/core/v1/capture/progress", async (route) => {
    observed.authorization.push(route.request().headers().authorization ?? "");
    await route.fulfill({
      json: {
        verification_id: sessionResponse.id,
        completions: [
          ...[...acceptedUploads].map((uploadId) => uploadBindings.get(uploadId)!),
          ...observed.adapterCompletions,
        ].map((binding) => ({
          upload_id: binding.uploadId,
          evidence_id: binding.evidenceId,
          requirement_key: binding.requirementKey,
          evidence_type: binding.evidenceType,
          artefact: binding.artefact,
          acquisition_method: binding.acquisitionMethod,
          ...(binding.fallbackCondition === undefined
            ? {}
            : { fallback_condition: binding.fallbackCondition }),
        })),
      },
      headers: { "X-Request-ID": "req_progress" },
    });
  });
  await page.route("**/core/v1/capture/authority/responses", async (route) => {
    const request = route.request();
    expect(request.headers()["idempotency-key"]).toMatch(/^"capture_notice_[^"]+"$/);
    const input = request.postDataJSON() as {
      readonly action: "acknowledge" | "consent" | "refuse";
      readonly locale: string;
      readonly rendered_experience_version: string;
    };
    observed.responses.push(input);
    await route.fulfill({
      status: 201,
      json: subjectResponse(input.action, input.locale),
      headers: { "X-Request-ID": "req_response" },
    });
  });
  await page.route("**/__capture_adapter_complete", async (route) => {
    const input = route.request().postDataJSON() as Record<string, unknown>;
    const ordinal = observed.adapterCompletions.length + 1;
    observed.adapterCompletions.push({
      uploadId: `upl_01M11HEQG0000000000000009${ordinal}`,
      evidenceId: `evd_01M11HEQG0000000000000009${ordinal}`,
      requirementKey: String(input.requirement_key),
      evidenceType: String(input.evidence_type),
      artefact: String(input.artefact),
      acquisitionMethod: String(input.acquisition_method),
      ...(input.fallback_condition === undefined
        ? {}
        : { fallbackCondition: String(input.fallback_condition) }),
      expectedBytes: 0,
      mediaType: "application/octet-stream",
      region: "idenqa.region.synthetic",
    });
    await route.fulfill({ status: 204, body: "" });
  });
  await page.route("**/core/v1/evidence-uploads", async (route) => {
    const input = route.request().postDataJSON() as Record<string, unknown>;
    observed.uploadIntents.push(input);
    const binding = uploadBinding(
      input,
      observed.uploadIntents.length,
      options.requirement?.evidenceType ?? "idenqa.evidence.selfie_image",
    );
    uploadBindings.set(binding.uploadId, binding);
    await route.fulfill({
      status: 201,
      json: evidenceUpload("issued", 1, binding),
      headers: { "X-Request-ID": "req_upload_issue", ETag: '"1"' },
    });
  });
  await page.route("**/core/v1/evidence-uploads/*", async (route) => {
    const request = route.request();
    const uploadId = new URL(request.url()).pathname.split("/").at(-1) ?? "";
    const binding = uploadBindings.get(uploadId);
    expect(binding).toBeDefined();
    const body = request.postDataBuffer();
    observed.uploadBodies.push(body?.toString("hex") ?? "");
    observed.uploadHeaders.push({
      contentType: request.headers()["content-type"] ?? "",
      contentDigest: request.headers()["content-digest"] ?? "",
      ifMatch: request.headers()["if-match"] ?? "",
    });
    acceptedUploads.add(uploadId);
    await route.fulfill({
      status: 200,
      json: evidenceUpload("accepted", 3, binding!),
      headers: { "X-Request-ID": "req_upload_accept", ETag: '"3"' },
    });
  });
  return observed;
}

const sessionResponse = {
  id: "ver_01M11HEQG00000000000000000",
  state: "collecting",
  version: 1,
  profile_id: "prf_01M11HEQG00000000000000000",
  profile_revision: 1,
  profile_digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  requirements: {
    schema_version: 1,
    registry: {
      schema_version: 1,
      revision: 1,
      digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    },
    requirements: [
      {
        key: "selfie",
        purpose: "idenqa.purpose.identity_verification",
        evidence_type: "idenqa.evidence.selfie_image",
        artefacts: ["idenqa.artefact.selfie_image"],
        acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
        required_assurances: [],
        constraints: [],
        fallbacks: [],
      },
    ],
  },
  created_at: "2026-08-30T00:00:00Z",
  updated_at: "2026-08-30T00:00:00Z",
  expires_at: "2026-08-30T01:00:00Z",
};

function captureSessionResponse(options: {
  readonly primaryMethods?: readonly string[];
  readonly fallbacks?: readonly Record<string, unknown>[];
  readonly requirement?: {
    readonly key: string;
    readonly evidenceType: string;
    readonly artefacts: readonly string[];
    readonly documentOptions?: readonly {
      readonly id: string;
      readonly label: string;
      readonly artefacts: readonly string[];
    }[];
  };
}) {
  const requirement = sessionResponse.requirements.requirements[0]!;
  return {
    ...sessionResponse,
    requirements: {
      ...sessionResponse.requirements,
      requirements: [
        {
          ...requirement,
          key: options.requirement?.key ?? requirement.key,
          evidence_type: options.requirement?.evidenceType ?? requirement.evidence_type,
          artefacts: options.requirement?.artefacts ?? requirement.artefacts,
          ...(options.requirement?.documentOptions === undefined
            ? {}
            : { document_options: options.requirement.documentOptions }),
          acquisition: {
            strategy: "any_of",
            methods: options.primaryMethods ?? ["idenqa.method.file_upload"],
          },
          fallbacks: options.fallbacks ?? [],
        },
      ],
    },
  };
}

function authorityResponse(options: {
  readonly consentRequired: boolean;
  readonly noticeLocale?: string;
  readonly requirement?: { readonly evidenceType: string };
}) {
  return {
    authority: {
      id: "aut_01M11HEQG00000000000000000",
      subject_id: "sub_01M11HEQG00000000000000000",
      verification_id: sessionResponse.id,
      notice_id: "ntc_01M11HEQG00000000000000000",
      category: "tenant.authority.customer_declared",
      purpose: "idenqa.purpose.identity_verification",
      jurisdiction: "tenant.jurisdiction.synthetic",
      policy_pack: "tenant.policy.synthetic_v1",
      consent_required: options.consentRequired,
      requirement_purposes: ["idenqa.purpose.identity_verification"],
      evidence_types: [options.requirement?.evidenceType ?? "idenqa.evidence.selfie_image"],
      recipient_reference: "tenant.recipient.primary",
      recipient_display_name: "Example Recipient",
      regions: ["idenqa.region.synthetic"],
      retention_reference: "tenant.retention.synthetic_v1",
      state: "active",
      version: 1,
      valid_from: "2026-08-30T00:00:00Z",
      expires_at: "2026-08-30T01:00:00Z",
      created_at: "2026-08-30T00:00:00Z",
      updated_at: "2026-08-30T00:00:00Z",
    },
    notice: {
      id: "ntc_01M11HEQG00000000000000000",
      key: "tenant.notice.identity_verification",
      locale: options.noticeLocale ?? "en",
      controller: "Example Controller",
      recipient: "Example Recipient",
      copy: {
        title: "Identity Verification Notice",
        summary: "We need to verify your identity.",
        purpose: "Your evidence is used only for identity verification.",
        consequences: "You may refuse and collection will not continue.",
      },
      effective_at: "2026-08-30T00:00:00Z",
      created_at: "2026-08-30T00:00:00Z",
      digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
    },
  };
}

function subjectResponse(action: "acknowledge" | "consent" | "refuse", locale = "en") {
  return {
    id: "ack_01M11HEQG00000000000000000",
    authority_id: "aut_01M11HEQG00000000000000000",
    notice_id: "ntc_01M11HEQG00000000000000000",
    subject_id: "sub_01M11HEQG00000000000000000",
    verification_id: sessionResponse.id,
    action,
    locale,
    rendered_experience_version: "idenqa.capture.web.v1",
    recorded_at: "2026-08-30T00:00:01Z",
  };
}

function evidenceUpload(
  state: "issued" | "accepted",
  version: number,
  binding: BrowserUploadBinding,
) {
  return {
    id: binding.uploadId,
    evidence_id: binding.evidenceId,
    state,
    version,
    attempt: state === "issued" ? 0 : 1,
    requirement_key: binding.requirementKey,
    evidence_type: binding.evidenceType,
    artefact: binding.artefact,
    acquisition_method: binding.acquisitionMethod,
    ...(binding.fallbackCondition === undefined
      ? {}
      : { fallback_condition: binding.fallbackCondition }),
    assurances: [],
    allowed_media_types: [binding.mediaType],
    maximum_bytes: 16777216,
    expected_bytes: binding.expectedBytes,
    media_type: binding.mediaType,
    region: binding.region,
    created_at: "2026-08-30T00:00:00Z",
    updated_at: "2026-08-30T00:00:01Z",
    expires_at: "2026-08-30T00:15:00Z",
    ...(state === "accepted" ? { accepted_at: "2026-08-30T00:00:01Z" } : {}),
  };
}

interface BrowserUploadBinding {
  readonly uploadId: string;
  readonly evidenceId: string;
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
  readonly fallbackCondition?: string;
  readonly expectedBytes: number;
  readonly mediaType: string;
  readonly region: string;
}

function uploadBinding(
  input: Record<string, unknown>,
  ordinal: number,
  evidenceType: string,
): BrowserUploadBinding {
  const suffix = String(ordinal);
  return {
    uploadId: `upl_01M11HEQG0000000000000000${suffix}`,
    evidenceId: `evd_01M11HEQG0000000000000000${suffix}`,
    requirementKey: String(input.requirement_key),
    evidenceType,
    artefact: String(input.artefact),
    acquisitionMethod: String(input.acquisition_method),
    ...(input.fallback_condition === undefined
      ? {}
      : { fallbackCondition: String(input.fallback_condition) }),
    expectedBytes: Number(input.expected_bytes),
    mediaType: String(input.media_type),
    region: String(input.region),
  };
}

function pngFile(name: string) {
  return {
    name,
    mimeType: "image/png",
    buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  };
}
