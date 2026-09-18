interface BootstrapResponse {
  readonly journeyId: string;
  readonly originalSubjectUrl: string;
}

interface ReviewCaseResponse {
  readonly id: string;
  readonly state: string;
  readonly reason: string;
  readonly region: string;
  readonly certificate: string;
}

interface RecaptureResponse {
  readonly verificationId: string;
  readonly subjectUrl: string;
}

interface ChildResultResponse {
  readonly complete: boolean;
  readonly state: string;
  readonly outcome?: string;
  readonly followUpRequired?: boolean;
}

interface CompletionResponse {
  readonly state: string;
  readonly sessionVersion: number;
}

let journeyId: string | undefined;
let evidenceURL: string | undefined;

const begin = button("review-begin");
const findCase = button("review-find-case");
const reviewEvidence = button("review-evidence-button");
const requestRecapture = button("review-request-recapture");
const checkResult = button("review-check-result");
const complete = button("review-complete");

begin.addEventListener("click", () => run(begin, beginJourney));
findCase.addEventListener("click", () => run(findCase, loadCase));
reviewEvidence.addEventListener("click", () => run(reviewEvidence, loadEvidence));
requestRecapture.addEventListener("click", () => run(requestRecapture, createRecapture));
checkResult.addEventListener("click", () => run(checkResult, loadChildResult));
complete.addEventListener("click", () => run(complete, completeJourney));

async function beginJourney() {
  status("Preparing a live review journey…");
  const result = await request<BootstrapResponse>("bootstrap");
  journeyId = result.journeyId;
  const link = anchor("review-original-link");
  link.href = result.originalSubjectUrl;
  show("review-original");
  progress("capture");
  status("Waiting for original capture");
}

async function loadCase() {
  status("Checking the review queue…");
  const value = await request<ReviewCaseResponse>("case");
  text("review-case-state", title(value.state));
  text("review-case-reason", title(value.reason));
  text("review-case-region", value.region);
  text("review-case-certificate", value.certificate);
  show("review-case");
  progress("review");
  status("Review case ready");
}

async function loadEvidence() {
  status("Opening protected evidence…");
  const response = await fetch(endpoint("evidence"), { method: "POST" });
  if (!response.ok) throw new Error("The protected evidence could not be opened.");
  const blob = await response.blob();
  if (evidenceURL !== undefined) URL.revokeObjectURL(evidenceURL);
  evidenceURL = URL.createObjectURL(blob);
  image("review-evidence-image").src = evidenceURL;
  show("review-evidence-section");
  status("Protected evidence ready");
}

async function createRecapture() {
  status("Creating a fresh linked session…");
  const value = await request<RecaptureResponse>("recapture");
  anchor("review-recapture-link").href = value.subjectUrl;
  show("review-recapture");
  progress("recapture");
  status("Waiting for the new capture");
}

async function loadChildResult() {
  status("Checking the linked session…");
  const value = await request<ChildResultResponse>("result", true);
  if (!value.complete) {
    status(`New capture is ${title(value.state)}`);
    return;
  }
  text("review-child-outcome", title(value.outcome ?? "completed"));
  show("review-outcome");
  status("New capture needs follow-up");
}

async function completeJourney() {
  status("Recording follow-up and re-evaluating…");
  const value = await request<CompletionResponse>("complete");
  text("review-final-outcome", title(value.state));
  show("review-completed");
  progress("decision");
  status("Review complete");
  if (evidenceURL !== undefined) URL.revokeObjectURL(evidenceURL);
}

async function run(control: HTMLButtonElement, action: () => Promise<void>) {
  const error = element("review-error");
  error.hidden = true;
  control.disabled = true;
  try {
    await action();
  } catch (cause) {
    error.textContent =
      cause instanceof Error ? cause.message : "The review journey could not continue.";
    error.hidden = false;
    status("Action needs attention");
  } finally {
    control.disabled = false;
  }
}

async function request<T>(operation: string, acceptPending = false): Promise<T> {
  const response = await fetch(endpoint(operation), { method: "POST" });
  if (!response.ok && !(acceptPending && response.status === 202)) {
    throw new Error("The review journey could not continue. Please try again.");
  }
  return (await response.json()) as T;
}

function endpoint(operation: string) {
  if (operation === "bootstrap") return "/__idenqa_demo/review/bootstrap";
  if (journeyId === undefined) throw new Error("Begin the review journey first.");
  return `/__idenqa_demo/review/${encodeURIComponent(journeyId)}/${operation}`;
}

function show(id: string) {
  for (const section of document.querySelectorAll<HTMLElement>(".review-card > section")) {
    section.hidden = section.id !== id;
  }
}

function progress(active: string) {
  const order = ["capture", "review", "recapture", "decision"];
  const activeIndex = order.indexOf(active);
  for (const item of document.querySelectorAll<HTMLElement>("[data-progress]")) {
    const index = order.indexOf(item.dataset.progress ?? "");
    item.dataset.state = index < activeIndex ? "complete" : index === activeIndex ? "active" : "";
  }
}

function status(value: string) {
  text("review-live-status", value);
}

function title(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function element(id: string): HTMLElement {
  const value = document.getElementById(id);
  if (value === null) throw new Error(`Missing review fixture element: ${id}`);
  return value;
}

function button(id: string): HTMLButtonElement {
  return element(id) as HTMLButtonElement;
}

function anchor(id: string): HTMLAnchorElement {
  return element(id) as HTMLAnchorElement;
}

function image(id: string): HTMLImageElement {
  return element(id) as HTMLImageElement;
}

function text(id: string, value: string) {
  element(id).textContent = value;
}
