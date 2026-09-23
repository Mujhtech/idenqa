import type {
  WirePolicySummary,
  WirePolicyRevisionInfo,
  WirePolicyRevisionDocument,
  WirePolicyActivationInfo,
  WirePolicyValidation,
  WirePolicyMutation,
  WirePolicyList,
  WirePolicyRevisionList,
  WirePolicyActivationList,
} from "./wire.js";
import type {
  PolicySummary,
  PolicyRevisionInfo,
  PolicyRevisionDocument,
  PolicyActivationInfo,
  PolicyValidation,
  PolicyMutation,
  PolicyList,
  PolicyRevisionList,
  PolicyActivationList,
} from "./types.js";
import type {
  WireWebhookEndpoint,
  WireWebhookDelivery,
  WireWebhookAttempt,
  WireWebhookEndpointMutation,
  WireWebhookDeliveryMutation,
  WireWebhookEndpointList,
  WireWebhookDeliveryList,
  WireWebhookAttemptList,
} from "./wire.js";
import type {
  WebhookEndpoint,
  WebhookDelivery,
  WebhookAttempt,
  WebhookEndpointMutation,
  WebhookDeliveryMutation,
  WebhookEndpointList,
  WebhookDeliveryList,
  WebhookAttemptList,
} from "./types.js";
import type {
  CaptureProfile,
  CaptureProfileList,
  CaptureProfileMutation,
  CaptureProfileRevision,
  CaptureProfileValidation,
  VerificationCreated,
  VerificationSession,
  VerificationCancellation,
  VerificationResumed,
  NoticeVersion,
  ProcessingAuthority,
  SubjectResponse,
  CaptureAuthoritySnapshot,
  CaptureProgress,
  CaptureOutcome,
  CaptureConnection,
  EvidenceUpload,
  PolicyDecisionBundle,
  PolicyDecisionReport,
} from "./types.js";
import type {
  WireCaptureProfile,
  WireCaptureProfileList,
  WireCaptureProfileMutation,
  WireCaptureProfileRevision,
  WireCaptureProfileValidation,
  WireVerificationCreated,
  WireVerificationSession,
  WireVerificationCancellation,
  WireVerificationResumed,
  WireNoticeVersion,
  WireProcessingAuthority,
  WireSubjectResponse,
  WireCaptureAuthoritySnapshot,
  WireCaptureProgress,
  WireCaptureOutcome,
  WireCaptureConnection,
  WireEvidenceUpload,
  WirePolicyDecisionBundle,
  WirePolicyDecisionReport,
} from "./wire.js";

export function captureProfile(value: WireCaptureProfile): CaptureProfile {
  return {
    id: value.id,
    name: value.name,
    state: value.state,
    version: value.version,
    latestRevision: value.latest_revision,
    ...(value.draft_revision === undefined ? {} : { draftRevision: value.draft_revision }),
    ...(value.published_revision === undefined
      ? {}
      : { publishedRevision: value.published_revision }),
    createdAt: value.created_at,
    updatedAt: value.updated_at,
    ...(value.deactivated_at === undefined ? {} : { deactivatedAt: value.deactivated_at }),
  };
}

export function captureProfileMutation(value: WireCaptureProfileMutation): CaptureProfileMutation {
  return {
    profileId: value.profile_id,
    name: value.name,
    state: value.state,
    version: value.version,
    latestRevision: value.latest_revision,
    ...(value.draft_revision === undefined ? {} : { draftRevision: value.draft_revision }),
    ...(value.published_revision === undefined
      ? {}
      : { publishedRevision: value.published_revision }),
    revision: value.revision,
    digest: value.digest,
    updatedAt: value.updated_at,
  };
}

export function captureProfileRevision(value: WireCaptureProfileRevision): CaptureProfileRevision {
  return {
    profileId: value.profile_id,
    revision: value.revision,
    state: value.state,
    document: value.document,
    digest: value.digest,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
    ...(value.published_at === undefined ? {} : { publishedAt: value.published_at }),
    ...(value.ended_at === undefined ? {} : { endedAt: value.ended_at }),
  };
}

export function captureProfileList(value: WireCaptureProfileList): CaptureProfileList {
  return {
    data: value.data.map(captureProfile),
    page: {
      hasMore: value.page.has_more,
      ...(value.page.next_cursor === undefined ? {} : { nextCursor: value.page.next_cursor }),
    },
  };
}

