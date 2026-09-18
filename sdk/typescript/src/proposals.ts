import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { SDKResponse } from "./types.js";

export type Proposal = components["schemas"]["Proposal"];
export type ProposalActionKind = components["schemas"]["ProposalActionKind"];
export type ProposalMode = components["schemas"]["Proposal"]["mode"];
export type ProposalStatus = components["schemas"]["Proposal"]["status"];
export type ModeConfig = components["schemas"]["ModeConfig"];
export type Prompt = components["schemas"]["Prompt"];

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

/** Tenant backend operations for AI proposals, automation modes, and prompts. */
export class ProposalsClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}

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
  ): Promise<SDKResponse<ModeConfig>> {
    if (!/^[a-z0-9._-]{1,64}$/.test(workflow))
      throw new TypeError("A bounded workflow name is required.");
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 0)
      throw new TypeError("A non-negative expected version is required.");
    return this.mutate<ModeConfig>(
      "PUT",
      `/v1/proposal-modes/${workflow}`,
      { mode, allowed_kinds: allowedKinds, expected_version: expectedVersion },
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
