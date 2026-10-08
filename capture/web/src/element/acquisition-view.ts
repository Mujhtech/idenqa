import { html, nothing, svg } from "lit";
import {
  isDocumentArtefact,
  type NormalizedCaptureDocumentCaptureOptions,
} from "../document-capture.js";
import type { DocumentDetection } from "../document-detection.js";
import { isActiveCaptureFlowSnapshot, type CaptureFlowSnapshot } from "../flow.js";
import type { CaptureLocalizer, CaptureMessageKey, CaptureMessageValues } from "../localisation.js";
import {
  captureMethodAdapterCopy,
  type CaptureMethodAdapter,
  type CaptureMethodAdapterProgress,
} from "../method-adapter.js";
import type { CapturePlanStep } from "../planner.js";
import { fileUploadPolicy, formatBytes } from "../upload.js";
import {
  adapterProgressMessage,
  formatNumber,
  friendlyArtefact,
  livenessPrompt,
  mediaTypeLabel,
  poseFeedbackKey,
  poseGuideStage,
} from "./copy.js";
import { documentGuideRect, documentHintMessage, quadPoints, roundCoordinate } from "./document.js";
import { documentIcon } from "./icons.js";
import { stepKey } from "./plan.js";
import type {
  ElementTemplate,
  StepAdapterState,
  StepCameraState,
  StepDocumentState,
  StepUploadState,
} from "./state.js";

/** Read-only presentation state and explicit callbacks; the element owns lifecycle effects. */
export interface AcquisitionViewContext {
  readonly flowSnapshot: CaptureFlowSnapshot | undefined;
  readonly fileSelected: (step: CapturePlanStep, event: Event) => Promise<void>;
  readonly text: (key: CaptureMessageKey, values?: CaptureMessageValues) => string;
  readonly localizer: CaptureLocalizer;
  readonly uploadFile: (step: CapturePlanStep, body: Blob) => Promise<void>;
  readonly documentCapture: NormalizedCaptureDocumentCaptureOptions;
  readonly hasFlowController: boolean;
  readonly documentStates: ReadonlyMap<string, StepDocumentState>;
  readonly startCamera: (step: CapturePlanStep, idSuffix: string) => Promise<void>;
  readonly capturingSteps: ReadonlySet<string>;
  readonly capturePhoto: (
    step: CapturePlanStep,
    videoId: string,
    detection?: DocumentDetection,
  ) => Promise<void>;
  readonly documentDetection: (key: string) => DocumentDetection | undefined;
  readonly cancelCamera: (step: CapturePlanStep) => void;
  readonly uploadCamera: (step: CapturePlanStep, body: Blob) => Promise<void>;
  readonly retakePhoto: (step: CapturePlanStep, idSuffix: string) => Promise<void>;
  readonly documentItem: (step: CapturePlanStep) => string;
  readonly selectedDocuments: ReadonlyMap<string, string>;
  readonly isDocumentCameraScreen: () => boolean;
  readonly cancelMethodAdapter: (step: CapturePlanStep) => void;
  readonly runMethodAdapter: (
    step: CapturePlanStep,
    adapter: CaptureMethodAdapter,
    previewId: string,
  ) => Promise<void>;
}

