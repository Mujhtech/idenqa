-- name: TryIdempotencyLock :one
SELECT pg_try_advisory_xact_lock(hashtextextended(
    concat_ws(
        E'\x1f',
        sqlc.arg(tenant_id)::text,
        sqlc.arg(principal_id)::text,
        sqlc.arg(operation)::text,
        sqlc.arg(idempotency_key)::text
    ),
    0
));

-- name: DeleteExpiredIdempotencyRecord :exec
DELETE FROM idenqa.idempotency_records
WHERE tenant_id = $1 AND principal_id = $2 AND operation = $3 AND
      idempotency_key = $4 AND expires_at <= $5;

-- name: CreateIdempotencyRecord :execrows
INSERT INTO idenqa.idempotency_records (
    tenant_id, principal_id, operation, idempotency_key, request_fingerprint,
    state, created_at, expires_at
) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7)
ON CONFLICT (tenant_id, principal_id, operation, idempotency_key) DO NOTHING;

-- name: FindIdempotencyRecord :one
SELECT *
FROM idenqa.idempotency_records
WHERE tenant_id = $1 AND principal_id = $2 AND operation = $3 AND idempotency_key = $4;

-- name: CompleteIdempotencyRecord :one
UPDATE idenqa.idempotency_records
SET state = 'completed', result_status = $6, result = $7, completed_at = $8
WHERE tenant_id = $1 AND principal_id = $2 AND operation = $3 AND
      idempotency_key = $4 AND request_fingerprint = $5 AND state = 'pending'
RETURNING *;