export function captureProfileValidation(
  value: WireCaptureProfileValidation,
): CaptureProfileValidation {
  return { valid: value.valid, digest: value.digest };
}

export function verificationSession(value: WireVerificationSession): VerificationSession {
  return {
    id: value.id,
    state: value.state,
    version: value.version,
    profileId: value.profile_id,
    profileRevision: value.profile_revision,
    profileDigest: value.profile_digest,
    policyId: value.policy_id,
    region: value.region,
    requirements: value.requirements,
    ...(value.document_selections === undefined
      ? {}
      : { documentSelections: value.document_selections }),
    createdAt: value.created_at,
    updatedAt: value.updated_at,
    expiresAt: value.expires_at,
  };
}

export function verificationCreated(value: WireVerificationCreated): VerificationCreated {
  return {
    session: verificationSession(value.session),
    captureToken: value.capture_token,
    outcomeToken: value.outcome_token,
    outcomeTokenExpiresAt: value.outcome_token_expires_at,
  };
}

export function verificationResumed(value: WireVerificationResumed): VerificationResumed {
  return {
    session: verificationSession(value.session),
    captureTokenId: value.capture_token_id,
    captureTokenExpiresAt: value.capture_token_expires_at,
    ...(value.capture_token === undefined ? {} : { captureToken: value.capture_token }),
    replaced: value.replaced,
    replayed: value.replayed,
  };
}

export function noticeVersion(value: WireNoticeVersion): NoticeVersion {
  return {
    id: value.id,
    key: value.key,
    locale: value.locale,
    controller: value.controller,
    recipient: value.recipient,
    copy: value.copy,
    effectiveAt: value.effective_at,
    createdAt: value.created_at,
    digest: value.digest,
  };
}

export function processingAuthority(value: WireProcessingAuthority): ProcessingAuthority {
  return {
    id: value.id,
    subjectId: value.subject_id,
    verificationId: value.verification_id,
    noticeId: value.notice_id,
    category: value.category,
    purpose: value.purpose,
    jurisdiction: value.jurisdiction,
    policyPack: value.policy_pack,
    consentRequired: value.consent_required,
    requirementPurposes: value.requirement_purposes,
    evidenceTypes: value.evidence_types,
    recipientReference: value.recipient_reference,
    recipientDisplayName: value.recipient_display_name,
    regions: value.regions,
    retentionReference: value.retention_reference,
    state: value.state,
    version: value.version,
    validFrom: value.valid_from,
    expiresAt: value.expires_at,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  };
}

export function subjectResponse(value: WireSubjectResponse): SubjectResponse {
  return {
    id: value.id,
    authorityId: value.authority_id,
    noticeId: value.notice_id,
    subjectId: value.subject_id,
    verificationId: value.verification_id,
    action: value.action,
    locale: value.locale,
    ...(value.rendered_experience_version === undefined
      ? {}
      : {
          renderedExperienceVersion: value.rendered_experience_version,
        }),
    recordedAt: value.recorded_at,
  };
}

export function captureAuthoritySnapshot(
  value: WireCaptureAuthoritySnapshot,
): CaptureAuthoritySnapshot {
  return {
    authority: processingAuthority(value.authority),
    notice: noticeVersion(value.notice),
    ...(value.latest_response === undefined
      ? {}
      : {
          latestResponse: subjectResponse(value.latest_response),
        }),
  };
}

export function evidenceUpload(value: WireEvidenceUpload): EvidenceUpload {
  return {
    id: value.id,
    evidenceId: value.evidence_id,
    state: value.state,
    version: value.version,
    attempt: value.attempt,
    requirementKey: value.requirement_key,
    evidenceType: value.evidence_type,
    artefact: value.artefact,
    acquisitionMethod: value.acquisition_method,
    ...(value.fallback_condition === undefined
      ? {}
      : { fallbackCondition: value.fallback_condition }),
    ...(value.sequence === undefined
      ? {}
      : {
          sequence: {
            sequenceDigest: value.sequence.sequence_digest,
            index: value.sequence.index,
            count: value.sequence.count,
            challengeId: value.sequence.challenge_id,
            capturedAt: value.sequence.captured_at,
            ...(value.sequence.previous_digest === undefined
              ? {}
              : { previousDigest: value.sequence.previous_digest }),
          },
        }),
    assurances: value.assurances,
    allowedMediaTypes: value.allowed_media_types,
    maximumBytes: value.maximum_bytes,
    expectedBytes: value.expected_bytes,
    mediaType: value.media_type,
    region: value.region,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
    expiresAt: value.expires_at,
    ...(value.accepted_at === undefined ? {} : { acceptedAt: value.accepted_at }),
  };
}

