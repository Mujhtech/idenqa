-- name: LockActiveCaptureProfileRevision :one
SELECT
    profiles.id AS profile_id,
    profiles.tenant_id,
    profiles.name,
    profiles.state AS profile_state,
    profiles.version AS profile_version,
    profiles.latest_revision,
    profiles.draft_revision,
    profiles.published_revision,
    profiles.created_at AS profile_created_at,
    profiles.updated_at AS profile_updated_at,
    profiles.deactivated_at,
    revisions.revision,
    revisions.state AS revision_state,
    revisions.schema_version,
    revisions.registry_schema_version,
    revisions.registry_revision,
    revisions.registry_digest,
    revisions.document,
    revisions.digest,
    revisions.created_at AS revision_created_at,
    revisions.updated_at AS revision_updated_at,
    revisions.published_at,
    revisions.ended_at
FROM idenqa.capture_profiles AS profiles
JOIN idenqa.capture_profile_revisions AS revisions
  ON revisions.tenant_id = profiles.tenant_id
 AND revisions.profile_id = profiles.id
 AND revisions.revision = profiles.published_revision
WHERE profiles.tenant_id = $1
  AND profiles.id = $2
  AND profiles.state = 'active'
  AND revisions.state = 'published'
FOR SHARE OF profiles, revisions;

-- name: CreateVerificationSession :exec
INSERT INTO idenqa.verification_sessions (
    id, tenant_id, state, version, source_profile_id,
    source_profile_revision, source_profile_digest, requirements, region, policy_id, decision_id,
    created_at, updated_at, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
);

-- name: FindVerificationSession :one
SELECT *
FROM idenqa.verification_sessions
WHERE tenant_id = $1 AND id = $2;

-- name: FindCaptureOutcome :one
SELECT
    sessions.id AS verification_id,
    sessions.state AS session_state,
    sessions.version AS session_version,
    sessions.updated_at,
    decisions.outcome AS decision_outcome
FROM idenqa.verification_sessions AS sessions
LEFT JOIN idenqa.verification_decisions AS decisions
  ON decisions.tenant_id = sessions.tenant_id
 AND decisions.verification_id = sessions.id
 AND decisions.id = sessions.completed_decision_id
WHERE sessions.tenant_id = $1 AND sessions.id = $2;

-- name: CreateCaptureToken :exec
INSERT INTO idenqa.capture_tokens (
    id, tenant_id, verification_id, key_version,
    issued_at, expires_at, revoked_at
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: FindCaptureToken :one
SELECT *
FROM idenqa.capture_tokens
WHERE tenant_id = $1 AND id = $2 AND verification_id = $3;

-- name: FindCaptureContext :one
SELECT
    tokens.id AS token_id,
    tokens.tenant_id,
    tokens.verification_id,
    tokens.key_version,
    tokens.issued_at,
    tokens.expires_at AS token_expires_at,
    tokens.revoked_at,
    sessions.state AS session_state,
    sessions.version AS session_version,
    sessions.source_profile_id,
    sessions.source_profile_revision,
    sessions.source_profile_digest,
    sessions.requirements,
    sessions.document_selections,
    sessions.capture_completed_at,
    sessions.region,
    sessions.policy_id,
    sessions.decision_id,
    sessions.created_at AS session_created_at,
    sessions.updated_at AS session_updated_at,
    sessions.expires_at AS session_expires_at
FROM idenqa.capture_tokens AS tokens
JOIN idenqa.verification_sessions AS sessions
  ON sessions.tenant_id = tokens.tenant_id
 AND sessions.id = tokens.verification_id
WHERE tokens.tenant_id = $1 AND tokens.id = $2 AND tokens.verification_id = $3;

-- name: CreateOutcomeToken :exec
INSERT INTO idenqa.outcome_tokens (
    id, tenant_id, verification_id, key_version,
    issued_at, expires_at, revoked_at
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: FindOutcomeToken :one
SELECT *
FROM idenqa.outcome_tokens
WHERE tenant_id = $1 AND id = $2 AND verification_id = $3;

-- name: FindOutcomeTokenByVerification :one
SELECT *
FROM idenqa.outcome_tokens
WHERE tenant_id = $1 AND verification_id = $2;

-- name: FindOutcomeContext :one
SELECT
    tokens.id AS token_id,
    tokens.tenant_id,
    tokens.verification_id,
    tokens.key_version,
    tokens.issued_at,
    tokens.expires_at AS token_expires_at,
    tokens.revoked_at
FROM idenqa.outcome_tokens AS tokens
WHERE tokens.tenant_id = $1 AND tokens.id = $2 AND tokens.verification_id = $3;

-- name: InsertVerificationSessionAudit :exec
INSERT INTO idenqa.verification_session_audit (
    tenant_id, verification_id, aggregate_version, action,
    actor_key_id, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertOutboxEvent :exec
INSERT INTO idenqa.outbox_events (
    id, tenant_id, aggregate_type, aggregate_id, aggregate_version,
    event_type, schema_version, payload, occurred_at, created_at, published_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
