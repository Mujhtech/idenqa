import {
  CaptureClient,
  OutcomeClient,
  IdenqaTransportError,
  createIdempotencyKey,
  type CaptureAuthoritySnapshot,
  type CaptureClientOptions,
  type CaptureCompletion,
  type CaptureProgress,
  type CaptureObservation,
  type CaptureOutcome,
  type CaptureOutcomeState,
  type CaptureRealtimeEvent,
  type CaptureStepUpdateInput,
  type EvidenceUpload,
  type EvidenceUploadCreate,
  type EvidenceUploadID,
  type ProcessingAuthority,
  type SDKResponse,
  type SDKConditionalResponse,
  type SubjectResponse,
  type SubjectResponseAction,
  type SubjectResponseCreate,
  type VerificationSession,
} from "@idenqa/sdk";

import {
  applyCaptureFailureFallback,
  createCapturePlan,
  type CapturePlan,
  type CapturePlannerCapabilities,
} from "./planner.js";
import type { CapturePlanStep } from "./planner.js";
import { LIVE_CAMERA_METHOD } from "./camera.js";
import {
  CaptureUploadError,
  FILE_UPLOAD_METHOD,
  fileUploadPolicy,
  prepareFileUpload,
} from "./upload.js";

export const CAPTURE_EXPERIENCE_VERSION = "idenqa.capture.web.v1";

export type CaptureActiveFlowStatus =
  "notice_required" | "capture_ready" | "refused" | "authority_blocked";
export type CaptureTerminalFlowStatus = Exclude<CaptureOutcomeState, "capture_required">;
export type CaptureFlowStatus = CaptureActiveFlowStatus | CaptureTerminalFlowStatus;

export interface CaptureActiveFlowSnapshot {
  readonly status: CaptureActiveFlowStatus;
  readonly outcome: CaptureOutcome;
  readonly session: VerificationSession;
  readonly authoritySnapshot: CaptureAuthoritySnapshot;
  readonly plan: CapturePlan;
  readonly progress: CaptureProgress;
  readonly region: string;
}

export interface CaptureOutcomeFlowSnapshot {
  readonly status: CaptureTerminalFlowStatus;
  readonly outcome: CaptureOutcome;
}

export type CaptureFlowSnapshot = CaptureActiveFlowSnapshot | CaptureOutcomeFlowSnapshot;

export interface CaptureFlowClient {
  selectDocument?(
    input: {
      readonly requirementKey: string;
      readonly documentType: string;
      readonly expectedVersion: number;
    },
    options: { readonly idempotencyKey: string; readonly signal?: AbortSignal },
  ): Promise<SDKResponse<VerificationSession>>;
  observe?(
    session: VerificationSession,
    options?: { readonly capabilities?: readonly string[]; readonly signal?: AbortSignal },
  ): CaptureObservation;
  getSession(options?: {
    readonly signal?: AbortSignal;
  }): Promise<SDKResponse<VerificationSession>>;
  getAuthority(options?: {
    readonly signal?: AbortSignal;
  }): Promise<SDKResponse<CaptureAuthoritySnapshot>>;
  getProgress(options?: { readonly signal?: AbortSignal }): Promise<SDKResponse<CaptureProgress>>;
  getOutcome(options?: { readonly signal?: AbortSignal }): Promise<SDKResponse<CaptureOutcome>>;
  pollProgress?(
    etag: string,
    options?: { readonly signal?: AbortSignal },
  ): Promise<SDKConditionalResponse<CaptureProgress>>;
  respond(
    input: SubjectResponseCreate,
    options: { readonly idempotencyKey: string; readonly signal?: AbortSignal },
  ): Promise<SDKResponse<SubjectResponse>>;
  createEvidenceUpload(
    input: EvidenceUploadCreate,
    options: { readonly idempotencyKey: string; readonly signal?: AbortSignal },
  ): Promise<SDKResponse<EvidenceUpload>>;
  getEvidenceUpload(
    uploadId: EvidenceUploadID,
    options?: { readonly signal?: AbortSignal },
  ): Promise<SDKResponse<EvidenceUpload>>;
  uploadEvidence(
    uploadId: EvidenceUploadID,
    body: Blob,
    options: { readonly etag: string; readonly digest: string; readonly signal?: AbortSignal },
  ): Promise<SDKResponse<EvidenceUpload>>;
}

