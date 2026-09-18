import type { AssuranceSummary, DecisionContext } from "./assurance.js";
import type { WirePolicyDefinition, WirePolicyDocument } from "./wire.js";
export type TenantID = string;
export type CaptureProfileID = string;
export type VerificationID = string;
export type NoticeID = string;
export type ProcessingAuthorityID = string;
export type SubjectID = string;
export type SubjectResponseID = string;
export type RequestID = string;
export type Cursor = string;
export type EntityTag = string;
export type EvidenceUploadID = string;
export type EvidenceID = string;
export type DecisionID = string;
export type PolicyID = string;

export type PolicyDirective =
  | "complete_verified"
  | "complete_not_verified"
  | "complete_inconclusive"
  | "request_input"
  | "run_check"
  | "retry_check"
  | "use_fallback"
  | "route_manual_review"
  | "fail_workflow";
export type PolicyOutcome = "verified" | "not_verified" | "inconclusive";
export type PolicyActor = "machine" | "human";
export type PolicyRequirementState =
  "satisfied" | "not_satisfied" | "inconclusive" | "unavailable" | "prohibited";
export type PolicyFactSourceKind =
  "check" | "processing_authority" | "subject_response" | "review_finding" | "fraud" | "identity";

export interface PolicyDecisionReport {
  readonly typedAssurance?: AssuranceSummary;
  readonly schemaMajor: number;
  readonly schemaMinor: number;
  readonly decisionId: DecisionID;
  readonly tenantId: TenantID;
  readonly verificationId: VerificationID;
  readonly policyId: PolicyID;
  readonly policyRevision: number;
  readonly policyDigest: string;
  readonly evaluatorMajor: number;
  readonly evaluatorMinor: number;
  readonly evaluatorDigest: string;
  readonly snapshotDigest: string;
  readonly evaluationDigest: string;
  readonly decisionDigest: string;
  readonly bundleDigest: string;
  readonly directive: PolicyDirective;
  readonly outcome: PolicyOutcome;
  readonly assurance: string;
  readonly actor: PolicyActor;
  readonly supersedes?: DecisionID;
  readonly factCount: number;
  readonly requirementCount: number;
  readonly evaluatedAt: string;
  readonly decidedAt: string;
  readonly reproduced: true;
}

export interface PolicyCanonicalReference {
  readonly id: PolicyID;
  readonly revision: number;
  readonly schema_major: number;
  readonly schema_minor: number;
  readonly digest: string;
}

export interface PolicyCanonicalEvaluator {
  readonly major: number;
  readonly minor: number;
  readonly digest: string;
}

export interface PolicyCanonicalFactSource {
  readonly kind: PolicyFactSourceKind;
  readonly check_id?: string;
  readonly check_version?: number;
  readonly attempt_id?: string;
  readonly observation_ids?: readonly string[];
  readonly contract_digest?: string;
  readonly implementation_digest?: string;
  readonly authority_id?: ProcessingAuthorityID;
  readonly acknowledgement_id?: SubjectResponseID;
  readonly review_finding?: string;
  readonly fraud_receipt?: string;
  readonly identity_receipt?: string;
}

export interface PolicyCanonicalFact {
  readonly key: string;
  readonly state: PolicyRequirementState;
  readonly source: PolicyCanonicalFactSource;
  readonly observed_at: string;
  readonly expires_at?: string;
  readonly reason_codes: readonly string[];
}

export interface PolicyCanonicalSnapshot {
  readonly context?: DecisionContext;
  readonly schema_major: 1;
  readonly schema_minor: 0;
  readonly tenant_id: TenantID;
  readonly verification_id: VerificationID;
  readonly authority_id: ProcessingAuthorityID;
  readonly acknowledgement_id: SubjectResponseID;
  readonly region: string;
  readonly policy: PolicyCanonicalReference;
  readonly evaluator: PolicyCanonicalEvaluator;
  readonly evaluated_at: string;
  readonly facts: readonly PolicyCanonicalFact[];
}

export interface PolicyCanonicalRequirementResult {
  readonly name: string;
  readonly state: PolicyRequirementState;
  readonly contributing_facts: readonly string[];
  readonly candidate: PolicyDirective;
  readonly priority: number;
  readonly reason_codes: readonly string[];
}

