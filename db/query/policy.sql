-- name: InsertPolicySnapshot :execrows
INSERT INTO idenqa.policy_snapshots (
    tenant_id, verification_id, snapshot_digest, policy_id, policy_revision,
    policy_schema_major, policy_schema_minor, policy_digest, evaluator_major,
    evaluator_minor, evaluator_digest, authority_id, acknowledgement_id,
    region, evaluated_at, canonical
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(snapshot_digest),
    sqlc.arg(policy_id), sqlc.arg(policy_revision), sqlc.arg(policy_schema_major),
    sqlc.arg(policy_schema_minor), sqlc.arg(policy_digest), sqlc.arg(evaluator_major),
    sqlc.arg(evaluator_minor), sqlc.arg(evaluator_digest), sqlc.arg(authority_id),
    sqlc.arg(acknowledgement_id), sqlc.arg(region), sqlc.arg(evaluated_at),
    sqlc.arg(canonical)
)
ON CONFLICT DO NOTHING;

-- name: FindPolicySnapshot :one
SELECT * FROM idenqa.policy_snapshots
WHERE tenant_id = sqlc.arg(tenant_id) AND snapshot_digest = sqlc.arg(snapshot_digest);

-- name: InsertPolicyEvaluation :execrows
INSERT INTO idenqa.policy_evaluations (
    tenant_id, verification_id, snapshot_digest, evaluation_digest,
    selected, outcome, assurance, canonical
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(verification_id), sqlc.arg(snapshot_digest),
    sqlc.arg(evaluation_digest), sqlc.arg(selected), sqlc.narg(outcome),
    sqlc.narg(assurance), sqlc.arg(canonical)
)
ON CONFLICT DO NOTHING;

-- name: FindPolicyEvaluation :one
SELECT * FROM idenqa.policy_evaluations
WHERE tenant_id = sqlc.arg(tenant_id) AND evaluation_digest = sqlc.arg(evaluation_digest);

-- name: LockPolicyDecision :one
SELECT * FROM idenqa.verification_decisions
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: InsertPolicyDecision :execrows
INSERT INTO idenqa.verification_decisions (
    id, tenant_id, verification_id, snapshot_digest, evaluation_digest,
    decision_digest, selected, outcome, actor, supersedes_id, decided_at, canonical
) VALUES (
    sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(verification_id),
    sqlc.arg(snapshot_digest), sqlc.arg(evaluation_digest), sqlc.arg(decision_digest),
    sqlc.arg(selected), sqlc.arg(outcome), sqlc.arg(actor), sqlc.narg(supersedes_id),
    sqlc.arg(decided_at), sqlc.arg(canonical)
)
ON CONFLICT DO NOTHING;

-- name: FindPolicyDecisionBundle :one
SELECT
    decisions.*,
    snapshots.canonical AS snapshot_canonical,
    evaluations.canonical AS evaluation_canonical
FROM idenqa.verification_decisions AS decisions
JOIN idenqa.policy_snapshots AS snapshots
  ON snapshots.tenant_id = decisions.tenant_id
 AND snapshots.verification_id = decisions.verification_id
 AND snapshots.snapshot_digest = decisions.snapshot_digest
JOIN idenqa.policy_evaluations AS evaluations
  ON evaluations.tenant_id = decisions.tenant_id
 AND evaluations.verification_id = decisions.verification_id
 AND evaluations.snapshot_digest = decisions.snapshot_digest
 AND evaluations.evaluation_digest = decisions.evaluation_digest
WHERE decisions.tenant_id = sqlc.arg(tenant_id) AND decisions.id = sqlc.arg(id);

-- name: FindLatestPolicyDecisionBundle :one
SELECT
    decisions.*,
    snapshots.canonical AS snapshot_canonical,
    evaluations.canonical AS evaluation_canonical
FROM idenqa.verification_decisions AS decisions
JOIN idenqa.policy_snapshots AS snapshots
  ON snapshots.tenant_id = decisions.tenant_id
 AND snapshots.verification_id = decisions.verification_id
 AND snapshots.snapshot_digest = decisions.snapshot_digest
JOIN idenqa.policy_evaluations AS evaluations
  ON evaluations.tenant_id = decisions.tenant_id
 AND evaluations.verification_id = decisions.verification_id
 AND evaluations.snapshot_digest = decisions.snapshot_digest
 AND evaluations.evaluation_digest = decisions.evaluation_digest
WHERE decisions.tenant_id = sqlc.arg(tenant_id)
  AND decisions.verification_id = sqlc.arg(verification_id)
  AND NOT EXISTS (
      SELECT 1
      FROM idenqa.verification_decisions AS successor
      WHERE successor.tenant_id = decisions.tenant_id
        AND successor.verification_id = decisions.verification_id
        AND successor.supersedes_id = decisions.id
  )