export function renderFileUpload(
  view: AcquisitionViewContext,
  step: CapturePlanStep,
  idSuffix: string,
  uploadState: StepUploadState | undefined,
  lockedByCamera: boolean,
): ElementTemplate {
  const flow = view.flowSnapshot;
  if (flow === undefined || !isActiveCaptureFlowSnapshot(flow)) return nothing;
  const policy = fileUploadPolicy(flow.session, step);
  const inputId = `idq-file-${idSuffix}`;
  const busy = uploadState?.status === "uploading";
  const reviewing = uploadState?.body !== undefined && uploadState.previewUrl !== undefined;
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
        @change=${(event: Event) => void view.fileSelected(step, event)}
      />
      ${
        reviewing
          ? html`
              <img
                class="review-image"
                src=${uploadState.previewUrl!}
                alt=${view.text("selectedFilePreview", {
                  artefact: friendlyArtefact(step.artefact, view.localizer),
                })}
                width="640"
                height="480"
              />
              <p class="camera-guidance">${view.text("reviewSelectedFile")}</p>
              ${
                uploadState.status === "error"
                  ? html`<p class="error" role="alert">${uploadState.message}</p>`
                  : nothing
              }
              <div class="camera-actions">
                <button
                  class="primary"
                  type="button"
                  ?disabled=${busy}
                  @click=${() => void view.uploadFile(step, uploadState.body!)}
                >
                  ${uploadState.status === "error" ? view.text("retryUpload") : view.text("useFile")}
                </button>
                <label class="file-label" for=${inputId}>${view.text("chooseDifferentFile")}</label>
              </div>
            `
          : html`
              <label class="file-label" for=${inputId}>
                ${
                  busy
                    ? view.text("uploadingFile")
                    : lockedByCamera
                      ? view.text("finishCameraFirst")
                      : view.text("chooseFile")
                }
              </label>
              <p class="file-guidance">
                ${view.text("fileGuidance", {
                  mediaTypes: policy.allowedMediaTypes.map(mediaTypeLabel).join(" or "),
                })}
                ${
                  policy.hasExplicitMaximum
                    ? view.text("maximumBytes", { maximum: formatBytes(policy.maximumBytes) })
                    : view.text("serverSizeLimit")
                }
              </p>
            `
      }
    </div>
  `;
}

export function renderCamera(
  view: AcquisitionViewContext,
  step: CapturePlanStep,
  idSuffix: string,
  state: StepCameraState | undefined,
  lockedByFile: boolean,
): ElementTemplate {
  const videoId = `idq-camera-${idSuffix}`;
  const key = stepKey(step);
  const documentCaptureActive =
    isDocumentArtefact(step.artefact) && view.documentCapture.enabled && view.hasFlowController;
  const documentState = view.documentStates.get(key);
  if (isDocumentArtefact(step.artefact)) {
    return renderDocumentCamera(view, step, idSuffix, state, lockedByFile, documentCaptureActive);
  }
  return html`
    <div class="camera-option">
      ${
        state === undefined || (state.status === "error" && state.previewUrl === undefined)
          ? html`<button
              type="button"
              ?disabled=${lockedByFile}
              @click=${() => void view.startCamera(step, idSuffix)}
            >
              ${
                lockedByFile
                  ? view.text("fileUploadInProgress")
                  : state?.status === "error"
                    ? view.text("retryCamera")
                    : view.text("startCamera")
              }
            </button>`
          : nothing
      }
      ${
        state?.status === "requesting"
          ? html`<button type="button" disabled>${view.text("requestingCamera")}</button>`
          : nothing
      }
      ${
        state?.status === "streaming"
          ? html`
              <div
                class="document-preview-frame"
                data-document-capture=${String(documentCaptureActive)}
              >
                <video
                  class="camera-preview"
                  id=${videoId}
                  width="640"
                  height="480"
                  autoplay
                  muted
                  playsinline
                  aria-label=${view.text("liveCameraPreviewLabel", {
                    artefact: friendlyArtefact(step.artefact, view.localizer),
                  })}
                ></video>
                ${documentCaptureActive ? renderDocumentGuide(view, state, documentState) : nothing}
              </div>
              <p class="camera-guidance">${view.text("cameraGuidance")}</p>
              ${
                documentCaptureActive && view.documentCapture.autoCapture
                  ? html`<p class="document-auto-capture">${view.text("documentAutoCapture")}</p>`
                  : nothing
              }
              <div class="camera-actions">
                <button
                  type="button"
                  ?disabled=${state.ready !== true || view.capturingSteps.has(key)}
                  @click=${() => void view.capturePhoto(step, videoId, view.documentDetection(key))}
                >
                  ${view.text("capturePhoto")}
                </button>
                <button type="button" @click=${() => view.cancelCamera(step)}>
                  ${view.text("cancelCamera")}
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
                alt=${view.text("capturedPreviewLabel", {
                  artefact: friendlyArtefact(step.artefact, view.localizer),
                })}
                width=${state.width}
                height=${state.height}
              />
              <div class="camera-actions">
                ${
                  state.body === undefined
                    ? nothing
                    : html`<button
                        class="primary"
                        type="button"
                        ?disabled=${state.status === "uploading"}
                        @click=${() => void view.uploadCamera(step, state.body!)}
                      >
                        ${
                          state.status === "uploading"
                            ? view.text("uploadingPhoto")
                            : view.text("usePhoto")
                        }
                      </button>`
                }
                <button
                  type="button"
                  ?disabled=${state.status === "uploading"}
                  @click=${() => void view.retakePhoto(step, idSuffix)}
                >
                  ${view.text("retakePhoto")}
                </button>
              </div>
            `
          : nothing
      }
      <p class="status" role="status" aria-live="polite">
        ${
          state?.status === "requesting"
            ? view.text("waitingForCameraPermission")
            : state?.status === "streaming"
              ? state.ready === true
                ? documentCaptureActive
                  ? documentHintMessage(documentState, view.localizer)
                  : view.text("cameraReady")
                : view.text("startingCameraPreview")
              : state?.status === "reviewing"
                ? view.text("reviewCapturedPhoto")
                : state?.status === "uploading"
                  ? view.text("uploadingCapturedPhoto")
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

export function renderDocumentCamera(
  view: AcquisitionViewContext,
  step: CapturePlanStep,
  idSuffix: string,
  state: StepCameraState | undefined,
  lockedByFile: boolean,
  detectionEnabled: boolean,
): ElementTemplate {
  const key = stepKey(step);
  const videoId = `idq-camera-${idSuffix}`;
  const reviewing = state?.previewUrl !== undefined;
  const uploading = state?.status === "uploading";
  const streaming = state?.status === "streaming";
  const documentState = view.documentStates.get(key);
  return html`
    <div class="document-camera camera-option" data-state=${state?.status ?? "idle"}>
      <h2 class="document-instruction">
        ${
          reviewing
            ? view.text("documentReviewInstruction")
            : view.text("documentCaptureInstruction", { item: view.documentItem(step) })
        }
      </h2>
      <div class="document-viewfinder">
        <div class="document-preview-frame" data-document-capture=${String(detectionEnabled)}>
          ${
            reviewing
              ? html`
                  <img
                    class="camera-preview"
                    src=${state.previewUrl!}
                    alt=${view.text("capturedPreviewLabel", { artefact: view.selectedDocuments.has(step.requirementKey) ? view.documentItem(step) : friendlyArtefact(step.artefact, view.localizer) })}
                    width=${state.width ?? 640}
                    height=${state.height ?? 480}
                  />
                `
              : streaming
                ? html`
                    <video
                      class="camera-preview"
                      id=${videoId}
                      width="640"
                      height="480"
                      autoplay
                      muted
                      playsinline
                      aria-label=${view.text("liveCameraPreviewLabel", { artefact: view.selectedDocuments.has(step.requirementKey) ? view.documentItem(step) : friendlyArtefact(step.artefact, view.localizer) })}
                    ></video>
                    ${detectionEnabled ? renderDocumentGuide(view, state, documentState) : nothing}
                  `
                : html`<div class="document-camera-placeholder" aria-hidden="true">
                    ${documentIcon()}
                  </div>`
          }
        </div>
        <div class="document-side-label">
          ${documentIcon()}<span
            >${view.text(step.artefact.endsWith("document_back") ? "documentBackLabel" : "documentFrontLabel")}</span
          >
        </div>
      </div>
      <p class="document-feedback" role="status" aria-live="polite">
        ${
          uploading
            ? view.text("uploadingCapturedPhoto")
            : state?.status === "requesting"
              ? view.text("waitingForCameraPermission")
              : streaming
                ? state.ready !== true
                  ? view.text("startingCameraPreview")
                  : detectionEnabled
                    ? documentHintMessage(documentState, view.localizer)
                    : view.text("cameraReady")
                : nothing
        }
      </p>
      ${
        streaming && detectionEnabled && view.documentCapture.autoCapture
          ? html`<p class="document-auto-copy">${view.text("documentAutoCapture")}</p>`
          : nothing
      }
      ${
        !reviewing
          ? html` <details class="document-help">
              <summary>${view.text("documentHelp")}</summary>
              <p>${view.text("documentHelpBody")}</p>
            </details>`
          : nothing
      }
      ${state?.status === "error" ? html`<p class="error" role="alert">${state.message}</p>` : nothing}
      <div class="document-controls camera-actions">
        ${
          reviewing
            ? html`
                <button
                  class="primary"
                  type="button"
                  ?disabled=${uploading || state.body === undefined}
                  @click=${() => void view.uploadCamera(step, state.body!)}
                >
                  ${view.text(uploading ? "uploadingPhoto" : "usePhoto")}
                </button>
                <button
                  type="button"
                  ?disabled=${uploading}
                  @click=${() => void view.retakePhoto(step, idSuffix)}
                >
                  ${view.text("retakePhoto")}
                </button>
              `
            : streaming
              ? html`
                  <button
                    class="document-shutter"
                    type="button"
                    ?disabled=${state.ready !== true || view.capturingSteps.has(key)}
                    @click=${() => void view.capturePhoto(step, videoId, view.documentDetection(key))}
                  >
                    <span class="shutter-icon" aria-hidden="true"></span
                    >${view.text("capturePhoto")}
                  </button>
                  ${
                    view.isDocumentCameraScreen()
                      ? nothing
                      : html`<button type="button" @click=${() => view.cancelCamera(step)}>
                          ${view.text("cancelCamera")}
                        </button>`
                  }
                `
              : html`
                  <button
                    class="primary"
                    type="button"
                    ?disabled=${lockedByFile || state?.status === "requesting"}
                    @click=${() => void view.startCamera(step, idSuffix)}
                  >
                    ${view.text(
                      lockedByFile
                        ? "fileUploadInProgress"
                        : state?.status === "requesting"
                          ? "requestingCamera"
                          : state?.status === "error"
                            ? "retryCamera"
                            : "startCamera",
                    )}
                  </button>
                `
        }
      </div>
    </div>
  `;
}

export function renderDocumentGuide(
  view: AcquisitionViewContext,
  state: StepCameraState,
  documentState: StepDocumentState | undefined,
): ElementTemplate {
  const width = state.width ?? 640;
  const height = state.height ?? 480;
  if (width <= 0 || height <= 0) return nothing;
  const guide = documentGuideRect(width, height);
  const quad = documentState?.quad;
  return html`
    <svg
      class="document-guide"
      viewBox="0 0 ${width} ${height}"
      preserveAspectRatio="xMidYMid meet"
      data-state=${documentState?.status ?? "searching"}
      aria-hidden="true"
      focusable="false"
    >
      <rect
        class="document-guide-target"
        x=${roundCoordinate(guide.x)}
        y=${roundCoordinate(guide.y)}
        width=${roundCoordinate(guide.width)}
        height=${roundCoordinate(guide.height)}
        rx=${roundCoordinate(Math.min(guide.width, guide.height) * 0.04)}
      ></rect>
      ${
        quad === undefined
          ? nothing
          : html`<polygon class="document-guide-quad" points=${quadPoints(quad)}></polygon>`
      }
    </svg>
  `;
}

export function renderPoseRing(
  view: AcquisitionViewContext,
  progress: CaptureMethodAdapterProgress | undefined,
): ElementTemplate {
  const prompt = progress?.prompt;
  const stage = poseGuideStage(progress);
  const center =
    prompt === "turn_right"
      ? 0
      : prompt === "turn_left"
        ? Math.PI
        : prompt === "look_up"
          ? -Math.PI / 2
          : Math.PI / 2;
  const arcHalfAngle = Math.PI / 6;
  return html`
    <svg
      class="liveness-face-guide"
      viewBox="0 0 100 100"
      preserveAspectRatio="xMidYMid meet"
      data-stage=${stage}
      aria-hidden="true"
      focusable="false"
    >
      ${[0, Math.PI / 2, Math.PI, -Math.PI / 2].map((arcCenter) => {
        const active = stage === "pose" && (prompt === "blink" || arcCenter === center);
        if (active) {
          return Array.from({ length: 29 }, (_, index) => {
            const angle = arcCenter - arcHalfAngle + (index / 28) * 2 * arcHalfAngle;
            const filled = Math.abs(index - 14) / 14 < (progress?.poseProgress ?? 0);
            return svg`<line
                x1=${50 + Math.cos(angle) * 40} y1=${50 + Math.sin(angle) * 40}
                x2=${50 + Math.cos(angle) * 46} y2=${50 + Math.sin(angle) * 46}
                data-filled=${String(filled)} />`;
          });
        }
        const start = arcCenter - arcHalfAngle;
        const end = arcCenter + arcHalfAngle;
        return svg`<path class="liveness-guide-arc"
            d=${`M ${50 + Math.cos(start) * 43} ${50 + Math.sin(start) * 43} A 43 43 0 0 1 ${50 + Math.cos(end) * 43} ${50 + Math.sin(end) * 43}`} />`;
      })}
    </svg>
  `;
}

export function renderMethodAdapter(
  view: AcquisitionViewContext,
  step: CapturePlanStep,
  idSuffix: string,
  adapter: CaptureMethodAdapter,
  state: StepAdapterState | undefined,
): ElementTemplate {
  const copy = captureMethodAdapterCopy(adapter, view.localizer.locale);
  const previewId = `idq-adapter-preview-${idSuffix}`;
  const running = state?.status === "running";
  const activeLiveness = adapter.presentation === "active_liveness";
  const prompt =
    state?.progress?.phase === "challenge" && state.progress.prompt !== undefined
      ? livenessPrompt(state.progress.prompt, view.localizer)
      : undefined;
  const challengeProgress =
    state?.progress?.phase === "challenge"
      ? view.text("challengeProgress", {
          current: formatNumber(state.progress.current!, view.localizer.locale),
          total: formatNumber(state.progress.total!, view.localizer.locale),
        })
      : undefined;
  const feedback = state?.progress?.poseFeedback;
  const guideStage = poseGuideStage(state?.progress);
  const guidance =
    guideStage === "centered"
      ? view.text("poseCentered")
      : feedback !== undefined && feedback !== "follow_prompt"
        ? view.text(poseFeedbackKey(feedback))
        : guideStage === "centering"
          ? view.text("poseCenterFace")
          : prompt;
  return html`
    <div
      class="adapter-option"
      data-presentation=${adapter.presentation ?? "default"}
      aria-busy=${String(running)}
    >
      ${
        state?.previewStream === undefined
          ? nothing
          : html`<div class="adapter-preview-frame">
              <video
                class="camera-preview"
                id=${previewId}
                width="640"
                height="480"
                autoplay
                muted
                playsinline
                aria-label=${view.text("adapterPreviewLabel", { method: copy.label })}
              ></video>
              ${
                activeLiveness
                  ? html`
                      ${renderPoseRing(view, state?.progress)}
                      ${
                        prompt === undefined
                          ? nothing
                          : html`<span class="liveness-overlay-prompt" aria-hidden="true"
                              >${guidance}</span
                            >`
                      }
                    `
                  : nothing
              }
            </div>`
      }
      ${
        running
          ? html`
              <div
                class=${activeLiveness ? "adapter-progress liveness-progress-panel" : "adapter-progress"}
                role="status"
                aria-live="polite"
              >
                ${
                  state.progress?.phase === "challenge" && prompt !== undefined
                    ? html`
                        <p
                          class=${activeLiveness ? "liveness-progress-label" : "adapter-challenge-label"}
                        >
                          ${challengeProgress}
                        </p>
                        ${renderChallengeProgress(
                          view,
                          state.progress,
                          activeLiveness,
                          challengeProgress,
                          guidance,
                        )}
                      `
                    : nothing
                }
                ${
                  activeLiveness
                    ? html`<p class="liveness-auto-capture">${view.text("livenessAutoCapture")}</p>`
                    : nothing
                }
                ${
                  activeLiveness && state.progress?.phase === "challenge"
                    ? nothing
                    : html`<p style="font-size:0.75rem">
                        ${adapterProgressMessage(state.progress, copy.label, view.localizer)}
                      </p>`
                }
              </div>
              <!--<button type="button" @click=${() => view.cancelMethodAdapter(step)}>
                  ${view.text("cancelMethod", { method: copy.label })}
                </button>-->
            `
          : html`
              <button
                class="primary"
                type="button"
                style="margin-left: var(--idq-capture-shell-padding); margin-right: var(--idq-capture-shell-padding);"
                @click=${() => void view.runMethodAdapter(step, adapter, previewId)}
              >
                ${copy.action}
              </button>
            `
      }
      ${
        state?.status === "error"
          ? html`<p
              class="error"
              style="margin-left: var(--idq-capture-shell-padding); margin-right: var(--idq-capture-shell-padding); font-size:0.75rem"
              role="alert"
            >
              ${state.message}
            </p>`
          : nothing
      }
    </div>
  `;
}

function renderChallengeProgress(
  view: AcquisitionViewContext,
  progress: CaptureMethodAdapterProgress,
  activeLiveness: boolean,
  challengeProgress: string | undefined,
  guidance: string | undefined,
): ElementTemplate {
  return activeLiveness
    ? html`
        <div
          class="liveness-segments"
          role="progressbar"
          aria-label=${view.text("livenessProgressLabel")}
          aria-live="off"
          aria-valuemin="1"
          aria-valuemax=${progress.total!}
          aria-valuenow=${progress.current!}
          aria-valuetext=${challengeProgress!}
        >
          ${Array.from(
            { length: progress.total! },
            (_, index) => html`
              <span
                class="liveness-segment"
                aria-hidden="true"
                data-state=${index + 1 < progress!.current! ? "complete" : index + 1 === progress!.current! ? "active" : "upcoming"}
              ></span>
            `,
          )}
        </div>
        <p class="visually-hidden">${guidance}</p>
      `
    : html`
        <progress
          aria-label=${view.text("livenessProgressLabel")}
          aria-live="off"
          value=${progress.current! - 1 + (progress.poseProgress ?? 0)}
          max=${progress.total!}
        ></progress>
        ${
          progress.poseProgress === undefined
            ? nothing
            : html`<progress
                class="liveness-pose-meter"
                aria-live="off"
                aria-label=${view.text("poseProgressLabel")}
                value=${progress.poseProgress}
                max="1"
              ></progress>`
        }
      `;
}