export interface PolicyCanonicalEvaluation {
  readonly schema_major: 1;
  readonly schema_minor: 0;
  readonly snapshot_digest: string;
  readonly results: readonly PolicyCanonicalRequirementResult[];
  readonly considered: readonly PolicyDirective[];
  readonly selected: PolicyDirective;
  readonly outcome?: PolicyOutcome;
  readonly assurance?: string;
  readonly reason_codes: readonly string[];
}

export interface PolicyCanonicalDecision {
  readonly schema_major: 1;
  readonly schema_minor: 0;
  readonly id: DecisionID;
  readonly tenant_id: TenantID;
  readonly verification_id: VerificationID;
  readonly snapshot_digest: string;
  readonly evaluation_digest: string;
  readonly actor: PolicyActor;
  readonly supersedes?: DecisionID;
  readonly decided_at: string;
}

/** The portable document preserves its published canonical snake-case field names. */
export interface PolicyDecisionBundleDocument {
  readonly schema_major: 1;
  readonly schema_minor: 0;
  readonly bundle_digest: string;
  readonly decision_id: DecisionID;
  readonly tenant_id: TenantID;
  readonly verification_id: VerificationID;
  readonly snapshot_digest: string;
  readonly evaluation_digest: string;
  readonly decision_digest: string;
  readonly snapshot: PolicyCanonicalSnapshot;
  readonly evaluation: PolicyCanonicalEvaluation;
  readonly decision: PolicyCanonicalDecision;
}

/** Exact canonical text plus its parsed, closed-schema portable document. */
export interface PolicyDecisionBundle {
  readonly canonical: string;
  readonly document: PolicyDecisionBundleDocument;
}

export interface CaptureConnection {
  /** Display-once URL containing a single-use ticket. Never log or persist this value casually. */
  readonly websocketUrl: string;
  readonly protocol: "idenqa.capture.v1";
  readonly expiresAt: string;
}

export type CaptureProfileState = "draft" | "active" | "deactivated";
export type CaptureProfileRevisionState = "draft" | "published" | "superseded" | "withdrawn";
/** Workflow state; the identity outcome is available through the decision resource. */
export type VerificationState =
  | "created"
  | "collecting"
  | "awaiting_input"
  | "processing"
  | "awaiting_external"
  | "manual_review"
  | "completed"
  | "cancelled"
  | "expired"
  | "failed";
export type AcquisitionStrategy = "any_of" | "all_of";
export type CaptureFallbackReason =
  "capability_unavailable" | "method_unavailable" | "capture_failed";
export type CaptureConstraintValue = string | number | boolean | readonly string[];

export interface RegistryReference {
  readonly schema_version: 1;
  readonly revision: number;
  readonly digest: string;
}

export interface CaptureAcquisition {
  readonly strategy: AcquisitionStrategy;
  readonly methods: readonly string[];
}

export interface CaptureConstraint {
  readonly name: string;
  readonly value: CaptureConstraintValue;
}

export interface CaptureFallback {
  readonly on: readonly CaptureFallbackReason[];
  readonly acquisition: CaptureAcquisition;
}

export interface CaptureRequirement {
  readonly key: string;
  readonly purpose: string;
  readonly evidence_type: string;
  readonly artefacts: readonly string[];
  readonly acquisition: CaptureAcquisition;
  readonly required_assurances: readonly string[];
  readonly constraints: readonly CaptureConstraint[];
  readonly fallbacks: readonly CaptureFallback[];
}

/**
 * Portable, registry-bound capture requirements. Field names intentionally
 * match the published JSON contract so the same document can be stored,
 * signed, and exchanged across SDK languages without lossy conversion.
 */
export interface CaptureProfileDocument {
  readonly schema_version: 1;
  readonly registry: RegistryReference;
  readonly requirements: readonly CaptureRequirement[];
}

export interface CaptureProfileWrite {
  readonly name: string;
  readonly document: CaptureProfileDocument;
}

export interface CaptureProfile {
  readonly id: CaptureProfileID;
  readonly name: string;
  readonly state: CaptureProfileState;
  readonly version: number;
  readonly latestRevision: number;
  readonly draftRevision?: number;
  readonly publishedRevision?: number;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly deactivatedAt?: string;
}

