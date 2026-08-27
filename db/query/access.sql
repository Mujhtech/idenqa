-- name: CreateAPIKey :execrows
INSERT INTO idenqa.api_keys (
    id,
    tenant_id,
    label,
    digest,
    pepper_version,
    requested_scopes,
    resolved_scopes,
    version,
    created_at,
    updated_at,
    expires_at,
    revoked_at,
    retired_at,
    replaces_key_id
) SELECT
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
WHERE EXISTS (
    SELECT 1
    FROM idenqa.tenants
    WHERE id = $2 AND state = 'active'
    FOR UPDATE
);

-- name: FindAPIKey :one
SELECT
    id,
    tenant_id,
    label,
    digest,
    pepper_version,
    requested_scopes,
    resolved_scopes,
    version,
    created_at,
    updated_at,
    expires_at,
    revoked_at,
    retired_at,
    replaces_key_id
FROM idenqa.api_keys
WHERE tenant_id = $1 AND id = $2;

-- name: FindAPIKeyForVerification :one
SELECT
    sqlc.embed(api_keys),
    tenants.state AS tenant_state
FROM idenqa.api_keys AS api_keys
JOIN idenqa.tenants AS tenants ON tenants.id = api_keys.tenant_id
WHERE api_keys.tenant_id = $1 AND api_keys.id = $2;

-- name: ListAPIKeys :many
SELECT
    id,
    tenant_id,
    label,
    digest,
    pepper_version,
    requested_scopes,
    resolved_scopes,
    version,
    created_at,
    updated_at,
    expires_at,
    revoked_at,
    retired_at,
    replaces_key_id
FROM idenqa.api_keys
WHERE tenant_id = $1
ORDER BY created_at DESC, id DESC;

-- name: SaveAPIKeyLifecycle :execrows
UPDATE idenqa.api_keys
SET
    version = sqlc.arg(new_version),
    updated_at = sqlc.arg(updated_at),
    revoked_at = sqlc.arg(revoked_at)::timestamptz,
    retired_at = sqlc.arg(retired_at)::timestamptz
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND version = sqlc.arg(expected_version)
  AND sqlc.arg(new_version) = sqlc.arg(expected_version) + 1
  AND (
      (
          sqlc.arg(revoked_at)::timestamptz IS NOT NULL AND
          revoked_at IS NULL AND
          sqlc.arg(revoked_at)::timestamptz = sqlc.arg(updated_at)::timestamptz AND
          sqlc.arg(retired_at)::timestamptz IS NOT DISTINCT FROM retired_at
      ) OR (
          sqlc.arg(revoked_at)::timestamptz IS NULL AND
          revoked_at IS NULL AND
          retired_at IS NULL AND
          sqlc.arg(retired_at)::timestamptz IS NOT NULL AND
          sqlc.arg(retired_at)::timestamptz >= sqlc.arg(updated_at)::timestamptz
      )
  );

-- name: InsertAPIKeyAdminAudit :exec
INSERT INTO idenqa.api_key_admin_audit (
    tenant_id,
    key_id,
    action,
    actor,
    reason,
    occurred_at
)
VALUES ($1, $2, $3, $4, $5, $6);
