ALTER TABLE idenqa.verification_decisions
    ADD CONSTRAINT verification_decisions_lifecycle_identity UNIQUE (tenant_id, verification_id, id);

ALTER TABLE idenqa.verification_sessions
    DROP CONSTRAINT verification_sessions_state,
    ADD CONSTRAINT verification_sessions_state CHECK (state IN (
        'created', 'collecting', 'awaiting_input', 'processing', 'awaiting_external',
        'manual_review', 'completed', 'cancelled', 'expired', 'failed'
    )),
    ADD COLUMN completed_decision_id text,
    ADD CONSTRAINT verification_sessions_completed_decision_fk
        FOREIGN KEY (tenant_id, id, completed_decision_id)
        REFERENCES idenqa.verification_decisions (tenant_id, verification_id, id),
    ADD CONSTRAINT verification_sessions_completion CHECK (
        (state = 'completed') = (completed_decision_id IS NOT NULL)
    );

CREATE TABLE idenqa.verification_transitions (
    tenant_id text NOT NULL,
    event_id text NOT NULL,
    verification_id text NOT NULL,
    from_state text NOT NULL,
    to_state text NOT NULL,
    expected_version bigint NOT NULL,
    resulting_version bigint NOT NULL,
    decision_id text,
    actor_id text NOT NULL,
    command_digest text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, event_id),
    UNIQUE (tenant_id, verification_id, resulting_version),
    FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    FOREIGN KEY (tenant_id, verification_id, decision_id)
        REFERENCES idenqa.verification_decisions (tenant_id, verification_id, id),
    CHECK (event_id ~ '^evt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CHECK (actor_id ~ '^(key|ctk|tsk)_[0-9A-HJKMNP-TV-Z]{26}$'),
    CHECK (command_digest ~ '^[0-9a-f]{64}$'),
    CHECK (expected_version > 0 AND expected_version < 9223372036854775807
        AND resulting_version = expected_version + 1),
    CHECK (from_state IN ('created', 'collecting', 'awaiting_input', 'processing',
        'awaiting_external', 'manual_review')),
    CHECK (to_state IN ('collecting', 'awaiting_input', 'processing', 'awaiting_external',
        'manual_review', 'completed', 'cancelled', 'expired', 'failed')),
    CHECK (from_state <> to_state),
    CHECK ((to_state = 'completed') = (decision_id IS NOT NULL))
);

ALTER TABLE idenqa.verification_transitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_transitions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON idenqa.verification_transitions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
REVOKE ALL ON idenqa.verification_transitions FROM PUBLIC;

CREATE FUNCTION idenqa.protect_verification_transition()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'verification transition receipts are immutable';
END;
$$;
CREATE TRIGGER verification_transitions_immutable
    BEFORE UPDATE OR DELETE ON idenqa.verification_transitions
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_verification_transition();

-- Defence in depth for the state primitive. Application services still own
-- transition causes, processing authority, decision validation and task intent.
CREATE FUNCTION idenqa.protect_verification_lifecycle()
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
CREATE TRIGGER verification_lifecycle_protected
    BEFORE UPDATE ON idenqa.verification_sessions
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_verification_lifecycle();
