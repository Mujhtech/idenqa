import { expect, test, type Page } from "@playwright/test";

const liveDemo = process.env.IDENQA_CAPTURE_LIVE_DEMO_URL === undefined ? test.skip : test;

test.describe.configure({ mode: "serial" });

for (const surface of ["hosted", "embedded"] as const) {
  liveDemo(
    `completes the ${surface} journey against a running self-hosted Core`,
    async ({ page }) => {
      await page.goto(`/${surface}.html`);

      await expect(page.getByRole("heading", { name: "Let’s Verify Your Identity" })).toBeVisible();
      await page.getByRole("button", { name: "Get Started" }).click();
      await expect(page.getByRole("heading", { name: "Review Before You Continue" })).toBeVisible();
      await page.getByRole("button", { name: "Agree & Continue" }).click();
      await page.getByRole("button", { name: "Use Another Method" }).click();
      await page.getByRole("button", { name: "Upload File" }).click();
      await page.getByRole("button", { name: "Continue to Capture" }).click();
      await page.getByLabel("Choose File").setInputFiles({
        name: "synthetic-selfie.png",
        mimeType: "image/png",
        buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
      });
      await page.getByRole("button", { name: "Use This File" }).click();
      await expect(page.getByRole("heading", { name: "Identity Verified" })).toBeVisible({
        timeout: 30_000,
      });
    },
  );
}

liveDemo("runs active-liveness capture through authoritative Core progress", async ({ page }) => {
  await page.goto("/hosted.html?method=active-liveness");

  await expect(page.getByRole("heading", { name: "Let’s Verify Your Identity" })).toBeVisible();
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Agree & Continue" }).click();
  await expect(page.getByRole("heading", { name: "Let’s Make Sure You’re You" })).toBeVisible();
  await page.getByRole("button", { name: "Start Liveness Check" }).click();

  await expect(page.locator("idenqa-capture").locator(".adapter-prompt")).toHaveText(
    "Look Straight at the Camera",
  );
  await expect(page.getByRole("heading", { name: "Identity Verified" })).toBeVisible({
    timeout: 30_000,
  });
});

liveDemo(
  "completes a composed review and linked-recapture journey against Core",
  async ({ context, page }, testInfo) => {
    testInfo.setTimeout(120_000);
    await page.goto("/review.html");
    await expect(
      page.getByRole("heading", { name: "Review and recapture, in one journey" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Begin Demonstration" }).click();
    await expect(
      page.getByRole("heading", { name: "Waiting for the original capture" }),
    ).toBeVisible({ timeout: 30_000 });

    const originalPagePromise = context.waitForEvent("page");
    await page.getByRole("link", { name: "Open Subject Capture" }).click();
    const original = await originalPagePromise;
    await completeReviewSubjectCapture(original, "Verifying Your Identity");

    await page.getByRole("button", { name: "Check Review Queue" }).click();
    await expect(page.getByRole("heading", { name: "A capture needs review" })).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByText("identity.review", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Review Evidence" }).click();
    await expect(page.getByAltText("Protected selfie evidence for review")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByRole("button", { name: "Request a New Capture" }).click();
    await expect(page.getByRole("heading", { name: "A new capture was requested" })).toBeVisible({
      timeout: 30_000,
    });

    const recapturePagePromise = context.waitForEvent("page");
    await page.getByRole("link", { name: "Open Recapture Journey" }).click();
    const recapture = await recapturePagePromise;
    await expect(recapture.getByRole("heading", { name: "A New Photo Is Needed" })).toBeVisible();
    await completeReviewSubjectCapture(recapture, "Identity Verified");

    await page.getByRole("button", { name: "Check New Capture" }).click();
    await expect(page.getByRole("heading", { name: "New capture received" })).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByText("Verified", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Acknowledge and Complete Review" }).click();
    await expect(page.getByRole("heading", { name: "Review completed" })).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator("#review-final-outcome")).toHaveText("Verified");
    await page.screenshot({ path: testInfo.outputPath("review-completed.png"), fullPage: true });
  },
);

liveDemo("renders a subject-cancelled Core session", async ({ page }) => {
  await page.goto("/hosted.html?outcome=cancelled");

  await expect(page.getByRole("heading", { name: "Capture Cancelled" })).toBeVisible({
    timeout: 15_000,
  });
  await expect(
    page.getByText("This verification was cancelled and cannot be continued."),
  ).toBeVisible();
});

liveDemo(
  "renders an expired Core session with the read-only outcome credential",
  async ({ page }) => {
    await page.goto("/hosted.html?outcome=expired");

    await expect(page.getByRole("heading", { name: "This Verification Has Expired" })).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.getByText("This capture link is no longer active.", { exact: false }),
    ).toBeVisible();
  },
);

for (const outcome of [
  {
    state: "not_verified",
    title: "We Couldn’t Verify Your Identity",
    body: "The verification finished without confirming your identity.",
  },
  {
    state: "inconclusive",
    title: "We Couldn’t Complete the Verification",
    body: "The verification did not reach a conclusive result.",
  },
  {
    state: "action_required",
    title: "Check the Next Step",
    body: "The organisation that requested this verification needs more information.",
  },
  {
    state: "failed",
    title: "We Couldn’t Complete the Verification",
    body: "A technical problem prevented this verification from completing.",
  },
] as const) {
  liveDemo(`renders the Core-authored ${outcome.state} outcome`, async ({ page }) => {
    await page.goto(`/hosted.html?outcome=${outcome.state}`);
    await completeFileCapture(page);

    await expect(page.getByRole("heading", { name: outcome.title })).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByText(outcome.body, { exact: false })).toBeVisible();
    await expect(page.getByLabel("Choose File")).toHaveCount(0);
  });
}

async function completeFileCapture(page: Page) {
  await expect(page.getByRole("heading", { name: "Let’s Verify Your Identity" })).toBeVisible();
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Agree & Continue" }).click();
  await page.getByRole("button", { name: "Use Another Method" }).click();
  await page.getByRole("button", { name: "Upload File" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  await page.getByLabel("Choose File").setInputFiles({
    name: "synthetic-selfie.png",
    mimeType: "image/png",
    buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  });
  await page.getByRole("button", { name: "Use This File" }).click();
}

async function completeReviewSubjectCapture(page: Page, terminalHeading: string) {
  await expect(page.getByRole("heading", { name: "Let’s Verify Your Identity" })).toBeVisible({
    timeout: 30_000,
  });
  await page.getByRole("button", { name: "Get Started" }).click();
  await page.getByRole("button", { name: "Agree & Continue" }).click();
  await page.getByRole("button", { name: "Continue to Capture" }).click();
  const encoded = await page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 320;
    canvas.height = 240;
    const context = canvas.getContext("2d");
    if (context === null) throw new Error("canvas is unavailable");
    context.fillStyle = "#d9e6e1";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.fillStyle = "#55756a";
    context.beginPath();
    context.arc(160, 92, 48, 0, Math.PI * 2);
    context.fill();
    context.beginPath();
    context.ellipse(160, 205, 90, 65, 0, Math.PI, Math.PI * 2);
    context.fill();
    const encoded = canvas.toDataURL("image/png").split(",")[1];
    if (encoded === undefined) throw new Error("canvas encoding failed");
    return encoded;
  });
  await page.getByLabel("Choose File").setInputFiles({
    name: "synthetic-subject.png",
    mimeType: "image/png",
    buffer: Buffer.from(encoded, "base64"),
  });
  await page.getByRole("button", { name: "Use This File" }).click();
  await expect(page.getByRole("heading", { name: terminalHeading })).toBeVisible({
    timeout: 30_000,
  });
}
