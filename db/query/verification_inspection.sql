-- name: FindVerificationInspectionSession :one
SELECT id FROM idenqa.verification_sessions
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- name: ListVerificationInspectionEvidence :many
SELECT id, requirement_key, evidence_type, artefact, acquisition_method,
       assurances, state, integrity, retention_class, created_at, updated_at
FROM idenqa.evidence_assets
WHERE tenant_id = sqlc.arg(tenant_id) AND verification_id = sqlc.arg(verification_id)
ORDER BY created_at, id
LIMIT 100;

-- name: ListVerificationInspectionAttempts :many
SELECT id, check_id, attempt_number, runner_kind, runner_id, runner_version,
       package_digest, contract_major, contract_minor, request_digest,
       configuration_digest, state, started_at, deadline, finished_at,
       failure_class, failure_code, retry_disposition,
       retry_after_milliseconds, result_digest
FROM idenqa.verification_attempts
WHERE tenant_id = sqlc.arg(tenant_id) AND verification_id = sqlc.arg(verification_id)
ORDER BY started_at, check_id, attempt_number
LIMIT 500;

-- name: ListVerificationInspectionDecisions :many
SELECT id, decision_digest, selected, outcome, actor, supersedes_id, decided_at
FROM idenqa.verification_decisions
WHERE tenant_id = sqlc.arg(tenant_id) AND verification_id = sqlc.arg(verification_id)
ORDER BY decided_at, id
LIMIT 100;

-- name: ListVerificationInspectionChecks :many
SELECT checks.id, checks.name, checks.state, checks.outcome,
       count(attempts.id)::integer AS attempt_count,
       checks.created_at, checks.updated_at
FROM idenqa.verification_checks AS checks
LEFT JOIN idenqa.verification_attempts AS attempts
  ON attempts.tenant_id = checks.tenant_id AND attempts.check_id = checks.id
WHERE checks.tenant_id = sqlc.arg(tenant_id)
  AND checks.verification_id = sqlc.arg(verification_id)
GROUP BY checks.tenant_id, checks.id
ORDER BY checks.created_at, checks.id
LIMIT 100;

-- name: ListVerificationInspectionRetention :many
SELECT data_class, region, policy_digest, expires_at
FROM idenqa.retention_bindings
WHERE tenant_id = sqlc.arg(tenant_id) AND aggregate_id = sqlc.arg(verification_id)
ORDER BY data_class
LIMIT 32;

-- name: VerificationInspectionHasLegalHold :one
SELECT EXISTS (
  SELECT 1 FROM idenqa.legal_holds
  WHERE tenant_id = sqlc.arg(tenant_id)
    AND aggregate_id = sqlc.arg(verification_id)
    AND released_at IS NULL
) AS held;

-- name: ListVerificationInspectionWebhooks :many
SELECT events.id AS event_id, events.event_type, events.state AS event_state,
       deliveries.id AS delivery_id, deliveries.state AS delivery_state,
       events.created_at
FROM idenqa.webhook_events AS events
LEFT JOIN idenqa.webhook_deliveries AS deliveries
  ON deliveries.tenant_id = events.tenant_id AND deliveries.event_id = events.id
WHERE events.tenant_id = sqlc.arg(tenant_id)
  AND sqlc.arg(verification_id)::text = ANY(events.aggregate_ids)
ORDER BY events.created_at DESC, events.id, deliveries.id
LIMIT 100;
