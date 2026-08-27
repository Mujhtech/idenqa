CREATE TABLE idenqa.verification_sessions (
    id text NOT NULL,
    tenant_id text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    source_profile_id text NOT NULL,
    source_profile_revision integer NOT NULL,
    source_profile_digest text NOT NULL,
    requirements jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT verification_sessions_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT verification_sessions_profile_revision_fk
        FOREIGN KEY (tenant_id, source_profile_id, source_profile_revision)
        REFERENCES idenqa.capture_profile_revisions (tenant_id, profile_id, revision),
    CONSTRAINT verification_sessions_id_format
        CHECK (id ~ '^ver_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_sessions_state CHECK (state IN ('collecting')),
    CONSTRAINT verification_sessions_version CHECK (version > 0),
    CONSTRAINT verification_sessions_profile_revision CHECK (source_profile_revision > 0),
    CONSTRAINT verification_sessions_profile_digest
        CHECK (source_profile_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT verification_sessions_requirements
        CHECK (jsonb_typeof(requirements) = 'object'),
    CONSTRAINT verification_sessions_times CHECK (
        updated_at >= created_at AND expires_at > created_at
    )
);

CREATE INDEX verification_sessions_tenant_created
    ON idenqa.verification_sessions (tenant_id, created_at DESC, id DESC);
CREATE INDEX verification_sessions_expiry
    ON idenqa.verification_sessions (expires_at)
    WHERE state = 'collecting';

CREATE OR REPLACE FUNCTION idenqa.protect_verification_session_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.source_profile_id <> OLD.source_profile_id OR
       NEW.source_profile_revision <> OLD.source_profile_revision OR
       NEW.source_profile_digest <> OLD.source_profile_digest OR
       NEW.requirements <> OLD.requirements OR NEW.created_at <> OLD.created_at OR
       NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'verification session identity, snapshot, and lifetime are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER verification_session_snapshot_immutable
BEFORE UPDATE ON idenqa.verification_sessions
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_verification_session_snapshot();

CREATE TABLE idenqa.capture_tokens (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    key_version integer NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT capture_tokens_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT capture_tokens_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT capture_tokens_id_format
        CHECK (id ~ '^ctk_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT capture_tokens_key_version
        CHECK (key_version BETWEEN 1 AND 65535),
    CONSTRAINT capture_tokens_times CHECK (
        expires_at > issued_at AND
        (revoked_at IS NULL OR revoked_at >= issued_at)
    )
);

CREATE INDEX capture_tokens_verification
    ON idenqa.capture_tokens (tenant_id, verification_id);
CREATE INDEX capture_tokens_expiry
    ON idenqa.capture_tokens (expires_at)
    WHERE revoked_at IS NULL;

CREATE OR REPLACE FUNCTION idenqa.protect_capture_token_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.verification_id <> OLD.verification_id OR
       NEW.key_version <> OLD.key_version OR NEW.issued_at <> OLD.issued_at OR
       NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'capture token identity and signed claims are immutable';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'capture token revocation is irreversible';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capture_token_record_immutable
BEFORE UPDATE ON idenqa.capture_tokens
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_capture_token_record();

CREATE TABLE idenqa.verification_session_audit (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    action text NOT NULL,
    actor_key_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, verification_id, aggregate_version),
    CONSTRAINT verification_session_audit_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT verification_session_audit_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id)
        REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT verification_session_audit_version CHECK (aggregate_version > 0),
    CONSTRAINT verification_session_audit_action CHECK (action IN ('create'))
);

CREATE INDEX verification_session_audit_time
    ON idenqa.verification_session_audit (tenant_id, verification_id, occurred_at DESC);

CREATE TABLE idenqa.outbox_events (
    id text NOT NULL,
    tenant_id text NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    event_type text NOT NULL,
    schema_version integer NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    published_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, aggregate_type, aggregate_id, aggregate_version, event_type),
    CONSTRAINT outbox_events_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT outbox_events_id_format
        CHECK (id ~ '^evt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT outbox_events_aggregate_type CHECK (
        char_length(aggregate_type) BETWEEN 1 AND 100 AND
        aggregate_type = btrim(aggregate_type) AND
        aggregate_type ~ '^[a-z][a-z0-9_.:-]*$'
    ),
    CONSTRAINT outbox_events_aggregate_id CHECK (
        char_length(aggregate_id) BETWEEN 1 AND 100 AND
        aggregate_id = btrim(aggregate_id) AND
        aggregate_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT outbox_events_aggregate_version CHECK (aggregate_version > 0),
    CONSTRAINT outbox_events_event_type CHECK (
        char_length(event_type) BETWEEN 1 AND 200 AND
        event_type = btrim(event_type) AND
        event_type ~ '^[a-z][a-z0-9_.:-]*$'
    ),
    CONSTRAINT outbox_events_schema_version CHECK (schema_version > 0),
    CONSTRAINT outbox_events_payload CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_times CHECK (
        created_at >= occurred_at AND
        (published_at IS NULL OR published_at >= created_at)
    )
);

CREATE INDEX outbox_events_pending
    ON idenqa.outbox_events (created_at, id)
    WHERE published_at IS NULL;

ALTER TABLE idenqa.verification_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_tokens FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_session_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_session_audit FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.outbox_events FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.verification_sessions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.capture_tokens
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_session_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.outbox_events
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.verification_sessions FROM PUBLIC;
REVOKE ALL ON idenqa.capture_tokens FROM PUBLIC;
REVOKE ALL ON idenqa.verification_session_audit FROM PUBLIC;
REVOKE ALL ON idenqa.outbox_events FROM PUBLIC;