LIMIT 1;

-- name: ListPolicyDecisionBundles :many
SELECT
    decisions.*,
    snapshots.canonical AS snapshot_canonical,
    evaluations.canonical AS evaluation_canonical
FROM idenqa.verification_decisions AS decisions
JOIN idenqa.policy_snapshots AS snapshots
  ON snapshots.tenant_id = decisions.tenant_id
 AND snapshots.verification_id = decisions.verification_id
 AND snapshots.snapshot_digest = decisions.snapshot_digest
JOIN idenqa.policy_evaluations AS evaluations
  ON evaluations.tenant_id = decisions.tenant_id
 AND evaluations.verification_id = decisions.verification_id
 AND evaluations.snapshot_digest = decisions.snapshot_digest
 AND evaluations.evaluation_digest = decisions.evaluation_digest
WHERE decisions.tenant_id = sqlc.arg(tenant_id)
  AND decisions.verification_id = sqlc.arg(verification_id)
  AND (
      sqlc.arg(before_id)::text = '' OR
      (decisions.decided_at, decisions.id) < (
          SELECT cursor.decided_at, cursor.id
          FROM idenqa.verification_decisions AS cursor
          WHERE cursor.tenant_id = sqlc.arg(tenant_id)
            AND cursor.verification_id = sqlc.arg(verification_id)
            AND cursor.id = sqlc.arg(before_id)
      )
  )
ORDER BY decisions.decided_at DESC, decisions.id DESC
LIMIT sqlc.arg(page_limit);

-- name: InsertPolicy :execrows
INSERT INTO idenqa.policies (
    tenant_id, id, activation_version, active_revision, created_at, updated_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(policy_id), 0, NULL,
    sqlc.arg(created_at), sqlc.arg(created_at)
)
ON CONFLICT DO NOTHING;

-- name: InsertPolicyRevision :execrows
INSERT INTO idenqa.policy_revisions (
    tenant_id, policy_id, revision, schema_major, schema_minor, digest,
    evaluator_major, evaluator_minor, evaluator_digest, canonical, created_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(policy_id), sqlc.arg(revision),
    sqlc.arg(schema_major), sqlc.arg(schema_minor), sqlc.arg(digest),
    sqlc.arg(evaluator_major), sqlc.arg(evaluator_minor), sqlc.arg(evaluator_digest),
    sqlc.arg(canonical), sqlc.arg(created_at)
)
ON CONFLICT DO NOTHING;

-- name: FindPolicyRevision :one
SELECT * FROM idenqa.policy_revisions
WHERE tenant_id = sqlc.arg(tenant_id)
  AND policy_id = sqlc.arg(policy_id)
  AND revision = sqlc.arg(revision);

-- name: ListPolicyRevisionMetadata :many
SELECT
    policy_id, revision, schema_major, schema_minor, digest,
    evaluator_major, evaluator_minor, evaluator_digest, created_at
FROM idenqa.policy_revisions
WHERE tenant_id = sqlc.arg(tenant_id)
  AND policy_id = sqlc.arg(policy_id)
  AND (sqlc.arg(before_revision)::bigint = 0 OR revision < sqlc.arg(before_revision))
ORDER BY revision DESC
LIMIT sqlc.arg(page_limit);

-- name: LockPolicy :one
SELECT * FROM idenqa.policies
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(policy_id)
FOR UPDATE;

-- name: ActivatePolicy :one
UPDATE idenqa.policies
SET activation_version = sqlc.arg(new_version),
    active_revision = sqlc.arg(revision)::bigint,
    updated_at = sqlc.arg(activated_at)
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(policy_id)
  AND activation_version = sqlc.arg(expected_version)
  AND sqlc.arg(new_version) = sqlc.arg(expected_version) + 1
RETURNING *;

-- name: InsertPolicyActivation :exec
INSERT INTO idenqa.policy_activations (
    tenant_id, policy_id, activation_version, revision, previous_revision,
    actor_key_id, activated_at
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(policy_id), sqlc.arg(activation_version),
    sqlc.arg(revision), sqlc.narg(previous_revision), sqlc.arg(actor_key_id),
    sqlc.arg(activated_at)
);

-- name: FindActivePolicy :one
SELECT
    revisions.*,
    policies.activation_version,
    activations.previous_revision,
    activations.actor_key_id,
    activations.activated_at
FROM idenqa.policies AS policies
JOIN idenqa.policy_revisions AS revisions
  ON revisions.tenant_id = policies.tenant_id
 AND revisions.policy_id = policies.id
 AND revisions.revision = policies.active_revision
JOIN idenqa.policy_activations AS activations
  ON activations.tenant_id = policies.tenant_id
 AND activations.policy_id = policies.id
 AND activations.activation_version = policies.activation_version
