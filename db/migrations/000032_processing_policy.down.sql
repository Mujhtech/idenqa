CREATE OR REPLACE FUNCTION idenqa.list_ready_policy_authorships(
    observed_at timestamptz,
    batch_size integer
)
RETURNS TABLE (
    tenant_id text,
    verification_id text,
    decision_id text,
    ready_at timestamptz
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT sessions.tenant_id, sessions.id, sessions.decision_id,
           GREATEST(sessions.capture_completed_at, MAX(checks.updated_at)) AS ready_at
    FROM idenqa.verification_sessions AS sessions
    JOIN idenqa.verification_checks AS checks
      ON checks.tenant_id = sessions.tenant_id
     AND checks.verification_id = sessions.id
    WHERE observed_at IS NOT NULL
      AND batch_size BETWEEN 1 AND 100
      AND sessions.policy_id IS NOT NULL
      AND sessions.decision_id IS NOT NULL
      AND sessions.capture_completed_at IS NOT NULL
      AND sessions.capture_completed_at <= observed_at
      AND NOT EXISTS (
          SELECT 1
          FROM idenqa.verification_checks AS pending
          WHERE pending.tenant_id = sessions.tenant_id
            AND pending.verification_id = sessions.id
            AND pending.state NOT IN (
                'completed', 'skipped_by_policy', 'timed_out', 'cancelled', 'failed'
            )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM idenqa.verification_decisions AS decisions
          WHERE decisions.tenant_id = sessions.tenant_id
            AND decisions.id = sessions.decision_id
      )
    GROUP BY sessions.tenant_id, sessions.id, sessions.decision_id,
             sessions.capture_completed_at
    ORDER BY ready_at, sessions.tenant_id, sessions.id
    LIMIT LEAST(batch_size, 100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_ready_policy_authorships(timestamptz, integer)
    FROM PUBLIC;