export interface CaptureFlowControllerOptions {
  readonly idempotencyKeyFactory?: () => string;
  readonly uploadIdempotencyKeyFactory?: () => string;
  readonly region?: string;
  readonly recoveryPollingIntervalMs?: number;
}

export interface CaptureFlowStartOptions extends CaptureClientOptions {
  readonly outcomeToken: string;
  readonly capabilities: CapturePlannerCapabilities;
  readonly region?: string;
  readonly recoveryPollingIntervalMs?: number;
}

export class CaptureFlowError extends Error {
  readonly code: "CAPTURE_FLOW_INVALID_SNAPSHOT" | "CAPTURE_FLOW_INVALID_STATE";

  constructor(code: CaptureFlowError["code"], message: string) {
    super(message);
    this.name = "CaptureFlowError";
    this.code = code;
  }
}

export class CaptureFlowController {
  readonly #client: CaptureFlowClient;
  readonly #capabilities: CapturePlannerCapabilities;
  readonly #idempotencyKeyFactory: () => string;
  readonly #uploadIdempotencyKeyFactory: () => string;
  readonly #region: string | undefined;
  readonly #recoveryPollingIntervalMs: number;
  readonly #responseKeys = new Map<SubjectResponseAction, string>();
  readonly #documentSelectionKeys = new Map<string, string>();
  readonly #uploads = new Map<string, UploadTransaction>();
  readonly #uploadIssueKeys = new Map<string, UploadIssueKey>();
  #snapshot: CaptureFlowSnapshot | undefined;
  #observation: CaptureObservation | undefined;
  #progressETag: string | undefined;

  constructor(
    client: CaptureFlowClient,
    capabilities: CapturePlannerCapabilities,
    options: CaptureFlowControllerOptions = {},
  ) {
    this.#client = client;
    this.#capabilities = capabilities;
    this.#idempotencyKeyFactory =
      options.idempotencyKeyFactory ?? (() => createIdempotencyKey("capture_notice"));
    this.#uploadIdempotencyKeyFactory =
      options.uploadIdempotencyKeyFactory ?? (() => createIdempotencyKey("capture_upload"));
    this.#region = options.region;
    this.#recoveryPollingIntervalMs = pollingInterval(options.recoveryPollingIntervalMs);
  }

  get snapshot(): CaptureFlowSnapshot | undefined {
    return this.#snapshot;
  }

  async load(signal?: AbortSignal): Promise<CaptureFlowSnapshot> {
    return this.#readSnapshot(signal, true);
  }

  /** Re-reads authoritative session, authority, and completion state without discarding retries. */
  async refresh(signal?: AbortSignal): Promise<CaptureFlowSnapshot> {
    if (this.#snapshot === undefined) {
      throw new CaptureFlowError(
        "CAPTURE_FLOW_INVALID_STATE",
        "Capture progress cannot be refreshed before the flow is loaded.",
      );
    }
    return this.#readSnapshot(signal, false);
  }

