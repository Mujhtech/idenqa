-- name: CreateEvidenceUploadIntent :exec
INSERT INTO idenqa.evidence_upload_intents (
    id, tenant_id, capture_token_id, subject_id, verification_id, evidence_id,
    authority_id, response_id, profile_id, profile_revision, profile_digest,
    registry_schema_version, registry_revision, registry_digest,
    requirement_key, purpose, evidence_type, artefact, acquisition_method,
    assurances, encryption_purpose, allowed_media_types, maximum_bytes,
    expected_bytes, expected_digest, media_type, region, retention_class,
    state, version, attempt, attempt_timeout_milliseconds, lease_expires_at,
    created_at, updated_at, expires_at, accepted_at, rejection_reason,
    fallback_condition
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
    $31, $32, $33, $34, $35, $36, $37, $38, $39
);

-- name: FindEvidenceUploadIntent :one
SELECT *
FROM idenqa.evidence_upload_intents
WHERE tenant_id = $1 AND id = $2;

-- name: CreateEvidenceTemporalFrame :exec
INSERT INTO idenqa.evidence_temporal_frames (
    tenant_id, verification_id, upload_id, evidence_id, sequence_digest,
    frame_index, frame_count, challenge_id, captured_at, previous_digest,
    content_digest
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);

-- name: FindEvidenceTemporalFrame :one
SELECT * FROM idenqa.evidence_temporal_frames
WHERE tenant_id=$1 AND upload_id=$2;

-- name: ListAcceptedEvidenceUploadIntents :many
SELECT evidence_upload_intents.*
FROM idenqa.evidence_upload_intents
WHERE evidence_upload_intents.tenant_id = $1
  AND (evidence_upload_intents.capture_token_id = $2 OR EXISTS (
    SELECT 1 FROM idenqa.capture_recovery_uploads recovery
    WHERE recovery.tenant_id=evidence_upload_intents.tenant_id AND recovery.new_token_id=$2
      AND recovery.upload_id=evidence_upload_intents.id AND recovery.disposition='retained'
  ))
  AND EXISTS(SELECT 1 FROM idenqa.capture_tokens token WHERE token.tenant_id=evidence_upload_intents.tenant_id AND token.id=$2 AND token.verification_id=$3 AND token.revoked_at IS NULL)
  AND evidence_upload_intents.verification_id = $3
  AND evidence_upload_intents.state = 'accepted'
  AND (
    NOT EXISTS (
      SELECT 1 FROM idenqa.evidence_temporal_frames own_frame
      WHERE own_frame.tenant_id=evidence_upload_intents.tenant_id
        AND own_frame.upload_id=evidence_upload_intents.id
    )
    OR EXISTS (
      SELECT 1
      FROM idenqa.evidence_temporal_frames own_frame
      WHERE own_frame.tenant_id=evidence_upload_intents.tenant_id
        AND own_frame.upload_id=evidence_upload_intents.id
        AND own_frame.frame_index=own_frame.frame_count-1
        AND own_frame.frame_count=(
          SELECT COUNT(*)
          FROM idenqa.evidence_temporal_frames sequence_frame
          JOIN idenqa.evidence_upload_intents sequence_upload
            ON sequence_upload.tenant_id=sequence_frame.tenant_id
           AND sequence_upload.id=sequence_frame.upload_id
           AND sequence_upload.state='accepted'
          WHERE sequence_frame.tenant_id=own_frame.tenant_id
            AND sequence_frame.verification_id=own_frame.verification_id
            AND sequence_frame.sequence_digest=own_frame.sequence_digest
        )
    )
  )
ORDER BY evidence_upload_intents.accepted_at, evidence_upload_intents.id
LIMIT $4;

-- name: LoadCaptureProgressPublication :one
SELECT
    sessions.requirements,
    sessions.document_selections,
    tokens.expires_at AS capture_token_expires_at,
    COALESCE(
        jsonb_agg(
            jsonb_build_object(
                'requirement_key', uploads.requirement_key,
                'artefact', uploads.artefact,
                'acquisition_method', uploads.acquisition_method,
                'sequence_digest', frames.sequence_digest,
                'frame_index', frames.frame_index,
                'frame_count', frames.frame_count,
                'challenge_id', frames.challenge_id,
                'captured_at', frames.captured_at,
                'previous_digest', frames.previous_digest,
                'content_digest', frames.content_digest
            )
            ORDER BY uploads.accepted_at, uploads.id
        ) FILTER (WHERE uploads.state = 'accepted'),
        '[]'::jsonb
    )::text AS accepted_bindings
FROM idenqa.verification_sessions AS sessions
JOIN idenqa.capture_tokens AS tokens
  ON tokens.tenant_id = sessions.tenant_id
 AND tokens.verification_id = sessions.id
LEFT JOIN idenqa.evidence_upload_intents AS uploads
  ON uploads.tenant_id = sessions.tenant_id
 AND uploads.verification_id = sessions.id
 AND (uploads.capture_token_id = tokens.id OR EXISTS (
 SELECT 1 FROM idenqa.capture_recovery_uploads recovery
 WHERE recovery.tenant_id=uploads.tenant_id AND recovery.new_token_id=tokens.id
 AND recovery.upload_id=uploads.id AND recovery.disposition='retained'))
LEFT JOIN idenqa.evidence_temporal_frames AS frames
  ON frames.tenant_id=uploads.tenant_id AND frames.upload_id=uploads.id
WHERE sessions.tenant_id = sqlc.arg(tenant_id)
  AND sessions.id = sqlc.arg(verification_id)
  AND tokens.id = sqlc.arg(capture_token_id)
  AND tokens.revoked_at IS NULL
GROUP BY sessions.requirements, sessions.document_selections, tokens.expires_at;

-- name: LockEvidenceUploadIntent :one
SELECT *
FROM idenqa.evidence_upload_intents
WHERE tenant_id = $1 AND id = $2
FOR UPDATE;

-- name: LockVerificationForUpload :one
SELECT * FROM idenqa.verification_sessions
WHERE tenant_id = $1 AND id = $2
FOR UPDATE;

-- name: LockCaptureTokenForUpload :one
SELECT * FROM idenqa.capture_tokens
WHERE tenant_id = $1 AND id = $2 AND verification_id = $3
FOR SHARE;

-- name: TransitionEvidenceUploadIntent :one
UPDATE idenqa.evidence_upload_intents
SET state = $4,
    version = $5,
    attempt = $6,
    lease_expires_at = $7,
    updated_at = $8,
    accepted_at = $9,
    rejection_reason = $10
WHERE tenant_id = $1 AND id = $2 AND version = $3
RETURNING *;

-- name: InsertEvidenceUploadIntentAudit :exec
INSERT INTO idenqa.evidence_upload_intent_audit (
    tenant_id, upload_id, aggregate_version, attempt,
    action, principal_id, reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: CompleteVerificationCapture :execrows
UPDATE idenqa.verification_sessions
SET capture_completed_at = COALESCE(capture_completed_at, sqlc.arg(completed_at)),
    updated_at = GREATEST(updated_at, sqlc.arg(completed_at))
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(verification_id)
  AND capture_completed_at IS NULL;