export function captureProgress(value: WireCaptureProgress): CaptureProgress {
  return {
    verificationId: value.verification_id,
    completions: value.completions.map((completion) => ({
      uploadId: completion.upload_id,
      evidenceId: completion.evidence_id,
      requirementKey: completion.requirement_key,
      evidenceType: completion.evidence_type,
      artefact: completion.artefact,
      acquisitionMethod: completion.acquisition_method,
      ...(completion.fallback_condition === undefined
        ? {}
        : { fallbackCondition: completion.fallback_condition }),
    })),
  };
}

export function captureOutcome(value: WireCaptureOutcome): CaptureOutcome {
  return {
    verificationId: value.verification_id,
    state: value.state,
    sessionVersion: value.session_version,
    updatedAt: value.updated_at,
  };
}

export function captureConnection(value: WireCaptureConnection): CaptureConnection {
  return {
    websocketUrl: value.websocket_url,
    protocol: value.protocol,
    expiresAt: value.expires_at,
  };
}

export function policyDecisionReport(value: WirePolicyDecisionReport): PolicyDecisionReport {
  return {
    schemaMajor: value.schema_major,
    schemaMinor: value.schema_minor,
    decisionId: value.decision_id,
    tenantId: value.tenant_id,
    verificationId: value.verification_id,
    policyId: value.policy_id,
    policyRevision: value.policy_revision,
    policyDigest: value.policy_digest,
    evaluatorMajor: value.evaluator_major,
    evaluatorMinor: value.evaluator_minor,
    evaluatorDigest: value.evaluator_digest,
    snapshotDigest: value.snapshot_digest,
    evaluationDigest: value.evaluation_digest,
    decisionDigest: value.decision_digest,
    bundleDigest: value.bundle_digest,
    directive: value.directive,
    outcome: value.outcome,
    assurance: value.assurance,
    ...(value.typed_assurance === undefined ? {} : { typedAssurance: value.typed_assurance }),
    actor: value.actor,
    ...(value.supersedes === undefined ? {} : { supersedes: value.supersedes }),
    factCount: value.fact_count,
    requirementCount: value.requirement_count,
    evaluatedAt: value.evaluated_at,
    decidedAt: value.decided_at,
    reproduced: value.reproduced,
  };
}

export function policyDecisionBundle(
  value: WirePolicyDecisionBundle,
  canonical: string,
): PolicyDecisionBundle {
  return { canonical, document: value };
}

export function verificationCancellation(
  value: WireVerificationCancellation,
): VerificationCancellation {
  return {
    eventId: value.event_id,
    verificationId: value.verification_id,
    state: value.state,
    version: value.version,
    occurredAt: value.occurred_at,
  };
}