  async selectDocument(
    requirementKey: string,
    documentType: string,
    signal?: AbortSignal,
  ): Promise<CaptureFlowSnapshot> {
    const current = this.#snapshot;
    if (
      current === undefined ||
      !isActiveSnapshot(current) ||
      current.status !== "capture_ready" ||
      this.#client.selectDocument === undefined
    ) {
      throw new CaptureFlowError(
        "CAPTURE_FLOW_INVALID_STATE",
        "Document selection is not available.",
      );
    }
    const requirement = current.session.requirements.requirements.find(
      (candidate) => candidate.key === requirementKey,
    );
    if (!requirement?.document_options?.some((option) => option.id === documentType)) {
      throw new CaptureFlowError(
        "CAPTURE_FLOW_INVALID_STATE",
        "Document type is not permitted by this session.",
      );
    }
    const expectedVersion = current.session.version;
    const key = JSON.stringify([requirementKey, documentType, expectedVersion]);
    let idempotencyKey = this.#documentSelectionKeys.get(key);
    if (idempotencyKey === undefined) {
      idempotencyKey = this.#idempotencyKeyFactory();
      this.#documentSelectionKeys.set(key, idempotencyKey);
    }
    await this.#client.selectDocument(
      { requirementKey, documentType, expectedVersion },
      {
        idempotencyKey,
        ...(signal === undefined ? {} : { signal }),
      },
    );
    return this.refresh(signal);
  }

  async #readSnapshot(
    signal: AbortSignal | undefined,
    resetTransactions: boolean,
  ): Promise<CaptureFlowSnapshot> {
    const outcomeResponse = await this.#client.getOutcome(signal === undefined ? {} : { signal });
    if (outcomeResponse.data.state !== "capture_required") {
      const snapshot = createOutcomeSnapshot(outcomeResponse.data);
      this.#snapshot = snapshot;
      this.#progressETag = undefined;
      return snapshot;
    }
    let sessionResponse: SDKResponse<VerificationSession>;
    let authorityResponse: SDKResponse<CaptureAuthoritySnapshot>;
    let progressResponse: SDKResponse<CaptureProgress>;
    try {
      [sessionResponse, authorityResponse, progressResponse] = await Promise.all([
        this.#client.getSession(signal === undefined ? {} : { signal }),
        this.#client.getAuthority(signal === undefined ? {} : { signal }),
        this.#client.getProgress(signal === undefined ? {} : { signal }),
      ]);
    } catch (error) {
      // The session can leave capture between the outcome read above and the
      // capture-only reads. Re-read the safe projection before surfacing that
      // expected transition as a page-load failure.
      const latestOutcome = await this.#client.getOutcome(signal === undefined ? {} : { signal });
      if (latestOutcome.data.state !== "capture_required") {
        const snapshot = createOutcomeSnapshot(latestOutcome.data);
        this.#snapshot = snapshot;
        this.#progressETag = undefined;
        return snapshot;
      }
      throw error;
    }
    const snapshot = createSnapshot(
      outcomeResponse.data,
      sessionResponse.data,
      authorityResponse.data,
      this.#capabilities,
      this.#region,
      progressResponse.data,
    );
    this.#snapshot = snapshot;
    this.#progressETag = progressResponse.etag;
    if (resetTransactions) {
      this.#responseKeys.clear();
      this.#documentSelectionKeys.clear();
      this.#uploads.clear();
      this.#uploadIssueKeys.clear();
    }
    return snapshot;
  }

  async observe(
    onEvent: (event: CaptureRealtimeEvent) => void,
    onSnapshot: (snapshot: CaptureFlowSnapshot) => void,
    signal?: AbortSignal,
  ): Promise<void> {
    const current = this.#snapshot;
    if (current === undefined || !isActiveSnapshot(current) || this.#client.observe === undefined)
      return;
    const observation = this.#client.observe(current.session, {
      capabilities: this.#capabilities.availableMethods,
      ...(signal === undefined ? {} : { signal }),
    });
    this.#observation = observation;
    try {
      for await (const event of observation) {
        if (isAborted(signal)) return;
        onEvent(event);
        if (
          event.type === "capture.progress" ||
          event.type === "verification.check.progress" ||
          event.type === "session.state_changed" ||
          event.type === "session.resync_required"
        ) {
          onSnapshot(await this.#readSnapshot(signal, false));
        }
      }
    } catch (error) {
      if (isAborted(signal)) return;
      if (!(error instanceof IdenqaTransportError) || this.#client.pollProgress === undefined) {
        throw error;
      }
      await this.#pollRecovery(onSnapshot, signal);
    } finally {
      observation.close();
      if (this.#observation === observation) this.#observation = undefined;
    }
  }

  /** Polls the safe outcome projection when a page is opened after capture stopped. */
  async pollOutcome(
    onSnapshot: (snapshot: CaptureFlowSnapshot) => void,
    signal?: AbortSignal,
  ): Promise<void> {
    for (;;) {
      await abortablePollingDelay(this.#recoveryPollingIntervalMs, signal);
      if (isAborted(signal)) return;
      try {
        const snapshot = await this.#readSnapshot(signal, false);
        onSnapshot(snapshot);
        if (
          !isActiveSnapshot(snapshot) &&
          snapshot.status !== "processing" &&
          snapshot.status !== "action_required"
        ) {
          return;
        }
      } catch (error) {
        if (isAborted(signal)) return;
        if (!(error instanceof IdenqaTransportError)) throw error;
      }
    }
  }

  async #pollRecovery(
    onSnapshot: (snapshot: CaptureFlowSnapshot) => void,
    signal?: AbortSignal,
  ): Promise<void> {
    for (;;) {
      await abortablePollingDelay(this.#recoveryPollingIntervalMs, signal);
      if (isAborted(signal)) return;
      const etag = this.#progressETag;
      if (etag === undefined || this.#client.pollProgress === undefined) {
        onSnapshot(await this.#readSnapshot(signal, false));
        continue;
      }
      try {
        const outcome = await this.#client.getOutcome(signal === undefined ? {} : { signal });
        if (outcome.data.state !== "capture_required") {
          onSnapshot(createOutcomeSnapshot(outcome.data));
          return;
        }
        const progress = await this.#client.pollProgress(
          etag,
          signal === undefined ? {} : { signal },
        );
        this.#progressETag = progress.etag;
        if (!progress.notModified) onSnapshot(await this.#readSnapshot(signal, false));
      } catch (error) {
        if (isAborted(signal)) return;
        if (!(error instanceof IdenqaTransportError)) throw error;
      }
    }
  }

  reportStep(input: CaptureStepUpdateInput): Promise<void> {
    const observation = this.#observation;
    if (observation === undefined) return Promise.resolve();
    return observation.reportStep(input).then((result) => {
      if (result.disposition === "rejected") {
        throw new CaptureFlowError(
          "CAPTURE_FLOW_INVALID_STATE",
          `The capture step update was rejected (${result.code ?? "unknown"}).`,
        );
      }
    });
  }

  async respond(
    action: SubjectResponseAction,
    signal?: AbortSignal,
  ): Promise<CaptureActiveFlowSnapshot> {
    const current = this.#snapshot;
    if (
      current === undefined ||
      !isActiveSnapshot(current) ||
      current.status !== "notice_required"
    ) {
      throw new CaptureFlowError(
        "CAPTURE_FLOW_INVALID_STATE",
        "A subject response is not accepted in the current capture flow state.",
      );
    }
    assertAllowedResponse(current.authoritySnapshot.authority, action);

    let idempotencyKey = this.#responseKeys.get(action);
    if (idempotencyKey === undefined) {
      idempotencyKey = this.#idempotencyKeyFactory();
      this.#responseKeys.set(action, idempotencyKey);
    }
    const response = await this.#client.respond(
      {
        action,
        locale: current.authoritySnapshot.notice.locale,
        renderedExperienceVersion: CAPTURE_EXPERIENCE_VERSION,
      },
      {
        idempotencyKey,
        ...(signal === undefined ? {} : { signal }),
      },
    );
    const authoritySnapshot: CaptureAuthoritySnapshot = {
      ...current.authoritySnapshot,
      latestResponse: response.data,
    };
    const next = createSnapshot(
      current.outcome,
      current.session,
      authoritySnapshot,
      this.#capabilities,
      current.region,
      current.progress,
    );
    this.#snapshot = next;
    return next;
  }

  async uploadFile(
    step: CapturePlanStep,
    body: Blob,
    signal?: AbortSignal,
  ): Promise<EvidenceUpload> {
    return this.#uploadEvidence(step, body, FILE_UPLOAD_METHOD, signal);
  }

  async uploadCamera(
    step: CapturePlanStep,
    body: Blob,
    signal?: AbortSignal,
  ): Promise<EvidenceUpload> {
    return this.#uploadEvidence(step, body, LIVE_CAMERA_METHOD, signal);
  }

  captureFailed(step: CapturePlanStep): CaptureActiveFlowSnapshot {
    const current = this.#snapshot;
    if (
      current === undefined ||
      !isActiveSnapshot(current) ||
      current.status !== "capture_ready" ||
      !step.methodOptions.includes(LIVE_CAMERA_METHOD)
    ) {
      throw new CaptureUploadError(
        "CAPTURE_UPLOAD_INVALID_STATE",
        "Capture failure cannot be recorded for this step.",
      );
    }
    const next: CaptureActiveFlowSnapshot = {
      ...current,
      plan: applyCaptureFailureFallback(current.plan, current.session, step, this.#capabilities),
    };
    this.#snapshot = next;
    return next;
  }

  async #uploadEvidence(
    step: CapturePlanStep,
    body: Blob,
    method: typeof FILE_UPLOAD_METHOD | typeof LIVE_CAMERA_METHOD,
    signal?: AbortSignal,
  ): Promise<EvidenceUpload> {
    const current = this.#snapshot;
    if (
      current === undefined ||
      !isActiveSnapshot(current) ||
      current.status !== "capture_ready" ||
      !step.methodOptions.includes(method)
    ) {
      throw new CaptureUploadError(
        "CAPTURE_UPLOAD_INVALID_STATE",
        "The selected acquisition method is not available for this capture step.",
      );
    }
    const requirement = current.plan.requirements.find(
      (candidate) => candidate.key === step.requirementKey,
    );
    if (
      requirement === undefined ||
      (requirement.documentOptions !== undefined && requirement.selectedDocument === undefined) ||
      !requirement.steps.some(
        (candidate) =>
          candidate.artefact === step.artefact &&
          candidate.methodOptions.includes(method) &&
          candidate.fallbackCondition === step.fallbackCondition,
      )
    ) {
      throw new CaptureUploadError(
        "CAPTURE_UPLOAD_INVALID_STATE",
        "Choose a permitted document before collecting its required side.",
      );
    }
    const prepared = await prepareFileUpload(body, fileUploadPolicy(current.session, step));
    const key = uploadStepKey(step, method);
    let transaction = this.#uploads.get(key);
    if (transaction !== undefined && !sameFile(transaction, prepared.body, prepared.digest)) {
      transaction = undefined;
      this.#uploads.delete(key);
      this.#uploadIssueKeys.delete(key);
    }
    if (transaction?.ambiguous === true) {
      const recovered = await this.#client.getEvidenceUpload(
        transaction.upload.id,
        signal === undefined ? {} : { signal },
      );
      assertRecoveredUpload(transaction.upload, recovered.data);
      transaction = recoveredTransaction(transaction, recovered);
      this.#uploads.set(key, transaction);
      if (recovered.data.state === "accepted") {
        this.#uploadIssueKeys.delete(key);
        return recovered.data;
      }
    }
    if (transaction === undefined) {
      const issueKey = this.#issueKey(key, prepared.body, prepared.digest);
      const issued = await this.#client.createEvidenceUpload(
        {
          requirementKey: step.requirementKey,
          artefact: step.artefact,
          acquisitionMethod: method,
          ...(step.fallbackCondition === undefined
            ? {}
            : { fallbackCondition: step.fallbackCondition }),
          expectedBytes: prepared.body.size,
          expectedDigest: prepared.digest,
          mediaType: prepared.mediaType,
          region: current.region,
        },
        {
          idempotencyKey: issueKey,
          ...(signal === undefined ? {} : { signal }),
        },
      );
      assertUploadBinding(issued.data, step, prepared.body, method, current.region);
      transaction = transactionFromResponse(issued, prepared.body, prepared.digest);
      this.#uploads.set(key, transaction);
      if (issued.data.state === "accepted") {
        this.#uploadIssueKeys.delete(key);
        return issued.data;
      }
    }
    if (transaction.upload.state === "accepted") return transaction.upload;
    if (transaction.upload.state !== "issued") {
      throw new CaptureUploadError(
        "CAPTURE_UPLOAD_RETRY_LATER",
        "The previous upload attempt is still being resolved. Try again shortly.",
      );
    }
    try {
      const accepted = await this.#client.uploadEvidence(transaction.upload.id, prepared.body, {
        etag: transaction.etag,
        digest: prepared.digest,
        ...(signal === undefined ? {} : { signal }),
      });
      assertRecoveredUpload(transaction.upload, accepted.data);
      this.#uploads.set(key, transactionFromResponse(accepted, prepared.body, prepared.digest));
      this.#uploadIssueKeys.delete(key);
      return accepted.data;
    } catch (error) {
      this.#uploads.set(key, { ...transaction, ambiguous: true });
      throw error;
    }
  }

  #issueKey(stepKey: string, body: Blob, digest: string): string {
    const existing = this.#uploadIssueKeys.get(stepKey);
    if (
      existing !== undefined &&
      existing.size === body.size &&
      existing.mediaType === body.type &&
      existing.digest === digest
    ) {
      return existing.key;
    }
    const key = this.#uploadIdempotencyKeyFactory();
    this.#uploadIssueKeys.set(stepKey, {
      key,
      size: body.size,
      mediaType: body.type,
      digest,
    });
    return key;
  }
}

