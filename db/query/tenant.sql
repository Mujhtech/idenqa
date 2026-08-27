-- name: SetTenantScope :one
SELECT set_config('idenqa.tenant_id', sqlc.arg(tenant_id), true);

-- name: CreateTenant :exec
INSERT INTO idenqa.tenants (id, state, version, created_at, updated_at, disabled_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: FindTenant :one
SELECT id, state, version, created_at, updated_at, disabled_at
FROM idenqa.tenants
WHERE id = $1;

-- name: DisableTenant :one
UPDATE idenqa.tenants
SET state = 'disabled', version = version + 1, updated_at = $3, disabled_at = $3
WHERE id = $1 AND version = $2 AND state = 'active'
RETURNING id, state, version, created_at, updated_at, disabled_at;

-- name: InsertTenantAdminAudit :exec
INSERT INTO idenqa.tenant_admin_audit (tenant_id, action, actor, reason, occurred_at)
VALUES ($1, $2, $3, $4, $5);
