import { html, nothing } from "lit";
import { LIVE_CAMERA_METHOD } from "../camera.js";
import { isDocumentArtefact } from "../document-capture.js";
import type { CaptureLocalizer, CaptureMessageKey, CaptureMessageValues } from "../localisation.js";
import { captureMethodAdapterCopy, type CaptureMethodAdapter } from "../method-adapter.js";
import type { CapturePlan, CapturePlanStep } from "../planner.js";
import { FILE_UPLOAD_METHOD } from "../upload.js";
import {
  captureInstruction,
  fallbackMessage,
  formatNumber,
  friendlyArtefact,
  methodAction,
  methodDescription,
} from "./copy.js";
import { isCameraBusy } from "./document.js";
import { documentIcon, renderLivenessIllustration } from "./icons.js";
import { captureProgress, planSteps, stepKey } from "./plan.js";
import type {
  ElementTemplate,
  StepAdapterState,
  StepCameraState,
  StepCompletion,
  StepStage,
  StepUploadState,
} from "./state.js";

/** Read-only presentation state and explicit callbacks; the element owns lifecycle effects. */
export interface JourneyViewContext {
  readonly activeStepIndex: number;
  readonly renderProcessing: () => ElementTemplate;
  readonly completedSteps: ReadonlyMap<string, StepCompletion>;
  readonly localizer: CaptureLocalizer;
  readonly text: (key: CaptureMessageKey, values?: CaptureMessageValues) => string;
  readonly documentChoices: Readonly<
    Record<string, { readonly options: readonly { readonly id: string; readonly label: string }[] }>
  >;
  readonly selectedDocuments: ReadonlyMap<string, string>;
  readonly choosingDocument: string | undefined;
  readonly stepStage: StepStage;
  readonly documentSelectionError: boolean;
  readonly savingDocument: boolean;
  readonly selectDocument: (step: CapturePlanStep, documentType: string) => Promise<void>;
  readonly adapterFor: (step: CapturePlanStep, method: string) => CaptureMethodAdapter | undefined;
  readonly chooseGuidedMethod: (step: CapturePlanStep, method: string) => void;
  readonly selectedMethods: ReadonlyMap<string, string>;
  readonly showCaptureTask: () => void;
  readonly startGuidedAdapter: (
    step: CapturePlanStep,
    adapter: CaptureMethodAdapter,
  ) => Promise<void>;
  readonly backFromPreparation: (step: CapturePlanStep) => void;
  readonly canChangeDocument: (step: CapturePlanStep) => boolean;
  readonly uploadStates: ReadonlyMap<string, StepUploadState>;
  readonly cameraStates: ReadonlyMap<string, StepCameraState>;
  readonly adapterStates: ReadonlyMap<string, StepAdapterState>;
  readonly capturingSteps: ReadonlySet<string>;
  readonly backToPreparation: (step: CapturePlanStep) => void;
  readonly renderCamera: (
    step: CapturePlanStep,
    idSuffix: string,
    state: StepCameraState | undefined,
    lockedByFile: boolean,
  ) => ElementTemplate;
  readonly renderMethodAdapter: (
    step: CapturePlanStep,
    idSuffix: string,
    adapter: CaptureMethodAdapter,
    state: StepAdapterState | undefined,
  ) => ElementTemplate;
  readonly renderFileUpload: (
    step: CapturePlanStep,
    idSuffix: string,
    uploadState: StepUploadState | undefined,
    lockedByCamera: boolean,
  ) => ElementTemplate;
  readonly selectMethod: (step: CapturePlanStep, method: string) => void;
  readonly resumeJourney: (plan: CapturePlan, showRecovery?: boolean) => void;
  readonly continueAfterConfirmation: (plan: CapturePlan) => void;
  readonly cancel: () => void;
  readonly documentBack: () => void;
}

