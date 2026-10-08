import type {
  CaptureJourneyAction,
  CaptureJourneyEventType,
  CaptureJourneyScreen,
  CaptureRealtimeEvent,
  EvidenceUpload,
  SubjectResponseAction,
} from "@idenqa/sdk";
import { LitElement, html, type PropertyValues } from "lit";
import {
  LIVE_CAMERA_METHOD,
  cameraFacingMode,
  captureCameraFrame,
  startCamera,
  stopCamera,
  type CameraFrame,
} from "./camera.js";
import {
  captureCorrectedDocumentFrame,
  createDocumentFrameObserver,
  isDocumentArtefact,
  normalizeCaptureDocumentCaptureOptions,
  type NormalizedCaptureDocumentCaptureOptions,
} from "./document-capture.js";
import { createDocumentCaptureGate, type DocumentDetection } from "./document-detection.js";
import {
  renderCamera,
  renderFileUpload,
  renderMethodAdapter,
  type AcquisitionViewContext,
} from "./element/acquisition-view.js";
import {
  browserLocale,
  cameraErrorMessage,
  mergeCaptureMessageCatalogues,
} from "./element/copy.js";
import { renderCountryJourney, type CountryViewContext } from "./element/country-view.js";
import { countryJourneyNoticeMatches, normalizeCountryOptions } from "./element/country.js";
import { documentStateSignature, isCameraActive } from "./element/document.js";
import {
  renderConfirmation,
  renderGuidedPlan,
  renderRecovery,
  type JourneyViewContext,
} from "./element/journey-view.js";
import {
  renderIntroduction,
  renderNotice,
  renderSecuredBy,
  type NoticeViewContext,
} from "./element/notice-view.js";
import { captureProgress, planSteps, stepKey } from "./element/plan.js";
import type {
  ComponentFlowState,
  CountryJourneyPhase,
  JourneyPhase,
  StepAdapterState,
  StepCameraState,
  StepCompletion,
  StepDocumentObservation,
  StepDocumentState,
  StepStage,
  StepUploadState,
} from "./element/state.js";
import {
  renderAuthoritativeOutcome,
  renderAuthorityBlocked,
  renderCancelled,
  renderComplete,
  renderError,
  renderLoading,
  renderNoFlow,
  renderProcessing,
  renderRefused,
  type StatusViewContext,
} from "./element/status-view.js";
import { captureElementStyles } from "./element/styles.js";
import type {
  CaptureCompleteDetail,
  CaptureCountryJourneyOptions,
  CaptureCountryOption,
  CaptureElementStartOptions,
  CaptureEvidenceAcceptedDetail,
  CaptureFlowErrorDetail,
  CaptureMethodSelectDetail,
  CaptureProgressDetail,
  CaptureRealtimeDetail,
  CaptureSubjectResponseDetail,
} from "./element/types.js";
import {
  applyCaptureExperienceTheme,
  captureExperiencePresentation,
  type CaptureExperiencePresentation,
} from "./experience.js";
import {
  createCaptureFlowController,
  isActiveCaptureFlowSnapshot,
  type CaptureActiveFlowSnapshot,
  type CaptureFlowController,
  type CaptureFlowSnapshot,
} from "./flow.js";
import { installCaptureFont } from "./font.js";
import {
  createCaptureLocalizer,
  type CaptureLocalizer,
  type CaptureMessageKey,
  type CaptureMessageValues,
} from "./localisation.js";
import {
  CaptureMethodAdapterError,
  captureMethodAdapterCopy,
  findCaptureMethodAdapter,
  normalizeCaptureMethodProgress,
  requireCaptureMethodAdapter,
  validateCaptureMethodAdapters,
  type CaptureMethodAdapter,
  type CaptureMethodAdapterContext,
} from "./method-adapter.js";
import type { CapturePlan, CapturePlanStep } from "./planner.js";
import { FILE_UPLOAD_METHOD, fileUploadPolicy } from "./upload.js";

export type {
  CaptureCompleteDetail,
  CaptureCountryJourneyNotice,
  CaptureCountryJourneyOptions,
  CaptureCountryOption,
  CaptureElementStartOptions,
  CaptureEvidenceAcceptedDetail,
  CaptureFlowErrorDetail,
  CaptureMethodSelectDetail,
  CaptureProgressDetail,
  CaptureRealtimeDetail,
  CaptureSubjectResponseDetail,
} from "./element/types.js";

export const IDENQA_CAPTURE_TAG_NAME = "idenqa-capture";

export class IdenqaCaptureElement extends LitElement {
  static override styles = captureElementStyles;

  /** Signed portable-experience presentation applied to this journey, if any. */
  get experience(): CaptureExperiencePresentation | undefined {
    return this.#experience;
  }

  readonly #selectedMethods = new Map<string, string>();
  #documentChoices: Readonly<
    Record<string, { readonly options: readonly { readonly id: string; readonly label: string }[] }>
  > = {};
  readonly #selectedDocuments = new Map<string, string>();
  #choosingDocument: string | undefined;
  #savingDocument = false;
  #documentSelectionError = false;
  readonly #uploadStates = new Map<string, StepUploadState>();
  readonly #cameraStates = new Map<string, StepCameraState>();
  readonly #adapterStates = new Map<string, StepAdapterState>();
  readonly #completedSteps = new Map<string, StepCompletion>();
  #methodAdapters: readonly CaptureMethodAdapter[] = [];
  #flowController: CaptureFlowController | undefined;
  #flowSnapshot: CaptureFlowSnapshot | undefined;
  #flowState: ComponentFlowState = "idle";
  #flowAbortController: AbortController | undefined;
  #countryJourney: CaptureCountryJourneyOptions | undefined;
  #countryJourneyPhase: CountryJourneyPhase = "intro";
  #countryQuery = "";
  #countrySelecting: string | undefined;
  #countrySelectionError = false;
  #countryAbortController: AbortController | undefined;
  #responseError = false;
  #captureCompleteDispatched = false;
  #outcomePolling = false;
  #localizer: CaptureLocalizer = createCaptureLocalizer(browserLocale());
  #experience: CaptureExperiencePresentation | undefined;
  #journeyPhase: JourneyPhase = "intro";
  #stepStage: StepStage = "method";
  #activeStepIndex = 0;
  #lastJourneyScreen: CaptureJourneyScreen | undefined;
  readonly #documentStates = new Map<string, StepDocumentState>();
  readonly #documentObservations = new Map<string, StepDocumentObservation>();
  readonly #capturingSteps = new Set<string>();
  #documentCapture: NormalizedCaptureDocumentCaptureOptions =
    normalizeCaptureDocumentCaptureOptions();

