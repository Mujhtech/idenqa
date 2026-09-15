-- Rolling back must never erase transition evidence or resurrect a workflow.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM idenqa.verification_transitions) OR
       EXISTS (SELECT 1 FROM idenqa.verification_sessions WHERE state <> 'collecting') THEN
        RAISE EXCEPTION 'lifecycle rollback requires no transition history and only collecting sessions';
    END IF;
END;
$$;

DROP TRIGGER verification_lifecycle_protected ON idenqa.verification_sessions;
DROP FUNCTION idenqa.protect_verification_lifecycle();
DROP TABLE idenqa.verification_transitions;
DROP FUNCTION idenqa.protect_verification_transition();
ALTER TABLE idenqa.verification_sessions
    DROP CONSTRAINT verification_sessions_completion,
    DROP CONSTRAINT verification_sessions_completed_decision_fk,
    DROP COLUMN completed_decision_id,
    DROP CONSTRAINT verification_sessions_state,
    ADD CONSTRAINT verification_sessions_state CHECK (state IN ('collecting'));
ALTER TABLE idenqa.verification_decisions
    DROP CONSTRAINT verification_decisions_lifecycle_identity;