export function createCaptureFlowController(
  options: CaptureFlowStartOptions,
): CaptureFlowController {
  const capture = new CaptureClient(options);
  const outcome = new OutcomeClient(options);
  const client: CaptureFlowClient = {
    observe: (session, observeOptions) => capture.observe(session, observeOptions),
    getSession: (requestOptions) => capture.getSession(requestOptions),
    getAuthority: (requestOptions) => capture.getAuthority(requestOptions),
    getProgress: (requestOptions) => capture.getProgress(requestOptions),
    getOutcome: (requestOptions) => outcome.getOutcome(requestOptions),
    pollProgress: (etag, requestOptions) => capture.pollProgress(etag, requestOptions),
    respond: (input, requestOptions) => capture.respond(input, requestOptions),
    selectDocument: (input, requestOptions) => capture.selectDocument(input, requestOptions),
    createEvidenceUpload: (input, requestOptions) =>
      capture.createEvidenceUpload(input, requestOptions),
    getEvidenceUpload: (uploadID, requestOptions) =>
      capture.getEvidenceUpload(uploadID, requestOptions),
    uploadEvidence: (uploadID, body, requestOptions) =>
      capture.uploadEvidence(uploadID, body, requestOptions),
  };
  return new CaptureFlowController(client, options.capabilities, {
    ...(options.region === undefined ? {} : { region: options.region }),
    ...(options.recoveryPollingIntervalMs === undefined
      ? {}
      : { recoveryPollingIntervalMs: options.recoveryPollingIntervalMs }),
  });
}

