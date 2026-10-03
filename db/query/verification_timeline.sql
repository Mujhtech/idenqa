-- name: InsertCaptureJourneyEvent :execrows
INSERT INTO idenqa.capture_journey_events (
    tenant_id, verification_id, capture_token_id, event_id, sequence,
    event_type, screen, action, requirement_key, artefact, acquisition_method,
    client_occurred_at, received_at, payload_digest
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(capture_token_id),
    sqlc.arg(event_id), sqlc.arg(sequence), sqlc.arg(event_type), sqlc.arg(screen),
    sqlc.narg(action), sqlc.narg(requirement_key), sqlc.narg(artefact),
    sqlc.narg(acquisition_method), sqlc.arg(client_occurred_at),
    sqlc.arg(received_at), sqlc.arg(payload_digest)
)
ON CONFLICT (tenant_id, event_id) DO NOTHING;

-- name: FindCaptureJourneyEventDigest :one
SELECT payload_digest FROM idenqa.capture_journey_events
WHERE tenant_id = sqlc.arg(tenant_id) AND event_id = sqlc.arg(event_id);

-- name: FindVerificationTimelineSession :one
SELECT id FROM idenqa.verification_sessions
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- name: ListVerificationTimeline :many
SELECT * FROM (
    SELECT ('origin:' || sessions.id)::text AS event_id, 'lifecycle'::text AS category,
           'core'::text AS source, 'verification.created'::text AS event_name,
           NULL::text AS status, NULL::text AS detail, sessions.created_at AS occurred_at,
           true AS authoritative
    FROM idenqa.verification_sessions AS sessions
    WHERE sessions.tenant_id = sqlc.arg(tenant_id) AND sessions.id = sqlc.arg(verification_id)
    UNION ALL
    SELECT transitions.event_id, 'lifecycle', 'core', 'verification.transitioned',
           transitions.to_state, transitions.from_state, transitions.occurred_at, true
    FROM idenqa.verification_transitions AS transitions
    WHERE transitions.tenant_id = sqlc.arg(tenant_id) AND transitions.verification_id = sqlc.arg(verification_id)
    UNION ALL
    SELECT events.event_id, 'interaction', 'capture_client', events.event_type,
           events.screen, events.action, events.received_at, false
    FROM idenqa.capture_journey_events AS events
    WHERE events.tenant_id = sqlc.arg(tenant_id) AND events.verification_id = sqlc.arg(verification_id)
    UNION ALL
    SELECT responses.id, 'interaction', 'core', ('notice.' || responses.action),
           responses.action, NULL::text, responses.recorded_at, true
    FROM idenqa.subject_responses AS responses
    WHERE responses.tenant_id = sqlc.arg(tenant_id) AND responses.verification_id = sqlc.arg(verification_id)
    UNION ALL
    SELECT assets.id, 'evidence', 'core', 'evidence.received', assets.state,
           assets.artefact, assets.created_at, true
    FROM idenqa.evidence_assets AS assets
    WHERE assets.tenant_id = sqlc.arg(tenant_id) AND assets.verification_id = sqlc.arg(verification_id)
    UNION ALL
    SELECT attempts.id || ':started', 'processing', 'core', 'check.attempt.started',
           attempts.state, attempts.runner_id, attempts.started_at, true
    FROM idenqa.verification_attempts AS attempts
    WHERE attempts.tenant_id = sqlc.arg(tenant_id) AND attempts.verification_id = sqlc.arg(verification_id)
    UNION ALL
    SELECT attempts.id || ':finished', 'processing', 'core', 'check.attempt.finished',
           attempts.state, attempts.failure_code, attempts.finished_at, true
    FROM idenqa.verification_attempts AS attempts
    WHERE attempts.tenant_id = sqlc.arg(tenant_id) AND attempts.verification_id = sqlc.arg(verification_id)
      AND attempts.finished_at IS NOT NULL
    UNION ALL
    SELECT observations.id, 'signal', 'core', observations.signal_name,
           observations.signal_outcome, NULL::text, observations.recorded_at, true
    FROM idenqa.verification_observations AS observations
    WHERE observations.tenant_id = sqlc.arg(tenant_id) AND observations.verification_id = sqlc.arg(verification_id)
    UNION ALL
    SELECT decisions.id, 'decision', 'core', 'decision.created', decisions.outcome,
           decisions.selected, decisions.decided_at, true
    FROM idenqa.verification_decisions AS decisions
    WHERE decisions.tenant_id = sqlc.arg(tenant_id) AND decisions.verification_id = sqlc.arg(verification_id)
) AS timeline
ORDER BY occurred_at, event_id
LIMIT 501;
