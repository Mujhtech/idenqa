-- name: CreateEvidenceAsset :exec
INSERT INTO idenqa.evidence_assets (
    id, tenant_id, subject_id, verification_id, requirement_key, evidence_type,
    artefact, acquisition_method, assurances, registry_schema_version,
    registry_revision, registry_digest, region, retention_class, content_revision,
    object_key, object_version, ciphertext_size, ciphertext_checksum,
    envelope_format_version, content_algorithm, key_purpose, key_provider,
    key_reference, key_version, key_algorithm, wrapped_key,
    context_schema_version, context_digest, plaintext_digest, media_type,
    integrity, state, version, created_at, updated_at, quarantine_reason, quarantined_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
    $31, $32, $33, $34, $35, $36, $37, $38
);

-- name: FindEvidenceAsset :one
SELECT * FROM idenqa.evidence_assets
WHERE tenant_id = $1 AND id = $2;

-- name: LockEvidenceAssetForKeyRewrap :one
SELECT * FROM idenqa.evidence_assets
WHERE tenant_id = $1 AND id = $2 AND version = $3
FOR UPDATE;

-- name: RewrapEvidenceAssetKey :one
UPDATE idenqa.evidence_assets
SET key_provider = $4, key_reference = $5, key_version = $6,
    key_algorithm = $7, wrapped_key = $8, version = $9, updated_at = $10
WHERE tenant_id = $1 AND id = $2 AND version = $3
RETURNING *;

-- name: TransitionEvidenceAsset :one
UPDATE idenqa.evidence_assets
SET integrity = $4, state = $5, version = $6, updated_at = $7,
    quarantine_reason = $8, quarantined_at = $9
WHERE tenant_id = $1 AND id = $2 AND version = $3 AND state = 'available'
RETURNING *;

-- name: InsertEvidenceAssetAudit :exec
INSERT INTO idenqa.evidence_asset_audit (
    tenant_id, evidence_id, aggregate_version, action, reason, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertEvidenceKeyRewrapAudit :exec
INSERT INTO idenqa.evidence_key_rewrap_audit (
    tenant_id, evidence_id, aggregate_version,
    previous_key_provider, previous_key_reference, previous_key_version, previous_key_algorithm,
    new_key_provider, new_key_reference, new_key_version, new_key_algorithm,
    principal_type, principal_id, tenant_actor_type, tenant_actor_id, reason, occurred_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15, $16, $17
);