export function renderGuidedPlan(
  view: JourneyViewContext,
  plan: CapturePlan,
  locale = view.localizer.locale,
): ElementTemplate {
  const steps = planSteps(plan);
  const step = steps[view.activeStepIndex];
  if (step === undefined) return view.renderProcessing();
  const activeRequirementIndex = plan.requirements.findIndex(
    (requirement) => requirement.key === step.requirementKey,
  );
  const completedRequirements = plan.requirements.filter((requirement) =>
    requirement.steps.every((candidate) => view.completedSteps.has(stepKey(candidate))),
  ).length;
  const totalRequirements = plan.requirements.length;
  const item = friendlyArtefact(step.artefact, view.localizer);
  return html`
    <div class="screen">
      <div class="journey-progress">
        <p>
          ${view.text("stepOf", {
            current: formatNumber(activeRequirementIndex + 1, locale),
            total: formatNumber(totalRequirements, locale),
          })}
        </p>
        <p>
          ${view.text("progressPercent", { percent: formatNumber(Math.round((completedRequirements / totalRequirements) * 100), locale) })}
        </p>
        <progress
          aria-label=${view.text("progressLabel")}
          value=${completedRequirements}
          max=${totalRequirements}
        ></progress>
      </div>
      ${
        step.fallbackCondition === undefined
          ? nothing
          : html`<p class="fallback" role="status">
              ${fallbackMessage(step.fallbackCondition, view.localizer)}
            </p>`
      }
      ${
        isDocumentArtefact(step.artefact) &&
        view.documentChoices[step.requirementKey] !== undefined &&
        (!view.selectedDocuments.has(step.requirementKey) ||
          view.choosingDocument === step.requirementKey)
          ? renderDocumentChoice(view, step)
          : view.stepStage === "method"
            ? renderMethodChoice(view, step, item)
            : view.stepStage === "preparation"
              ? renderPreparation(view, step, item)
              : renderCaptureTask(view, step, item)
      }
    </div>
  `;
}

export function renderDocumentChoice(
  view: JourneyViewContext,
  step: CapturePlanStep,
): ElementTemplate {
  if (
    view.documentChoices[step.requirementKey]?.options.length === 1 &&
    !view.documentSelectionError
  ) {
    return html`<p role="status">${view.text("savingDocument")}</p>
      ${renderJourneyExit(view)}`;
  }
  return html`
    <div>
      <h2>${view.text("chooseDocumentTitle")}</h2>
      <p class="screen-copy">${view.text("chooseDocumentBody")}</p>
    </div>
    <div class="method-list">
      ${view.documentChoices[step.requirementKey]!.options.map(
        (option) => html`
          <button
            class="method-card"
            type="button"
            ?disabled=${view.savingDocument}
            aria-pressed=${String(view.selectedDocuments.get(step.requirementKey) === option.id)}
            @click=${() => void view.selectDocument(step, option.id)}
          >
            <span class="method-icon" aria-hidden="true">${documentIcon()}</span>
            <span>${option.label}</span><span class="chevron" aria-hidden="true">›</span>
          </button>
        `,
      )}
    </div>
    ${view.savingDocument ? html`<p role="status">${view.text("savingDocument")}</p>` : nothing}
    ${view.documentSelectionError ? html`<p class="error" role="alert">${view.text("documentSelectionFailed")}</p>` : nothing}
    ${
      view.choosingDocument === step.requirementKey
        ? html`
            <button
              type="button"
              ?disabled=${view.savingDocument}
              @click=${() => view.documentBack()}
            >
              ${view.text("back")}
            </button>
          `
        : nothing
    }
    ${renderJourneyExit(view)}
  `;
}

export function renderMethodChoice(
  view: JourneyViewContext,
  step: CapturePlanStep,
  item: string,
): ElementTemplate {
  return html`
    <div>
      <p class="eyebrow">${view.text("chooseMethod")}</p>
      <h2>${view.text("chooseMethodTitle", { item })}</h2>
      <p class="screen-copy">${view.text("chooseMethodBody")}</p>
    </div>
    <div
      class="method-list"
      role="group"
      aria-label=${view.text("captureMethodsLabel", { artefact: item })}
    >
      ${step.methodOptions.map((method) => {
        const adapter = view.adapterFor(step, method);
        const copy =
          adapter === undefined
            ? undefined
            : captureMethodAdapterCopy(adapter, view.localizer.locale);
        return html`
          <button
            class="method-card"
            type="button"
            @click=${() => view.chooseGuidedMethod(step, method)}
          >
            <span class="method-icon" aria-hidden="true">
              ${adapter !== undefined ? "◎" : method === LIVE_CAMERA_METHOD ? "◉" : method === FILE_UPLOAD_METHOD ? "↑" : "→"}
            </span>
            <span class="method-copy">
              <strong>${copy?.action ?? methodAction(method, view.localizer)}</strong>
              <small>${copy?.description ?? methodDescription(method, view.localizer)}</small>
            </span>
            <span class="chevron" aria-hidden="true">›</span>
          </button>
        `;
      })}
    </div>
    ${renderJourneyExit(view)}
  `;
}

