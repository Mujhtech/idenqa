import { expect, test } from "@playwright/test";

import type { IdenqaCaptureElement } from "../../src/index.js";

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

test("shows the exact notice and records explicit consent before revealing capture", async ({
  page,
}) => {
  const captureToken = "synthetic-capture-token-that-must-not-enter-the-dom";
  const requests = await mockCaptureFlow(page, { consentRequired: true });
  await page.goto("/");

  await startCaptureFlow(page, captureToken);
  expect(requests.authorization).toEqual([
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

  await expect(page.getByText("Consent recorded. You can continue with capture.")).toBeVisible();
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

  const input = page.getByLabel("Choose File");
  await expect(input).toHaveAttribute("accept", "image/jpeg,image/png");
  await input.setInputFiles({
    name: "private-subject-name.png",
    mimeType: "image/png",
    buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  });

  await expect(page.getByRole("status").filter({ hasText: "File accepted." })).toBeVisible();
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

  await expect(page.getByLabel("Choose File")).toBeAttached();
  await expect(page.getByRole("button", { name: "Start Camera" })).toBeVisible();
  await page.getByLabel("Choose File").setInputFiles(pngFile("choice.png"));

  await expect(
    page.getByRole("progressbar", { name: "Evidence capture progress" }),
  ).toHaveJSProperty("value", 1);
  await expect(page.getByText("Captured with File upload.")).toBeVisible();
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Start Camera" })).toHaveCount(0);
  await expect(
    page.getByText("Required evidence capture is complete. Verification may continue."),
  ).toBeVisible();
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
  await page.getByLabel("Choose File").setInputFiles(pngFile("first-load.png"));
  await expect(
    page.getByRole("progressbar", { name: "Evidence capture progress" }),
  ).toHaveJSProperty("value", 1);

  await startCaptureFlow(page, "synthetic-recovery-token");

  await expect(
    page.getByRole("progressbar", { name: "Evidence capture progress" }),
  ).toHaveJSProperty("value", 1);
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
  await expect(
    page.getByText("Required evidence capture is complete. Verification may continue."),
  ).toBeVisible();
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

  const progress = page.getByRole("progressbar", { name: "Evidence capture progress" });
  await expect(progress).toHaveJSProperty("max", 2);
  await expect(progress).toHaveJSProperty("value", 0);
  const inputs = page.getByLabel("Choose File");
  await expect(inputs).toHaveCount(2);

  await inputs.nth(0).setInputFiles(pngFile("front.png"));
  await expect(progress).toHaveJSProperty("value", 1);
  await expect(
    page.getByText("Required evidence capture is complete. Verification may continue."),
  ).toHaveCount(0);

  await inputs.nth(0).setInputFiles(pngFile("back.png"));
  await expect(progress).toHaveJSProperty("value", 2);
  await expect(
    page.getByText("Required evidence capture is complete. Verification may continue."),
  ).toBeVisible();
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

  await page.getByRole("button", { name: "Start Camera" }).click();
  await expect(page.getByLabel("Live camera preview for Selfie Image")).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Camera ready." })).toBeVisible();
  await page.getByRole("button", { name: "Capture Photo" }).click();
  await expect(page.getByAltText("Captured Selfie Image preview")).toBeVisible();
  await page.getByRole("button", { name: "Use Photo" }).click();

  await expect(
    page.getByRole("status").filter({ hasText: "Captured photo accepted." }),
  ).toBeVisible();
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
  await page.getByRole("button", { name: "Start Camera" }).click();

  await expect(
    page.getByText("The camera attempt failed. This policy-approved alternative is available."),
  ).toBeVisible();
  await expect(page.getByLabel("Choose File")).toBeAttached();
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
  await page.getByRole("button", { name: "Start Camera" }).click();
  await expect(page.getByLabel("Live camera preview for Selfie Image")).toBeVisible();
  await page.getByRole("button", { name: "Cancel Camera" }).click();

  await expect(page.getByRole("button", { name: "Start Camera" })).toBeVisible();
  await expect(page.getByText(/camera attempt failed/i)).toHaveCount(0);
  await expect(page.getByLabel("Choose File")).toHaveCount(0);
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
      messageCatalogue: {
        fr: {
          secureCapture: "Capture sécurisée",
          acknowledgeAndContinue: "Reconnaître et continuer",
          chooseFile: "Choisir un fichier",
          captureComplete: "Collecte des preuves terminée.",
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
  await page.getByRole("button", { name: "Reconnaître et continuer" }).click();
  await page.getByLabel("Choisir un fichier").setInputFiles(pngFile("react.png"));

  await expect(page.getByText("Collecte des preuves terminée.", { exact: true })).toBeVisible();
  await expect(page.locator("#react-status")).toHaveText("React host observed completion: 1/1.");
});

async function startCaptureFlow(
  page: Parameters<typeof mockCaptureFlow>[0],
  captureToken: string,
  methods: readonly string[] = ["idenqa.method.file_upload"],
) {
  await page.locator("idenqa-capture").evaluate(
    (element, input) =>
      (element as IdenqaCaptureElement).start({
        baseUrl: new URL("/core/", location.href),
        captureToken: input.token,
        capabilities: {
          supportedMethods: [...input.selectedMethods],
          availableMethods: [...input.selectedMethods],
        },
      }),
    { token: captureToken, selectedMethods: methods },
  );
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
    };
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
  } = { authorization: [], responses: [], uploadIntents: [], uploadBodies: [], uploadHeaders: [] };
  const uploadBindings = new Map<string, BrowserUploadBinding>();
  const acceptedUploads = new Set<string>();
  await page.route("**/core/v1/capture/session", async (route) => {
    observed.authorization.push(route.request().headers().authorization ?? "");
    await route.fulfill({
      json: captureSessionResponse(options),
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
        completions: [...acceptedUploads].map((uploadId) => {
          const binding = uploadBindings.get(uploadId)!;
          return {
            upload_id: binding.uploadId,
            evidence_id: binding.evidenceId,
            requirement_key: binding.requirementKey,
            evidence_type: binding.evidenceType,
            artefact: binding.artefact,
            acquisition_method: binding.acquisitionMethod,
            ...(binding.fallbackCondition === undefined
              ? {}
              : { fallback_condition: binding.fallbackCondition }),
          };
        }),
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