WHERE policies.tenant_id = sqlc.arg(tenant_id)
  AND policies.id = sqlc.arg(policy_id);

-- name: ListPolicyActivationMetadata :many
SELECT
    activations.policy_id,
    activations.activation_version,
    activations.revision,
    activations.previous_revision,
    activations.actor_key_id,
    activations.activated_at,
    revisions.schema_major,
    revisions.schema_minor,
    revisions.digest,
    revisions.evaluator_major,
    revisions.evaluator_minor,
    revisions.evaluator_digest,
    revisions.created_at
FROM idenqa.policy_activations AS activations
JOIN idenqa.policy_revisions AS revisions
  ON revisions.tenant_id = activations.tenant_id
 AND revisions.policy_id = activations.policy_id
 AND revisions.revision = activations.revision
WHERE activations.tenant_id = sqlc.arg(tenant_id)
  AND activations.policy_id = sqlc.arg(policy_id)
  AND (
      sqlc.arg(before_activation_version)::bigint = 0 OR
      activations.activation_version < sqlc.arg(before_activation_version)
  )
ORDER BY activations.activation_version DESC
LIMIT sqlc.arg(page_limit);

-- name: FindPolicyAuthoritativeHeader :one
SELECT
    sessions.region,
    sessions.policy_id,
    authorities.id AS authority_id,
    responses.id AS acknowledgement_id,
    responses.action AS response_action,
    responses.recorded_at AS response_recorded_at
FROM idenqa.verification_sessions AS sessions
JOIN idenqa.processing_authorities AS authorities
  ON authorities.tenant_id = sessions.tenant_id
 AND authorities.id = sessions.authority_id
JOIN LATERAL (
    SELECT id, action, recorded_at
    FROM idenqa.subject_responses
    WHERE tenant_id = authorities.tenant_id
      AND authority_id = authorities.id
      AND verification_id = sessions.id
      AND recorded_at <= sqlc.arg(evaluated_at)
    ORDER BY recorded_at DESC, id DESC
    LIMIT 1
) AS responses ON true
WHERE sessions.tenant_id = sqlc.arg(tenant_id)
  AND sessions.id = sqlc.arg(verification_id)
  AND sessions.created_at <= sqlc.arg(evaluated_at)
  AND sessions.region IS NOT NULL
  AND sessions.policy_id IS NOT NULL
  AND authorities.state = 'active'
  AND authorities.valid_from <= sqlc.arg(evaluated_at)
  AND authorities.expires_at > sqlc.arg(evaluated_at);

-- name: ListPolicyAuthoritativeObservations :many
WITH completed_attempts AS (
    SELECT DISTINCT ON (attempts.check_id)
        attempts.*
    FROM idenqa.verification_attempts AS attempts
    WHERE attempts.tenant_id = sqlc.arg(tenant_id)
      AND attempts.verification_id = sqlc.arg(verification_id)
      AND attempts.state = 'completed'
      AND attempts.finished_at <= sqlc.arg(evaluated_at)
    ORDER BY attempts.check_id, attempts.attempt_number DESC
)
SELECT
    checks.id AS check_id,
    checks.name AS check_name,
    checks.outcome AS check_outcome,
    checks.version AS check_version,
    checks.updated_at AS check_updated_at,
    attempts.id AS attempt_id,
    attempts.attempt_number,
    attempts.runner_kind,
    attempts.runner_id,
    attempts.runner_version,
    attempts.package_digest,
    attempts.contract_major,
    attempts.contract_minor,
    attempts.request_digest,
    attempts.configuration_digest,
    attempts.result_digest,
    observations.id AS observation_id,
    observations.signal_name,
    observations.signal_outcome,
    observations.reason_codes,
    observations.recorded_at
FROM idenqa.verification_checks AS checks
JOIN completed_attempts AS attempts
  ON attempts.tenant_id = checks.tenant_id
 AND attempts.verification_id = checks.verification_id
 AND attempts.check_id = checks.id
JOIN idenqa.verification_observations AS observations
  ON observations.tenant_id = attempts.tenant_id
 AND observations.verification_id = attempts.verification_id
 AND observations.check_id = attempts.check_id
 AND observations.attempt_id = attempts.id
WHERE checks.tenant_id = sqlc.arg(tenant_id)
  AND checks.verification_id = sqlc.arg(verification_id)
  AND checks.state = 'completed'
  AND checks.updated_at <= sqlc.arg(evaluated_at)
  AND observations.recorded_at <= sqlc.arg(evaluated_at)
ORDER BY checks.id, observations.recorded_at, observations.id
-- One sentinel row above the in-memory bound makes overflow fail closed rather
-- than silently truncating authoritative provenance.
LIMIT 8193;