export function renderPreparation(
  view: JourneyViewContext,
  step: CapturePlanStep,
  item: string,
): ElementTemplate {
  const method = view.selectedMethods.get(stepKey(step)) ?? step.methodOptions[0]!;
  const adapter = view.adapterFor(step, method);
  const copy =
    adapter === undefined ? undefined : captureMethodAdapterCopy(adapter, view.localizer.locale);
  const activeLiveness = adapter?.presentation === "active_liveness";
  return html`
    ${
      activeLiveness
        ? renderLivenessIllustration()
        : html`<span class="hero-icon" aria-hidden="true"
            >${adapter !== undefined ? "◎" : method === LIVE_CAMERA_METHOD ? "◉" : "↑"}</span
          >`
    }
    <div>
      <p class="eyebrow">${view.text("prepare")}</p>
      <h2>${copy?.title ?? view.text("prepareTitle", { item })}</h2>
      <p class="screen-copy">
        ${copy?.preparation ?? (method === LIVE_CAMERA_METHOD ? view.text("cameraPreparation") : view.text("filePreparation"))}
      </p>
    </div>
    ${
      copy?.tips === undefined
        ? html`<ul class="benefits">
            <li>${view.text("tipLighting")}</li>
            <li>${view.text("tipReadable")}</li>
            <li>${view.text("tipPrivacy")}</li>
          </ul>`
        : copy.tips.length === 0
          ? nothing
          : html`<ul class="benefits">
              ${copy.tips.map((tip) => html`<li>${tip}</li>`)}
            </ul>`
    }
    <div class="journey-actions">
      <button
        class="primary"
        type="button"
        @click=${() =>
          adapter === undefined
            ? view.showCaptureTask()
            : void view.startGuidedAdapter(step, adapter)}
      >
        ${copy?.action ?? view.text("continue")}
      </button>
      <button type="button" @click=${() => view.backFromPreparation(step)}>
        ${
          view.canChangeDocument(step) || step.methodOptions.length === 1
            ? view.text("back")
            : view.text("chooseAnotherMethod")
        }
      </button>
    </div>
  `;
}

export function renderCaptureTask(
  view: JourneyViewContext,
  step: CapturePlanStep,
  item: string,
): ElementTemplate {
  const key = stepKey(step);
  const method = view.selectedMethods.get(key) ?? step.methodOptions[0]!;
  const uploadState = view.uploadStates.get(key);
  const cameraState = view.cameraStates.get(key);
  const adapter = view.adapterFor(step, method);
  const adapterState = view.adapterStates.get(key);
  const copy =
    adapter === undefined ? undefined : captureMethodAdapterCopy(adapter, view.localizer.locale);
  const busy =
    uploadState?.status === "uploading" ||
    isCameraBusy(cameraState) ||
    adapterState?.status === "running";
  if (method === LIVE_CAMERA_METHOD && adapter === undefined && isDocumentArtefact(step.artefact)) {
    const documentBusy = cameraState?.status === "uploading" || view.capturingSteps.has(key);
    return html`
      <div class="document-navigation">
        <button
          type="button"
          ?disabled=${documentBusy}
          @click=${() => view.backToPreparation(step)}
        >
          ${view.text("back")}
        </button>
        <button type="button" ?disabled=${documentBusy} @click=${() => view.cancel()}>
          ${view.text("cancel")}
        </button>
      </div>
      ${view.renderCamera(step, `guided-${view.activeStepIndex}`, cameraState, false)}
    `;
  }
  return html`
    <div class="document-navigation">
      <button
        type="button"
        ?disabled=${uploadState?.status === "uploading" || isCameraBusy(cameraState)}
        @click=${() => view.backToPreparation(step)}
      >
        ${view.text("back")}
      </button>
      <button class="quiet" type="button" ?disabled=${busy} @click=${() => view.cancel()}>
        ${view.text("cancel")}
      </button>
    </div>

    <div class="document-camera">
      <h2 class="document-instruction">
        ${copy?.instruction ?? captureInstruction(method, view.localizer)}
      </h2>
      <!--<h2>${view.text("captureTitle", { item })}</h2>
        <p class="screen-copy">
          ${copy?.instruction ?? captureInstruction(method, view.localizer)}
        </p>-->
    </div>
    <div class="capture-panel">
      ${
        adapter !== undefined
          ? view.renderMethodAdapter(step, `guided-${view.activeStepIndex}`, adapter, adapterState)
          : method === FILE_UPLOAD_METHOD
            ? view.renderFileUpload(step, `guided-${view.activeStepIndex}`, uploadState, false)
            : method === LIVE_CAMERA_METHOD
              ? view.renderCamera(step, `guided-${view.activeStepIndex}`, cameraState, false)
              : html`<button
                  class="primary"
                  type="button"
                  @click=${() => view.selectMethod(step, method)}
                >
                  ${methodAction(method, view.localizer)}
                </button>`
      }
    </div>
  `;
}

