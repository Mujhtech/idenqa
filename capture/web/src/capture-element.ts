import { LitElement, css, html, nothing, type PropertyValues } from "lit";

import type { CaptureRealtimeEvent, SubjectResponseAction } from "@idenqa/sdk";
import type { EvidenceUpload } from "@idenqa/sdk";

import {
  createCaptureFlowController,
  type CaptureFlowController,
  type CaptureFlowSnapshot,
  type CaptureFlowStartOptions,
} from "./flow.js";
import type { CapturePlan, CapturePlanStep } from "./planner.js";
import type { CaptureRuntimeFallbackReason } from "./planner.js";
import {
  CaptureCameraError,
  LIVE_CAMERA_METHOD,
  cameraFacingMode,
  captureCameraFrame,
  startCamera,
  stopCamera,
} from "./camera.js";
import { FILE_UPLOAD_METHOD, fileUploadPolicy, formatBytes } from "./upload.js";
import {
  createCaptureLocalizer,
  type CaptureLocalizer,
  type CaptureMessageCatalogue,
  type CaptureMessageKey,
  type CaptureMessageValues,
} from "./localisation.js";

export const IDENQA_CAPTURE_TAG_NAME = "idenqa-capture";

export interface CaptureMethodSelectDetail {
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly method: string;
  readonly fallbackCondition?: CaptureRuntimeFallbackReason;
}

export interface CaptureSubjectResponseDetail {
  readonly action: SubjectResponseAction;
  readonly responseId: string;
  readonly authorityId: string;
  readonly noticeId: string;
}

export interface CaptureFlowErrorDetail {
  readonly code: "CAPTURE_FLOW_REQUEST_FAILED";
}

export interface CaptureRealtimeDetail {
  readonly event: CaptureRealtimeEvent;
}

export interface CaptureEvidenceAcceptedDetail {
  readonly uploadId: string;
  readonly evidenceId: string;
  readonly requirementKey: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
}

export interface CaptureProgressDetail {
  readonly verificationId: string;
  readonly completedSteps: number;
  readonly totalSteps: number;
}

export interface CaptureElementStartOptions extends CaptureFlowStartOptions {
  /** Optional translations for package-owned UI copy. The server notice is never overridden. */
  readonly messageCatalogue?: CaptureMessageCatalogue;
}

export interface CaptureCompleteDetail extends CaptureProgressDetail {
  readonly captureComplete: true;
}

interface StepUploadState {
  readonly status: "uploading" | "error" | "accepted";
  readonly body?: Blob;
  readonly message?: string;
}

interface StepCameraState {
  readonly status: "requesting" | "streaming" | "reviewing" | "uploading" | "error" | "accepted";
  readonly stream?: MediaStream;
  readonly ready?: boolean;
  readonly body?: Blob;
  readonly previewUrl?: string;
  readonly width?: number;
  readonly height?: number;
  readonly message?: string;
}

interface StepCompletion {
  readonly acquisitionMethod: string;
  readonly uploadId: string;
  readonly evidenceId: string;
}

type ComponentFlowState = "idle" | "loading" | "ready" | "responding" | "error" | "cancelled";

export class IdenqaCaptureElement extends LitElement {
  static override properties = {
    plan: { attribute: false },
  };

