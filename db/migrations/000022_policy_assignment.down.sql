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
       NEW.created_at <> OLD.created_at OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'verification session identity, snapshot, placement, and lifetime are immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP FUNCTION idenqa.list_ready_policy_authorships(timestamptz, integer);

ALTER TABLE idenqa.verification_sessions
    DROP CONSTRAINT verification_sessions_capture_completed_time,
    DROP CONSTRAINT verification_sessions_decision_format,
    DROP CONSTRAINT verification_sessions_policy_format,
    DROP CONSTRAINT verification_sessions_policy_fk,
    DROP COLUMN capture_completed_at,
    DROP COLUMN decision_id,
    DROP COLUMN policy_id;
