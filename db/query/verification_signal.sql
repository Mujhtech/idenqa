-- name: FindVerificationSignalSession :one
SELECT id FROM idenqa.verification_sessions
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- name: ListVerificationSignals :many
SELECT id, check_id, attempt_id, runner_kind, runner_id, runner_version,
       contract_major, contract_minor, signal_name, signal_outcome,
       reason_codes, recorded_at
FROM idenqa.verification_observations
WHERE tenant_id = sqlc.arg(tenant_id) AND verification_id = sqlc.arg(verification_id)
ORDER BY recorded_at, id
LIMIT 201;