  static override styles = css`
    :host {
      --idq-capture-accent: #175cd3;
      --idq-capture-accent-strong: #004eaf;
      --idq-capture-background: #ffffff;
      --idq-capture-border: #d0d5dd;
      --idq-capture-muted: #475467;
      --idq-capture-surface: #f8fafc;
      --idq-capture-text: #101828;
      color: var(--idq-capture-text);
      display: block;
      font-family:
        Inter,
        ui-sans-serif,
        system-ui,
        -apple-system,
        BlinkMacSystemFont,
        "Segoe UI",
        sans-serif;
      line-height: 1.5;
    }

    * {
      box-sizing: border-box;
    }

    .shell {
      background: var(--idq-capture-background);
      border: 1px solid var(--idq-capture-border);
      border-radius: 1rem;
      margin-inline: auto;
      max-width: 44rem;
      overflow-wrap: anywhere;
      padding: clamp(1rem, 4vw, 2rem);
    }

    .eyebrow {
      color: var(--idq-capture-accent-strong);
      font-size: 0.75rem;
      font-weight: 700;
      letter-spacing: 0.08em;
      margin: 0 0 0.5rem;
      text-transform: uppercase;
    }

    h2,
    h3,
    h4,
    p {
      margin-block-start: 0;
    }

    h2 {
      font-size: clamp(1.5rem, 5vw, 2rem);
      line-height: 1.2;
      margin-block-end: 0.75rem;
      text-wrap: balance;
    }

    h3 {
      font-size: 1.125rem;
      line-height: 1.35;
      margin-block-end: 0.25rem;
      text-wrap: balance;
    }

    h4 {
      font-size: 1rem;
      margin-block-end: 0.75rem;
    }

    .intro,
    .requirement-copy,
    .notice-meta,
    .status {
      color: var(--idq-capture-muted);
    }

    .notice {
      background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: 0.75rem;
      margin-block-start: 1.5rem;
      padding: 1rem;
    }

    .notice h4 {
      margin-block: 1rem 0.25rem;
    }

    .notice-copy {
      white-space: pre-wrap;
    }

    .notice-meta {
      display: grid;
      font-size: 0.875rem;
      gap: 0.75rem;
      grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
      margin-block: 1.25rem 0;
    }

    .notice-meta div {
      min-width: 0;
    }

    .notice-meta dt {
      font-weight: 700;
    }

    .notice-meta dd {
      margin-inline-start: 0;
    }

    .notice-actions {
      display: grid;
      gap: 0.625rem;
      grid-template-columns: repeat(auto-fit, minmax(min(100%, 11rem), 1fr));
      margin-block-start: 1rem;
    }

    .notice-guidance {
      border-inline-start: 0.25rem solid var(--idq-capture-accent);
      margin-block: 1.25rem 0;
      padding-inline-start: 0.75rem;
    }

    .error {
      color: #b42318;
      font-weight: 650;
    }

    .requirements {
      display: grid;
      gap: 1.25rem;
      margin-block-start: 1.5rem;
    }

    .progress-summary {
      background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: 0.75rem;
      margin-block-start: 1.25rem;
      padding: 1rem;
    }

    .progress-copy,
    .completion-copy {
      margin-block-end: 0.5rem;
    }

    progress {
      accent-color: var(--idq-capture-accent);
      display: block;
      inline-size: 100%;
    }

    .completion-copy {
      color: var(--idq-capture-accent-strong);
      font-weight: 700;
      margin-block: 0.75rem 0;
    }

    .requirement {
      border-block-start: 1px solid var(--idq-capture-border);
      padding-block-start: 1.25rem;
    }

    .steps {
      display: grid;
      gap: 0.875rem;
      margin-block-start: 1rem;
    }

    .step {
      background: var(--idq-capture-surface);
      border: 1px solid var(--idq-capture-border);
      border-radius: 0.75rem;
      padding: 1rem;
    }

    .step-complete {
      border-color: var(--idq-capture-accent);
    }

    .fallback {
      border-inline-start: 0.25rem solid var(--idq-capture-accent);
      color: var(--idq-capture-muted);
      font-size: 0.875rem;
      margin-block-end: 1rem;
      padding-inline-start: 0.75rem;
    }

    .methods {
      display: grid;
      gap: 0.625rem;
      grid-template-columns: repeat(auto-fit, minmax(min(100%, 11rem), 1fr));
    }

    .file-option {
      display: grid;
      gap: 0.5rem;
    }

    .file-input {
      block-size: 1px;
      clip-path: inset(50%);
      inline-size: 1px;
      overflow: hidden;
      position: absolute;
      white-space: nowrap;
    }

    .file-label {
      align-items: center;
      background: var(--idq-capture-background);
      border: 1px solid var(--idq-capture-border);
      border-radius: 0.625rem;
      cursor: pointer;
      display: inline-flex;
      font-weight: 650;
      justify-content: center;
      min-height: 2.75rem;
      padding: 0.625rem 0.875rem;
      touch-action: manipulation;
      -webkit-tap-highlight-color: rgb(23 92 211 / 18%);
    }

    .file-input:focus-visible + .file-label {
      outline: 0.1875rem solid var(--idq-capture-accent);
      outline-offset: 0.1875rem;
    }

    .file-input:disabled + .file-label {
      cursor: wait;
      opacity: 0.65;
    }

    .file-guidance {
      color: var(--idq-capture-muted);
      font-size: 0.8125rem;
      margin: 0;
    }

    .camera-option {
      display: grid;
      gap: 0.75rem;
      grid-column: 1 / -1;
      min-width: 0;
    }

    .camera-preview {
      aspect-ratio: 4 / 3;
      background: #0c111d;
      border-radius: 0.625rem;
      display: block;
      height: auto;
      max-height: 32rem;
      object-fit: contain;
      width: 100%;
    }

    .camera-actions {
      display: grid;
      gap: 0.625rem;
      grid-template-columns: repeat(auto-fit, minmax(min(100%, 10rem), 1fr));
    }

    .camera-guidance {
      color: var(--idq-capture-muted);
      font-size: 0.875rem;
      margin: 0;
    }

    button {
      align-items: center;
      appearance: none;
      background: var(--idq-capture-background);
      border: 1px solid var(--idq-capture-border);
      border-radius: 0.625rem;
      color: var(--idq-capture-text);
      cursor: pointer;
      display: inline-flex;
      font: inherit;
      font-weight: 650;
      justify-content: center;
      min-height: 2.75rem;
      padding: 0.625rem 0.875rem;
      touch-action: manipulation;
      -webkit-tap-highlight-color: rgb(23 92 211 / 18%);
    }

    button:hover {
      border-color: var(--idq-capture-accent);
    }

    button:disabled {
      cursor: wait;
      opacity: 0.65;
    }

    button:active,
    button[aria-pressed="true"] {
      background: var(--idq-capture-accent);
      border-color: var(--idq-capture-accent);
      color: #ffffff;
    }

    button:focus-visible {
      outline: 0.1875rem solid var(--idq-capture-accent);
      outline-offset: 0.1875rem;
    }

    .status {
      font-size: 0.875rem;
      margin-block: 0.75rem 0;
      min-height: 1.3125rem;
    }

    @media (max-width: 30rem) {
      .shell {
        border-inline: 0;
        border-radius: 0;
      }
    }

    @media (forced-colors: active) {
      button[aria-pressed="true"] {
        outline: 0.1875rem solid ButtonText;
      }
    }
  `;

  declare plan: CapturePlan | undefined;
  readonly #selectedMethods = new Map<string, string>();
  readonly #uploadStates = new Map<string, StepUploadState>();
  readonly #cameraStates = new Map<string, StepCameraState>();
  readonly #completedSteps = new Map<string, StepCompletion>();
  #flowController: CaptureFlowController | undefined;
  #flowSnapshot: CaptureFlowSnapshot | undefined;
  #flowState: ComponentFlowState = "idle";
  #flowAbortController: AbortController | undefined;
  #responseError = false;
  #captureCompleteDispatched = false;
  #localizer: CaptureLocalizer = createCaptureLocalizer(browserLocale());

