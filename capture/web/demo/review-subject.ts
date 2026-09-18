import {
  createRecaptureHandoff,
  defineIdenqaCapture,
  type IdenqaCaptureElement,
  type RecaptureHandoff,
} from "../src/index.js";

interface SubjectHandoff extends RecaptureHandoff {
  readonly baseUrl: string;
}

defineIdenqaCapture();

const params = new URLSearchParams(location.search);
const journey = params.get("journey");
const stage = params.get("stage");
const capture = document.querySelector<IdenqaCaptureElement>("idenqa-capture");
const status = document.getElementById("subject-status");
if (
  journey === null ||
  (stage !== "original" && stage !== "recapture") ||
  capture === null ||
  status === null
) {
  throw new Error("The subject handoff is incomplete.");
}

if (stage === "recapture") {
  text("subject-title", "A New Photo Is Needed");
  text(
    "subject-copy",
    "A reviewer needs a clearer selfie. Your earlier submission remains unchanged.",
  );
}

capture.addEventListener("idenqa-capture-complete", () => {
  status.textContent =
    stage === "recapture" ? "New capture sent for review." : "Capture sent for review.";
});

const obtain = async ({ signal }: { readonly signal: AbortSignal }): Promise<SubjectHandoff> => {
  const response = await fetch(
    `/__idenqa_demo/review/${encodeURIComponent(journey)}/handoff?stage=${stage}`,
    { method: "POST", signal },
  );
  if (!response.ok) throw new Error("This secure capture link is not available.");
  return (await response.json()) as SubjectHandoff;
};

const controller = new AbortController();
const options = {
  baseUrl: new URL("/core/", location.href),
  capabilities: {
    supportedMethods: ["idenqa.method.file_upload"],
    availableMethods: ["idenqa.method.file_upload"],
  },
};

if (stage === "recapture") {
  await createRecaptureHandoff(capture, options, obtain).start();
} else {
  const handoff = await obtain({ signal: controller.signal });
  await capture.start({
    ...options,
    captureToken: handoff.captureToken,
    outcomeToken: handoff.outcomeToken,
    expectedVerificationId: handoff.verificationId,
  });
}

function text(id: string, value: string) {
  const element = document.getElementById(id);
  if (element === null) throw new Error(`Missing subject fixture element: ${id}`);
  element.textContent = value;
}