  /**
   * Presents privacy and country selection for a document flow before a
   * verification session exists. Flows without documents use start directly.
   * Selecting a country delegates session creation to the host,
   * verifies the returned notice, records the accepted response, and then
   * continues with the ordinary immutable capture flow.
   */
  startCountryJourney(options: CaptureCountryJourneyOptions): void {
    const countries = normalizeCountryOptions(options.countries);
    if (
      options.captureItemCount !== undefined &&
      (!Number.isInteger(options.captureItemCount) ||
        options.captureItemCount < 1 ||
        options.captureItemCount > 99)
    ) {
      throw new TypeError("Capture item count must be an integer between 1 and 99.");
    }
    this.cancel();
    this.#countryJourney = { ...options, countries };
    this.#countryJourneyPhase = "intro";
    this.#countryQuery = "";
    this.#countrySelecting = undefined;
    this.#countrySelectionError = false;
    this.#localizer = createCaptureLocalizer(browserLocale(), options.messageCatalogue);
    this.#flowState = "idle";
    this.requestUpdate();
  }

  async start(options: CaptureElementStartOptions): Promise<CaptureFlowSnapshot> {
    const {
      messageCatalogue = {},
      experience,
      expectedVerificationId,
      methodAdapters = [],
      documentCapture,
      ...flowOptions
    } = options;
    const presentation =
      experience === undefined ? undefined : captureExperiencePresentation(experience);
    this.#experience = presentation;
    applyCaptureExperienceTheme(this, presentation?.theme);
    const catalogue = mergeCaptureMessageCatalogues(
      presentation?.messageCatalogue,
      messageCatalogue,
    );
    const documentCaptureOptions = normalizeCaptureDocumentCaptureOptions(documentCapture);
    this.#localizer = createCaptureLocalizer(presentation?.locale ?? browserLocale(), catalogue);
    this.#documentCapture = documentCaptureOptions;
    this.#documentChoices = {};
    this.#selectedDocuments.clear();
    this.#choosingDocument = undefined;
    this.#savingDocument = false;
    this.#documentSelectionError = false;
    this.#clearCameraStates();
    this.#clearAdapterStates();
    this.#flowAbortController?.abort();
    this.#countryAbortController?.abort();
    this.#countryAbortController = undefined;
    this.#countryJourney = undefined;
    const abortController = new AbortController();
    this.#flowAbortController = abortController;
    this.#flowController = createCaptureFlowController(flowOptions);
    this.#methodAdapters = [];
    this.#flowSnapshot = undefined;
    this.#flowState = "loading";
    this.#journeyPhase = "intro";
    this.#stepStage = "method";
    this.#activeStepIndex = 0;
    this.#lastJourneyScreen = undefined;
    this.#responseError = false;
    this.#clearUploadStates();
    this.#cameraStates.clear();
    this.#completedSteps.clear();
    this.#captureCompleteDispatched = false;
    this.#outcomePolling = false;
    this.requestUpdate();
    try {
      this.#methodAdapters = validateCaptureMethodAdapters(methodAdapters);
      const snapshot = await this.#flowController.load(abortController.signal);
      if (
        expectedVerificationId !== undefined &&
        snapshot.outcome.verificationId !== expectedVerificationId
      )
        throw new Error("Capture credential does not match the expected linked session.");
      if (this.#flowAbortController !== abortController) return snapshot;
      this.#flowSnapshot = snapshot;
      this.#flowState = "ready";
      if (!isActiveCaptureFlowSnapshot(snapshot)) {
        if (snapshot.status === "processing" || snapshot.status === "action_required") {
          this.#pollAuthoritativeOutcome();
        } else {
          this.#dropFlowController();
        }
      } else {
        this.#localizer = createCaptureLocalizer(
          this.#experience?.locale ?? snapshot.authoritySnapshot.notice.locale,
          catalogue,
        );
        this.#assertMethodAdapters(snapshot.plan);
        this.#restoreRecoveredProgress(snapshot);
        if (snapshot.status === "authority_blocked" || snapshot.status === "refused") {
          this.#dropFlowController();
          this.requestUpdate();
          return snapshot;
        }
        void this.#flowController
          .observe(
            (event) => this.#dispatchRealtimeEvent(event),
            (recovered) => {
              if (abortController.signal.aborted) return;
              this.#flowSnapshot = recovered;
              if (isActiveCaptureFlowSnapshot(recovered)) {
                this.#restoreRecoveredProgress(recovered);
              } else if (
                recovered.status === "processing" ||
                recovered.status === "action_required"
              ) {
                this.#pollAuthoritativeOutcome();
              } else {
                this.#dropFlowController();
              }
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
    this.#countryAbortController?.abort();
    this.#countryAbortController = undefined;
    this.#countryJourney = undefined;
    this.#dropFlowController();
    this.#flowSnapshot = undefined;
    this.#flowState = "cancelled";
    this.#journeyPhase = "intro";
    this.#responseError = false;
    this.#clearUploadStates();
    this.#clearCameraStates();
    this.#clearAdapterStates();
    this.#methodAdapters = [];
    this.#completedSteps.clear();
    this.#captureCompleteDispatched = false;
    this.requestUpdate();
  }

  override disconnectedCallback(): void {
    this.cancel();
    super.disconnectedCallback();
  }

  protected override updated(changedProperties: PropertyValues<this>): void {
    super.updated(changedProperties);
    const screen = this.#currentJourneyScreen();
    if (screen === undefined || screen === this.#lastJourneyScreen) return;
    this.#lastJourneyScreen = screen;
    this.#recordJourney("screen_viewed", screen);
    const stateEvent: Partial<Record<CaptureJourneyScreen, CaptureJourneyEventType>> = {
      recovery: "recovery_started",
      processing: "processing_started",
      completion: "completion_shown",
      error: "error_shown",
    };
    const eventType = stateEvent[screen];
    if (eventType !== undefined) this.#recordJourney(eventType, screen);
  }

  protected override render() {
    return html`
      <section
        class=${this.#isDocumentCameraScreen() || this.#isLiveCameraScreen() ? "shell document-camera-shell" : "shell"}
        aria-labelledby="capture-title"
        lang=${this.#localizer.locale}
        dir=${this.#localizer.direction}
      >
        <h1 class="visually-hidden" id="capture-title">${this.#text("identityVerification")}</h1>
        ${
          this.#countryJourney !== undefined
            ? renderCountryJourney(this.#countryView())
            : this.#flowState === "loading"
              ? renderLoading(this.#statusView())
              : this.#flowState === "error"
                ? renderError(this.#statusView())
                : this.#flowState === "cancelled"
                  ? renderCancelled(this.#statusView())
                  : this.#flowSnapshot !== undefined
                    ? this.#renderFlow(this.#flowSnapshot)
                    : renderNoFlow(this.#statusView())
        }
      </section>
    `;
  }

  async #selectCountry(country: CaptureCountryOption): Promise<void> {
    const journey = this.#countryJourney;
    if (journey === undefined || this.#countrySelecting !== undefined) return;
    const controller = new AbortController();
    this.#countryAbortController?.abort();
    this.#countryAbortController = controller;
    this.#countrySelecting = country.code;
    this.#countrySelectionError = false;
    this.requestUpdate();
    try {
      const options = await journey.resolve(country, controller.signal);
      if (controller.signal.aborted || this.#countryAbortController !== controller) return;
      const snapshot = await this.start(options);
      if (
        !isActiveCaptureFlowSnapshot(snapshot) ||
        snapshot.status !== "notice_required" ||
        !countryJourneyNoticeMatches(journey.notice, snapshot)
      ) {
        throw new Error("The country-bound session did not return the accepted notice.");
      }
      await this.#respond(journey.notice.consentRequired ? "consent" : "acknowledge");
    } catch (error) {
      if (controller.signal.aborted) return;
      this.#countryAbortController = undefined;
      this.#countrySelecting = undefined;
      this.#countrySelectionError = true;
      this.requestUpdate();
      void error;
    }
  }

  #renderFlow(flow: CaptureFlowSnapshot) {
    if (!isActiveCaptureFlowSnapshot(flow))
      return renderAuthoritativeOutcome(this.#statusView(), flow);
    if (this.#journeyPhase === "intro") return renderIntroduction(this.#noticeView(), flow);
    if (flow.status === "refused") return renderRefused(this.#statusView());
    if (flow.status === "authority_blocked") return renderAuthorityBlocked(this.#statusView());
    if (this.#journeyPhase === "notice" || flow.status === "notice_required") {
      return renderNotice(this.#noticeView(), flow);
    }
    if (this.#journeyPhase === "recovery") return renderRecovery(this.#journeyView(), flow.plan);
    if (this.#journeyPhase === "confirmation")
      return renderConfirmation(this.#journeyView(), flow.plan);
    if (this.#journeyPhase === "processing") return renderProcessing(this.#statusView());
    if (this.#journeyPhase === "complete") return renderComplete(this.#statusView());
    return renderGuidedPlan(this.#journeyView(), flow.plan, flow.authoritySnapshot.notice.locale);
  }

  #isDocumentCameraScreen(): boolean {
    const flow = this.#flowSnapshot;
    if (
      flow === undefined ||
      !isActiveCaptureFlowSnapshot(flow) ||
      flow.status !== "capture_ready" ||
      this.#journeyPhase !== "capture" ||
      this.#stepStage !== "capture"
    )
      return false;
    const step = planSteps(flow.plan)[this.#activeStepIndex];
    return (
      step !== undefined &&
      isDocumentArtefact(step.artefact) &&
      (this.#selectedMethods.get(stepKey(step)) ?? step.methodOptions[0]) === LIVE_CAMERA_METHOD
    );
  }

  #isLiveCameraScreen(): boolean {
    const flow = this.#flowSnapshot;
    if (
      flow === undefined ||
      !isActiveCaptureFlowSnapshot(flow) ||
      flow.status !== "capture_ready" ||
      this.#journeyPhase !== "capture" ||
      this.#stepStage !== "capture"
    )
      return false;
    const step = planSteps(flow.plan)[this.#activeStepIndex];
    if (step === undefined) return false;
    const method = this.#selectedMethods.get(stepKey(step)) ?? step.methodOptions[0];
    return (
      method === LIVE_CAMERA_METHOD &&
      step.evidenceType === "idenqa.evidence.selfie_image" &&
      step.artefact === "idenqa.artefact.selfie_image" &&
      this.#adapterFor(step, method)?.presentation === "active_liveness"
    );
  }

  async #selectDocument(step: CapturePlanStep, documentType: string): Promise<void> {
    if (this.#savingDocument || this.#flowController === undefined) return;
    const controller = this.#flowController;
    const abort = this.#flowAbortController;
    this.#savingDocument = true;
    this.#documentSelectionError = false;
    this.requestUpdate();
    try {
      const snapshot = await controller.selectDocument(
        step.requirementKey,
        documentType,
        abort?.signal,
      );
      if (this.#flowController !== controller || abort?.signal.aborted) return;
      this.#flowSnapshot = snapshot;
      this.#restoreRecoveredProgress(snapshot);
      this.#choosingDocument = undefined;
      if (isActiveCaptureFlowSnapshot(snapshot)) {
        this.#enterNextIncompleteStep(snapshot.plan, 0);
      } else if (snapshot.status === "processing" || snapshot.status === "action_required") {
        this.#pollAuthoritativeOutcome();
      }
      const confirmedType = isActiveCaptureFlowSnapshot(snapshot)
        ? snapshot.session.documentSelections?.[step.requirementKey]
        : undefined;
      if (confirmedType !== undefined) {
        this.dispatchEvent(
          new CustomEvent("idenqa:document-selected", {
            bubbles: true,
            composed: true,
            detail: { requirementKey: step.requirementKey, documentType: confirmedType },
          }),
        );
      }
    } catch {
      if (this.#flowController === controller && !abort?.signal.aborted)
        this.#documentSelectionError = true;
    } finally {
      if (this.#flowController === controller) {
        this.#savingDocument = false;
        this.requestUpdate();
      }
    }
  }

  #documentItem(step: CapturePlanStep): string {
    const selected = this.#documentChoices[step.requirementKey]?.options.find(
      (option) => option.id === this.#selectedDocuments.get(step.requirementKey),
    );
    return this.#text(
      step.artefact.endsWith("document_back") ? "documentBackOf" : "documentFrontOf",
      {
        document: selected?.label ?? this.#text("identityDocument"),
      },
    );
  }

  #beginJourney(flow: CaptureActiveFlowSnapshot): void {
    this.#recordJourney("action_selected", "intro", "continue");
    if (flow.status === "notice_required") {
      this.#journeyPhase = "notice";
    } else if (flow.status === "capture_ready") {
      this.#resumeJourney(flow.plan, true);
    } else if (flow.status === "refused") {
      this.#journeyPhase = "complete";
    }
    this.requestUpdate();
  }

  #resumeJourney(plan: CapturePlan, showRecovery = false): void {
    const progress = captureProgress(plan, this.#completedSteps);
    if (showRecovery && this.#completedSteps.size > 0) {
      this.#journeyPhase = "recovery";
      this.requestUpdate();
      return;
    }
    if (progress.completedSteps === progress.totalSteps) {
      this.#journeyPhase = "processing";
      this.#pollAuthoritativeOutcome();
      this.requestUpdate();
      return;
    }
    this.#enterNextIncompleteStep(plan, 0);
  }

  #enterNextIncompleteStep(plan: CapturePlan, startIndex: number): void {
    const steps = planSteps(plan);
    const relativeIndex = steps
      .slice(startIndex)
      .findIndex((step) => !this.#completedSteps.has(stepKey(step)));
    if (relativeIndex < 0) {
      this.#journeyPhase = "processing";
      this.#pollAuthoritativeOutcome();
      this.requestUpdate();
      return;
    }
    this.#activeStepIndex = startIndex + relativeIndex;
    const step = steps[this.#activeStepIndex]!;
    const key = stepKey(step);
    this.#selectedMethods.set(key, step.methodOptions[0]!);
    this.#stepStage =
      isDocumentArtefact(step.artefact) &&
      (this.#documentChoices[step.requirementKey]?.options.length ?? 0) > 1 &&
      step.methodOptions.length > 1 &&
      !steps.some(
        (candidate) =>
          candidate.requirementKey === step.requirementKey &&
          this.#completedSteps.has(stepKey(candidate)),
      )
        ? "method"
        : "preparation";
    this.#journeyPhase = "capture";
    const options = this.#documentChoices[step.requirementKey]?.options;
    if (options?.length === 1 && !this.#selectedDocuments.has(step.requirementKey)) {
      void this.#selectDocument(step, options[0]!.id);
    }
    this.requestUpdate();
  }

  #chooseGuidedMethod(step: CapturePlanStep, method: string): void {
    this.#selectMethod(step, method);
    this.#recordStepJourney("action_selected", "method", step, "select_method", method);
    this.#stepStage = "preparation";
    this.requestUpdate();
  }

  #canChangeDocument(step: CapturePlanStep): boolean {
    const flow = this.#flowSnapshot;
    return (
      isDocumentArtefact(step.artefact) &&
      (this.#documentChoices[step.requirementKey]?.options.length ?? 0) > 1 &&
      flow !== undefined &&
      isActiveCaptureFlowSnapshot(flow) &&
      !planSteps(flow.plan).some(
        (candidate) =>
          candidate.requirementKey === step.requirementKey &&
          this.#completedSteps.has(stepKey(candidate)),
      )
    );
  }

  #backFromPreparation(step: CapturePlanStep): void {
    this.#recordStepJourney("navigation_back", "preparation", step, "back");
    if (this.#canChangeDocument(step)) {
      this.#choosingDocument = step.requirementKey;
      this.#documentSelectionError = false;
    } else if (step.methodOptions.length > 1) {
      this.#selectedMethods.delete(stepKey(step));
      this.#stepStage = "method";
    } else {
      this.#journeyPhase = "notice";
    }
    this.requestUpdate();
  }

  #showCaptureTask(): void {
    const step = this.#activeJourneyStep();
    if (step !== undefined) {
      this.#recordStepJourney("capture_started", "preparation", step, "start_capture");
    }
    this.#stepStage = "capture";
    this.requestUpdate();
  }

  async #startGuidedAdapter(step: CapturePlanStep, adapter: CaptureMethodAdapter): Promise<void> {
    this.#stepStage = "capture";
    this.requestUpdate();
    await this.updateComplete;
    await this.#runMethodAdapter(
      step,
      adapter,
      `idq-adapter-preview-guided-${this.#activeStepIndex}`,
    );
  }

  #backToPreparation(step: CapturePlanStep): void {
    this.#recordStepJourney("navigation_back", "capture", step, "back");
    const key = stepKey(step);
    this.#clearUploadState(key);
    this.#clearCameraState(key);
    this.#clearAdapterState(key);
    this.#stepStage = "preparation";
    this.requestUpdate();
  }

  #continueAfterConfirmation(plan: CapturePlan): void {
    this.#enterNextIncompleteStep(plan, this.#activeStepIndex + 1);
  }

  #adapterFor(step: CapturePlanStep, method: string): CaptureMethodAdapter | undefined {
    const plan =
      this.#flowSnapshot !== undefined && isActiveCaptureFlowSnapshot(this.#flowSnapshot)
        ? this.#flowSnapshot.plan
        : undefined;
    if (plan === undefined) return undefined;
    return findCaptureMethodAdapter(this.#methodAdapters, {
      verificationId: plan.verificationId,
      requirementKey: step.requirementKey,
      evidenceType: step.evidenceType,
      artefact: step.artefact,
      acquisitionMethod: method,
      ...(step.fallbackCondition === undefined
        ? {}
        : { fallbackCondition: step.fallbackCondition }),
    });
  }

  #assertMethodAdapters(plan: CapturePlan): void {
    for (const step of planSteps(plan)) {
      for (const method of step.methodOptions) {
        const context = {
          verificationId: plan.verificationId,
          requirementKey: step.requirementKey,
          evidenceType: step.evidenceType,
          artefact: step.artefact,
          acquisitionMethod: method,
          ...(step.fallbackCondition === undefined
            ? {}
            : { fallbackCondition: step.fallbackCondition }),
        };
        const adapter = findCaptureMethodAdapter(this.#methodAdapters, context);
        if (adapter !== undefined) captureMethodAdapterCopy(adapter, this.#localizer.locale);
        if (
          method === LIVE_CAMERA_METHOD &&
          step.evidenceType === "idenqa.evidence.selfie_image" &&
          step.artefact === "idenqa.artefact.selfie_image"
        ) {
          const selfieAdapter = requireCaptureMethodAdapter(this.#methodAdapters, context);
          if (selfieAdapter.presentation !== "active_liveness") {
            throw new CaptureMethodAdapterError(
              "CAPTURE_METHOD_ADAPTER_INVALID",
              "Live selfie capture requires the guided active-liveness presentation.",
            );
          }
          continue;
        }
        if (
          method !== FILE_UPLOAD_METHOD &&
          method !== LIVE_CAMERA_METHOD &&
          adapter === undefined
        ) {
          requireCaptureMethodAdapter(this.#methodAdapters, context);
        }
      }
    }
  }

  async #runMethodAdapter(
    step: CapturePlanStep,
    adapter: CaptureMethodAdapter,
    previewId: string,
  ): Promise<void> {
    const flowController = this.#flowController;
    const parentSignal = this.#flowAbortController?.signal;
    const plan =
      this.#flowSnapshot !== undefined && isActiveCaptureFlowSnapshot(this.#flowSnapshot)
        ? this.#flowSnapshot.plan
        : undefined;
    const key = stepKey(step);
    if (
      flowController === undefined ||
      parentSignal === undefined ||
      plan === undefined ||
      parentSignal.aborted ||
      this.#completedSteps.has(key) ||
      this.#adapterStates.get(key)?.status === "running"
    ) {
      return;
    }
    this.#clearUploadState(key);
    this.#clearCameraState(key);
    const controller = new AbortController();
    const parentAborted = () => controller.abort(parentSignal.reason);
    parentSignal.addEventListener("abort", parentAborted, { once: true });
    this.#adapterStates.set(key, { status: "running", controller });
    this.#selectMethod(step, adapter.method);
    this.#reportStep(step, adapter.method, "started");
    this.requestUpdate();
    const context: CaptureMethodAdapterContext = {
      verificationId: plan.verificationId,
      requirementKey: step.requirementKey,
      evidenceType: step.evidenceType,
      artefact: step.artefact,
      acquisitionMethod: adapter.method,
      ...(step.fallbackCondition === undefined
        ? {}
        : { fallbackCondition: step.fallbackCondition }),
      locale: this.#localizer.locale,
      signal: controller.signal,
    };
    try {
      await adapter.acquire(context, {
        update: (progress) => {
          const current = this.#adapterStates.get(key);
          if (current?.controller !== controller || controller.signal.aborted) return;
          this.#adapterStates.set(key, {
            ...current,
            progress: normalizeCaptureMethodProgress(progress),
          });
          this.requestUpdate();
        },
        setPreview: (stream) => this.#setAdapterPreview(key, controller, previewId, stream),
      });
      if (controller.signal.aborted) return;
      const snapshot = await flowController.refresh(controller.signal);
      if (controller.signal.aborted) return;
      this.#flowSnapshot = snapshot;
      this.#restoreRecoveredProgress(snapshot);
      if (!this.#completedSteps.has(key)) {
        throw new CaptureMethodAdapterError(
          "CAPTURE_METHOD_ADAPTER_UNCONFIRMED",
          "Core did not confirm this acquisition. Check the integration and retry.",
        );
      }
      this.#releaseAdapterPreview(key, controller, previewId);
      this.#adapterStates.delete(key);
    } catch (error) {
      if (parentSignal.aborted) return;
      if (controller.signal.aborted) {
        this.#adapterStates.delete(key);
        this.requestUpdate();
        return;
      }
      const unconfirmed =
        error instanceof CaptureMethodAdapterError &&
        error.code === "CAPTURE_METHOD_ADAPTER_UNCONFIRMED";
      this.#releaseAdapterPreview(key, controller, previewId);
      this.#adapterStates.set(key, {
        status: "error",
        controller,
        message: unconfirmed ? this.#text("adapterUnconfirmed") : this.#text("adapterFailed"),
      });
      this.#reportStep(
        step,
        adapter.method,
        "failed",
        unconfirmed ? "adapter_completion_unconfirmed" : "adapter_failed",
      );
      if (!unconfirmed) {
        try {
          this.#flowSnapshot = flowController.captureFailed(step);
          this.#adapterStates.delete(key);
          this.#selectedMethods.delete(key);
          if (this.#journeyPhase === "capture") {
            const fallbackStep = planSteps(this.#flowSnapshot.plan)[this.#activeStepIndex];
            if (fallbackStep !== undefined && fallbackStep.methodOptions.length === 1) {
              this.#selectedMethods.set(stepKey(fallbackStep), fallbackStep.methodOptions[0]!);
              this.#stepStage = "preparation";
            } else {
              this.#stepStage = "method";
            }
          }
          return;
        } catch {
          // No usable capture_failed fallback exists; keep the adapter retry local.
        }
      }
      this.#dispatchFlowError();
    } finally {
      parentSignal.removeEventListener("abort", parentAborted);
      this.requestUpdate();
    }
  }

  async #setAdapterPreview(
    key: string,
    controller: AbortController,
    previewId: string,
    stream: MediaStream | undefined,
  ): Promise<void> {
    const current = this.#adapterStates.get(key);
    if (current?.controller !== controller || controller.signal.aborted) return;
    if (stream !== undefined && !(stream instanceof MediaStream)) {
      throw new CaptureMethodAdapterError(
        "CAPTURE_METHOD_ADAPTER_INVALID",
        "The acquisition adapter preview must be a MediaStream.",
      );
    }
    if (stream === undefined && current.previewStream !== undefined) {
      const currentVideo = this.renderRoot.querySelector<HTMLVideoElement>(`#${previewId}`);
      if (currentVideo !== null) currentVideo.srcObject = null;
    }
    this.#adapterStates.set(
      key,
      stream === undefined
        ? {
            status: current.status,
            controller,
            ...(current.progress === undefined ? {} : { progress: current.progress }),
            ...(current.message === undefined ? {} : { message: current.message }),
          }
        : { ...current, previewStream: stream },
    );
    this.requestUpdate();
    await this.updateComplete;
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${previewId}`);
    if (stream === undefined) {
      if (video !== null) video.srcObject = null;
      return;
    }
    if (video === null || controller.signal.aborted) return;
    video.srcObject = stream;
    await video.play();
  }

  #releaseAdapterPreview(key: string, controller: AbortController, previewId: string): void {
    const current = this.#adapterStates.get(key);
    if (current?.controller !== controller || current.previewStream === undefined) return;
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${previewId}`);
    if (video !== null) video.srcObject = null;
    stopCamera(current.previewStream);
  }

  #cancelMethodAdapter(step: CapturePlanStep): void {
    const key = stepKey(step);
    const state = this.#adapterStates.get(key);
    if (state === undefined) return;
    state.controller.abort(new DOMException("The acquisition method was cancelled.", "AbortError"));
    if (state.previewStream !== undefined) stopCamera(state.previewStream);
    this.#adapterStates.delete(key);
    const method = this.#selectedMethods.get(key) ?? step.methodOptions[0]!;
    this.#reportStep(step, method, "cancelled", "subject_cancelled");
    this.requestUpdate();
  }

  #clearAdapterState(key: string): void {
    const state = this.#adapterStates.get(key);
    if (state === undefined) return;
    state.controller.abort(new DOMException("The acquisition method was stopped.", "AbortError"));
    if (state.previewStream !== undefined) stopCamera(state.previewStream);
    this.#adapterStates.delete(key);
  }

  #clearAdapterStates(): void {
    for (const key of [...this.#adapterStates.keys()]) this.#clearAdapterState(key);
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
    if (
      isCameraActive(this.#cameraStates.get(key)) ||
      this.#adapterStates.get(key)?.status === "running"
    ) {
      return;
    }
    this.#clearCameraState(key);
    this.#clearUploadState(key);
    this.#selectMethod(step, FILE_UPLOAD_METHOD);
    this.#uploadStates.set(key, {
      status: "reviewing",
      body,
      previewUrl: URL.createObjectURL(body),
    });
    this.requestUpdate();
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
    const current = this.#uploadStates.get(key);
    this.#uploadStates.set(key, {
      status: "uploading",
      body,
      ...(current?.previewUrl === undefined ? {} : { previewUrl: current.previewUrl }),
    });
    this.#reportStep(step, FILE_UPLOAD_METHOD, "started");
    this.requestUpdate();
    try {
      const upload = await controller.uploadFile(step, body, signal);
      if (signal.aborted) return;
      this.#clearCameraState(key);
      if (current?.previewUrl !== undefined) URL.revokeObjectURL(current.previewUrl);
      this.#uploadStates.set(key, { status: "accepted" });
      this.#completeStep(step, FILE_UPLOAD_METHOD, upload);
    } catch (error) {
      if (signal.aborted) return;
      const message = this.#text("fileRejected");
      this.#uploadStates.set(key, {
        status: "error",
        body,
        message,
        ...(current?.previewUrl === undefined ? {} : { previewUrl: current.previewUrl }),
      });
      this.#reportStep(step, FILE_UPLOAD_METHOD, "failed", "file_upload_failed");
      this.#dispatchFlowError();
    }
    this.requestUpdate();
  }

  async #startCamera(step: CapturePlanStep, idSuffix: string): Promise<void> {
    const signal = this.#flowAbortController?.signal;
    if (this.#flowController === undefined || signal === undefined || signal.aborted) return;
    const key = stepKey(step);
    if (
      this.#completedSteps.has(key) ||
      this.#uploadStates.get(key)?.status === "uploading" ||
      this.#adapterStates.get(key)?.status === "running"
    )
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
      video.addEventListener(
        "loadedmetadata",
        () => this.#markCameraReady(step, key, stream, video),
        {
          once: true,
        },
      );
      await video.play();
      this.#markCameraReady(step, key, stream, video);
    } catch (error) {
      if (signal.aborted) return;
      this.#handleCameraFailure(step, error);
    }
  }

  async #capturePhoto(
    step: CapturePlanStep,
    videoId: string,
    detection?: DocumentDetection,
  ): Promise<void> {
    const key = stepKey(step);
    const state = this.#cameraStates.get(key);
    const video = this.renderRoot.querySelector<HTMLVideoElement>(`#${videoId}`);
    if (state?.status !== "streaming" || state.ready !== true || video === null) return;
    if (this.#capturingSteps.has(key)) return;
    const flow = this.#flowSnapshot;
    if (flow === undefined || !isActiveCaptureFlowSnapshot(flow)) return;
    this.#capturingSteps.add(key);
    this.#stopDocumentObservation(key);
    try {
      const policy = fileUploadPolicy(flow.session, step);
      const mediaType = policy.allowedMediaTypes.includes("image/jpeg")
        ? "image/jpeg"
        : policy.allowedMediaTypes[0]!;
      let frame: CameraFrame | undefined;
      if (detection !== undefined && isDocumentArtefact(step.artefact)) {
        frame = await captureCorrectedDocumentFrame(
          video,
          mediaType,
          detection,
          this.#documentCapture,
        );
      }
      const captured = frame ?? (await captureCameraFrame(video, mediaType));
      stopCamera(state.stream);
      video.srcObject = null;
      this.#cameraStates.set(key, {
        status: "reviewing",
        body: captured.body,
        previewUrl: URL.createObjectURL(captured.body),
        width: captured.width,
        height: captured.height,
      });
      this.requestUpdate();
    } catch (error) {
      this.#handleCameraFailure(step, error);
    } finally {
      this.#capturingSteps.delete(key);
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
    this.#recordStepJourney("capture_retake", "review", step, "retake", LIVE_CAMERA_METHOD);
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
        if (this.#journeyPhase === "capture") {
          const fallbackStep = planSteps(this.#flowSnapshot.plan)[this.#activeStepIndex];
          if (fallbackStep !== undefined && fallbackStep.methodOptions.length === 1) {
            this.#selectedMethods.set(stepKey(fallbackStep), fallbackStep.methodOptions[0]!);
            this.#stepStage = "preparation";
          } else {
            this.#stepStage = "method";
          }
        }
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
    this.#stopDocumentObservation(key);
    this.#capturingSteps.delete(key);
    const state = this.#cameraStates.get(key);
    stopCamera(state?.stream);
    if (state?.previewUrl !== undefined) URL.revokeObjectURL(state.previewUrl);
    this.#cameraStates.delete(key);
  }

  #clearUploadState(key: string): void {
    const state = this.#uploadStates.get(key);
    if (state?.previewUrl !== undefined) URL.revokeObjectURL(state.previewUrl);
    this.#uploadStates.delete(key);
  }

  #clearUploadStates(): void {
    for (const key of [...this.#uploadStates.keys()]) this.#clearUploadState(key);
  }

  #markCameraReady(
    step: CapturePlanStep,
    key: string,
    stream: MediaStream,
    video: HTMLVideoElement,
  ): void {
    const state = this.#cameraStates.get(key);
    if (
      state?.status !== "streaming" ||
      state.stream !== stream ||
      video.videoWidth <= 0 ||
      video.videoHeight <= 0
    ) {
      return;
    }
    this.#cameraStates.set(key, {
      ...state,
      ready: true,
      width: video.videoWidth,
      height: video.videoHeight,
    });
    if (
      isDocumentArtefact(step.artefact) &&
      this.#documentCapture.enabled &&
      !this.#documentObservations.has(key)
    ) {
      this.#startDocumentObservation(step, key, video);
    }
    this.requestUpdate();
  }

  #clearCameraStates(): void {
    for (const key of [...this.#documentObservations.keys()]) this.#stopDocumentObservation(key);
    for (const key of [...this.#cameraStates.keys()]) this.#clearCameraState(key);
    this.#documentStates.clear();
    this.#capturingSteps.clear();
  }

  #startDocumentObservation(step: CapturePlanStep, key: string, video: HTMLVideoElement): void {
    this.#stopDocumentObservation(key);
    const observer = createDocumentFrameObserver(this.#documentCapture);
    const gate = createDocumentCaptureGate(this.#documentCapture.gate);
    const observation: StepDocumentObservation = {
      interval: undefined,
      autoCaptureTimer: undefined,
      observer,
      gate,
      detection: undefined,
    };
    observation.interval = globalThis.setInterval(() => {
      this.#observeDocument(step, key, video, observation);
    }, this.#documentCapture.observationIntervalMs);
    this.#documentObservations.set(key, observation);
    this.#setDocumentState(key, { status: "searching", progress: 0 });
  }

  #observeDocument(
    step: CapturePlanStep,
    key: string,
    video: HTMLVideoElement,
    observation: StepDocumentObservation,
  ): void {
    if (this.#documentObservations.get(key) !== observation) return;
    const camera = this.#cameraStates.get(key);
    if (camera?.status !== "streaming" || camera.ready !== true) return;
    if (this.#capturingSteps.has(key) || this.#completedSteps.has(key)) return;
    const observed = observation.observer.observe(video);
    if (observed === undefined) return;
    const result = observation.gate.observe(observed);
    observation.detection = observed.detection;
    this.#setDocumentState(key, {
      status: result.status,
      progress: result.requiredFrames === 0 ? 0 : result.stableFrames / result.requiredFrames,
      ...(result.reason === undefined ? {} : { reason: result.reason }),
      ...(observed.detection === undefined
        ? {}
        : {
            quad: observed.detection.quad,
            frameWidth: observed.detection.frameWidth,
            frameHeight: observed.detection.frameHeight,
          }),
    });
    if (
      result.status === "ready" &&
      this.#documentCapture.autoCapture &&
      observed.detection !== undefined &&
      observation.autoCaptureTimer === undefined
    ) {
      const detection = observed.detection;
      observation.autoCaptureTimer = globalThis.setTimeout(() => {
        observation.autoCaptureTimer = undefined;
        if (this.#documentObservations.get(key) !== observation) return;
        void this.#capturePhoto(step, video.id, detection);
      }, this.#documentCapture.autoCaptureDelayMs);
    }
  }

  #stopDocumentObservation(key: string): void {
    const observation = this.#documentObservations.get(key);
    if (observation === undefined) return;
    if (observation.interval !== undefined) globalThis.clearInterval(observation.interval);
    if (observation.autoCaptureTimer !== undefined) {
      globalThis.clearTimeout(observation.autoCaptureTimer);
    }
    observation.observer.dispose();
    this.#documentObservations.delete(key);
    this.#documentStates.delete(key);
  }

  #setDocumentState(key: string, next: StepDocumentState): void {
    const current = this.#documentStates.get(key);
    if (current !== undefined && documentStateSignature(current) === documentStateSignature(next)) {
      return;
    }
    this.#documentStates.set(key, next);
    this.requestUpdate();
  }

  #completeStep(step: CapturePlanStep, acquisitionMethod: string, upload: EvidenceUpload): void {
    const key = stepKey(step);
    if (this.#completedSteps.has(key)) return;
    this.#completedSteps.set(key, {
      acquisitionMethod,
      uploadId: upload.id,
      evidenceId: upload.evidenceId,
    });
    this.#recordStepJourney(
      "capture_accepted",
      "review",
      step,
      "accept_capture",
      acquisitionMethod,
    );
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
    const plan =
      this.#flowSnapshot !== undefined && isActiveCaptureFlowSnapshot(this.#flowSnapshot)
        ? this.#flowSnapshot.plan
        : undefined;
    if (plan === undefined) return;
    const progress = captureProgress(plan, this.#completedSteps);
    if (this.#flowSnapshot !== undefined && this.#journeyPhase === "capture") {
      this.#journeyPhase =
        progress.completedSteps === progress.totalSteps ? "processing" : "confirmation";
      this.requestUpdate();
    }
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
    this.#pollAuthoritativeOutcome();
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
    if (!isActiveCaptureFlowSnapshot(snapshot)) return;
    const choices: Record<string, { options: readonly { id: string; label: string }[] }> = {};
    this.#selectedDocuments.clear();
    for (const requirement of snapshot.plan.requirements) {
      if (requirement.documentOptions !== undefined)
        choices[requirement.key] = { options: requirement.documentOptions };
      if (requirement.selectedDocument !== undefined)
        this.#selectedDocuments.set(requirement.key, requirement.selectedDocument);
    }
    this.#documentChoices = choices;
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
    const progress = captureProgress(snapshot.plan, this.#completedSteps);
    const activeStep = planSteps(snapshot.plan)[this.#activeStepIndex];
    if (
      this.#journeyPhase === "capture" &&
      activeStep !== undefined &&
      this.#completedSteps.has(stepKey(activeStep))
    ) {
      this.#journeyPhase =
        progress.completedSteps === progress.totalSteps ? "processing" : "confirmation";
    }
    if (this.#completedSteps.size === 0) return;
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
    this.#recordJourney(
      "action_selected",
      "notice",
      action === "refuse" ? "refuse_notice" : "accept_notice",
    );
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
      } else if (snapshot.status === "capture_ready") {
        this.#resumeJourney(snapshot.plan, true);
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
    this.#recordJourney("error_shown", "error");
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
    this.#clearAdapterStates();
    this.#flowAbortController?.abort();
    this.#flowAbortController = undefined;
    this.#flowController = undefined;
    this.#outcomePolling = false;
  }

  #pollAuthoritativeOutcome(): void {
    const controller = this.#flowController;
    const abortController = this.#flowAbortController;
    if (controller === undefined || abortController === undefined || this.#outcomePolling) return;
    this.#outcomePolling = true;
    void controller
      .pollOutcome((recovered) => {
        if (abortController.signal.aborted || this.#flowController !== controller) return;
        this.#flowSnapshot = recovered;
        if (isActiveCaptureFlowSnapshot(recovered)) {
          this.#restoreRecoveredProgress(recovered);
        } else if (recovered.status !== "processing" && recovered.status !== "action_required") {
          this.#dropFlowController();
        }
        this.requestUpdate();
      }, abortController.signal)
      .catch(() => {
        if (!abortController.signal.aborted && this.#flowController === controller) {
          this.#dispatchFlowError();
        }
      })
      .finally(() => {
        if (this.#flowController === controller) this.#outcomePolling = false;
      });
  }

  #countryView(): CountryViewContext {
    return {
      countryJourney: this.#countryJourney,
      countryJourneyPhase: this.#countryJourneyPhase,
      text: (key, values) => this.#text(key, values),
      localizer: this.#localizer,
      renderSecuredBy: () => renderSecuredBy(this.#noticeView()),
      countryQuery: this.#countryQuery,
      countrySelecting: this.#countrySelecting,
      selectCountry: (country) => this.#selectCountry(country),
      countrySelectionError: this.#countrySelectionError,
      cancel: () => this.cancel(),
      countryShowNotice: () => {
        this.#countryJourneyPhase = "notice";
        this.requestUpdate();
      },
      countrySearch: (query) => {
        this.#countryQuery = query;
        this.#countrySelectionError = false;
        this.requestUpdate();
      },
      countryBack: () => {
        this.#countryJourneyPhase = "notice";
        this.#countryQuery = "";
        this.#countrySelectionError = false;
        this.requestUpdate();
      },
      countryContinue: () => {
        this.#countryJourneyPhase = "country";
        this.requestUpdate();
      },
    };
  }

  #noticeView(): NoticeViewContext {
    return {
      text: (key, values) => this.#text(key, values),
      localizer: this.#localizer,
      beginJourney: (flow) => this.#beginJourney(flow),
      journeyPhase: this.#journeyPhase,
      resumeJourney: (plan, showRecovery) => this.#resumeJourney(plan, showRecovery),
      flowState: this.#flowState,
      respond: (action) => this.#respond(action),
      responseError: this.#responseError,
    };
  }

  #journeyView(): JourneyViewContext {
    return {
      activeStepIndex: this.#activeStepIndex,
      renderProcessing: () => renderProcessing(this.#statusView()),
      completedSteps: this.#completedSteps,
      localizer: this.#localizer,
      text: (key, values) => this.#text(key, values),
      documentChoices: this.#documentChoices,
      selectedDocuments: this.#selectedDocuments,
      choosingDocument: this.#choosingDocument,
      stepStage: this.#stepStage,
      documentSelectionError: this.#documentSelectionError,
      savingDocument: this.#savingDocument,
      selectDocument: (step, documentType) => this.#selectDocument(step, documentType),
      adapterFor: (step, method) => this.#adapterFor(step, method),
      chooseGuidedMethod: (step, method) => this.#chooseGuidedMethod(step, method),
      selectedMethods: this.#selectedMethods,
      showCaptureTask: () => this.#showCaptureTask(),
      startGuidedAdapter: (step, adapter) => this.#startGuidedAdapter(step, adapter),
      backFromPreparation: (step) => this.#backFromPreparation(step),
      canChangeDocument: (step) => this.#canChangeDocument(step),
      uploadStates: this.#uploadStates,
      cameraStates: this.#cameraStates,
      adapterStates: this.#adapterStates,
      capturingSteps: this.#capturingSteps,
      backToPreparation: (step) => this.#backToPreparation(step),
      renderCamera: (step, idSuffix, state, lockedByFile) =>
        renderCamera(this.#acquisitionView(), step, idSuffix, state, lockedByFile),
      renderMethodAdapter: (step, idSuffix, adapter, state) =>
        renderMethodAdapter(this.#acquisitionView(), step, idSuffix, adapter, state),
      renderFileUpload: (step, idSuffix, uploadState, lockedByCamera) =>
        renderFileUpload(this.#acquisitionView(), step, idSuffix, uploadState, lockedByCamera),
      selectMethod: (step, method) => this.#selectMethod(step, method),
      resumeJourney: (plan, showRecovery) => this.#resumeJourney(plan, showRecovery),
      continueAfterConfirmation: (plan) => this.#continueAfterConfirmation(plan),
      cancel: () => this.cancel(),
      documentBack: () => {
        this.#choosingDocument = undefined;
        this.#documentSelectionError = false;
        this.requestUpdate();
      },
    };
  }

  #statusView(): StatusViewContext {
    return {
      text: (key, values) => this.#text(key, values),
    };
  }

  #acquisitionView(): AcquisitionViewContext {
    return {
      flowSnapshot: this.#flowSnapshot,
      fileSelected: (step, event) => this.#fileSelected(step, event),
      text: (key, values) => this.#text(key, values),
      localizer: this.#localizer,
      uploadFile: (step, body) => this.#uploadFile(step, body),
      documentCapture: this.#documentCapture,
      hasFlowController: this.#flowController !== undefined,
      documentStates: this.#documentStates,
      startCamera: (step, idSuffix) => this.#startCamera(step, idSuffix),
      capturingSteps: this.#capturingSteps,
      capturePhoto: (step, videoId, detection) => this.#capturePhoto(step, videoId, detection),
      documentDetection: (key) => this.#documentObservations.get(key)?.detection,
      cancelCamera: (step) => this.#cancelCamera(step),
      uploadCamera: (step, body) => this.#uploadCamera(step, body),
      retakePhoto: (step, idSuffix) => this.#retakePhoto(step, idSuffix),
      documentItem: (step) => this.#documentItem(step),
      selectedDocuments: this.#selectedDocuments,
      isDocumentCameraScreen: () => this.#isDocumentCameraScreen(),
      cancelMethodAdapter: (step) => this.#cancelMethodAdapter(step),
      runMethodAdapter: (step, adapter, previewId) =>
        this.#runMethodAdapter(step, adapter, previewId),
    };
  }

  #text(key: CaptureMessageKey, values?: CaptureMessageValues): string {
    return this.#localizer.text(key, values);
  }

  #currentJourneyScreen(): CaptureJourneyScreen | undefined {
    if (this.#flowController === undefined && this.#flowSnapshot === undefined) return undefined;
    if (this.#flowState === "error") return "error";
    const flow = this.#flowSnapshot;
    if (flow !== undefined && !isActiveCaptureFlowSnapshot(flow)) {
      if (flow.status === "processing" || flow.status === "action_required") return "processing";
      if (flow.status === "failed") return "error";
      return "completion";
    }
    if (this.#journeyPhase === "confirmation") return "review";
    if (this.#journeyPhase === "complete") return "completion";
    if (this.#journeyPhase !== "capture") return this.#journeyPhase;
    return this.#stepStage;
  }

  #activeJourneyStep(): CapturePlanStep | undefined {
    const flow = this.#flowSnapshot;
    if (flow === undefined || !isActiveCaptureFlowSnapshot(flow)) return undefined;
    return planSteps(flow.plan)[this.#activeStepIndex];
  }

  #recordStepJourney(
    eventType: CaptureJourneyEventType,
    screen: CaptureJourneyScreen,
    step: CapturePlanStep,
    action?: CaptureJourneyAction,
    acquisitionMethod?: string,
  ): void {
    this.#recordJourney(eventType, screen, action, {
      requirementKey: step.requirementKey,
      artefact: step.artefact,
      ...(acquisitionMethod === undefined ? {} : { acquisitionMethod }),
    });
  }

  #recordJourney(
    eventType: CaptureJourneyEventType,
    screen: CaptureJourneyScreen,
    action?: CaptureJourneyAction,
    context: {
      readonly requirementKey?: string;
      readonly artefact?: string;
      readonly acquisitionMethod?: string;
    } = {},
  ): void {
    void this.#flowController
      ?.recordJourneyEvent({
        eventType,
        screen,
        ...(action === undefined ? {} : { action }),
        ...context,
      })
      .catch(() => undefined);
  }
}

export function defineIdenqaCapture(
  tagName: string = IDENQA_CAPTURE_TAG_NAME,
): typeof IdenqaCaptureElement {
  if (typeof customElements === "undefined") {
    throw new Error("Custom elements are not available in this environment.");
  }
  installCaptureFont();
  const existing = customElements.get(tagName);
  if (existing === undefined) {
    customElements.define(tagName, IdenqaCaptureElement);
  } else if (existing !== IdenqaCaptureElement) {
    throw new Error(`The custom-element name ${tagName} is already registered.`);
  }
  return IdenqaCaptureElement;
}

declare global {
  interface HTMLElementTagNameMap {
    "idenqa-capture": IdenqaCaptureElement;
  }
}
