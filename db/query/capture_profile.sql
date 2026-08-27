-- name: CreateCaptureProfile :exec
INSERT INTO idenqa.capture_profiles (
    id, tenant_id, name, state, version, latest_revision, draft_revision,
    published_revision, created_at, updated_at, deactivated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
);

-- name: CreateCaptureProfileRevision :exec
INSERT INTO idenqa.capture_profile_revisions (
    tenant_id, profile_id, revision, state, schema_version,
    registry_schema_version, registry_revision, registry_digest,
    document, digest, created_at, updated_at, published_at, ended_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
);

-- name: FindCaptureProfile :one
SELECT *
FROM idenqa.capture_profiles
WHERE tenant_id = $1 AND id = $2;

-- name: FindCaptureProfileRevision :one
SELECT *
FROM idenqa.capture_profile_revisions
WHERE tenant_id = $1 AND profile_id = $2 AND revision = $3;

-- name: ListCaptureProfilesFirst :many
SELECT *
FROM idenqa.capture_profiles
WHERE tenant_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListCaptureProfilesAfter :many
SELECT *
FROM idenqa.capture_profiles
WHERE tenant_id = $1 AND (created_at, id) < (
    sqlc.arg(after_created_at)::timestamptz,
    sqlc.arg(after_id)::text
)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: SaveCaptureProfile :one
UPDATE idenqa.capture_profiles
SET name = $3,
    state = $4,
    version = $5,
    latest_revision = $6,
    draft_revision = $7,
    published_revision = $8,
    updated_at = $9,
    deactivated_at = $10
WHERE tenant_id = $1 AND id = $2 AND version = sqlc.arg(expected_version)
RETURNING *;

-- name: SaveCaptureProfileDraft :one
UPDATE idenqa.capture_profile_revisions
SET schema_version = $4,
    registry_schema_version = $5,
    registry_revision = $6,
    registry_digest = $7,
    document = $8,
    digest = $9,
    updated_at = $10
WHERE tenant_id = $1 AND profile_id = $2 AND revision = $3 AND state = 'draft'
RETURNING *;

-- name: PublishCaptureProfileRevision :one
UPDATE idenqa.capture_profile_revisions
SET state = 'published', updated_at = $4, published_at = $4
WHERE tenant_id = $1 AND profile_id = $2 AND revision = $3 AND state = 'draft'
RETURNING *;

-- name: SupersedeCaptureProfileRevision :one
UPDATE idenqa.capture_profile_revisions
SET state = 'superseded', updated_at = $4, ended_at = $4
WHERE tenant_id = $1 AND profile_id = $2 AND revision = $3 AND state = 'published'
RETURNING *;

-- name: WithdrawCaptureProfileRevision :one
UPDATE idenqa.capture_profile_revisions
SET state = 'withdrawn', updated_at = $4, ended_at = $4
WHERE tenant_id = $1 AND profile_id = $2 AND revision = $3 AND state = 'draft'
RETURNING *;

-- name: InsertCaptureProfileAudit :exec
INSERT INTO idenqa.capture_profile_audit (
    tenant_id, profile_id, aggregate_version, revision, action, actor_key_id, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7);
