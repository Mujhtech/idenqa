-- name: FindVerificationLifecycleOrigin :one
SELECT 'collecting'::text AS state, 1::bigint AS version, created_at AS occurred_at
FROM idenqa.verification_sessions
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(verification_id);

-- name: ListVerificationLifecycleTransitions :many
SELECT event_id, from_state, to_state, resulting_version, decision_id, occurred_at
FROM idenqa.verification_transitions
WHERE tenant_id = sqlc.arg(tenant_id) AND verification_id = sqlc.arg(verification_id)
ORDER BY resulting_version, event_id
LIMIT 101;
