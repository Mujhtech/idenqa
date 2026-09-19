ALTER TABLE idenqa.verification_sessions
    ADD COLUMN failure_class text,
    ADD COLUMN failure_code text;

UPDATE idenqa.verification_sessions
SET failure_class = 'legacy',
    failure_code = 'unspecified'
WHERE state = 'failed';

ALTER TABLE idenqa.verification_sessions
    ADD CONSTRAINT verification_sessions_failure_bounds CHECK (
        (state = 'failed' AND failure_class IS NOT NULL AND failure_code IS NOT NULL
            AND failure_class ~ '^[a-z][a-z0-9_]{0,31}$'
            AND failure_code ~ '^[a-z][a-z0-9_]{0,63}$')
        OR
        (state <> 'failed' AND failure_class IS NULL AND failure_code IS NULL)
    );

CREATE OR REPLACE FUNCTION idenqa.protect_verification_lifecycle()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state IN ('completed', 'cancelled', 'expired', 'failed') AND
       (NEW.state IS DISTINCT FROM OLD.state OR NEW.version IS DISTINCT FROM OLD.version OR
        NEW.updated_at IS DISTINCT FROM OLD.updated_at OR
        NEW.capture_completed_at IS DISTINCT FROM OLD.capture_completed_at OR
        NEW.completed_decision_id IS DISTINCT FROM OLD.completed_decision_id OR
        NEW.failure_class IS DISTINCT FROM OLD.failure_class OR
        NEW.failure_code IS DISTINCT FROM OLD.failure_code) THEN
        RAISE EXCEPTION 'terminal verification lifecycle is immutable';
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state AND
       (NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at) THEN
        RAISE EXCEPTION 'verification lifecycle requires the next version and monotonic time';
    END IF;
    RETURN NEW;
END;
$$;