export function renderRecovery(view: JourneyViewContext, plan: CapturePlan): ElementTemplate {
  const progress = captureProgress(plan, view.completedSteps);
  return html`
    <div class="screen">
      <span class="hero-icon" aria-hidden="true">↻</span>
      <div>
        <p class="eyebrow">${view.text("recovery")}</p>
        <h2>${view.text("recoveryTitle")}</h2>
        <p class="screen-copy">
          ${view.text("recoveryBody", {
            completed: formatNumber(progress.completedSteps, view.localizer.locale),
            total: formatNumber(progress.totalSteps, view.localizer.locale),
          })}
        </p>
      </div>
      <div class="confirmation-card">
        <span class="state-icon" aria-hidden="true">✓</span>
        <p><strong>${view.text("savedProgress")}</strong>${view.text("savedProgressBody")}</p>
      </div>
      <div class="journey-actions">
        <button class="primary" type="button" @click=${() => view.resumeJourney(plan)}>
          ${progress.completedSteps === progress.totalSteps ? view.text("reviewAndFinish") : view.text("resumeCapture")}
        </button>
        <button class="quiet" type="button" @click=${() => view.cancel()}>
          ${view.text("cancel")}
        </button>
      </div>
    </div>
  `;
}

export function renderConfirmation(view: JourneyViewContext, plan: CapturePlan): ElementTemplate {
  const steps = planSteps(plan);
  const step = steps[view.activeStepIndex];
  const item =
    step === undefined
      ? view.text("requiredItem")
      : friendlyArtefact(step.artefact, view.localizer);
  const isLast = steps.every((candidate) => view.completedSteps.has(stepKey(candidate)));
  const progress = captureProgress(plan, view.completedSteps);
  const activeRequirementIndex = plan.requirements.findIndex(
    (requirement) => requirement.key === step?.requirementKey,
  );
  return html`
    <div class="screen" aria-live="polite">
      <div class="journey-progress">
        <p>
          ${view.text("stepOf", {
            current: formatNumber(
              Math.min(activeRequirementIndex + 1, progress.totalSteps),
              view.localizer.locale,
            ),
            total: formatNumber(progress.totalSteps, view.localizer.locale),
          })}
        </p>
        <p>
          ${view.text("progressPercent", {
            percent: formatNumber(
              Math.round((progress.completedSteps / progress.totalSteps) * 100),
              view.localizer.locale,
            ),
          })}
        </p>
        <progress
          aria-label=${view.text("progressLabel")}
          value=${progress.completedSteps}
          max=${progress.totalSteps}
        ></progress>
      </div>
      <span class="hero-icon" aria-hidden="true">✓</span>
      <div>
        <p class="eyebrow">${view.text("saved")}</p>
        <h2>${view.text("confirmationTitle", { item })}</h2>
        <p class="screen-copy">${view.text("confirmationBody")}</p>
      </div>
      <div class="confirmation-card">
        <span class="state-icon" aria-hidden="true">✓</span>
        <p><strong>${item}</strong>${view.text("securelyUploaded")}</p>
      </div>
      <div class="journey-actions">
        <button class="primary" type="button" @click=${() => view.continueAfterConfirmation(plan)}>
          ${isLast ? view.text("reviewAndFinish") : view.text("nextItem")}
        </button>
        <button class="quiet" type="button" @click=${() => view.cancel()}>
          ${view.text("cancel")}
        </button>
      </div>
    </div>
  `;
}

export function renderJourneyExit(view: JourneyViewContext): ElementTemplate {
  return html`<button class="quiet" type="button" @click=${() => view.cancel()}>
    ${view.text("cancel")}
  </button>`;
}