  async start(options: CaptureElementStartOptions): Promise<CaptureFlowSnapshot> {
    const { messageCatalogue = {}, ...flowOptions } = options;
    this.#localizer = createCaptureLocalizer(browserLocale(), messageCatalogue);
    this.#clearCameraStates();
    this.#flowAbortController?.abort();
    const abortController = new AbortController();
    this.#flowAbortController = abortController;
    this.#flowController = createCaptureFlowController(flowOptions);
    this.#flowSnapshot = undefined;
    this.#flowState = "loading";
    this.#responseError = false;
    this.#uploadStates.clear();
    this.#cameraStates.clear();
    this.#completedSteps.clear();
    this.#captureCompleteDispatched = false;
    this.plan = undefined;
    this.requestUpdate();
    try {
      const snapshot = await this.#flowController.load(abortController.signal);
      if (this.#flowAbortController !== abortController) return snapshot;
      this.#localizer = createCaptureLocalizer(
        snapshot.authoritySnapshot.notice.locale,
        messageCatalogue,
      );
      this.#flowSnapshot = snapshot;
      this.#flowState = "ready";
      this.#restoreRecoveredProgress(snapshot);
      if (snapshot.status === "authority_blocked" || snapshot.status === "refused") {
        this.#dropFlowController();
      } else {
        void this.#flowController
          .observe(
            (event) => this.#dispatchRealtimeEvent(event),
            (recovered) => {
              if (abortController.signal.aborted) return;
              this.#flowSnapshot = recovered;
              this.#restoreRecoveredProgress(recovered);
              this.requestUpdate();
            },
            abortController.signal,
          )
          .catch(() => {
            if (!abortController.signal.aborted) this.#dispatchFlowError();
          });
      }
      this.requestUpdate();
      return snapshot;
    } catch (error) {
      if (this.#flowAbortController !== abortController) throw error;
      const wasAborted = abortController.signal.aborted;
      this.#flowState = wasAborted ? "cancelled" : "error";
      this.#dropFlowController();
      this.requestUpdate();
      if (!wasAborted) this.#dispatchFlowError();
      throw error;
    }
  }

  cancel(): void {
    this.#dropFlowController();
    this.#flowSnapshot = undefined;
    this.#flowState = "cancelled";
    this.#responseError = false;
    this.#uploadStates.clear();
    this.#clearCameraStates();
    this.#completedSteps.clear();
    this.#captureCompleteDispatched = false;
    this.requestUpdate();
  }

  override disconnectedCallback(): void {
    this.cancel();
    super.disconnectedCallback();
  }

  protected override willUpdate(changedProperties: PropertyValues<this>): void {
    if (changedProperties.has("plan")) {
      this.#selectedMethods.clear();
      this.#uploadStates.clear();
      this.#clearCameraStates();
      this.#completedSteps.clear();
      this.#captureCompleteDispatched = false;
    }
  }

  protected override render() {
    return html`
      <section
        class="shell"
        aria-labelledby="capture-title"
        lang=${this.#localizer.locale}
        dir=${this.#localizer.direction}
      >
        <p class="eyebrow">${this.#text("secureCapture")}</p>
        <h2 id="capture-title">${this.#text("identityVerification")}</h2>
        ${
          this.#flowState === "loading"
            ? html`<p class="status" role="status" aria-live="polite">
                ${this.#text("preparingSecureCapture")}
              </p>`
            : this.#flowState === "error"
              ? html`<p class="error" role="alert">${this.#text("capturePreparationFailed")}</p>`
              : this.#flowState === "cancelled"
                ? html`<p class="status" role="status" aria-live="polite">
                    ${this.#text("captureCancelled")}
                  </p>`
                : this.#flowSnapshot !== undefined
                  ? this.#renderFlow(this.#flowSnapshot)
                  : this.plan === undefined
                    ? html`<p class="status" role="status" aria-live="polite">
                        ${this.#text("preparingCapture")}
                      </p>`
                    : this.#renderPlan(this.plan)
        }
      </section>
    `;
  }

  #renderFlow(flow: CaptureFlowSnapshot) {
    const { authority, notice, latestResponse } = flow.authoritySnapshot;
    return html`
      <article class="notice" aria-labelledby="idq-notice-title" lang=${notice.locale}>
        <h3 id="idq-notice-title">${notice.copy.title}</h3>
        <p class="notice-copy">${notice.copy.summary}</p>
        <h4>${this.#text("whyInformationNeeded")}</h4>
        <p class="notice-copy">${notice.copy.purpose}</p>
        <h4>${this.#text("ifYouRefuse")}</h4>
        <p class="notice-copy">${notice.copy.consequences}</p>
        <dl class="notice-meta">
          <div>
            <dt>${this.#text("controller")}</dt>
            <dd>${notice.controller}</dd>
          </div>
          <div>
            <dt>${this.#text("recipient")}</dt>
            <dd>${notice.recipient}</dd>
          </div>
        </dl>
        ${
          flow.status === "notice_required"
            ? this.#renderNoticeActions(authority.consentRequired)
            : flow.status === "capture_ready"
              ? html`<p class="notice-guidance" role="status">
                  ${
                    latestResponse?.action === "consent"
                      ? this.#text("consentRecorded")
                      : this.#text("acknowledgementRecorded")
                  }
                </p>`
              : flow.status === "refused"
                ? html`<p class="notice-guidance" role="status">
                    ${this.#text("refusalRecorded")}
                  </p>`
                : html`<p class="error" role="alert">${this.#text("authorityBlocked")}</p>`
        }
      </article>
      ${flow.status === "capture_ready" ? this.#renderPlan(flow.plan, notice.locale) : nothing}
    `;
  }

  #renderNoticeActions(consentRequired: boolean) {
    const busy = this.#flowState === "responding";
    const acceptedAction: SubjectResponseAction = consentRequired ? "consent" : "acknowledge";
    return html`
      <p class="notice-guidance">
        ${consentRequired ? this.#text("reviewConsent") : this.#text("reviewAcknowledgement")}
      </p>
      <div class="notice-actions" role="group" aria-label=${this.#text("noticeActionsLabel")}>
        <button type="button" ?disabled=${busy} @click=${() => void this.#respond(acceptedAction)}>
          ${
            busy
              ? this.#text("recordingResponse")
              : consentRequired
                ? this.#text("agreeAndContinue")
                : this.#text("acknowledgeAndContinue")
          }
        </button>
        <button type="button" ?disabled=${busy} @click=${() => void this.#respond("refuse")}>
          ${this.#text("refuse")}
        </button>
      </div>
      ${
        this.#responseError
          ? html`<p class="error" role="alert">${this.#text("responseFailed")}</p>`
          : nothing
      }
    `;
  }

  #renderPlan(plan: CapturePlan, locale = this.#localizer.locale) {
    const steps = plan.requirements.flatMap((requirement) => requirement.steps);
    const completedSteps = steps.filter((step) => this.#completedSteps.has(stepKey(step))).length;
    const totalSteps = steps.length;
    return html`
      <p class="intro">${this.#text("chooseApprovedMethod")}</p>
      <section class="progress-summary" aria-labelledby="idq-progress-copy">
        <p class="progress-copy" id="idq-progress-copy">
          ${this.#text("progressSummary", {
            completed: formatNumber(completedSteps, locale),
            total: formatNumber(totalSteps, locale),
          })}
        </p>
        <progress
          aria-label=${this.#text("progressLabel")}
          value=${completedSteps}
          max=${totalSteps}
        ></progress>
        ${
          completedSteps === totalSteps
            ? html`<p class="completion-copy" role="status" aria-live="polite">
                ${this.#text("captureComplete")}
              </p>`
            : nothing
        }
      </section>
      <div class="requirements">
        ${plan.requirements.map((requirement, requirementIndex) => {
          const requirementCompleted = requirement.steps.filter((step) =>
            this.#completedSteps.has(stepKey(step)),
          ).length;
          return html`
            <section class="requirement" aria-labelledby=${`idq-requirement-${requirementIndex}`}>
              <h3 id=${`idq-requirement-${requirementIndex}`}>
                ${labelForIdentifier(requirement.evidenceType)}
              </h3>
              <p class="requirement-copy">
                ${this.#text("requirementProgress", {
                  completed: formatNumber(requirementCompleted, locale),
                  total: formatNumber(requirement.steps.length, locale),
                })}
              </p>
              <div class="steps">
                ${requirement.steps.map((step, stepIndex) =>
                  this.#renderStep(step, `${requirementIndex}-${stepIndex}`),
                )}
              </div>
            </section>
          `;
        })}
      </div>
    `;
  }

  #renderStep(step: CapturePlanStep, idSuffix: string) {
    const key = stepKey(step);
    const selectedMethod = this.#selectedMethods.get(key);
    const uploadState = this.#uploadStates.get(key);
    const cameraState = this.#cameraStates.get(key);
    const completion = this.#completedSteps.get(key);
    const cameraActive = isCameraActive(cameraState);
    const fileActive = uploadState?.status === "uploading";
    return html`
      <section
        class=${completion === undefined ? "step" : "step step-complete"}
        aria-labelledby=${`idq-step-${idSuffix}`}
      >
        <h4 id=${`idq-step-${idSuffix}`}>${labelForIdentifier(step.artefact)}</h4>
        ${
          step.fallbackCondition === undefined
            ? nothing
            : html`<p class="fallback">
                ${fallbackMessage(step.fallbackCondition, this.#localizer)}
              </p>`
        }
        ${
          completion === undefined
            ? html`<div
                class="methods"
                role="group"
                aria-label=${this.#text("captureMethodsLabel", {
                  artefact: labelForIdentifier(step.artefact),
                })}
              >
                ${step.methodOptions.map((method) =>
                  method === FILE_UPLOAD_METHOD && this.#flowController !== undefined
                    ? this.#renderFileUpload(step, idSuffix, uploadState, cameraActive)
                    : method === LIVE_CAMERA_METHOD && this.#flowController !== undefined
                      ? this.#renderCamera(step, idSuffix, cameraState, fileActive)
                      : html`
                          <button
                            type="button"
                            aria-pressed=${String(selectedMethod === method)}
                            @click=${() => this.#selectMethod(step, method)}
                          >
                            ${methodAction(method, this.#localizer)}
                          </button>
                        `,
                )}
              </div>`
            : html`<p class="completion-copy">
                ${this.#text("capturedWith", {
                  method: methodLabel(completion.acquisitionMethod, this.#localizer),
                })}
              </p>`
        }
        <p class="status" role="status" aria-live="polite">
          ${
            completion !== undefined
              ? completionStatus(completion.acquisitionMethod, this.#localizer)
              : uploadState?.status === "uploading"
                ? this.#text("preparingFileUpload")
                : uploadState?.status === "accepted"
                  ? this.#text("fileAccepted")
                  : cameraState?.status === "accepted"
                    ? this.#text("photoAccepted")
                    : selectedMethod === undefined
                      ? nothing
                      : this.#text("methodSelected", {
                          method: methodLabel(selectedMethod, this.#localizer),
                        })
          }
        </p>
        ${
          uploadState?.status === "error"
            ? html`<p class="error" role="alert">${uploadState.message}</p>
                ${
                  uploadState.body === undefined
                    ? nothing
                    : html`<button
                        type="button"
                        @click=${() => void this.#uploadFile(step, uploadState.body!)}
                      >
                        ${this.#text("retryUpload")}
                      </button>`
                }`
            : nothing
        }
      </section>
    `;
  }

  #renderFileUpload(
    step: CapturePlanStep,
    idSuffix: string,
    uploadState: StepUploadState | undefined,
    lockedByCamera: boolean,
  ) {
    const flow = this.#flowSnapshot!;
    const policy = fileUploadPolicy(flow.session, step);
    const inputId = `idq-file-${idSuffix}`;
    const busy = uploadState?.status === "uploading";
    return html`
      <div class="file-option">
        <input
          class="file-input"
          id=${inputId}
          name=${`evidence-${step.requirementKey}-${idSuffix}`}
          type="file"
          autocomplete="off"
          accept=${policy.allowedMediaTypes.join(",")}
          ?disabled=${busy || lockedByCamera}
          @change=${(event: Event) => void this.#fileSelected(step, event)}
        />
        <label class="file-label" for=${inputId}>
          ${
            busy
              ? this.#text("uploadingFile")
              : lockedByCamera
                ? this.#text("finishCameraFirst")
                : this.#text("chooseFile")
          }
        </label>
        <p class="file-guidance">
          ${this.#text("fileGuidance", {
            mediaTypes: policy.allowedMediaTypes.map(mediaTypeLabel).join(" or "),
          })}
          ${
            policy.hasExplicitMaximum
              ? this.#text("maximumBytes", { maximum: formatBytes(policy.maximumBytes) })
              : this.#text("serverSizeLimit")
          }
        </p>
      </div>
    `;
  }

  #renderCamera(
    step: CapturePlanStep,
    idSuffix: string,
    state: StepCameraState | undefined,
    lockedByFile: boolean,
  ) {
    const videoId = `idq-camera-${idSuffix}`;
    return html`
      <div class="camera-option">
        ${
          state === undefined || (state.status === "error" && state.previewUrl === undefined)
            ? html`<button
                type="button"
                ?disabled=${lockedByFile}
                @click=${() => void this.#startCamera(step, idSuffix)}
              >
                ${
                  lockedByFile
                    ? this.#text("fileUploadInProgress")
                    : state?.status === "error"
                      ? this.#text("retryCamera")
                      : this.#text("startCamera")
                }
              </button>`
            : nothing
        }
        ${
          state?.status === "requesting"
            ? html`<button type="button" disabled>${this.#text("requestingCamera")}</button>`
            : nothing
        }
        ${
          state?.status === "streaming"
            ? html`
                <video
                  class="camera-preview"
                  id=${videoId}
                  width="640"
                  height="480"
                  autoplay
                  muted
                  playsinline
                  aria-label=${this.#text("liveCameraPreviewLabel", {
                    artefact: labelForIdentifier(step.artefact),
                  })}
                ></video>
                <p class="camera-guidance">${this.#text("cameraGuidance")}</p>
                <div class="camera-actions">
                  <button
                    type="button"
                    ?disabled=${state.ready !== true}
                    @click=${() => void this.#capturePhoto(step, videoId)}
                  >
                    ${this.#text("capturePhoto")}
                  </button>
                  <button type="button" @click=${() => this.#cancelCamera(step)}>
                    ${this.#text("cancelCamera")}
                  </button>
                </div>
              `
            : nothing
        }
        ${
          (state?.status === "reviewing" ||
            state?.status === "uploading" ||
            (state?.status === "error" && state.previewUrl !== undefined)) &&
          state.previewUrl !== undefined &&
          state.width !== undefined &&
          state.height !== undefined
            ? html`
                <img
                  class="camera-preview"
                  src=${state.previewUrl}
                  alt=${this.#text("capturedPreviewLabel", {
                    artefact: labelForIdentifier(step.artefact),
                  })}
                  width=${state.width}
                  height=${state.height}
                />
                <div class="camera-actions">
                  ${
                    state.body === undefined
                      ? nothing
                      : html`<button
                          type="button"
                          ?disabled=${state.status === "uploading"}
                          @click=${() => void this.#uploadCamera(step, state.body!)}
                        >
                          ${
                            state.status === "uploading"
                              ? this.#text("uploadingPhoto")
                              : this.#text("usePhoto")
                          }
                        </button>`
                  }
                  <button
                    type="button"
                    ?disabled=${state.status === "uploading"}
                    @click=${() => void this.#retakePhoto(step, idSuffix)}
                  >
                    ${this.#text("retakePhoto")}
                  </button>
                </div>
              `
            : nothing
        }
        <p class="status" role="status" aria-live="polite">
          ${
            state?.status === "requesting"
              ? this.#text("waitingForCameraPermission")
              : state?.status === "streaming"
                ? state.ready === true
                  ? this.#text("cameraReady")
                  : this.#text("startingCameraPreview")
                : state?.status === "reviewing"
                  ? this.#text("reviewCapturedPhoto")
                  : state?.status === "uploading"
                    ? this.#text("uploadingCapturedPhoto")
                    : nothing
          }
        </p>
        ${
          state?.status === "error"
            ? html`<p class="error" role="alert">${state.message}</p>`
            : nothing
        }
      </div>
    `;
  }

  #selectMethod(step: CapturePlanStep, method: string): void {
    const key = stepKey(step);
    if (this.#completedSteps.has(key)) return;
    this.#selectedMethods.set(key, method);
    this.requestUpdate();
    const detail: CaptureMethodSelectDetail = {
      requirementKey: step.requirementKey,
      evidenceType: step.evidenceType,
      artefact: step.artefact,
      method,
      ...(step.fallbackCondition === undefined
        ? {}
        : { fallbackCondition: step.fallbackCondition }),
    };
    this.dispatchEvent(
      new CustomEvent<CaptureMethodSelectDetail>("idenqa-method-select", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
  }

  async #fileSelected(step: CapturePlanStep, event: Event): Promise<void> {
    const input = event.currentTarget as HTMLInputElement;
    const body = input.files?.item(0);
    input.value = "";
    const key = stepKey(step);
    if (body === null || body === undefined || this.#completedSteps.has(key)) return;
    if (isCameraActive(this.#cameraStates.get(key))) return;
    this.#clearCameraState(key);
    this.#selectMethod(step, FILE_UPLOAD_METHOD);
    await this.#uploadFile(step, body);
  }

  async #uploadFile(step: CapturePlanStep, body: Blob): Promise<void> {
    const controller = this.#flowController;
    const signal = this.#flowAbortController?.signal;
    if (
      controller === undefined ||
      signal === undefined ||
      signal.aborted ||
      this.#completedSteps.has(stepKey(step))
    ) {
      return;
    }
    const key = stepKey(step);
    this.#uploadStates.set(key, { status: "uploading", body });
    this.#reportStep(step, FILE_UPLOAD_METHOD, "started");
    this.requestUpdate();
    try {
      const upload = await controller.uploadFile(step, body, signal);
      if (signal.aborted) return;
      this.#clearCameraState(key);
      this.#uploadStates.set(key, { status: "accepted" });
      this.#completeStep(step, FILE_UPLOAD_METHOD, upload);
    } catch (error) {
      if (signal.aborted) return;
      const message = this.#text("fileRejected");
      this.#uploadStates.set(key, { status: "error", body, message });
      this.#reportStep(step, FILE_UPLOAD_METHOD, "failed", "file_upload_failed");
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  async #startCamera(step: CapturePlanStep, idSuffix: string): Promise<void> {
    const signal = this.#flowAbortController?.signal;
    if (this.#flowController === undefined || signal === undefined || signal.aborted) return;
    const key = stepKey(step);
    if (this.#completedSteps.has(key) || this.#uploadStates.get(key)?.status === "uploading")
      return;
    this.#clearCameraState(key);
    this.#uploadStates.delete(key);
    this.#selectMethod(step, LIVE_CAMERA_METHOD);
    this.#cameraStates.set(key, { status: "requesting" });
    this.#reportStep(step, LIVE_CAMERA_METHOD, "started");
    this.requestUpdate();
    try {
      const stream = await startCamera(cameraFacingMode(step.artefact), signal);
      if (signal.aborted) {
        stopCamera(stream);
        return;
      }
      this.#cameraStates.set(key, { status: "streaming", stream, ready: false });
      this.requestUpdate();
      await this.updateComplete;
      const videoId = `idq-camera-${idSuffix}`;
      const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${videoId}`);
      if (video === null) {
        stopCamera(stream);
        return;
      }
      video.srcObject = stream;
      video.addEventListener("loadedmetadata", () => this.#markCameraReady(key, stream, video), {
        once: true,
      });
      await video.play();
      this.#markCameraReady(key, stream, video);
    } catch (error) {
      if (signal.aborted) return;
      this.#handleCameraFailure(step, error);
    }
  }

  async #capturePhoto(step: CapturePlanStep, videoId: string): Promise<void> {
    const key = stepKey(step);
    const state = this.#cameraStates.get(key);
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${videoId}`);
    if (state?.status !== "streaming" || state.ready !== true || video === null) return;
    try {
      const policy = fileUploadPolicy(this.#flowSnapshot!.session, step);
      const mediaType = policy.allowedMediaTypes.includes("image/jpeg")
        ? "image/jpeg"
        : policy.allowedMediaTypes[0]!;
      const frame = await captureCameraFrame(video, mediaType);
      stopCamera(state.stream);
      video.srcObject = null;
      this.#cameraStates.set(key, {
        status: "reviewing",
        body: frame.body,
        previewUrl: URL.createObjectURL(frame.body),
        width: frame.width,
        height: frame.height,
      });
      this.requestUpdate();
    } catch (error) {
      this.#handleCameraFailure(step, error);
    }
  }

  async #uploadCamera(step: CapturePlanStep, body: Blob): Promise<void> {
    const controller = this.#flowController;
    const signal = this.#flowAbortController?.signal;
    const key = stepKey(step);
    const current = this.#cameraStates.get(key);
    if (
      controller === undefined ||
      signal === undefined ||
      signal.aborted ||
      current?.previewUrl === undefined ||
      current.width === undefined ||
      current.height === undefined
    ) {
      return;
    }
    this.#cameraStates.set(key, { ...current, status: "uploading", body });
    this.requestUpdate();
    try {
      const upload = await controller.uploadCamera(step, body, signal);
      if (signal.aborted) return;
      URL.revokeObjectURL(current.previewUrl);
      this.#uploadStates.delete(key);
      this.#cameraStates.set(key, { status: "accepted" });
      this.#completeStep(step, LIVE_CAMERA_METHOD, upload);
    } catch (error) {
      if (signal.aborted) return;
      const message = this.#text("photoRejected");
      this.#cameraStates.set(key, { ...current, status: "error", body, message });
      this.#reportStep(step, LIVE_CAMERA_METHOD, "failed", "camera_upload_failed");
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  async #retakePhoto(step: CapturePlanStep, idSuffix: string): Promise<void> {
    this.#clearCameraState(stepKey(step));
    await this.#startCamera(step, idSuffix);
  }

  #cancelCamera(step: CapturePlanStep): void {
    const key = stepKey(step);
    this.#clearCameraState(key);
    this.#selectedMethods.delete(key);
    this.#reportStep(step, LIVE_CAMERA_METHOD, "cancelled", "subject_cancelled");
    this.requestUpdate();
  }

  #handleCameraFailure(step: CapturePlanStep, error: unknown): void {
    const key = stepKey(step);
    this.#clearCameraState(key);
    const controller = this.#flowController;
    this.#reportStep(step, LIVE_CAMERA_METHOD, "failed", "camera_capture_failed");
    if (controller !== undefined) {
      try {
        this.#flowSnapshot = controller.captureFailed(step);
        this.#selectedMethods.delete(key);
        this.requestUpdate();
        return;
      } catch {
        // No usable capture_failed fallback exists; keep the camera retry local.
      }
    }
    this.#cameraStates.set(key, {
      status: "error",
      message: cameraErrorMessage(error, this.#localizer),
    });
    this.#dispatchFlowError();
    this.requestUpdate();
  }

  #clearCameraState(key: string): void {
    const state = this.#cameraStates.get(key);
    stopCamera(state?.stream);
    if (state?.previewUrl !== undefined) URL.revokeObjectURL(state.previewUrl);
    this.#cameraStates.delete(key);
  }

  #markCameraReady(key: string, stream: MediaStream, video: HTMLVideoElement): void {
    const state = this.#cameraStates.get(key);
    if (
      state?.status !== "streaming" ||
      state.stream !== stream ||
      video.videoWidth <= 0 ||
      video.videoHeight <= 0
    ) {
      return;
    }
    this.#cameraStates.set(key, { ...state, ready: true });
    this.requestUpdate();
  }

  #clearCameraStates(): void {
    for (const key of [...this.#cameraStates.keys()]) this.#clearCameraState(key);
  }

  #completeStep(step: CapturePlanStep, acquisitionMethod: string, upload: EvidenceUpload): void {
    const key = stepKey(step);
    if (this.#completedSteps.has(key)) return;
    this.#completedSteps.set(key, {
      acquisitionMethod,
      uploadId: upload.id,
      evidenceId: upload.evidenceId,
    });
    const detail: CaptureEvidenceAcceptedDetail = {
      uploadId: upload.id,
      evidenceId: upload.evidenceId,
      requirementKey: step.requirementKey,
      artefact: step.artefact,
      acquisitionMethod,
    };
    this.dispatchEvent(
      new CustomEvent<CaptureEvidenceAcceptedDetail>("idenqa-evidence-accepted", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
    const plan = this.#flowSnapshot?.plan ?? this.plan;
    if (plan === undefined) return;
    const progress = captureProgress(plan, this.#completedSteps);
    const progressDetail: CaptureProgressDetail = {
      verificationId: plan.verificationId,
      ...progress,
    };
    this.dispatchEvent(
      new CustomEvent<CaptureProgressDetail>("idenqa-capture-progress", {
        bubbles: true,
        composed: true,
        detail: progressDetail,
      }),
    );
    if (progress.completedSteps !== progress.totalSteps || this.#captureCompleteDispatched) return;
    this.#captureCompleteDispatched = true;
    const completeDetail: CaptureCompleteDetail = { ...progressDetail, captureComplete: true };
    this.dispatchEvent(
      new CustomEvent<CaptureCompleteDetail>("idenqa-capture-complete", {
        bubbles: true,
        composed: true,
        detail: completeDetail,
      }),
    );
  }

  #restoreRecoveredProgress(snapshot: CaptureFlowSnapshot): void {
    const steps = snapshot.plan.requirements.flatMap((requirement) => requirement.steps);
    for (const completion of snapshot.progress.completions) {
      const matches = steps.filter(
        (step) =>
          step.requirementKey === completion.requirementKey &&
          step.evidenceType === completion.evidenceType &&
          step.artefact === completion.artefact &&
          step.methodOptions.includes(completion.acquisitionMethod) &&
          step.fallbackCondition === completion.fallbackCondition,
      );
      if (matches.length !== 1) continue;
      this.#completedSteps.set(stepKey(matches[0]!), {
        acquisitionMethod: completion.acquisitionMethod,
        uploadId: completion.uploadId,
        evidenceId: completion.evidenceId,
      });
    }
    if (this.#completedSteps.size === 0) return;
    const progress = captureProgress(snapshot.plan, this.#completedSteps);
    const detail: CaptureProgressDetail = {
      verificationId: snapshot.plan.verificationId,
      ...progress,
    };
    this.dispatchEvent(
      new CustomEvent<CaptureProgressDetail>("idenqa-capture-progress", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
    if (progress.completedSteps !== progress.totalSteps) return;
    this.#captureCompleteDispatched = true;
    this.dispatchEvent(
      new CustomEvent<CaptureCompleteDetail>("idenqa-capture-complete", {
        bubbles: true,
        composed: true,
        detail: { ...detail, captureComplete: true },
      }),
    );
  }

  async #respond(action: SubjectResponseAction): Promise<void> {
    const controller = this.#flowController;
    const signal = this.#flowAbortController?.signal;
    if (controller === undefined || this.#flowState === "responding") return;
    this.#flowState = "responding";
    this.#responseError = false;
    this.requestUpdate();
    try {
      const snapshot = await controller.respond(action, signal);
      if (signal?.aborted === true) return;
      this.#flowSnapshot = snapshot;
      this.#flowState = "ready";
      const response = snapshot.authoritySnapshot.latestResponse;
      if (response !== undefined) {
        const detail: CaptureSubjectResponseDetail = {
          action: response.action,
          responseId: response.id,
          authorityId: response.authorityId,
          noticeId: response.noticeId,
        };
        this.dispatchEvent(
          new CustomEvent<CaptureSubjectResponseDetail>("idenqa-subject-response", {
            bubbles: true,
            composed: true,
            detail,
          }),
        );
      }
      if (snapshot.status === "refused" || snapshot.status === "authority_blocked") {
        this.#dropFlowController();
      }
    } catch {
      if (signal?.aborted === true) return;
      this.#flowState = "ready";
      this.#responseError = true;
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  #dispatchFlowError(): void {
    const detail: CaptureFlowErrorDetail = { code: "CAPTURE_FLOW_REQUEST_FAILED" };
    this.dispatchEvent(
      new CustomEvent<CaptureFlowErrorDetail>("idenqa-flow-error", {
        bubbles: true,
        composed: true,
        detail,
      }),
    );
  }

  #dispatchRealtimeEvent(event: CaptureRealtimeEvent): void {
    this.dispatchEvent(
      new CustomEvent<CaptureRealtimeDetail>("idenqa-realtime-event", {
        bubbles: true,
        composed: true,
        detail: { event },
      }),
    );
  }

  #reportStep(
    step: CapturePlanStep,
    acquisitionMethod: string,
    state: "started" | "failed" | "cancelled",
    code?: string,
  ): void {
    void this.#flowController
      ?.reportStep({
        state,
        requirementKey: step.requirementKey,
        artefact: step.artefact,
        acquisitionMethod,
        ...(code === undefined ? {} : { code }),
      })
      .catch(() => this.#dispatchFlowError());
  }

  #dropFlowController(): void {
    this.#clearCameraStates();
    this.#flowAbortController?.abort();
    this.#flowAbortController = undefined;
    this.#flowController = undefined;
  }

  #text(key: CaptureMessageKey, values?: CaptureMessageValues): string {
    return this.#localizer.text(key, values);
  }
}

export function defineIdenqaCapture(
  tagName: string = IDENQA_CAPTURE_TAG_NAME,
): typeof IdenqaCaptureElement {
  if (typeof customElements === "undefined") {
    throw new Error("Custom elements are not available in this environment.");
  }
  const existing = customElements.get(tagName);
  if (existing === undefined) {
    customElements.define(tagName, IdenqaCaptureElement);
  } else if (existing !== IdenqaCaptureElement) {
    throw new Error(`The custom-element name ${tagName} is already registered.`);
  }
  return IdenqaCaptureElement;
}

function stepKey(step: CapturePlanStep): string {
  return JSON.stringify([
    step.requirementKey,
    step.artefact,
    step.methodOptions,
    step.fallbackCondition,
  ]);
}

function captureProgress(
  plan: CapturePlan,
  completedSteps: ReadonlyMap<string, StepCompletion>,
): Pick<CaptureProgressDetail, "completedSteps" | "totalSteps"> {
  const steps = plan.requirements.flatMap((requirement) => requirement.steps);
  return {
    completedSteps: steps.filter((step) => completedSteps.has(stepKey(step))).length,
    totalSteps: steps.length,
  };
}

function isCameraActive(state: StepCameraState | undefined): boolean {
  return (
    state?.status === "requesting" ||
    state?.status === "streaming" ||
    state?.status === "reviewing" ||
    state?.status === "uploading"
  );
}

function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(value);
}

function completionStatus(method: string, localizer: CaptureLocalizer): string {
  return method === LIVE_CAMERA_METHOD
    ? localizer.text("cameraStepComplete")
    : localizer.text("fileStepComplete");
}

function fallbackMessage(
  condition: CaptureRuntimeFallbackReason,
  localizer: CaptureLocalizer,
): string {
  if (condition === "capability_unavailable") {
    return localizer.text("capabilityFallback");
  }
  if (condition === "capture_failed") {
    return localizer.text("captureFailureFallback");
  }
  return localizer.text("methodFallback");
}

function cameraErrorMessage(error: unknown, localizer: CaptureLocalizer): string {
  if (error instanceof CaptureCameraError && error.code === "CAPTURE_CAMERA_PERMISSION_DENIED") {
    return localizer.text("cameraPermissionDenied");
  }
  if (error instanceof CaptureCameraError && error.code === "CAPTURE_CAMERA_FRAME_FAILED") {
    return localizer.text("cameraFrameFailed", { message: error.message });
  }
  return localizer.text("cameraUnavailable");
}

function methodAction(method: string, localizer: CaptureLocalizer): string {
  if (method === "idenqa.method.file_upload") return localizer.text("uploadFileAction");
  if (method === "idenqa.method.live_camera") return localizer.text("useCameraAction");
  return localizer.text("useMethodAction", { method: methodLabel(method, localizer) });
}

function methodLabel(method: string, localizer: CaptureLocalizer): string {
  if (method === "idenqa.method.file_upload") return localizer.text("fileUploadMethod");
  if (method === "idenqa.method.live_camera") return localizer.text("cameraMethod");
  return labelForIdentifier(method);
}

function mediaTypeLabel(mediaType: string): string {
  return mediaType === "image/jpeg" ? "JPEG" : mediaType === "image/png" ? "PNG" : mediaType;
}

function labelForIdentifier(identifier: string): string {
  const segment = identifier.split(".").at(-1) ?? identifier;
  return segment
    .split("_")
    .filter((word) => word.length > 0)
    .map((word) => word[0]?.toUpperCase() + word.slice(1))
    .join(" ");
}

function browserLocale(): string {
  return globalThis.navigator?.languages?.[0] ?? globalThis.navigator?.language ?? "en";
}

declare global {
  interface HTMLElementTagNameMap {
    "idenqa-capture": IdenqaCaptureElement;
  }
}