export interface CaptureProfileMutation {
  readonly profileId: CaptureProfileID;
  readonly name: string;
  readonly state: CaptureProfileState;
  readonly version: number;
  readonly latestRevision: number;
  readonly draftRevision?: number;
  readonly publishedRevision?: number;
  readonly revision: number;
  readonly digest: string;
  readonly updatedAt: string;
}

export interface CaptureProfileRevision {
  readonly profileId: CaptureProfileID;
  readonly revision: number;
  readonly state: CaptureProfileRevisionState;
  readonly document: CaptureProfileDocument;
  readonly digest: string;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly publishedAt?: string;
  readonly endedAt?: string;
}

export interface CaptureProfileValidation {
  readonly valid: true;
  readonly digest: string;
}

export interface Page {
  readonly hasMore: boolean;
  readonly nextCursor?: Cursor;
}

export interface CaptureProfileList {
  readonly data: readonly CaptureProfile[];
  readonly page: Page;
}

export interface VerificationCreate {
  readonly captureProfileId: CaptureProfileID;
  readonly policyId: PolicyID;
  readonly verificationTtlSeconds?: number;
  readonly captureTokenTtlSeconds?: number;
  readonly outcomeTokenPostExpiryTtlSeconds?: number;
}

export interface VerificationSession {
  readonly id: VerificationID;
  readonly state: VerificationState;
  readonly version: number;
  readonly profileId: CaptureProfileID;
  readonly profileRevision: number;
  readonly profileDigest: string;
  readonly policyId: PolicyID;
  readonly requirements: CaptureProfileDocument;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly expiresAt: string;
}

export interface VerificationCreated {
  readonly session: VerificationSession;
  /** Display-once bearer credential. Never log or persist this value casually. */
  readonly captureToken: string;
  /** Display-once, read-only bearer credential. Keep it separate from capture authority. */
  readonly outcomeToken: string;
  readonly outcomeTokenExpiresAt: string;
}

export interface NoticeCopy {
  readonly title: string;
  readonly summary: string;
  readonly purpose: string;
  readonly consequences: string;
}

export interface NoticeVersionCreate {
  readonly key: string;
  readonly locale: string;
  readonly controller: string;
  readonly recipient: string;
  readonly copy: NoticeCopy;
  readonly effectiveAt: string;
}

export interface NoticeVersion extends NoticeVersionCreate {
  readonly id: NoticeID;
  readonly createdAt: string;
  readonly digest: string;
}

export type ProcessingAuthorityState = "active" | "restricted" | "withdrawn" | "superseded";

export interface ProcessingAuthorityDeclare {
  readonly noticeId: NoticeID;
  readonly category: string;
  readonly purpose: string;
  readonly jurisdiction: string;
  readonly policyPack: string;
  readonly consentRequired: boolean;
  readonly recipientReference: string;
  readonly recipientDisplayName: string;
  readonly regions: readonly string[];
  readonly retentionReference: string;
  readonly validFrom: string;
  readonly expiresAt: string;
}

export interface ProcessingAuthority {
  readonly id: ProcessingAuthorityID;
  readonly subjectId: SubjectID;
  readonly verificationId: VerificationID;
  readonly noticeId: NoticeID;
  readonly category: string;
  readonly purpose: string;
  readonly jurisdiction: string;
  readonly policyPack: string;
  readonly consentRequired: boolean;
  readonly requirementPurposes: readonly string[];
  readonly evidenceTypes: readonly string[];
  readonly recipientReference: string;
  readonly recipientDisplayName: string;
  readonly regions: readonly string[];
  readonly retentionReference: string;
  readonly state: ProcessingAuthorityState;
  readonly version: number;
  readonly validFrom: string;
  readonly expiresAt: string;
  readonly createdAt: string;
  readonly updatedAt: string;
}

export type SubjectResponseAction = "acknowledge" | "consent" | "refuse";

export interface SubjectResponseCreate {
  readonly action: SubjectResponseAction;
  readonly locale: string;
  readonly renderedExperienceVersion?: string;
}

