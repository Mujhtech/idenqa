-- name: InsertVerificationCheck :exec
INSERT INTO idenqa.verification_checks (
    id, tenant_id, verification_id, name, state, outcome, version, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(name),
    sqlc.arg(state), sqlc.narg(outcome), sqlc.arg(version), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: FindVerificationCheck :one
SELECT * FROM idenqa.verification_checks
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- name: UpdateVerificationCheck :execrows
UPDATE idenqa.verification_checks
SET state = sqlc.arg(state), outcome = sqlc.narg(outcome), version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: LockVerificationCheck :one
SELECT * FROM idenqa.verification_checks
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: InsertVerificationAttempt :execrows
INSERT INTO idenqa.verification_attempts (
    id, tenant_id, verification_id, check_id, attempt_number, fence, runner_kind,
    runner_id, runner_version, package_digest, contract_major, contract_minor,
    request_digest, configuration_digest, state, started_at, deadline, finished_at,
    failure_class, failure_code, retry_disposition, retry_after_milliseconds, result_digest
) VALUES (
    sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(check_id),
    sqlc.arg(attempt_number), sqlc.arg(fence), sqlc.arg(runner_kind), sqlc.arg(runner_id),
    sqlc.arg(runner_version), sqlc.arg(package_digest), sqlc.arg(contract_major),
    sqlc.arg(contract_minor), sqlc.arg(request_digest), sqlc.arg(configuration_digest),
    sqlc.arg(state), sqlc.arg(started_at), sqlc.arg(deadline), sqlc.narg(finished_at),
    sqlc.narg(failure_class), sqlc.narg(failure_code), sqlc.narg(retry_disposition),
    sqlc.narg(retry_after_milliseconds), sqlc.narg(result_digest)
)
ON CONFLICT (tenant_id, id) DO NOTHING;

-- name: CompleteVerificationAttempt :execrows
UPDATE idenqa.verification_attempts
SET state = sqlc.arg(state), finished_at = sqlc.arg(finished_at),
    failure_class = sqlc.narg(failure_class), failure_code = sqlc.narg(failure_code),
    retry_disposition = sqlc.narg(retry_disposition),
    retry_after_milliseconds = sqlc.narg(retry_after_milliseconds), result_digest = sqlc.arg(result_digest)
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id) AND check_id = sqlc.arg(check_id)
  AND fence = sqlc.arg(fence) AND state = 'running';

-- name: ListVerificationAttempts :many
SELECT * FROM idenqa.verification_attempts
WHERE tenant_id = sqlc.arg(tenant_id) AND check_id = sqlc.arg(check_id)
ORDER BY attempt_number;

-- name: InsertVerificationObservation :exec
INSERT INTO idenqa.verification_observations (
    id, tenant_id, verification_id, check_id, attempt_id, runner_kind,
    runner_id, runner_version, package_digest, contract_major, contract_minor,
    request_digest, configuration_digest, signal_name, signal_outcome, reason_codes, recorded_at
) VALUES (
    sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(check_id),
    sqlc.arg(attempt_id), sqlc.arg(runner_kind), sqlc.arg(runner_id), sqlc.arg(runner_version),
    sqlc.arg(package_digest), sqlc.arg(contract_major), sqlc.arg(contract_minor),
    sqlc.arg(request_digest), sqlc.arg(configuration_digest), sqlc.arg(signal_name),
    sqlc.arg(signal_outcome), sqlc.arg(reason_codes), sqlc.arg(recorded_at)
);

-- name: ListVerificationObservations :many
SELECT * FROM idenqa.verification_observations
WHERE tenant_id = sqlc.arg(tenant_id) AND check_id = sqlc.arg(check_id)
ORDER BY recorded_at, id;

-- name: InsertVerificationAttemptDiagnostic :execrows
INSERT INTO idenqa.verification_attempt_diagnostics (
    tenant_id, verification_id, check_id, attempt_id, kind, code, recorded_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(check_id),
    sqlc.arg(attempt_id), sqlc.arg(kind), sqlc.arg(code), sqlc.arg(recorded_at)
)
ON CONFLICT DO NOTHING;

-- name: ListVerificationAttemptDiagnostics :many
SELECT * FROM idenqa.verification_attempt_diagnostics
WHERE tenant_id = sqlc.arg(tenant_id) AND check_id = sqlc.arg(check_id)
ORDER BY recorded_at, attempt_id, kind;

