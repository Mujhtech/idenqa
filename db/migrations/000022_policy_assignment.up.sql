ALTER TABLE idenqa.verification_sessions
    ADD COLUMN policy_id text,
    ADD COLUMN decision_id text,
    ADD COLUMN capture_completed_at timestamptz;

ALTER TABLE idenqa.verification_sessions
    ADD CONSTRAINT verification_sessions_policy_fk
        FOREIGN KEY (tenant_id, policy_id)
        REFERENCES idenqa.policies (tenant_id, id),
    ADD CONSTRAINT verification_sessions_policy_format
        CHECK (policy_id IS NULL OR policy_id ~ '^pol_[0-9A-HJKMNP-TV-Z]{26}$'),
    ADD CONSTRAINT verification_sessions_decision_format
        CHECK (decision_id IS NULL OR decision_id ~ '^dec_[0-9A-HJKMNP-TV-Z]{26}$'),
    ADD CONSTRAINT verification_sessions_capture_completed_time
        CHECK (capture_completed_at IS NULL OR capture_completed_at >= created_at);

CREATE UNIQUE INDEX verification_sessions_decision_assignment
    ON idenqa.verification_sessions (tenant_id, decision_id)
    WHERE decision_id IS NOT NULL;

CREATE OR REPLACE FUNCTION idenqa.protect_verification_session_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.source_profile_id <> OLD.source_profile_id OR
       NEW.source_profile_revision <> OLD.source_profile_revision OR
       NEW.source_profile_digest <> OLD.source_profile_digest OR
       NEW.requirements <> OLD.requirements OR
       NEW.region IS DISTINCT FROM OLD.region OR
       NEW.policy_id IS DISTINCT FROM OLD.policy_id OR
       NEW.decision_id IS DISTINCT FROM OLD.decision_id OR
       NEW.created_at <> OLD.created_at OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'verification session identity, snapshot, placement, policy, and lifetime are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION idenqa.list_ready_policy_authorships(
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
