import { AssuranceClient } from "./assurance.js";
import {
  validateExperienceResolution,
  type ExperienceRequestOptions,
  type ExperienceResolution,
} from "./experience.js";
import { IdentityClient } from "./identity.js";
import { FraudClient } from "./fraud.js";
import { ProposalsClient } from "./proposals.js";
import { ReviewsClient } from "./reviews.js";
import type {
  PolicySummary,
  PolicyRevisionDocument,
  PolicyValidation,
  PolicyMutation,
  PolicyList,
  PolicyRevisionList,
  PolicyActivationList,
  PolicyID,
  PolicyDefinition,
  PolicyListOptions,
} from "./types.js";
import type {
  WirePolicySummary,
  WirePolicyRevisionDocument,
  WirePolicyValidation,
  WirePolicyMutation,
  WirePolicyList,
  WirePolicyRevisionList,
  WirePolicyActivationList,
} from "./wire.js";
import {
  policySummary,
  policyRevisionDocument,
  policyValidation,
  policyMutation,
  policyList,
  policyRevisionList,
  policyActivationList,
} from "./mappers.js";
import type {
  WebhookEndpoint,
  WebhookDelivery,
  WebhookEndpointMutation,
  WebhookDeliveryMutation,
  WebhookEndpointList,
  WebhookDeliveryList,
  WebhookAttemptList,
  WebhookEndpointID,
  WebhookDeliveryID,
  WebhookListOptions,
} from "./types.js";
import type {
  WireWebhookEndpoint,
  WireWebhookDelivery,
  WireWebhookEndpointMutation,
  WireWebhookDeliveryMutation,
  WireWebhookEndpointList,
  WireWebhookDeliveryList,
  WireWebhookAttemptList,
} from "./wire.js";
import {
  webhookEndpoint,
  webhookDelivery,
  webhookEndpointMutation,
  webhookDeliveryMutation,
  webhookEndpointList,
  webhookDeliveryList,
  webhookAttemptList,
} from "./mappers.js";
import {
  captureProfile,
  captureProfileList,
  captureProfileMutation,
  captureProfileRevision,
  captureProfileValidation,
  verificationCreated,
  verificationSession,
  verificationCancellation,
  verificationResumed,
  noticeVersion,
  processingAuthority,
  subjectResponse,
  captureAuthoritySnapshot,
  evidenceUpload,
  captureProgress,
  captureOutcome,
  captureConnection,
  policyDecisionBundle,
  policyDecisionReport,
} from "./mappers.js";
import { JSONTransport } from "./transport.js";
import {
  createCaptureObservation,
  type CaptureObservation,
  type CaptureObserveOptions,
} from "./realtime.js";
import type {
  CaptureClientOptions,
  OutcomeClientOptions,
  CaptureProfile,
  CaptureProfileID,
  CaptureProfileList,
  CaptureProfileListOptions,
  CaptureProfileMutation,
  CaptureProfileRevision,
  CaptureProfileValidation,
  CaptureProfileWrite,
  ConditionalIdempotentRequestOptions,
  ConditionalRequestOptions,
  IdempotentRequestOptions,
  RequestOptions,
  SDKResponse,
  SDKConditionalResponse,
  TenantClientOptions,
  VerificationCreate,
  VerificationCreated,
  VerificationID,
  VerificationSession,
  VerificationCancellation,
  VerificationResumed,
  NoticeID,
  NoticeVersion,
  NoticeVersionCreate,
  ProcessingAuthority,
  ProcessingAuthorityDeclare,
  SubjectResponse,
  SubjectResponseCreate,
  CaptureAuthoritySnapshot,
  CaptureProgress,
  CaptureOutcome,
  CaptureConnection,
  EvidenceUpload,
  EvidenceUploadCreate,
  EvidenceUploadID,
  EvidenceUploadOptions,
  EntityTag,
  DecisionID,
  PolicyDecisionBundle,
  PolicyDecisionReport,
} from "./types.js";
import type {
  WireCaptureProfile,
  WireCaptureProfileList,
  WireCaptureProfileMutation,
  WireCaptureProfileRevision,
  WireCaptureProfileSupersede,
  WireCaptureProfileValidation,
  WireCaptureProfileWrite,
  WireVerificationCreate,
  WireVerificationCreated,
  WireVerificationSession,
  WireVerificationCancellation,
  WireVerificationResumed,
  WireNoticeVersion,
  WireNoticeVersionCreate,
  WireProcessingAuthority,
  WireProcessingAuthorityDeclare,
  WireSubjectResponse,
  WireSubjectResponseCreate,
  WireCaptureAuthoritySnapshot,
  WireCaptureProgress,
  WireCaptureOutcome,
  WireCaptureConnection,
  WireEvidenceUpload,
  WireEvidenceUploadCreate,
  WirePolicyDecisionBundle,
  WirePolicyDecisionReport,
} from "./wire.js";

