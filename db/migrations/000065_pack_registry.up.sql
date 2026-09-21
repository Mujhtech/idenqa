CREATE TABLE idenqa.pack_release_states (
    country text NOT NULL,
    revision bigint NOT NULL,
    state text NOT NULL,
    digest text NOT NULL,
    transition_version bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (country, revision),
    CONSTRAINT pack_release_states_country CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT pack_release_states_revision CHECK (revision > 0),
    CONSTRAINT pack_release_states_state
        CHECK (state IN ('draft', 'active', 'deprecated', 'retired')),
    CONSTRAINT pack_release_states_digest CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT pack_release_states_version CHECK (transition_version > 0)
);

CREATE INDEX pack_release_states_active
    ON idenqa.pack_release_states (country)
    WHERE state = 'active';

CREATE TABLE idenqa.pack_release_history (
    country text NOT NULL,
    revision bigint NOT NULL,
    transition_version bigint NOT NULL,
    operation text NOT NULL,
    previous_state text NOT NULL,
    state text NOT NULL,
    pack_digest text NOT NULL,
    reason text NOT NULL,
    actor text NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (country, revision, transition_version),
    CONSTRAINT pack_release_history_country CHECK (country ~ '^[A-Z]{2}$'),
    CONSTRAINT pack_release_history_revision CHECK (revision > 0),
    CONSTRAINT pack_release_history_operation
        CHECK (operation IN ('activate', 'deprecate', 'retire')),
    CONSTRAINT pack_release_history_previous_state
        CHECK (previous_state IN ('draft', 'active', 'deprecated', 'retired')),
    CONSTRAINT pack_release_history_state
        CHECK (state IN ('draft', 'active', 'deprecated', 'retired')),
    CONSTRAINT pack_release_history_digest CHECK (pack_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT pack_release_history_reason CHECK (reason ~ '^[a-z][a-z0-9._-]{0,99}$'),
    CONSTRAINT pack_release_history_actor CHECK (actor ~ '^[a-z][a-z0-9._-]{0,99}$'),
    CONSTRAINT pack_release_history_version CHECK (transition_version > 0)
);

CREATE TRIGGER pack_release_history_append_only
    BEFORE UPDATE OR DELETE ON idenqa.pack_release_history
    FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_catalog_record_change();

REVOKE ALL ON idenqa.pack_release_states FROM PUBLIC;
REVOKE ALL ON idenqa.pack_release_history FROM PUBLIC;