function pollingInterval(value: number | undefined): number {
  const interval = value ?? 2000;
  if (!Number.isInteger(interval) || interval < 250 || interval > 30_000) {
    throw new TypeError("recoveryPollingIntervalMs must be an integer between 250 and 30000.");
  }
  return interval;
}

function isAborted(signal?: AbortSignal): boolean {
  return signal?.aborted === true;
}

function abortablePollingDelay(delay: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted === true) return Promise.resolve();
  return new Promise((resolve) => {
    const finish = () => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", finish);
      resolve();
    };
    const timer = setTimeout(finish, delay);
    signal?.addEventListener("abort", finish, { once: true });
  });
}

function createSnapshot(
  outcome: CaptureOutcome,
  session: VerificationSession,
  authoritySnapshot: CaptureAuthoritySnapshot,
  capabilities: CapturePlannerCapabilities,
  requestedRegion?: string,
  progress?: CaptureProgress,
): CaptureActiveFlowSnapshot {
  assertSnapshotCorrelation(session, authoritySnapshot);
  if (outcome.verificationId !== session.id || outcome.state !== "capture_required") {
    throw new CaptureFlowError(
      "CAPTURE_FLOW_INVALID_SNAPSHOT",
      "Capture outcome does not belong to the active verification session.",
    );
  }
  const recovered = recoverCapturePlan(
    session,
    capabilities,
    progress ?? { verificationId: session.id, completions: [] },
  );
  return {
    status: flowStatus(authoritySnapshot),
    outcome,
    session,
    authoritySnapshot,
    plan: recovered.plan,
    progress: recovered.progress,
    region: resolveRegion(authoritySnapshot.authority, requestedRegion),
  };
}