export interface SubjectResponse extends SubjectResponseCreate {
  readonly id: SubjectResponseID;
  readonly authorityId: ProcessingAuthorityID;
  readonly noticeId: NoticeID;
  readonly subjectId: SubjectID;
  readonly verificationId: VerificationID;
  readonly recordedAt: string;
}

export interface CaptureAuthoritySnapshot {
  readonly authority: ProcessingAuthority;
  readonly notice: NoticeVersion;
  readonly latestResponse?: SubjectResponse;
}

export type EvidenceMediaType = "image/jpeg" | "image/png";
export type EvidenceUploadState = "issued" | "uploading" | "accepted" | "rejected" | "expired";

export interface EvidenceUploadCreate {
  readonly requirementKey: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
  readonly fallbackCondition?: CaptureFallbackReason;
  readonly expectedBytes: number;
  readonly expectedDigest: string;
  readonly mediaType: EvidenceMediaType;
  readonly region: string;
}

export interface EvidenceUpload {
  readonly id: EvidenceUploadID;
  readonly evidenceId: EvidenceID;
  readonly state: EvidenceUploadState;
  readonly version: number;
  readonly attempt: number;
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
  readonly fallbackCondition?: CaptureFallbackReason;
  readonly assurances: readonly string[];
  readonly allowedMediaTypes: readonly EvidenceMediaType[];
  readonly maximumBytes: number;
  readonly expectedBytes: number;
  readonly mediaType: EvidenceMediaType;
  readonly region: string;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly expiresAt: string;
  readonly acceptedAt?: string;
}

export interface CaptureCompletion {
  readonly uploadId: EvidenceUploadID;
  readonly evidenceId: EvidenceID;
  readonly requirementKey: string;
  readonly evidenceType: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
  readonly fallbackCondition?: CaptureFallbackReason;
}

export interface CaptureProgress {
  readonly verificationId: VerificationID;
  readonly completions: readonly CaptureCompletion[];
}

export type CaptureOutcomeState =
  | "capture_required"
  | "processing"
  | "action_required"
  | "verified"
  | "not_verified"
  | "inconclusive"
  | "cancelled"
  | "expired"
  | "failed";

/** Subject-safe authoritative state; detailed policy and diagnostic data is intentionally absent. */
export interface CaptureOutcome {
  readonly verificationId: VerificationID;
  readonly state: CaptureOutcomeState;
  readonly sessionVersion: number;
  readonly updatedAt: string;
}

export interface EvidenceUploadOptions extends ConditionalRequestOptions {
  /** Canonical `sha256:<lowercase hex>` digest supplied when the intent was created. */
  readonly digest: string;
}

export interface SDKResponse<T> {
  readonly data: T;
  readonly requestId: RequestID;
  readonly etag?: EntityTag;
  readonly location?: string;
}

export type SDKConditionalResponse<T> =
  | (SDKResponse<T> & { readonly notModified: false })
  | {
      readonly notModified: true;
      readonly requestId: RequestID;
      readonly etag: EntityTag;
    };

export interface RequestOptions {
  readonly signal?: AbortSignal;
}

export interface IdempotentRequestOptions extends RequestOptions {
  /** Plain key value; the SDK encodes it as an RFC 9651 String field value. */
  readonly idempotencyKey: string;
}

export interface ConditionalRequestOptions extends RequestOptions {
  readonly etag: EntityTag;
}

export interface ConditionalIdempotentRequestOptions extends IdempotentRequestOptions {
  readonly etag: EntityTag;
}

export interface CaptureProfileListOptions extends RequestOptions {
  readonly cursor?: Cursor;
  readonly limit?: number;
}

export interface ClientOptions {
  readonly baseUrl: string | URL;
  readonly fetch?: typeof globalThis.fetch;
}

export interface TenantClientOptions extends ClientOptions {
  readonly apiKey: string;
}

export interface CaptureClientOptions extends ClientOptions {
  readonly captureToken: string;
}

export interface OutcomeClientOptions extends ClientOptions {
  readonly outcomeToken: string;
}

/** Original committed cancellation receipt; cancellation is not an identity outcome. */
export interface VerificationCancellation {
  readonly eventId: string;
  readonly verificationId: VerificationID;
  readonly state: "cancelled";
  readonly version: number;
  readonly occurredAt: string;
}

