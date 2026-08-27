-- name: CreateNoticeVersion :one
INSERT INTO idenqa.notice_versions (
    id, tenant_id, semantic_key, locale, controller_display_name,
    recipient_display_name, title, summary, purpose_copy,
    consequence_copy, effective_at, created_at, created_by, digest
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING *;

-- name: InsertNoticeVersionAudit :exec
INSERT INTO idenqa.notice_version_audit (
    tenant_id, notice_id, action, actor_key_id, occurred_at
) VALUES ($1, $2, $3, $4, $5);

-- name: FindNoticeVersion :one
SELECT *
FROM idenqa.notice_versions
WHERE tenant_id = $1 AND id = $2;

-- name: LockVerificationForAuthority :one
SELECT id, tenant_id, requirements, expires_at, authority_id
FROM idenqa.verification_sessions
WHERE tenant_id = $1 AND id = $2
FOR UPDATE;

-- name: CreateVerificationSubject :exec
INSERT INTO idenqa.subjects (id, tenant_id, verification_id, created_at)
VALUES ($1, $2, $3, $4);

-- name: CreateProcessingAuthority :one
INSERT INTO idenqa.processing_authorities (
    id, tenant_id, subject_id, verification_id, notice_id, category,
    purpose, jurisdiction, policy_pack, consent_required,
    requirement_purposes, evidence_types, recipient_reference,
    recipient_display_name, regions, retention_reference, state, version,
    valid_from, expires_at, created_at, updated_at, created_by,
    restricted_at, withdrawn_at, superseded_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18,
    $19, $20, $21, $22, $23, $24, $25, $26
)
RETURNING *;

-- name: BindVerificationAuthority :execrows
UPDATE idenqa.verification_sessions
SET subject_id = $3, authority_id = $4, notice_id = $5
WHERE tenant_id = $1 AND id = $2
  AND subject_id IS NULL AND authority_id IS NULL AND notice_id IS NULL;

-- name: FindProcessingAuthority :one
SELECT *
FROM idenqa.processing_authorities
WHERE tenant_id = $1 AND id = $2;

-- name: FindProcessingAuthorityByVerification :one
SELECT *
FROM idenqa.processing_authorities
WHERE tenant_id = $1 AND verification_id = $2;

-- name: TransitionProcessingAuthority :one
UPDATE idenqa.processing_authorities
SET state = $4,
    version = $5,
    updated_at = $6,
    restricted_at = $7,
    withdrawn_at = $8,
    superseded_at = $9
WHERE tenant_id = $1 AND id = $2 AND version = $3 AND state = 'active'
RETURNING *;

-- name: CreateSubjectResponse :one
INSERT INTO idenqa.subject_responses (
    id, tenant_id, authority_id, notice_id, subject_id, verification_id,
    capture_token_id, action, locale, rendered_experience_version, recorded_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: FindLatestSubjectResponse :one
SELECT *
FROM idenqa.subject_responses
WHERE tenant_id = $1 AND authority_id = $2
ORDER BY recorded_at DESC, id DESC
LIMIT 1;

-- name: FindSubjectResponse :one
SELECT *
FROM idenqa.subject_responses
WHERE tenant_id = $1 AND id = $2;

-- name: InsertAuthorityAudit :exec
INSERT INTO idenqa.authority_audit (
    tenant_id, authority_id, aggregate_version, action,
    actor_type, actor_id, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7);