export class IdenqaClient {
  readonly reviews: ReviewsClient;
  readonly fraud: FraudClient;
  readonly identity: IdentityClient;
  readonly assurance: AssuranceClient;
  readonly proposals: ProposalsClient;
  readonly captureProfiles: CaptureProfilesClient;
  readonly verifications: VerificationsClient;
  readonly notices: NoticesClient;
  readonly authorities: AuthoritiesClient;
  readonly decisions: DecisionsClient;
  readonly webhooks: WebhooksClient;
  readonly policies: PoliciesClient;

  constructor(options: TenantClientOptions) {
    const token = requiredToken(options.apiKey, "apiKey");
    const transport = new JSONTransport(options);
    this.reviews = new ReviewsClient(transport, token);
    this.fraud = new FraudClient(transport, token);
    this.identity = new IdentityClient(transport, token);
    this.assurance = new AssuranceClient(transport, token);
    this.proposals = new ProposalsClient(transport, token);
    this.captureProfiles = new CaptureProfilesClient(transport, token);
    this.verifications = new VerificationsClient(transport, token);
    this.notices = new NoticesClient(transport, token);
    this.authorities = new AuthoritiesClient(transport, token);
    this.decisions = new DecisionsClient(transport, token);
    this.webhooks = new WebhooksClient(transport, token);
    this.policies = new PoliciesClient(transport, token);
  }
}

export class DecisionsClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }

  async get(
    decisionId: DecisionID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PolicyDecisionReport>> {
    const response = await this.#transport.request<WirePolicyDecisionReport>({
      method: "GET",
      path: this.#decisionPath(decisionId),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policyDecisionReport);
  }

  async poll(
    decisionId: DecisionID,
    etag: EntityTag,
    options: RequestOptions = {},
  ): Promise<SDKConditionalResponse<PolicyDecisionReport>> {
    const response = await this.#transport.request<WirePolicyDecisionReport>({
      method: "GET",
      path: this.#decisionPath(decisionId),
      bearerToken: this.#token,
      headers: { "If-None-Match": entityTag(etag) },
      allowNotModified: true,
      ...signal(options),
    });
    if (response.notModified) return response;
    return { ...mapResponse(response, policyDecisionReport), notModified: false };
  }

  async getLatest(
    verificationId: VerificationID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PolicyDecisionReport>> {
    const response = await this.#transport.request<WirePolicyDecisionReport>({
      method: "GET",
      path: `v1/verifications/${pathSegment(verificationId, "verificationId")}/decision`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policyDecisionReport);
  }

  async pollLatest(
    verificationId: VerificationID,
    etag: EntityTag,
    options: RequestOptions = {},
  ): Promise<SDKConditionalResponse<PolicyDecisionReport>> {
    const response = await this.#transport.request<WirePolicyDecisionReport>({
      method: "GET",
      path: `v1/verifications/${pathSegment(verificationId, "verificationId")}/decision`,
      bearerToken: this.#token,
      headers: { "If-None-Match": entityTag(etag) },
      allowNotModified: true,
      ...signal(options),
    });
    if (response.notModified) return response;
    return { ...mapResponse(response, policyDecisionReport), notModified: false };
  }

  async exportBundle(
    decisionId: DecisionID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PolicyDecisionBundle>> {
    const response = await this.#transport.requestCanonicalJSON<WirePolicyDecisionBundle>(
      {
        method: "GET",
        path: `${this.#decisionPath(decisionId)}/bundle`,
        bearerToken: this.#token,
        ...signal(options),
      },
      "application/vnd.idenqa.decision-bundle.v1+json",
    );
    return mapResponse(response, ({ canonical, document }) =>
      policyDecisionBundle(document, canonical),
    );
  }

  async pollBundle(
    decisionId: DecisionID,
    etag: EntityTag,
    options: RequestOptions = {},
  ): Promise<SDKConditionalResponse<PolicyDecisionBundle>> {
    const response = await this.#transport.requestCanonicalJSON<WirePolicyDecisionBundle>(
      {
        method: "GET",
        path: `${this.#decisionPath(decisionId)}/bundle`,
        bearerToken: this.#token,
        headers: { "If-None-Match": entityTag(etag) },
        allowNotModified: true,
        ...signal(options),
      },
      "application/vnd.idenqa.decision-bundle.v1+json",
    );
    if (response.notModified) return response;
    return {
      ...mapResponse(response, ({ canonical, document }) =>
        policyDecisionBundle(document, canonical),
      ),
      notModified: false,
    };
  }

  #decisionPath(decisionId: DecisionID): string {
    return `v1/decisions/${pathSegment(decisionId, "decisionId")}`;
  }
}

