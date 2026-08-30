-- name: CreateEvidenceObjectReconciliation :execrows
INSERT INTO idenqa.evidence_object_reconciliations (
    tenant_id, upload_id, evidence_id, upload_attempt,
    object_key, object_version, object_size, object_checksum,
    state, version, claim, available_at, lease_expires_at,
    created_at, updated_at, resolved_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    $9, $10, $11, $12, $13, $14, $15, $16
)
ON CONFLICT (tenant_id, upload_id, upload_attempt) DO NOTHING;

-- name: FindEvidenceObjectReconciliation :one
SELECT *
FROM idenqa.evidence_object_reconciliations
WHERE tenant_id = $1 AND upload_id = $2 AND upload_attempt = $3;

-- name: LockEvidenceObjectReconciliation :one
SELECT *
FROM idenqa.evidence_object_reconciliations
WHERE tenant_id = $1 AND upload_id = $2 AND upload_attempt = $3
FOR UPDATE;

-- name: LockNextEvidenceObjectReconciliation :one
SELECT *
FROM idenqa.evidence_object_reconciliations
WHERE tenant_id = $1
  AND ((state = 'pending' AND available_at <= $2)
    OR (state = 'claimed' AND lease_expires_at <= $2))
ORDER BY COALESCE(lease_expires_at, available_at), upload_id, upload_attempt
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: TransitionEvidenceObjectReconciliation :one
UPDATE idenqa.evidence_object_reconciliations
SET state = $5,
    version = $6,
    claim = $7,
    available_at = $8,
    lease_expires_at = $9,
    updated_at = $10,
    resolved_at = $11
WHERE tenant_id = $1 AND upload_id = $2 AND upload_attempt = $3 AND version = $4
RETURNING *;

-- name: RetainEvidenceObjectReconciliationForAcceptance :one
UPDATE idenqa.evidence_object_reconciliations
SET state = 'retained',
    version = version + 1,
    lease_expires_at = NULL,
    updated_at = $9,
    resolved_at = $9
WHERE tenant_id = $1 AND upload_id = $2 AND upload_attempt = $3
  AND evidence_id = $4 AND object_key = $5 AND object_version = $6
  AND object_size = $7 AND object_checksum = $8 AND state = 'pending'
RETURNING *;

-- name: InsertEvidenceObjectReconciliationAudit :exec
INSERT INTO idenqa.evidence_object_reconciliation_audit (
    tenant_id, upload_id, upload_attempt, aggregate_version,
    claim, action, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: IsEvidenceObjectReferenced :one
SELECT EXISTS (
    SELECT 1
    FROM idenqa.evidence_assets AS asset
    WHERE asset.tenant_id = sqlc.arg(tenant_id)
      AND asset.object_key = sqlc.arg(object_key)
      AND asset.object_version = sqlc.arg(object_version)
    UNION ALL
    SELECT 1
    FROM idenqa.evidence_object_reconciliations AS reconciliation
    WHERE reconciliation.tenant_id = sqlc.arg(tenant_id)
      AND reconciliation.object_key = sqlc.arg(object_key)
      AND reconciliation.object_version = sqlc.arg(object_version)
      AND reconciliation.state IN ('pending', 'claimed', 'retained')
) AS referenced;