export function webhookEndpoint(value: WireWebhookEndpoint): WebhookEndpoint {
  return {
    id: value.id,
    url: value.url,
    eventTypes: value.event_types,
    version: value.version,
    secretVersion: value.secret_version,
    ...(value.previous_valid_until === undefined
      ? {}
      : { previousValidUntil: value.previous_valid_until }),
    ...(value.disabled_at === undefined ? {} : { disabledAt: value.disabled_at }),
    ...(value.disabled_reason === undefined ? {} : { disabledReason: value.disabled_reason }),
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  };
}
export function webhookDelivery(value: WireWebhookDelivery): WebhookDelivery {
  return {
    id: value.id,
    endpointId: value.endpoint_id,
    eventId: value.event_id,
    eventType: value.event_type,
    state: value.state,
    attemptCount: value.attempt_count,
    maxAttempts: value.max_attempts,
    nextAttemptAt: value.next_attempt_at,
    ...(value.delivered_at === undefined ? {} : { deliveredAt: value.delivered_at }),
    ...(value.replay_of === undefined ? {} : { replayOf: value.replay_of }),
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  };
}
export function webhookAttempt(value: WireWebhookAttempt): WebhookAttempt {
  return {
    number: value.number,
    secretVersion: value.secret_version,
    statusCode: value.status_code,
    errorClass: value.error_class,
    retryAfterMs: value.retry_after_ms,
    responseBody: value.response_body ?? null,
    responseTruncated: value.response_truncated,
    completedAt: value.completed_at,
  };
}
export function webhookEndpointMutation(
  value: WireWebhookEndpointMutation,
): WebhookEndpointMutation {
  return {
    endpoint: webhookEndpoint(value.endpoint),
    replayed: value.replayed,
    ...(value.signing_secret === undefined ? {} : { signingSecret: value.signing_secret }),
  };
}
export function webhookDeliveryMutation(
  value: WireWebhookDeliveryMutation,
): WebhookDeliveryMutation {
  return { delivery: webhookDelivery(value.delivery), replayed: value.replayed };
}
export function webhookEndpointList(value: WireWebhookEndpointList): WebhookEndpointList {
  return { data: value.data.map(webhookEndpoint), page: webhookPage(value.page) };
}
export function webhookDeliveryList(value: WireWebhookDeliveryList): WebhookDeliveryList {
  return { data: value.data.map(webhookDelivery), page: webhookPage(value.page) };
}
export function webhookAttemptList(value: WireWebhookAttemptList): WebhookAttemptList {
  return { data: value.data.map(webhookAttempt) };
}
function webhookPage(value: WireWebhookEndpointList["page"]): WebhookEndpointList["page"] {
  return {
    hasMore: value.has_more,
    ...(value.next_cursor === undefined ? {} : { nextCursor: value.next_cursor }),
  };
}

export function policySummary(value: WirePolicySummary): PolicySummary {
  return {
    id: value.id,
    latestRevision: value.latest_revision,
    ...(value.active_revision === undefined ? {} : { activeRevision: value.active_revision }),
    activationVersion: value.activation_version,
    createdAt: value.created_at,
    ...(value.activated_at === undefined ? {} : { activatedAt: value.activated_at }),
  };
}

export function policyRevisionInfo(value: WirePolicyRevisionInfo): PolicyRevisionInfo {
  return {
    policyId: value.policy_id,
    revision: value.revision,
    schemaMajor: value.schema_major,
    schemaMinor: value.schema_minor,
    digest: value.digest,
    evaluatorMajor: value.evaluator_major,
    evaluatorMinor: value.evaluator_minor,
    evaluatorDigest: value.evaluator_digest,
    createdAt: value.created_at,
  };
}

export function policyActivationInfo(value: WirePolicyActivationInfo): PolicyActivationInfo {
  return {
    policyId: value.policy_id,
    revision: value.revision,
    previousRevision: value.previous_revision,
    version: value.version,
    actorId: value.actor_id,
    activatedAt: value.activated_at,
  };
}

export function policyValidation(value: WirePolicyValidation): PolicyValidation {
  return {
    valid: value.valid,
    ruleCount: value.rule_count,
    evaluatorMajor: value.evaluator_major,
    evaluatorMinor: value.evaluator_minor,
    evaluatorDigest: value.evaluator_digest,
  };
}
export function policyRevisionDocument(value: WirePolicyRevisionDocument): PolicyRevisionDocument {
  return { ...policyRevisionInfo(value), document: value.document };
}
export function policyMutation(value: WirePolicyMutation): PolicyMutation {
  return {
    policy: policySummary(value.policy),
    replayed: value.replayed,
    ...(value.revision === undefined ? {} : { revision: policyRevisionInfo(value.revision) }),
    ...(value.activation === undefined
      ? {}
      : { activation: policyActivationInfo(value.activation) }),
  };
}
export function policyList(value: WirePolicyList): PolicyList {
  return {
    data: value.data.map(policySummary),
    page: {
      hasMore: value.page.has_more,
      ...(value.page.next_cursor === undefined ? {} : { nextCursor: value.page.next_cursor }),
    },
  };
}
export function policyRevisionList(value: WirePolicyRevisionList): PolicyRevisionList {
  return {
    data: value.data.map(policyRevisionInfo),
    page: {
      hasMore: value.page.has_more,
      ...(value.page.next_cursor === undefined ? {} : { nextCursor: value.page.next_cursor }),
    },
  };
}
export function policyActivationList(value: WirePolicyActivationList): PolicyActivationList {
  return {
    data: value.data.map(policyActivationInfo),
    page: {
      hasMore: value.page.has_more,
      ...(value.page.next_cursor === undefined ? {} : { nextCursor: value.page.next_cursor }),
    },
  };
}
