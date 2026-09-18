import type { components } from "./generated/openapi.js";
import { JSONTransport } from "./transport.js";
import type { SDKResponse } from "./types.js";

export interface ReviewRequestOptions {
  readonly signal?: AbortSignal;
}
export interface ReviewMutationOptions extends ReviewRequestOptions {
  readonly idempotencyKey: string;
}
/** Server-side tenant integration. Forward only capture credentials or redacted display output to browsers. */
export class ReviewsClient {
  constructor(
    private readonly transport: JSONTransport,
    private readonly token: string,
  ) {}
  async listReviewCases(
    query: {
      readonly state?: string;
      readonly region?: string;
      readonly reviewer?: string;
      readonly language?: string;
      readonly reason?: string;
      readonly assurance?: string;
      readonly risk?: string;
      readonly certificate?: string;
      readonly cursor?: string;
      readonly sampled?: boolean;
      readonly overdue?: boolean;
      readonly limit?: number;
    } = {},
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewQueue"]>> {
    let path = `/v1/review-cases`;
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(query))
      if (value !== undefined) search.set(key, String(value));
    if (search.size) path += `?${search}`;
    return this.transport.request<components["schemas"]["ReviewQueue"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async getReviewCase(
    caseID: string,
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewCase"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}`;
    return this.transport.request<components["schemas"]["ReviewCase"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async claimReviewCase(
    caseID: string,
    body: components["schemas"]["ReviewVersionRequest"],
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewCase"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/claim`;
    return this.transport.request<components["schemas"]["ReviewCase"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
    });
  }
  async submitReviewFinding(
    caseID: string,
    body: components["schemas"]["ReviewFindingRequest"],
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewCase"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/findings`;
    return this.transport.request<components["schemas"]["ReviewCase"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
    });
  }
  async attachReviewSuccessor(
    caseID: string,
    body: components["schemas"]["ReviewCorrectionRequest"],
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewCase"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/corrections`;
    return this.transport.request<components["schemas"]["ReviewCase"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
    });
  }
  async arbitrateReviewCase(
    caseID: string,
    body: components["schemas"]["ReviewArbitrationRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewFollowup"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/arbitrations`;
    return this.transport.request<components["schemas"]["ReviewFollowup"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async evaluateReviewCorrection(
    caseID: string,
    body: components["schemas"]["ReviewVersionRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewFollowup"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/corrections/evaluate`;
    return this.transport.request<components["schemas"]["ReviewFollowup"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async issueReviewEvidenceGrant(
    caseID: string,
    body: components["schemas"]["ReviewEvidenceRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewEvidenceGrant"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/evidence-grants`;
    return this.transport.request<components["schemas"]["ReviewEvidenceGrant"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async createReviewRecapture(
    caseID: string,
    body: components["schemas"]["ReviewVersionRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewRecaptureCredential"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/recaptures`;
    return this.transport.request<components["schemas"]["ReviewRecaptureCredential"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async renewReviewRecapture(
    caseID: string,
    body: components["schemas"]["ReviewRenewRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewRecaptureCredential"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/recaptures/renew`;
    return this.transport.request<components["schemas"]["ReviewRecaptureCredential"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async acknowledgeReviewRecapture(
    caseID: string,
    body: components["schemas"]["ReviewChildOutcomeRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewAcknowledgement"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/recaptures/acknowledgements`;
    return this.transport.request<components["schemas"]["ReviewAcknowledgement"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async reevaluateReviewRecapture(
    caseID: string,
    body: components["schemas"]["ReviewChildOutcomeRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewReevaluation"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/recaptures/reevaluations`;
    return this.transport.request<components["schemas"]["ReviewReevaluation"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async listReviewEvidence(
    caseID: string,
    query: { readonly expected_version: number },
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewEvidenceList"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/evidence`;
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(query))
      if (value !== undefined) search.set(key, String(value));
    if (search.size) path += `?${search}`;
    return this.transport.request<components["schemas"]["ReviewEvidenceList"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async readReviewEvidence(
    grantID: string,
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<Uint8Array>> {
    if (grantID.length === 0) throw new TypeError("Invalid grantID.");
    let path = `/v1/review-evidence-grants/${encodeURIComponent(String(grantID))}/content`;
    return this.transport.requestBytes({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async listReviewRecaptures(
    caseID: string,
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewRecaptureList"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/recaptures`;
    return this.transport.request<components["schemas"]["ReviewRecaptureList"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async openReviewCorrection(
    decisionID: string,
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewFollowup"]>> {
    if (decisionID.length === 0) throw new TypeError("Invalid decisionID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/decisions/${encodeURIComponent(String(decisionID))}/review-cases`;
    return this.transport.request<components["schemas"]["ReviewFollowup"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async requestReviewAppeal(
    caseID: string,
    body: components["schemas"]["ReviewAppealRequest"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewAppeal"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/appeals`;
    return this.transport.request<components["schemas"]["ReviewAppeal"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async getReviewAppeal(
    appealID: string,
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewAppeal"]>> {
    if (appealID.length === 0) throw new TypeError("Invalid appealID.");
    let path = `/v1/appeals/${encodeURIComponent(String(appealID))}`;
    return this.transport.request<components["schemas"]["ReviewAppeal"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async assignReviewAppeal(
    appealID: string,
    body: components["schemas"]["ReviewVersionRequest"],
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewAppeal"]>> {
    if (appealID.length === 0) throw new TypeError("Invalid appealID.");
    let path = `/v1/appeals/${encodeURIComponent(String(appealID))}/assign`;
    return this.transport.request<components["schemas"]["ReviewAppeal"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
    });
  }
  async resolveReviewAppeal(
    appealID: string,
    body: components["schemas"]["ReviewAppealResolutionRequest"],
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewAppeal"]>> {
    if (appealID.length === 0) throw new TypeError("Invalid appealID.");
    let path = `/v1/appeals/${encodeURIComponent(String(appealID))}/resolve`;
    return this.transport.request<components["schemas"]["ReviewAppeal"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
    });
  }
  async withdrawReviewAppeal(
    appealID: string,
    body: components["schemas"]["ReviewVersionRequest"],
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewAppeal"]>> {
    if (appealID.length === 0) throw new TypeError("Invalid appealID.");
    let path = `/v1/appeals/${encodeURIComponent(String(appealID))}/withdraw`;
    return this.transport.request<components["schemas"]["ReviewAppeal"]>({
      method: "POST",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
    });
  }
  async putReviewOperator(
    keyID: string,
    body: components["schemas"]["ReviewOperatorWrite"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewOperatorRecord"]>> {
    if (keyID.length === 0) throw new TypeError("Invalid keyID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-operators/${encodeURIComponent(String(keyID))}`;
    return this.transport.request<components["schemas"]["ReviewOperatorRecord"]>({
      method: "PUT",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async getReviewOperator(
    keyID: string,
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewOperatorRecord"]>> {
    if (keyID.length === 0) throw new TypeError("Invalid keyID.");
    let path = `/v1/review-operators/${encodeURIComponent(String(keyID))}`;
    return this.transport.request<components["schemas"]["ReviewOperatorRecord"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async putReviewPolicy(
    policyID: string,
    revision: number,
    body: components["schemas"]["ReviewPolicyWrite"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewPolicyRecord"]>> {
    if (policyID.length === 0) throw new TypeError("Invalid policyID.");
    if (!Number.isSafeInteger(revision) || revision < 1) throw new TypeError("Invalid revision.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-policies/${encodeURIComponent(String(policyID))}/revisions/${encodeURIComponent(String(revision))}`;
    return this.transport.request<components["schemas"]["ReviewPolicyRecord"]>({
      method: "PUT",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async getReviewPolicy(
    policyID: string,
    revision: number,
    options: ReviewRequestOptions = {},
  ): Promise<SDKResponse<components["schemas"]["ReviewPolicyRecord"]>> {
    if (policyID.length === 0) throw new TypeError("Invalid policyID.");
    if (!Number.isSafeInteger(revision) || revision < 1) throw new TypeError("Invalid revision.");
    let path = `/v1/review-policies/${encodeURIComponent(String(policyID))}/revisions/${encodeURIComponent(String(revision))}`;
    return this.transport.request<components["schemas"]["ReviewPolicyRecord"]>({
      method: "GET",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  }
  async putReviewQueue(
    caseID: string,
    body: components["schemas"]["ReviewQueueWrite"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewQueueRecord"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/queue`;
    return this.transport.request<components["schemas"]["ReviewQueueRecord"]>({
      method: "PUT",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
  async putReviewCaseSettings(
    caseID: string,
    body: components["schemas"]["ReviewPolicySettings"],
    options: ReviewMutationOptions,
  ): Promise<SDKResponse<components["schemas"]["ReviewPolicyRecord"]>> {
    if (caseID.length === 0) throw new TypeError("Invalid caseID.");
    if (!options.idempotencyKey || options.idempotencyKey.length > 128)
      throw new TypeError("An idempotency key is required.");
    let path = `/v1/review-cases/${encodeURIComponent(String(caseID))}/settings`;
    return this.transport.request<components["schemas"]["ReviewPolicyRecord"]>({
      method: "PUT",
      path,
      bearerToken: this.token,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
    });
  }
}

function idempotencyHeaders(key: string): Readonly<Record<string, string>> {
  if (key.length === 0 || /[^\x20-\x7e]/.test(key)) {
    throw new TypeError("idempotencyKey must be non-empty printable ASCII.");
  }
  const encoded = `"${key.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
  if (encoded.length > 130) {
    throw new TypeError("encoded idempotencyKey must not exceed 130 characters.");
  }
  return { "Idempotency-Key": encoded };
}