function createOutcomeSnapshot(outcome: CaptureOutcome): CaptureOutcomeFlowSnapshot {
  if (outcome.state === "capture_required") {
    throw new CaptureFlowError(
      "CAPTURE_FLOW_INVALID_SNAPSHOT",
      "An active capture outcome requires the full capture snapshot.",
    );
  }
  return { status: outcome.state, outcome };
}

export function isActiveCaptureFlowSnapshot(
  snapshot: CaptureFlowSnapshot,
): snapshot is CaptureActiveFlowSnapshot {
  return isActiveSnapshot(snapshot);
}

function isActiveSnapshot(snapshot: CaptureFlowSnapshot): snapshot is CaptureActiveFlowSnapshot {
  return "session" in snapshot;
}

function recoverCapturePlan(
  session: VerificationSession,
  capabilities: CapturePlannerCapabilities,
  progress: CaptureProgress,
): { readonly plan: CapturePlan; readonly progress: CaptureProgress } {
  if (progress.verificationId !== session.id) {
    throw invalidProgressSnapshot("Capture progress does not belong to the verification session.");
  }
  let plan = createCapturePlan(session, capabilities);
  const logicalSteps = new Set<string>();
  for (const completion of progress.completions) {
    if (completion.fallbackCondition === "capture_failed") {
      const candidates = planSteps(plan).filter(
        (step) =>
          step.requirementKey === completion.requirementKey &&
          step.evidenceType === completion.evidenceType &&
          step.artefact === completion.artefact,
      );
      if (candidates.length !== 1) {
        throw invalidProgressSnapshot(
          "Recovered capture fallback does not identify one planned step.",
        );
      }
      plan = applyCaptureFailureFallback(plan, session, candidates[0]!, capabilities);
    }
    const matches = planSteps(plan).filter((step) => completionMatches(step, completion));
    if (matches.length !== 1) {
      throw invalidProgressSnapshot(
        "Recovered capture completion does not match one planned step.",
      );
    }
    const key = recoveredStepKey(matches[0]!);
    if (logicalSteps.has(key)) {
      throw invalidProgressSnapshot("Recovered capture progress repeats a logical step.");
    }
    logicalSteps.add(key);
  }

  return { plan, progress };
}

