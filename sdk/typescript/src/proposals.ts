import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { SDKResponse } from "./types.js";
import { resourceQuery } from "./administration.js";

export type Proposal = components["schemas"]["Proposal"];
export type ProposalActionKind = components["schemas"]["ProposalActionKind"];
export type ProposalMode = components["schemas"]["Proposal"]["mode"];
export type ProposalStatus = components["schemas"]["Proposal"]["status"];
export type ModeConfig = components["schemas"]["ModeConfig"];
export type Prompt = components["schemas"]["Prompt"];
export type ProposalModel = components["schemas"]["ProposalModel"];
export type ProposalActivation = components["schemas"]["ProposalActivation"];
export type ProposalActivationHistory = components["schemas"]["ProposalActivationHistory"];
export type ProposalUsageReport = components["schemas"]["ProposalUsageReport"];
export type ProposalImpactAssessment = components["schemas"]["ProposalImpactAssessment"];
export type ProposalImpactAssessmentCreate =
  components["schemas"]["ProposalImpactAssessmentCreate"];
export type ProposalImpactAssessmentList = components["schemas"]["ProposalImpactAssessmentList"];

export interface ModePinsInput {
  readonly model_registry_id: string;
  readonly model_registry_version: number;
  readonly prompt_id: string;
  readonly prompt_registry_version: number;
  readonly activation_revision: number;
}

/** An action whose bounded arguments are an opaque JSON object. */
export interface ProposalActionInput {
  readonly kind: ProposalActionKind;
  readonly args: unknown;
}

/** The redacted bounded context for a proposal. Raw evidence is never included. */
export interface ProposalRequestInput {
  readonly verification_id?: string;
  readonly policy_id?: string;
  readonly mode: ProposalMode;
  readonly actions: readonly ProposalActionInput[];
  readonly evidence_refs?: readonly string[];
  readonly signal_refs?: readonly string[];
  readonly model_id: string;
  readonly model_version: string;
  readonly prompt_version: string;
  readonly context_digest: string;
  readonly expires_at: string;
  readonly supersedes?: string;
}

export interface ProposalRequestOptions {
  readonly signal?: AbortSignal;
}

export interface ProposalMutationOptions extends ProposalRequestOptions {
  readonly idempotencyKey: string;
}

const PROPOSAL_ID = /^prp_[0-9A-HJKMNP-TV-Z]{26}$/;
const PROMPT_ID = /^prm_[0-9A-HJKMNP-TV-Z]{26}$/;
const MODEL_ID = /^mdl_[0-9A-HJKMNP-TV-Z]{26}$/;

