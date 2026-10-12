import { CaptureClient, type ExperienceResolution } from "@idenqa/sdk";

import {
  defineIdenqaCapture,
  type CaptureElementStartOptions,
  type IdenqaCaptureElement,
} from "../src/index.js";

interface HostedBootstrap {
  readonly baseUrl: string;
  readonly verificationId: string;
  readonly captureToken: string;
  readonly outcomeToken: string;
  readonly region: string;
}

defineIdenqaCapture();

const capture = document.querySelector<IdenqaCaptureElement>("idenqa-capture");
if (capture === null) throw new Error("The hosted Capture Web element is missing.");

void startHostedJourney(capture).catch(() => {
  capture.cancel();
  capture.hidden = true;
  const error = document.querySelector<HTMLElement>("#hosted-error");
  if (error !== null) error.hidden = false;
});

async function startHostedJourney(element: IdenqaCaptureElement): Promise<void> {
  const launchCredential = consumeLaunchCredential();
  const bootstrap = await requestHostedBootstrap(launchCredential);
  await element.start(await hostedStartOptions(bootstrap));
}

function consumeLaunchCredential(): string {
  const launch = new URLSearchParams(location.hash.slice(1));
  const credential = launch.get("launch");
  history.replaceState(history.state, "", `${location.pathname}${location.search}`);
  if (credential === null || credential.length < 16 || credential.length > 2_048) {
    throw new Error("A valid capture launch credential is required.");
  }
  return credential;
}

async function requestHostedBootstrap(launchCredential: string): Promise<HostedBootstrap> {
  const response = await fetch(new URL("/capture/bootstrap", location.href), {
    method: "POST",
    cache: "no-store",
    credentials: "same-origin",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ launchCredential }),
  });
  if (!response.ok) throw new Error("The hosted Capture Web journey could not be prepared.");
  return parseHostedBootstrap(await response.json());
}

function parseHostedBootstrap(value: unknown): HostedBootstrap {
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw new Error("The capture bootstrap response is invalid.");
  }
  const bootstrap = value as Record<string, unknown>;
  return {
    baseUrl: requiredString(bootstrap, "baseUrl"),
    verificationId: requiredString(bootstrap, "verificationId"),
    captureToken: requiredString(bootstrap, "captureToken"),
    outcomeToken: requiredString(bootstrap, "outcomeToken"),
    region: requiredString(bootstrap, "region"),
  };
}

function requiredString(value: Record<string, unknown>, name: string): string {
  const field = value[name];
  if (typeof field !== "string" || field.length === 0) {
    throw new Error(`The capture bootstrap response has no ${name}.`);
  }
  return field;
}

async function hostedStartOptions(bootstrap: HostedBootstrap): Promise<CaptureElementStartOptions> {
  const baseUrl = new URL(bootstrap.baseUrl, location.href);
  const client = new CaptureClient({ baseUrl, captureToken: bootstrap.captureToken });
  const experience = await resolvePinnedExperience(client);
  return {
    baseUrl,
    captureToken: bootstrap.captureToken,
    outcomeToken: bootstrap.outcomeToken,
    expectedVerificationId: bootstrap.verificationId,
    capabilities: browserCapabilities(),
    region: bootstrap.region,
    ...(experience === undefined ? {} : { experience }),
  };
}

async function resolvePinnedExperience(
  client: CaptureClient,
): Promise<ExperienceResolution | undefined> {
  try {
    return (await client.getExperience()).data;
  } catch {
    // Experience signing is optional for self-hosted Core. The component keeps
    // its package-owned accessible presentation when no resolution is exposed.
    return undefined;
  }
}

function browserCapabilities() {
  const fileUpload = "idenqa.method.file_upload";
  const liveCamera = "idenqa.method.live_camera";
  const cameraAvailable =
    globalThis.isSecureContext && typeof navigator.mediaDevices?.getUserMedia === "function";
  return {
    supportedMethods: [liveCamera, fileUpload],
    availableMethods: cameraAvailable ? [liveCamera, fileUpload] : [fileUpload],
  };
}