function completionMatches(step: CapturePlanStep, completion: CaptureCompletion): boolean {
  return (
    step.requirementKey === completion.requirementKey &&
    step.evidenceType === completion.evidenceType &&
    step.artefact === completion.artefact &&
    step.methodOptions.includes(completion.acquisitionMethod) &&
    step.fallbackCondition === completion.fallbackCondition
  );
}

function planSteps(plan: CapturePlan): readonly CapturePlanStep[] {
  return plan.requirements.flatMap((requirement) => requirement.steps);
}

function recoveredStepKey(step: CapturePlanStep): string {
  return `${step.requirementKey}\u0000${step.evidenceType}\u0000${step.artefact}`;
}

function invalidProgressSnapshot(message: string): CaptureFlowError {
  return new CaptureFlowError("CAPTURE_FLOW_INVALID_SNAPSHOT", message);
}

interface UploadTransaction {
  readonly upload: EvidenceUpload;
  readonly etag: string;
  readonly size: number;
  readonly mediaType: string;
  readonly digest: string;
  readonly ambiguous: boolean;
}

interface UploadIssueKey {
  readonly key: string;
  readonly size: number;
  readonly mediaType: string;
  readonly digest: string;
}

function transactionFromResponse(
  response: SDKResponse<EvidenceUpload>,
  body: Blob,
  digest: string,
): UploadTransaction {
  return transactionFromMetadata(response, body.size, body.type, digest);
}

function transactionFromMetadata(
  response: SDKResponse<EvidenceUpload>,
  size: number,
  mediaType: string,
  digest: string,
): UploadTransaction {
  if (response.etag === undefined) {
    throw new CaptureUploadError(
      "CAPTURE_UPLOAD_INVALID_STATE",
      "The upload response is missing its ETag.",
    );
  }
  return {
    upload: response.data,
    etag: response.etag,
    size,
    mediaType,
    digest,
    ambiguous: false,
  };
}

function recoveredTransaction(
  current: UploadTransaction,
  response: SDKResponse<EvidenceUpload>,
): UploadTransaction {
  return transactionFromMetadata(response, current.size, current.mediaType, current.digest);
}

function sameFile(transaction: UploadTransaction, body: Blob, digest: string): boolean {
  return (
    transaction.size === body.size &&
    transaction.mediaType === body.type &&
    transaction.digest === digest
  );
}

function assertUploadBinding(
  upload: EvidenceUpload,
  step: CapturePlanStep,
  body: Blob,
  method: typeof FILE_UPLOAD_METHOD | typeof LIVE_CAMERA_METHOD,
  region: string,
): void {
  if (
    upload.requirementKey !== step.requirementKey ||
    upload.evidenceType !== step.evidenceType ||
    upload.artefact !== step.artefact ||
    upload.acquisitionMethod !== method ||
    upload.expectedBytes !== body.size ||
    upload.mediaType !== body.type ||
    upload.region !== region ||
    !upload.allowedMediaTypes.includes(upload.mediaType) ||
    upload.expectedBytes > upload.maximumBytes
  ) {
    throw new CaptureUploadError(
      "CAPTURE_UPLOAD_INVALID_STATE",
      "The upload response does not match the requested evidence binding.",
    );
  }
}