/** Tenant backend operations for AI proposals, automation modes, and prompts. */
export class ProposalsClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}

  /** This API does not offer idempotent creation; do not blindly retry an ambiguous failure. */
  createImpactAssessment(
    input: ProposalImpactAssessmentCreate,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ProposalImpactAssessment>> {
    return this.transport.request({
      method: "POST",
      path: "v1/proposal-impact-assessments",
      body: input,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }

  getImpactAssessment(
    assessmentId: string,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ProposalImpactAssessment>> {
    if (!/^imp_[0-9]+$/.test(assessmentId))
      throw new TypeError("An impact assessment identifier is required.");
    return this.read(`v1/proposal-impact-assessments/${assessmentId}`, options);
  }

  listImpactAssessments(
    options: ProposalRequestOptions & { readonly limit?: number; readonly before?: string } = {},
  ): Promise<SDKResponse<ProposalImpactAssessmentList>> {
    return this.read(
      `v1/proposal-impact-assessments${resourceQuery({ limit: options.limit, before: options.before })}`,
      options,
    );
  }

  create(
    request: ProposalRequestInput,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<Proposal>> {
    return this.mutate<Proposal>("POST", "/v1/proposals", request, options);
  }

  get(proposalID: string, options: ProposalRequestOptions = {}): Promise<SDKResponse<Proposal>> {
    if (!PROPOSAL_ID.test(proposalID)) throw new TypeError("A proposal identifier is required.");
    return this.read<Proposal>(`/v1/proposals/${proposalID}`, options);
  }

  approve(
    proposalID: string,
    expectedVersion: number,
    humanApproved: boolean,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<Proposal>> {
    if (!PROPOSAL_ID.test(proposalID)) throw new TypeError("A proposal identifier is required.");
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 1)
      throw new TypeError("A positive expected version is required.");
    return this.mutate<Proposal>(
      "POST",
      `/v1/proposals/${proposalID}/approve`,
      { expected_version: expectedVersion, human_approved: humanApproved },
      options,
    );
  }

  reject(
    proposalID: string,
    expectedVersion: number,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<Proposal>> {
    if (!PROPOSAL_ID.test(proposalID)) throw new TypeError("A proposal identifier is required.");
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 1)
      throw new TypeError("A positive expected version is required.");
    return this.mutate<Proposal>(
      "POST",
      `/v1/proposals/${proposalID}/reject`,
      { expected_version: expectedVersion },
      options,
    );
  }

  cancel(
    proposalID: string,
    expectedVersion: number,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<Proposal>> {
    if (!PROPOSAL_ID.test(proposalID)) throw new TypeError("A proposal identifier is required.");
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 1)
      throw new TypeError("A positive expected version is required.");
    return this.mutate<Proposal>(
      "POST",
      `/v1/proposals/${proposalID}/cancel`,
      { expected_version: expectedVersion },
      options,
    );
  }

  putMode(
    workflow: string,
    mode: ProposalMode,
    expectedVersion: number,
    options: ProposalMutationOptions,
    allowedKinds: readonly ProposalActionKind[] = [],
    pins?: ModePinsInput,
  ): Promise<SDKResponse<ModeConfig>> {
    if (!/^[a-z0-9._-]{1,64}$/.test(workflow))
      throw new TypeError("A bounded workflow name is required.");
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 0)
      throw new TypeError("A non-negative expected version is required.");
    if (pins !== undefined) {
      if (!MODEL_ID.test(pins.model_registry_id) || !PROMPT_ID.test(pins.prompt_id))
        throw new TypeError("Valid model and prompt registry identifiers are required.");
      this.validatePositiveRevision(pins.model_registry_version, "model registry version");
      this.validatePositiveRevision(pins.prompt_registry_version, "prompt registry version");
      this.validatePositiveRevision(pins.activation_revision, "activation revision");
    }
    return this.mutate<ModeConfig>(
      "PUT",
      `/v1/proposal-modes/${workflow}`,
      { mode, allowed_kinds: allowedKinds, expected_version: expectedVersion, ...pins },
      options,
    );
  }

  getMode(
    workflow: string,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ModeConfig>> {
    if (!/^[a-z0-9._-]{1,64}$/.test(workflow))
      throw new TypeError("A bounded workflow name is required.");
    return this.read<ModeConfig>(`/v1/proposal-modes/${workflow}`, options);
  }

  createPrompt(
    content: string,
    modelID: string,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<Prompt>> {
    if (!content || content.length > 16384)
      throw new TypeError("Bounded prompt content is required.");
    if (!modelID) throw new TypeError("A model identifier is required.");
    return this.mutate<Prompt>("POST", "/v1/prompts", { content, model_id: modelID }, options);
  }

  getPrompt(
    promptID: string,
    version: number,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<Prompt>> {
    if (!PROMPT_ID.test(promptID)) throw new TypeError("A prompt identifier is required.");
    if (!Number.isSafeInteger(version) || version < 1)
      throw new TypeError("A positive prompt version is required.");
    return this.read<Prompt>(`/v1/prompts/${promptID}/${version}`, options);
  }

  createModel(
    modelID: string,
    digest: string,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<ProposalModel>> {
    if (!/^[a-z][a-z0-9._:-]{0,63}$/.test(modelID))
      throw new TypeError("A logical model identifier is required.");
    if (!/^[0-9a-f]{64}$/.test(digest)) throw new TypeError("A model digest is required.");
    return this.mutate<ProposalModel>(
      "POST",
      "/v1/proposal-models",
      { model_id: modelID, digest },
      options,
    );
  }

  getModel(
    modelID: string,
    version: number,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ProposalModel>> {
    if (!MODEL_ID.test(modelID)) throw new TypeError("A model registry identifier is required.");
    if (!Number.isSafeInteger(version) || version < 1)
      throw new TypeError("A positive model version is required.");
    return this.read<ProposalModel>(`/v1/proposal-models/${modelID}/${version}`, options);
  }

  activate(
    workflow: string,
    input: components["schemas"]["ProposalActivationRequest"],
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<ProposalActivation>> {
    this.validateWorkflow(workflow);
    if (!MODEL_ID.test(input.model_registry_id) || !PROMPT_ID.test(input.prompt_registry_id))
      throw new TypeError("Valid model and prompt registry identifiers are required.");
    this.validatePositiveRevision(input.model_registry_version, "model registry version");
    this.validatePositiveRevision(input.prompt_registry_version, "prompt registry version");
    if (!Number.isSafeInteger(input.expected_revision) || input.expected_revision < 0)
      throw new TypeError("A non-negative expected revision is required.");
    return this.mutate<ProposalActivation>(
      "PUT",
      `/v1/proposal-activations/${workflow}`,
      input,
      options,
    );
  }

  getActivation(
    workflow: string,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ProposalActivation>> {
    this.validateWorkflow(workflow);
    return this.read<ProposalActivation>(`/v1/proposal-activations/${workflow}`, options);
  }

  activationHistory(
    workflow: string,
    limit = 50,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ProposalActivationHistory>> {
    this.validateWorkflow(workflow);
    if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100)
      throw new TypeError("Activation history limit must be from 1 to 100.");
    return this.read<ProposalActivationHistory>(
      `/v1/proposal-activations/${workflow}/history?limit=${limit}`,
      options,
    );
  }

  retire(
    workflow: string,
    expectedRevision: number,
    reason: string,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<ProposalActivation>> {
    this.validateWorkflow(workflow);
    this.validatePositiveRevision(expectedRevision, "expected revision");
    return this.mutate<ProposalActivation>(
      "POST",
      `/v1/proposal-activations/${workflow}/retire`,
      { expected_revision: expectedRevision, reason },
      options,
    );
  }

  rollback(
    workflow: string,
    expectedRevision: number,
    targetRevision: number,
    reason: string,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<ProposalActivation>> {
    this.validateWorkflow(workflow);
    this.validatePositiveRevision(expectedRevision, "expected revision");
    this.validatePositiveRevision(targetRevision, "target revision");
    return this.mutate<ProposalActivation>(
      "POST",
      `/v1/proposal-activations/${workflow}/rollback`,
      { expected_revision: expectedRevision, target_revision: targetRevision, reason },
      options,
    );
  }

  usage(
    from: string,
    to: string,
    options: ProposalRequestOptions = {},
  ): Promise<SDKResponse<ProposalUsageReport>> {
    const fromTime = Date.parse(from);
    const toTime = Date.parse(to);
    if (
      !Number.isFinite(fromTime) ||
      !Number.isFinite(toTime) ||
      toTime <= fromTime ||
      toTime - fromTime > 366 * 24 * 60 * 60 * 1000
    )
      throw new TypeError("A valid usage interval of at most 366 days is required.");
    return this.read<ProposalUsageReport>(
      `/v1/proposal-usage?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
      options,
    );
  }

  private validateWorkflow(workflow: string): void {
    if (!/^[a-z0-9._-]{1,64}$/.test(workflow))
      throw new TypeError("A bounded workflow name is required.");
  }

  private validatePositiveRevision(value: number, name: string): void {
    if (!Number.isSafeInteger(value) || value < 1)
      throw new TypeError(`A positive ${name} is required.`);
  }

  private read<T>(path: string, options: ProposalRequestOptions): Promise<SDKResponse<T>> {
    return this.transport.request<T>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }

  private mutate<T>(
    method: "POST" | "PUT",
    path: string,
    body: unknown,
    options: ProposalMutationOptions,
  ): Promise<SDKResponse<T>> {
    if (
      !/^[\x21-\x7e]{1,200}$/.test(options.idempotencyKey) ||
      /["\\]/.test(options.idempotencyKey)
    )
      throw new TypeError("A bounded idempotency key is required.");
    return this.transport.request<T>({
      method,
      path,
      body,
      bearerToken: this.token,
      headers: { "Idempotency-Key": JSON.stringify(options.idempotencyKey) },
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
}
