-- name: CreateEvidenceProcessingGrant :exec
INSERT INTO idenqa.evidence_processing_grants (
    id, tenant_id, subject_id, verification_id, evidence_id, requirement_key,
    authority_id, response_id, check_reference, runner_identity, workload_version,
    purpose, operation, permitted_variants, region, recipient_reference,
    output_destination, policy_reference, maximum_uses, uses, created_at,
    expires_at, revoked_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
);

-- name: InsertEvidenceProcessingGrantAudit :exec
INSERT INTO idenqa.evidence_processing_grant_audit (
    tenant_id, grant_id, aggregate_version, action, principal_type,
    principal_id, tenant_actor_type, tenant_actor_id, reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertEvidenceGrantAccessAttemptAudit :exec
INSERT INTO idenqa.evidence_grant_access_attempt_audit (
    tenant_id, presented_grant_id, redemption_id, runner_identity,
    workload_version, decision, denial_reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: LockEvidenceProcessingGrant :one
SELECT * FROM idenqa.evidence_processing_grants
WHERE tenant_id = $1 AND id = $2
FOR UPDATE;

-- name: LockEvidenceGrantRedemptionID :one
SELECT true
FROM pg_advisory_xact_lock(hashtextextended(
    concat_ws(E'\x1f', 'evidence_grant_redemption', sqlc.arg(tenant_id)::text,
              sqlc.arg(redemption_id)::text),
    0
));

-- name: FindEvidenceGrantRedemption :one
SELECT * FROM idenqa.evidence_grant_redemptions
WHERE tenant_id = $1 AND id = $2;

-- name: IncrementEvidenceProcessingGrantUse :one
UPDATE idenqa.evidence_processing_grants
SET uses = uses + 1
WHERE tenant_id = $1 AND id = $2 AND uses = $3
RETURNING *;

-- name: CreatePendingEvidenceGrantRedemption :exec
INSERT INTO idenqa.evidence_grant_redemptions (
    id, tenant_id, grant_id, runner_identity, workload_version, claimed_use,
    denial_reason, attempted_at
) VALUES ($1, $2, $3, $4, $5, $6, NULL, $7);

-- name: CreateDeniedEvidenceGrantRedemption :exec
INSERT INTO idenqa.evidence_grant_redemptions (
    id, tenant_id, grant_id, runner_identity, workload_version, claimed_use,
    denial_reason, attempted_at
) VALUES ($1, $2, $3, $4, $5, NULL, $6, $7);

-- name: CreateEvidenceGrantRedemptionOutcome :execrows
INSERT INTO idenqa.evidence_grant_redemption_outcomes (
    tenant_id, redemption_id, grant_id, outcome, denial_reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, redemption_id) DO NOTHING;

-- name: FindEvidenceGrantRedemptionOutcome :one
SELECT * FROM idenqa.evidence_grant_redemption_outcomes
WHERE tenant_id = $1 AND redemption_id = $2 AND grant_id = $3;

-- name: FindEvidenceProcessingGrant :one
SELECT * FROM idenqa.evidence_processing_grants
WHERE tenant_id = $1 AND id = $2;

-- name: RevokeEvidenceProcessingGrant :one
UPDATE idenqa.evidence_processing_grants
SET revoked_at = $3
WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
RETURNING *;
