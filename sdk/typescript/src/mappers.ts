import type {
  CaptureProfile,
  CaptureProfileList,
  CaptureProfileMutation,
  CaptureProfileRevision,
  CaptureProfileValidation,
  VerificationCreated,
  VerificationSession,
  NoticeVersion,
  ProcessingAuthority,
  SubjectResponse,
  CaptureAuthoritySnapshot,
  CaptureProgress,
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
  WireNoticeVersion,
  WireProcessingAuthority,
  WireSubjectResponse,
  WireCaptureAuthoritySnapshot,
  WireCaptureProgress,
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
    requirements: value.requirements,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
    expiresAt: value.expires_at,
  };
}

export function verificationCreated(value: WireVerificationCreated): VerificationCreated {
  return {
    session: verificationSession(value.session),
    captureToken: value.capture_token,
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