export class CaptureClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(options: CaptureClientOptions) {
    this.#token = requiredToken(options.captureToken, "captureToken");
    this.#transport = new JSONTransport(options);
  }

  /** Cancels this token's verification. Reuse the key and expected version on retry. */
  async cancel(
    expectedVersion: number,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<VerificationCancellation>> {
    const response = await this.#transport.request<WireVerificationCancellation>({
      method: "POST",
      path: "v1/capture/cancel",
      bearerToken: this.#token,
      body: { expected_version: positiveInteger(expectedVersion, "expectedVersion") },
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, verificationCancellation);
  }

  async getSession(options: RequestOptions = {}): Promise<SDKResponse<VerificationSession>> {
    const response = await this.#transport.request<WireVerificationSession>({
      method: "GET",
      path: "v1/capture/session",
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, verificationSession);
  }

  async getAuthority(options: RequestOptions = {}): Promise<SDKResponse<CaptureAuthoritySnapshot>> {
    const response = await this.#transport.request<WireCaptureAuthoritySnapshot>({
      method: "GET",
      path: "v1/capture/authority",
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureAuthoritySnapshot);
  }

  /**
   * Resolves the session's pinned portable capture experience. The first call
   * pins the exact experience, locale, tenant-copy, and mandatory-copy
   * versions; later calls preserve them across resume and fall back to the
   * signed accessible safe default after revocation or resolution failure.
   */
  async getExperience(
    options: ExperienceRequestOptions = {},
  ): Promise<SDKResponse<ExperienceResolution>> {
    const query = new URLSearchParams();
    for (const [name, value] of [
      ["workflow", options.workflow],
      ["country", options.country],
      ["application_id", options.applicationId],
      ["origin", options.origin],
      ["sdk_version", options.sdkVersion],
      ["locale", options.locale],
    ] as const) {
      if (value !== undefined) query.set(name, value);
    }
    const suffix = query.size > 0 ? `?${query.toString()}` : "";
    const response = await this.#transport.request<unknown>({
      method: "GET",
      path: `v1/capture/experience${suffix}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, validateExperienceResolution);
  }

  async getProgress(options: RequestOptions = {}): Promise<SDKResponse<CaptureProgress>> {
    const response = await this.#transport.request<WireCaptureProgress>({
      method: "GET",
      path: "v1/capture/progress",
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureProgress);
  }

  async pollProgress(
    etag: string,
    options: RequestOptions = {},
  ): Promise<SDKConditionalResponse<CaptureProgress>> {
    const response = await this.#transport.request<WireCaptureProgress>({
      method: "GET",
      path: "v1/capture/progress",
      bearerToken: this.#token,
      headers: { "If-None-Match": entityTag(etag) },
      allowNotModified: true,
      ...signal(options),
    });
    if (response.notModified) return response;
    return { ...mapResponse(response, captureProgress), notModified: false };
  }

  async createConnection(options: RequestOptions = {}): Promise<SDKResponse<CaptureConnection>> {
    const response = await this.#transport.request<WireCaptureConnection>({
      method: "POST",
      path: "v1/capture/connections",
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureConnection);
  }

  observe(session: VerificationSession, options: CaptureObserveOptions = {}): CaptureObservation {
    return createCaptureObservation(
      session,
      async (abortSignal) => (await this.createConnection({ signal: abortSignal })).data,
      options,
    );
  }

  async respond(
    input: SubjectResponseCreate,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<SubjectResponse>> {
    const body: WireSubjectResponseCreate = {
      action: input.action,
      locale: input.locale,
      ...(input.renderedExperienceVersion === undefined
        ? {}
        : {
            rendered_experience_version: input.renderedExperienceVersion,
          }),
    };
    const response = await this.#transport.request<WireSubjectResponse>({
      method: "POST",
      path: "v1/capture/authority/responses",
      bearerToken: this.#token,
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, subjectResponse);
  }

  async createEvidenceUpload(
    input: EvidenceUploadCreate,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<EvidenceUpload>> {
    const body: WireEvidenceUploadCreate = {
      requirement_key: input.requirementKey,
      artefact: input.artefact,
      acquisition_method: input.acquisitionMethod,
      ...(input.fallbackCondition === undefined
        ? {}
        : { fallback_condition: input.fallbackCondition }),
      expected_bytes: positiveInteger(input.expectedBytes, "expectedBytes"),
      expected_digest: digest(input.expectedDigest),
      media_type: input.mediaType,
      region: input.region,
    };
    const response = await this.#transport.request<WireEvidenceUpload>({
      method: "POST",
      path: "v1/evidence-uploads",
      bearerToken: this.#token,
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, evidenceUpload);
  }

  async getEvidenceUpload(
    uploadId: EvidenceUploadID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<EvidenceUpload>> {
    const response = await this.#transport.request<WireEvidenceUpload>({
      method: "GET",
      path: `v1/evidence-uploads/${pathSegment(uploadId, "uploadId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, evidenceUpload);
  }

  async uploadEvidence(
    uploadId: EvidenceUploadID,
    body: Blob,
    options: EvidenceUploadOptions,
  ): Promise<SDKResponse<EvidenceUpload>> {
    if (!(body instanceof Blob) || body.size <= 0) {
      throw new TypeError("body must be one non-empty Blob.");
    }
    if (body.type !== "image/jpeg" && body.type !== "image/png") {
      throw new TypeError("body.type must be exactly image/jpeg or image/png.");
    }
    const response = await this.#transport.request<WireEvidenceUpload>({
      method: "PUT",
      path: `v1/evidence-uploads/${pathSegment(uploadId, "uploadId")}`,
      bearerToken: this.#token,
      rawBody: body,
      headers: {
        "Content-Type": body.type,
        "Content-Digest": contentDigest(options.digest),
        "If-Match": entityTag(options.etag),
      },
      ...signal(options),
    });
    return mapResponse(response, evidenceUpload);
  }
}