export type WebhookEndpointID = string;
export type WebhookDeliveryID = string;
export interface WebhookEndpoint {
  readonly id: WebhookEndpointID;
  readonly url: string;
  /** Exact catalogue event names, or the single `*` wildcard entry. */
  readonly eventTypes: readonly string[];
  readonly version: number;
  readonly secretVersion: number;
  readonly previousValidUntil?: string;
  readonly disabledAt?: string;
  readonly disabledReason?: string;
  readonly createdAt: string;
  readonly updatedAt: string;
}
export interface WebhookDelivery {
  readonly id: WebhookDeliveryID;
  readonly endpointId: WebhookEndpointID;
  readonly eventId: string;
  readonly eventType: string;
  readonly state: "pending" | "delivered" | "exhausted" | "cancelled";
  readonly attemptCount: number;
  readonly maxAttempts: number;
  readonly nextAttemptAt: string;
  readonly deliveredAt?: string;
  readonly replayOf?: WebhookDeliveryID;
  readonly createdAt: string;
  readonly updatedAt: string;
}
export interface WebhookAttempt {
  readonly number: number;
  readonly secretVersion: number;
  readonly statusCode: number;
  readonly errorClass: string;
  readonly retryAfterMs: number;
  /** UTF-8 sanitised receiver response excerpt, at most 4096 bytes. Untrusted receiver content. */
  readonly responseBody: string | null;
  readonly responseTruncated: boolean;
  readonly completedAt: string;
}
export interface WebhookEndpointMutation {
  readonly endpoint: WebhookEndpoint;
  readonly replayed: boolean;
  /** Display-once unpadded Base64URL secret. Absent on idempotent replay. Never log it. */
  readonly signingSecret?: string;
}
export interface WebhookDeliveryMutation {
  readonly delivery: WebhookDelivery;
  readonly replayed: boolean;
}
export interface WebhookListOptions extends RequestOptions {
  readonly limit?: number;
  readonly cursor?: Cursor;
}
export interface WebhookEndpointList {
  readonly data: readonly WebhookEndpoint[];
  readonly page: Page;
}
export interface WebhookDeliveryList {
  readonly data: readonly WebhookDelivery[];
  readonly page: Page;
}
export interface WebhookAttemptList {
  readonly data: readonly WebhookAttempt[];
}

export type PolicyDefinition = WirePolicyDefinition;
export type PolicyDocument = WirePolicyDocument;
export interface PolicySummary {
  readonly id: string;
  readonly latestRevision: number;
  readonly activeRevision?: number;
  readonly activationVersion: number;
  readonly createdAt: string;
  readonly activatedAt?: string;
}
export interface PolicyRevisionInfo {
  readonly policyId: string;
  readonly revision: number;
  readonly schemaMajor: number;
  readonly schemaMinor: number;
  readonly digest: string;
  readonly evaluatorMajor: number;
  readonly evaluatorMinor: number;
  readonly evaluatorDigest: string;
  readonly createdAt: string;
}
export interface PolicyActivationInfo {
  readonly policyId: string;
  readonly revision: number;
  readonly previousRevision: number;
  readonly version: number;
  readonly actorId: string;
  readonly activatedAt: string;
}
export interface PolicyValidation {
  readonly valid: boolean;
  readonly ruleCount: number;
  readonly evaluatorMajor: number;
  readonly evaluatorMinor: number;
  readonly evaluatorDigest: string;
}
export interface PolicyRevisionDocument extends PolicyRevisionInfo {
  readonly document: PolicyDocument;
}
export interface PolicyMutation {
  readonly policy: PolicySummary;
  readonly revision?: PolicyRevisionInfo;
  readonly activation?: PolicyActivationInfo;
  readonly replayed: boolean;
}
export interface PolicyListOptions extends RequestOptions {
  readonly limit?: number;
  readonly cursor?: Cursor;
}
export interface PolicyList {
  readonly data: readonly PolicySummary[];
  readonly page: Page;
}
export interface PolicyRevisionList {
  readonly data: readonly PolicyRevisionInfo[];
  readonly page: Page;
}
export interface PolicyActivationList {
  readonly data: readonly PolicyActivationInfo[];
  readonly page: Page;
}