-- name: ClaimVerificationResultInbox :execrows
INSERT INTO idenqa.verification_result_inbox (
    tenant_id, verification_id, check_id, attempt_id, result_digest, received_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(check_id),
    sqlc.arg(attempt_id), sqlc.arg(result_digest), sqlc.arg(received_at)
)
ON CONFLICT DO NOTHING;

-- name: InsertVerificationReconciliation :execrows
INSERT INTO idenqa.verification_reconciliations (
    tenant_id, verification_id, check_id, attempt_id, reason, status,
    claim_token, lease_expires_at, claim_count, available_at, created_at, updated_at, resolved_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(check_id),
    sqlc.arg(attempt_id), sqlc.arg(reason), 'pending', NULL, NULL, 0,
    sqlc.arg(available_at), sqlc.arg(created_at), sqlc.arg(updated_at), NULL
)
ON CONFLICT DO NOTHING;

-- name: ClaimVerificationReconciliation :one
WITH candidate AS (
    SELECT check_id, attempt_id, reason
    FROM idenqa.verification_reconciliations
    WHERE tenant_id = sqlc.arg(tenant_id)
      AND available_at <= sqlc.arg(claimed_at)
      AND (status = 'pending' OR (status = 'claimed' AND lease_expires_at <= sqlc.arg(claimed_at)))
    ORDER BY available_at, created_at, check_id, attempt_id, reason
    LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE idenqa.verification_reconciliations AS reconciliations
SET status = 'claimed', claim_token = sqlc.arg(claim_token), lease_expires_at = sqlc.arg(lease_expires_at),
    claim_count = claim_count + 1, updated_at = sqlc.arg(claimed_at)
FROM candidate
WHERE reconciliations.tenant_id = sqlc.arg(tenant_id)
  AND reconciliations.check_id = candidate.check_id
  AND reconciliations.attempt_id = candidate.attempt_id
  AND reconciliations.reason = candidate.reason
RETURNING reconciliations.*;

-- name: ClaimVerificationReconciliationForAttempt :one
UPDATE idenqa.verification_reconciliations
SET status = 'claimed', claim_token = sqlc.arg(claim_token),
    lease_expires_at = sqlc.arg(lease_expires_at), claim_count = claim_count + 1,
    updated_at = sqlc.arg(claimed_at)
WHERE tenant_id = sqlc.arg(tenant_id)
  AND check_id = sqlc.arg(check_id)
  AND attempt_id = sqlc.arg(attempt_id)
  AND available_at <= sqlc.arg(claimed_at)
  AND (
      status = 'pending' OR
      (status = 'claimed' AND lease_expires_at <= sqlc.arg(claimed_at))
  )
RETURNING *;

-- name: FindVerificationReconciliationForAttempt :one
SELECT *
FROM idenqa.verification_reconciliations
WHERE tenant_id = sqlc.arg(tenant_id)
  AND check_id = sqlc.arg(check_id)
  AND attempt_id = sqlc.arg(attempt_id);

-- name: ResolveVerificationReconciliation :execrows
UPDATE idenqa.verification_reconciliations
SET status = 'resolved', claim_token = NULL, lease_expires_at = NULL,
    updated_at = sqlc.arg(resolved_at), resolved_at = sqlc.arg(resolved_at)
WHERE tenant_id = sqlc.arg(tenant_id) AND check_id = sqlc.arg(check_id)
  AND attempt_id = sqlc.arg(attempt_id) AND reason = sqlc.arg(reason)
  AND status = 'claimed' AND claim_token = sqlc.arg(claim_token)
  AND updated_at <= sqlc.arg(resolved_at) AND lease_expires_at > sqlc.arg(resolved_at);

-- name: ListPendingCheckProgressEvents :many
SELECT events.*, sessions.expires_at AS session_expires_at
FROM idenqa.outbox_events AS events
JOIN idenqa.verification_sessions AS sessions
  ON sessions.tenant_id = events.tenant_id
 AND sessions.id = (events.payload ->> 'verification_id')
WHERE events.tenant_id = sqlc.arg(tenant_id)
  AND events.event_type = 'verification.check.progress.v1'
  AND events.schema_version = 1
  AND events.published_at IS NULL
ORDER BY events.created_at, events.id
LIMIT sqlc.arg(batch_size)
FOR UPDATE OF events SKIP LOCKED;

-- name: MarkCheckProgressPublished :execrows
UPDATE idenqa.outbox_events
SET published_at = sqlc.arg(published_at)
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND event_type = 'verification.check.progress.v1'
  AND schema_version = 1
  AND published_at IS NULL;

-- name: NotifyCheckProgress :exec
SELECT pg_notify('idenqa_check_progress_v1', sqlc.arg(tenant_id)::text);