/** Read-only subject outcome client. Its credential cannot call capture operations. */
export class OutcomeClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(options: OutcomeClientOptions) {
    this.#token = requiredToken(options.outcomeToken, "outcomeToken");
    this.#transport = new JSONTransport(options);
  }

  async getOutcome(options: RequestOptions = {}): Promise<SDKResponse<CaptureOutcome>> {
    const response = await this.#transport.request<WireCaptureOutcome>({
      method: "GET",
      path: "v1/capture/outcome",
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureOutcome);
  }
}

export class NoticesClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }

  async create(
    input: NoticeVersionCreate,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<NoticeVersion>> {
    const body: WireNoticeVersionCreate = {
      key: input.key,
      locale: input.locale,
      controller: input.controller,
      recipient: input.recipient,
      copy: input.copy,
      effective_at: input.effectiveAt,
    };
    const response = await this.#transport.request<WireNoticeVersion>({
      method: "POST",
      path: "v1/notices",
      bearerToken: this.#token,
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, noticeVersion);
  }

  async get(noticeId: NoticeID, options: RequestOptions = {}): Promise<SDKResponse<NoticeVersion>> {
    const response = await this.#transport.request<WireNoticeVersion>({
      method: "GET",
      path: `v1/notices/${pathSegment(noticeId, "noticeId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, noticeVersion);
  }
}

export class AuthoritiesClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }

  async declare(
    verificationId: VerificationID,
    input: ProcessingAuthorityDeclare,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<ProcessingAuthority>> {
    const body: WireProcessingAuthorityDeclare = {
      notice_id: input.noticeId,
      category: input.category,
      purpose: input.purpose,
      jurisdiction: input.jurisdiction,
      policy_pack: input.policyPack,
      consent_required: input.consentRequired,
      recipient_reference: input.recipientReference,
      recipient_display_name: input.recipientDisplayName,
      regions: [...input.regions],
      retention_reference: input.retentionReference,
      valid_from: input.validFrom,
      expires_at: input.expiresAt,
    };
    const response = await this.#transport.request<WireProcessingAuthority>({
      method: "POST",
      path: `${this.#path(verificationId)}`,
      bearerToken: this.#token,
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, processingAuthority);
  }

  async get(
    verificationId: VerificationID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<ProcessingAuthority>> {
    const response = await this.#transport.request<WireProcessingAuthority>({
      method: "GET",
      path: this.#path(verificationId),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, processingAuthority);
  }

  async restrict(verificationId: VerificationID, options: ConditionalIdempotentRequestOptions) {
    return this.#transition(verificationId, "restrict", options);
  }

  async withdraw(verificationId: VerificationID, options: ConditionalIdempotentRequestOptions) {
    return this.#transition(verificationId, "withdraw", options);
  }

  async supersede(verificationId: VerificationID, options: ConditionalIdempotentRequestOptions) {
    return this.#transition(verificationId, "supersede", options);
  }

  async #transition(
    verificationId: VerificationID,
    action: "restrict" | "withdraw" | "supersede",
    options: ConditionalIdempotentRequestOptions,
  ): Promise<SDKResponse<ProcessingAuthority>> {
    const response = await this.#transport.request<WireProcessingAuthority>({
      method: "POST",
      path: `${this.#path(verificationId)}/${action}`,
      bearerToken: this.#token,
      headers: conditionalIdempotencyHeaders(options),
      ...signal(options),
    });
    return mapResponse(response, processingAuthority);
  }

  #path(verificationId: VerificationID): string {
    return `v1/verifications/${pathSegment(verificationId, "verificationId")}/authority`;
  }
}

