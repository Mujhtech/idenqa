CREATE OR REPLACE FUNCTION idenqa.protect_verification_lifecycle()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state IN ('completed', 'cancelled', 'expired', 'failed') AND
       (NEW.state IS DISTINCT FROM OLD.state OR NEW.version IS DISTINCT FROM OLD.version OR
        NEW.updated_at IS DISTINCT FROM OLD.updated_at OR
        NEW.capture_completed_at IS DISTINCT FROM OLD.capture_completed_at OR
        NEW.completed_decision_id IS DISTINCT FROM OLD.completed_decision_id) THEN
        RAISE EXCEPTION 'terminal verification lifecycle is immutable';
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state AND
       (NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at) THEN
        RAISE EXCEPTION 'verification lifecycle requires the next version and monotonic time';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE idenqa.verification_sessions
    DROP CONSTRAINT IF EXISTS verification_sessions_failure_bounds,
    DROP COLUMN IF EXISTS failure_code,
    DROP COLUMN IF EXISTS failure_class;