function assertRecoveredUpload(previous: EvidenceUpload, recovered: EvidenceUpload): void {
  if (
    recovered.id !== previous.id ||
    recovered.evidenceId !== previous.evidenceId ||
    recovered.requirementKey !== previous.requirementKey ||
    recovered.evidenceType !== previous.evidenceType ||
    recovered.artefact !== previous.artefact ||
    recovered.acquisitionMethod !== previous.acquisitionMethod ||
    recovered.expectedBytes !== previous.expectedBytes ||
    recovered.mediaType !== previous.mediaType ||
    recovered.region !== previous.region
  ) {
    throw new CaptureUploadError(
      "CAPTURE_UPLOAD_INVALID_STATE",
      "The recovered upload does not match its original evidence binding.",
    );
  }
  if (recovered.state === "rejected" || recovered.state === "expired") {
    throw new CaptureUploadError(
      "CAPTURE_UPLOAD_INVALID_STATE",
      "The upload intent can no longer accept evidence.",
    );
  }
}

function uploadStepKey(
  step: CapturePlanStep,
  method: typeof FILE_UPLOAD_METHOD | typeof LIVE_CAMERA_METHOD,
): string {
  return JSON.stringify([step.requirementKey, step.artefact, method, step.fallbackCondition]);
}

function resolveRegion(authority: ProcessingAuthority, requested?: string): string {
  if (requested !== undefined) {
    if (!authority.regions.includes(requested)) invalidSnapshot();
    return requested;
  }
  if (authority.regions.length !== 1) {
    throw new CaptureFlowError(
      "CAPTURE_FLOW_INVALID_SNAPSHOT",
      "Capture region must be selected when the authority permits more than one region.",
    );
  }
  return authority.regions[0]!;
}

function assertSnapshotCorrelation(
  session: VerificationSession,
  snapshot: CaptureAuthoritySnapshot,
): void {
  const { authority, notice, latestResponse } = snapshot;
  if (
    authority.verificationId !== session.id ||
    authority.noticeId !== notice.id ||
    authority.recipientDisplayName !== notice.recipient ||
    !session.requirements.requirements.every(
      (requirement) =>
        authority.requirementPurposes.includes(requirement.purpose) &&
        authority.evidenceTypes.includes(requirement.evidence_type),
    )
  ) {
    invalidSnapshot();
  }
  if (
    latestResponse !== undefined &&
    (latestResponse.authorityId !== authority.id ||
      latestResponse.noticeId !== notice.id ||
      latestResponse.subjectId !== authority.subjectId ||
      latestResponse.verificationId !== session.id)
  ) {
    invalidSnapshot();
  }
  if (
    notice.locale.trim() === "" ||
    notice.controller.trim() === "" ||
    notice.recipient.trim() === "" ||
    notice.copy.title.trim() === "" ||
    notice.copy.summary.trim() === "" ||
    notice.copy.purpose.trim() === "" ||
    notice.copy.consequences.trim() === ""
  ) {
    invalidSnapshot();
  }
}

function invalidSnapshot(): never {
  throw new CaptureFlowError(
    "CAPTURE_FLOW_INVALID_SNAPSHOT",
    "The capture authority snapshot does not match the verification session.",
  );
}

function flowStatus(snapshot: CaptureAuthoritySnapshot): CaptureActiveFlowStatus {
  const { authority, latestResponse } = snapshot;
  if (authority.state !== "active") return "authority_blocked";
  if (latestResponse?.action === "refuse") return "refused";
  if (latestResponse === undefined) return "notice_required";
  if (authority.consentRequired) {
    return latestResponse.action === "consent" ? "capture_ready" : "notice_required";
  }
  return latestResponse.action === "acknowledge" || latestResponse.action === "consent"
    ? "capture_ready"
    : "notice_required";
}

function assertAllowedResponse(
  authority: ProcessingAuthority,
  action: SubjectResponseAction,
): void {
  const acceptedAction = authority.consentRequired ? "consent" : "acknowledge";
  if (action !== acceptedAction && action !== "refuse") {
    throw new CaptureFlowError(
      "CAPTURE_FLOW_INVALID_STATE",
      `The current notice accepts only ${acceptedAction} or refuse.`,
    );
  }
}