export class CaptureProfilesClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }

  async create(
    input: CaptureProfileWrite,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<CaptureProfileMutation>> {
    const body: WireCaptureProfileWrite = input;
    const response = await this.#transport.request<WireCaptureProfileMutation>({
      method: "POST",
      path: "v1/capture-profiles",
      bearerToken: this.#token,
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, captureProfileMutation);
  }

  async list(options: CaptureProfileListOptions = {}): Promise<SDKResponse<CaptureProfileList>> {
    const query = new URLSearchParams();
    if (options.cursor !== undefined) query.set("cursor", options.cursor);
    if (options.limit !== undefined) {
      query.set("limit", String(positiveInteger(options.limit, "limit")));
    }
    const suffix = query.size === 0 ? "" : `?${query.toString()}`;
    const response = await this.#transport.request<WireCaptureProfileList>({
      method: "GET",
      path: `v1/capture-profiles${suffix}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureProfileList);
  }

  async get(
    profileId: CaptureProfileID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<CaptureProfile>> {
    const response = await this.#transport.request<WireCaptureProfile>({
      method: "GET",
      path: `v1/capture-profiles/${pathSegment(profileId, "profileId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureProfile);
  }

  async updateDraft(
    profileId: CaptureProfileID,
    input: CaptureProfileWrite,
    options: ConditionalRequestOptions,
  ): Promise<SDKResponse<CaptureProfileMutation>> {
    const body: WireCaptureProfileWrite = input;
    const response = await this.#transport.request<WireCaptureProfileMutation>({
      method: "PUT",
      path: `v1/capture-profiles/${pathSegment(profileId, "profileId")}/draft`,
      bearerToken: this.#token,
      body,
      headers: { "If-Match": entityTag(options.etag) },
      ...signal(options),
    });
    return mapResponse(response, captureProfileMutation);
  }

  async validateDraft(
    profileId: CaptureProfileID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<CaptureProfileValidation>> {
    const response = await this.#transport.request<WireCaptureProfileValidation>({
      method: "POST",
      path: `v1/capture-profiles/${pathSegment(profileId, "profileId")}/validate`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureProfileValidation);
  }

  async publish(
    profileId: CaptureProfileID,
    options: ConditionalIdempotentRequestOptions,
  ): Promise<SDKResponse<CaptureProfileMutation>> {
    return this.#conditionalCommand(profileId, "publish", options);
  }

  async supersede(
    profileId: CaptureProfileID,
    document: CaptureProfileWrite["document"],
    options: ConditionalIdempotentRequestOptions,
  ): Promise<SDKResponse<CaptureProfileMutation>> {
    const body: WireCaptureProfileSupersede = { document };
    const response = await this.#transport.request<WireCaptureProfileMutation>({
      method: "POST",
      path: `v1/capture-profiles/${pathSegment(profileId, "profileId")}/supersede`,
      bearerToken: this.#token,
      body,
      headers: conditionalIdempotencyHeaders(options),
      ...signal(options),
    });
    return mapResponse(response, captureProfileMutation);
  }

  async deactivate(
    profileId: CaptureProfileID,
    options: ConditionalIdempotentRequestOptions,
  ): Promise<SDKResponse<CaptureProfileMutation>> {
    return this.#conditionalCommand(profileId, "deactivate", options);
  }

  async getRevision(
    profileId: CaptureProfileID,
    revision: number,
    options: RequestOptions = {},
  ): Promise<SDKResponse<CaptureProfileRevision>> {
    const response = await this.#transport.request<WireCaptureProfileRevision>({
      method: "GET",
      path: `v1/capture-profiles/${pathSegment(profileId, "profileId")}/revisions/${positiveInteger(revision, "revision")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, captureProfileRevision);
  }

  async #conditionalCommand(
    profileId: CaptureProfileID,
    operation: "publish" | "deactivate",
    options: ConditionalIdempotentRequestOptions,
  ): Promise<SDKResponse<CaptureProfileMutation>> {
    const response = await this.#transport.request<WireCaptureProfileMutation>({
      method: "POST",
      path: `v1/capture-profiles/${pathSegment(profileId, "profileId")}/${operation}`,
      bearerToken: this.#token,
      headers: conditionalIdempotencyHeaders(options),
      ...signal(options),
    });
    return mapResponse(response, captureProfileMutation);
  }
}

export class VerificationsClient {
  readonly #transport: JSONTransport;
  readonly #token: string;

  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }

  async create(
    input: VerificationCreate,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<VerificationCreated>> {
    const body: WireVerificationCreate = {
      capture_profile_id: input.captureProfileId,
      policy_id: input.policyId,
      ...(input.verificationTtlSeconds === undefined
        ? {}
        : {
            verification_ttl_seconds: positiveInteger(
              input.verificationTtlSeconds,
              "verificationTtlSeconds",
            ),
          }),
      ...(input.captureTokenTtlSeconds === undefined
        ? {}
        : {
            capture_token_ttl_seconds: positiveInteger(
              input.captureTokenTtlSeconds,
              "captureTokenTtlSeconds",
            ),
          }),
      ...(input.outcomeTokenPostExpiryTtlSeconds === undefined
        ? {}
        : {
            outcome_token_post_expiry_ttl_seconds: positiveInteger(
              input.outcomeTokenPostExpiryTtlSeconds,
              "outcomeTokenPostExpiryTtlSeconds",
            ),
          }),
    };
    const response = await this.#transport.request<WireVerificationCreated>({
      method: "POST",
      path: "v1/verifications",
      bearerToken: this.#token,
      body,
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, verificationCreated);
  }

  /** Requires verification_sessions:cancel; returns the original receipt on retry. */
  async cancel(
    verificationId: VerificationID,
    expectedVersion: number,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<VerificationCancellation>> {
    const response = await this.#transport.request<WireVerificationCancellation>({
      method: "POST",
      path: `v1/verifications/${pathSegment(verificationId, "verificationId")}/cancel`,
      bearerToken: this.#token,
      body: { expected_version: positiveInteger(expectedVersion, "expectedVersion") },
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, verificationCancellation);
  }

  /** Requires verification_sessions:resume; bearer material is display-once. */
  async resume(
    verificationId: VerificationID,
    expectedVersion: number,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<VerificationResumed>> {
    const response = await this.#transport.request<WireVerificationResumed>({
      method: "POST",
      path: `v1/verifications/${pathSegment(verificationId, "verificationId")}/resume`,
      bearerToken: this.#token,
      body: { expected_version: positiveInteger(expectedVersion, "expectedVersion") },
      headers: idempotencyHeaders(options.idempotencyKey),
      ...signal(options),
    });
    return mapResponse(response, verificationResumed);
  }

  async get(
    verificationId: VerificationID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<VerificationSession>> {
    const response = await this.#transport.request<WireVerificationSession>({
      method: "GET",
      path: `v1/verifications/${pathSegment(verificationId, "verificationId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, verificationSession);
  }
}

/** Creates a non-secret retry key. Persist and reuse it with the same operation input. */
export function createIdempotencyKey(prefix = "sdk"): string {
  if (!/^[A-Za-z0-9_-]{1,32}$/.test(prefix)) {
    throw new TypeError("idempotency key prefix must contain 1-32 safe ASCII characters.");
  }
  return `${prefix}_${globalThis.crypto.randomUUID()}`;
}

function signal(options: RequestOptions): { readonly signal?: AbortSignal } {
  return options.signal === undefined ? {} : { signal: options.signal };
}

function idempotencyHeaders(key: string): Readonly<Record<string, string>> {
  const encoded = structuredString(key);
  return { "Idempotency-Key": encoded };
}

function conditionalIdempotencyHeaders(
  options: ConditionalIdempotentRequestOptions,
): Readonly<Record<string, string>> {
  return {
    ...idempotencyHeaders(options.idempotencyKey),
    "If-Match": entityTag(options.etag),
  };
}

function structuredString(value: string): string {
  if (value.length === 0 || /[^\x20-\x7e]/.test(value)) {
    throw new TypeError("idempotencyKey must be non-empty printable ASCII.");
  }
  const encoded = `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
  if (encoded.length > 130) {
    throw new TypeError("encoded idempotencyKey must not exceed 130 characters.");
  }
  return encoded;
}

function entityTag(value: string): string {
  if (!/^"[^"\\]+"$/.test(value)) {
    throw new TypeError("etag must be one strong quoted entity tag returned by Idenqa Core.");
  }
  return value;
}

function digest(value: string): string {
  if (!/^sha256:[0-9a-f]{64}$/.test(value)) {
    throw new TypeError("digest must be one canonical SHA-256 digest.");
  }
  return value;
}

function contentDigest(value: string): string {
  const canonical = digest(value);
  const hex = canonical.slice("sha256:".length);
  let binary = "";
  for (let index = 0; index < hex.length; index += 2) {
    binary += String.fromCharCode(Number.parseInt(hex.slice(index, index + 2), 16));
  }
  return `sha-256=:${globalThis.btoa(binary)}:`;
}

function requiredToken(value: string, name: string): string {
  if (value.trim() === "" || /[\r\n]/.test(value)) {
    throw new TypeError(`${name} must be a non-empty bearer credential.`);
  }
  return value;
}

function pathSegment(value: string, name: string): string {
  if (value === "") throw new TypeError(`${name} must not be empty.`);
  return encodeURIComponent(value);
}

function positiveInteger(value: number, name: string): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`${name} must be a positive safe integer.`);
  }
  return value;
}

function mapResponse<Input, Output>(
  response: SDKResponse<Input>,
  mapper: (input: Input) => Output,
): SDKResponse<Output> {
  return {
    data: mapper(response.data),
    requestId: response.requestId,
    ...(response.etag === undefined ? {} : { etag: response.etag }),
    ...(response.location === undefined ? {} : { location: response.location }),
  };
}

/** Tenant backend webhook administration; signing secrets must never enter capture clients. */
export class WebhooksClient {
  readonly #transport: JSONTransport;
  readonly #token: string;
  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }
  async create(
    input: { readonly url: string; readonly eventTypes?: readonly string[] },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<WebhookEndpointMutation>> {
    const response = await this.#transport.request<WireWebhookEndpointMutation>({
      method: "POST",
      path: "v1/webhook-endpoints",
      bearerToken: this.#token,
      ...signal(options),
      body: {
        url: input.url,
        ...(input.eventTypes === undefined ? {} : { event_types: [...input.eventTypes] }),
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, webhookEndpointMutation);
  }
  async subscribe(
    endpointId: WebhookEndpointID,
    input: { readonly expectedVersion: number; readonly eventTypes: readonly string[] },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<WebhookEndpointMutation>> {
    const response = await this.#transport.request<WireWebhookEndpointMutation>({
      method: "POST",
      path: `v1/webhook-endpoints/${pathSegment(endpointId, "endpointId")}/subscriptions`,
      bearerToken: this.#token,
      ...signal(options),
      body: {
        expected_version: positiveInteger(input.expectedVersion, "expectedVersion"),
        event_types: [...input.eventTypes],
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, webhookEndpointMutation);
  }
  async get(
    endpointId: WebhookEndpointID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<WebhookEndpoint>> {
    const response = await this.#transport.request<WireWebhookEndpoint>({
      method: "GET",
      path: `v1/webhook-endpoints/${pathSegment(endpointId, "endpointId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, webhookEndpoint);
  }
  async rotate(
    endpointId: WebhookEndpointID,
    input: { readonly expectedVersion: number; readonly overlapSeconds: number },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<WebhookEndpointMutation>> {
    const response = await this.#transport.request<WireWebhookEndpointMutation>({
      method: "POST",
      path: `v1/webhook-endpoints/${pathSegment(endpointId, "endpointId")}/rotate`,
      bearerToken: this.#token,
      ...signal(options),
      body: {
        expected_version: positiveInteger(input.expectedVersion, "expectedVersion"),
        overlap_seconds: positiveInteger(input.overlapSeconds, "overlapSeconds"),
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, webhookEndpointMutation);
  }
  async disable(
    endpointId: WebhookEndpointID,
    input: { readonly expectedVersion: number; readonly reason: string },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<WebhookEndpointMutation>> {
    const response = await this.#transport.request<WireWebhookEndpointMutation>({
      method: "POST",
      path: `v1/webhook-endpoints/${pathSegment(endpointId, "endpointId")}/disable`,
      bearerToken: this.#token,
      ...signal(options),
      body: {
        expected_version: positiveInteger(input.expectedVersion, "expectedVersion"),
        reason: input.reason,
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, webhookEndpointMutation);
  }
  async getDelivery(
    deliveryId: WebhookDeliveryID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<WebhookDelivery>> {
    const response = await this.#transport.request<WireWebhookDelivery>({
      method: "GET",
      path: `v1/webhook-deliveries/${pathSegment(deliveryId, "deliveryId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, webhookDelivery);
  }
  async listAttempts(
    deliveryId: WebhookDeliveryID,
    options: RequestOptions = {},
  ): Promise<SDKResponse<WebhookAttemptList>> {
    const response = await this.#transport.request<WireWebhookAttemptList>({
      method: "GET",
      path: `v1/webhook-deliveries/${pathSegment(deliveryId, "deliveryId")}/attempts`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, webhookAttemptList);
  }
  async replay(
    deliveryId: WebhookDeliveryID,
    input: { readonly reason: string },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<WebhookDeliveryMutation>> {
    const response = await this.#transport.request<WireWebhookDeliveryMutation>({
      method: "POST",
      path: `v1/webhook-deliveries/${pathSegment(deliveryId, "deliveryId")}/replay`,
      bearerToken: this.#token,
      ...signal(options),
      body: { reason: input.reason },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, webhookDeliveryMutation);
  }
  async list(options: WebhookListOptions = {}): Promise<SDKResponse<WebhookEndpointList>> {
    const response = await this.#transport.request<WireWebhookEndpointList>({
      method: "GET",
      path: "v1/webhook-endpoints" + catalogQuery(options),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, webhookEndpointList);
  }
  async listDeliveries(
    endpointId: WebhookEndpointID,
    options: WebhookListOptions = {},
  ): Promise<SDKResponse<WebhookDeliveryList>> {
    const response = await this.#transport.request<WireWebhookDeliveryList>({
      method: "GET",
      path:
        `v1/webhook-endpoints/${pathSegment(endpointId, "endpointId")}/deliveries` +
        catalogQuery(options),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, webhookDeliveryList);
  }
}
function catalogQuery(options: WebhookListOptions): string {
  const query = new URLSearchParams();
  if (options.limit !== undefined) {
    if (positiveInteger(options.limit, "limit") > 100)
      throw new TypeError("limit must not exceed 100.");
    query.set("limit", String(options.limit));
  }
  if (options.cursor !== undefined) query.set("cursor", options.cursor);
  return query.size === 0 ? "" : `?${query.toString()}`;
}

/** Tenant policy administration. Source documents retain portable snake_case names. */
export class PoliciesClient {
  readonly #transport: JSONTransport;
  readonly #token: string;
  constructor(transport: JSONTransport, token: string) {
    this.#transport = transport;
    this.#token = token;
  }
  async create(
    definition: PolicyDefinition,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<PolicyMutation>> {
    const response = await this.#transport.request<WirePolicyMutation>({
      method: "POST",
      path: "v1/policies",
      bearerToken: this.#token,
      ...signal(options),
      body: { definition },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, policyMutation);
  }
  async validate(
    definition: PolicyDefinition,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PolicyValidation>> {
    const response = await this.#transport.request<WirePolicyValidation>({
      method: "POST",
      path: "v1/policies/validate",
      bearerToken: this.#token,
      ...signal(options),
      body: { definition },
    });
    return mapResponse(response, policyValidation);
  }
  async get(policyId: PolicyID, options: RequestOptions = {}): Promise<SDKResponse<PolicySummary>> {
    const response = await this.#transport.request<WirePolicySummary>({
      method: "GET",
      path: `v1/policies/${pathSegment(policyId, "policyId")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policySummary);
  }
  async createRevision(
    policyId: PolicyID,
    definition: PolicyDefinition,
    expectedRevision: number,
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<PolicyMutation>> {
    const response = await this.#transport.request<WirePolicyMutation>({
      method: "POST",
      path: `v1/policies/${pathSegment(policyId, "policyId")}/revisions`,
      bearerToken: this.#token,
      ...signal(options),
      body: {
        definition,
        expected_revision: positiveInteger(expectedRevision, "expectedRevision"),
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, policyMutation);
  }
  async getRevision(
    policyId: PolicyID,
    revision: number,
    options: RequestOptions = {},
  ): Promise<SDKResponse<PolicyRevisionDocument>> {
    const response = await this.#transport.request<WirePolicyRevisionDocument>({
      method: "GET",
      path: `v1/policies/${pathSegment(policyId, "policyId")}/revisions/${positiveInteger(revision, "revision")}`,
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policyRevisionDocument);
  }
  async list(options: PolicyListOptions = {}): Promise<SDKResponse<PolicyList>> {
    const response = await this.#transport.request<WirePolicyList>({
      method: "GET",
      path: "v1/policies" + catalogQuery(options),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policyList);
  }
  async listRevisions(
    policyId: PolicyID,
    options: PolicyListOptions = {},
  ): Promise<SDKResponse<PolicyRevisionList>> {
    const response = await this.#transport.request<WirePolicyRevisionList>({
      method: "GET",
      path: `v1/policies/${pathSegment(policyId, "policyId")}/revisions` + catalogQuery(options),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policyRevisionList);
  }
  async listActivations(
    policyId: PolicyID,
    options: PolicyListOptions = {},
  ): Promise<SDKResponse<PolicyActivationList>> {
    const response = await this.#transport.request<WirePolicyActivationList>({
      method: "GET",
      path: `v1/policies/${pathSegment(policyId, "policyId")}/activations` + catalogQuery(options),
      bearerToken: this.#token,
      ...signal(options),
    });
    return mapResponse(response, policyActivationList);
  }
  async activate(
    policyId: PolicyID,
    input: { readonly revision: number; readonly expectedVersion: number; readonly reason: string },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<PolicyMutation>> {
    const response = await this.#transport.request<WirePolicyMutation>({
      method: "POST",
      path: `v1/policies/${pathSegment(policyId, "policyId")}/activate`,
      bearerToken: this.#token,
      ...signal(options),
      body: {
        revision: positiveInteger(input.revision, "revision"),
        expected_version: activationVersion(input.expectedVersion),
        reason: input.reason,
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, policyMutation);
  }
  async rollback(
    policyId: PolicyID,
    input: { readonly revision: number; readonly expectedVersion: number; readonly reason: string },
    options: IdempotentRequestOptions,
  ): Promise<SDKResponse<PolicyMutation>> {
    const response = await this.#transport.request<WirePolicyMutation>({
      method: "POST",
      path: `v1/policies/${pathSegment(policyId, "policyId")}/rollback`,
      bearerToken: this.#token,
      ...signal(options),
      body: {
        revision: positiveInteger(input.revision, "revision"),
        expected_version: activationVersion(input.expectedVersion),
        reason: input.reason,
      },
      headers: idempotencyHeaders(options.idempotencyKey),
    });
    return mapResponse(response, policyMutation);
  }
}
function activationVersion(value: number): number {
  if (!Number.isSafeInteger(value) || value < 0)
    throw new TypeError("expectedVersion must be a nonnegative safe integer.");
  return value;
}
