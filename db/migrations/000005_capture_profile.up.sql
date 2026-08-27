CREATE TABLE idenqa.capture_profiles (
    id text NOT NULL,
    tenant_id text NOT NULL,
    name text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    latest_revision integer NOT NULL,
    draft_revision integer,
    published_revision integer,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deactivated_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT capture_profiles_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT capture_profiles_id_format
        CHECK (id ~ '^prf_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT capture_profiles_tenant_id_format
        CHECK (tenant_id ~ '^ten_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT capture_profiles_name CHECK (
        char_length(name) BETWEEN 1 AND 100 AND
        name = btrim(name) AND
        name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT capture_profiles_state
        CHECK (state IN ('draft', 'active', 'deactivated')),
    CONSTRAINT capture_profiles_version CHECK (version > 0),
    CONSTRAINT capture_profiles_latest_revision CHECK (latest_revision > 0),
    CONSTRAINT capture_profiles_revision_order CHECK (
        (draft_revision IS NULL OR draft_revision = latest_revision) AND
        (published_revision IS NULL OR published_revision <= latest_revision)
    ),
    CONSTRAINT capture_profiles_lifecycle CHECK (
        (state = 'draft' AND draft_revision IS NOT NULL AND published_revision IS NULL AND deactivated_at IS NULL) OR
        (state = 'active' AND published_revision IS NOT NULL AND deactivated_at IS NULL) OR
        (state = 'deactivated' AND draft_revision IS NULL AND published_revision IS NOT NULL AND deactivated_at IS NOT NULL)
    ),
    CONSTRAINT capture_profiles_times CHECK (
        updated_at >= created_at AND
        (deactivated_at IS NULL OR deactivated_at = updated_at)
    )
);

CREATE TABLE idenqa.capture_profile_revisions (
    tenant_id text NOT NULL,
    profile_id text NOT NULL,
    revision integer NOT NULL,
    state text NOT NULL,
    schema_version integer NOT NULL,
    registry_schema_version integer NOT NULL,
    registry_revision integer NOT NULL,
    registry_digest text NOT NULL,
    document jsonb NOT NULL,
    digest text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    published_at timestamptz,
    ended_at timestamptz,
    PRIMARY KEY (profile_id, revision),
    UNIQUE (tenant_id, profile_id, revision),
    CONSTRAINT capture_profile_revisions_profile_fk
        FOREIGN KEY (tenant_id, profile_id)
        REFERENCES idenqa.capture_profiles (tenant_id, id),
    CONSTRAINT capture_profile_revisions_revision CHECK (revision > 0),
    CONSTRAINT capture_profile_revisions_state
        CHECK (state IN ('draft', 'published', 'superseded', 'withdrawn')),
    CONSTRAINT capture_profile_revisions_schema CHECK (
        schema_version > 0 AND registry_schema_version > 0 AND registry_revision > 0
    ),
    CONSTRAINT capture_profile_revisions_registry_digest
        CHECK (registry_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT capture_profile_revisions_digest
        CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT capture_profile_revisions_document
        CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT capture_profile_revisions_lifecycle CHECK (
        (state = 'draft' AND published_at IS NULL AND ended_at IS NULL) OR
        (state = 'published' AND published_at IS NOT NULL AND ended_at IS NULL) OR
        (state = 'superseded' AND published_at IS NOT NULL AND ended_at IS NOT NULL) OR
        (state = 'withdrawn' AND published_at IS NULL AND ended_at IS NOT NULL)
    ),
    CONSTRAINT capture_profile_revisions_times CHECK (
        updated_at >= created_at AND
        (published_at IS NULL OR published_at BETWEEN created_at AND updated_at) AND
        (ended_at IS NULL OR ended_at = updated_at)
    )
);

ALTER TABLE idenqa.capture_profiles
    ADD CONSTRAINT capture_profiles_draft_revision_fk
        FOREIGN KEY (tenant_id, id, draft_revision)
        REFERENCES idenqa.capture_profile_revisions (tenant_id, profile_id, revision)
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT capture_profiles_published_revision_fk
        FOREIGN KEY (tenant_id, id, published_revision)
        REFERENCES idenqa.capture_profile_revisions (tenant_id, profile_id, revision)
        DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX capture_profile_revisions_one_draft
    ON idenqa.capture_profile_revisions (tenant_id, profile_id)
    WHERE state = 'draft';

CREATE INDEX capture_profiles_tenant_created
    ON idenqa.capture_profiles (tenant_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION idenqa.protect_capture_profile_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'capture profile revisions cannot be deleted';
    END IF;
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.profile_id <> OLD.profile_id OR
       NEW.revision <> OLD.revision OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'capture profile revision identity is immutable';
    END IF;
    IF OLD.state = 'published' AND NEW.state = 'superseded' AND
       NEW.schema_version = OLD.schema_version AND
       NEW.registry_schema_version = OLD.registry_schema_version AND
       NEW.registry_revision = OLD.registry_revision AND
       NEW.registry_digest = OLD.registry_digest AND
       NEW.document = OLD.document AND NEW.digest = OLD.digest AND
       NEW.published_at = OLD.published_at AND NEW.ended_at = NEW.updated_at THEN
        RETURN NEW;
    END IF;
    IF OLD.state <> 'draft' THEN
        RAISE EXCEPTION 'published, superseded, and withdrawn capture profile revisions are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capture_profile_revision_immutable
BEFORE UPDATE OR DELETE ON idenqa.capture_profile_revisions
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_capture_profile_revision();

CREATE TABLE idenqa.capture_profile_audit (
    tenant_id text NOT NULL,
    profile_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    revision integer,
    action text NOT NULL,
    actor_key_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, profile_id, aggregate_version),
    CONSTRAINT capture_profile_audit_profile_fk
        FOREIGN KEY (tenant_id, profile_id)
        REFERENCES idenqa.capture_profiles (tenant_id, id),
    CONSTRAINT capture_profile_audit_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id)
        REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT capture_profile_audit_version CHECK (aggregate_version > 0),
    CONSTRAINT capture_profile_audit_revision CHECK (revision IS NULL OR revision > 0),
    CONSTRAINT capture_profile_audit_action CHECK (
        action IN ('create_draft', 'update_draft', 'publish', 'begin_supersession', 'deactivate')
    )
);

CREATE INDEX capture_profile_audit_profile_time
    ON idenqa.capture_profile_audit (tenant_id, profile_id, occurred_at DESC);

CREATE TABLE idenqa.idempotency_records (
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    operation text NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    state text NOT NULL,
    result_status integer,
    result jsonb,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, principal_id, operation, idempotency_key),
    CONSTRAINT idempotency_records_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT idempotency_records_principal_fk
        FOREIGN KEY (tenant_id, principal_id)
        REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT idempotency_records_operation CHECK (
        char_length(operation) BETWEEN 1 AND 200 AND operation = btrim(operation) AND
        operation ~ '^[a-z][a-z0-9_.:-]*$'
    ),
    CONSTRAINT idempotency_records_key CHECK (
        octet_length(idempotency_key) BETWEEN 1 AND 255 AND
        idempotency_key = btrim(idempotency_key) AND
        idempotency_key !~ '[[:cntrl:]]'
    ),
    CONSTRAINT idempotency_records_fingerprint
        CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT idempotency_records_state CHECK (state IN ('pending', 'completed')),
    CONSTRAINT idempotency_records_result_status CHECK (
        result_status IS NULL OR result_status BETWEEN 200 AND 599
    ),
    CONSTRAINT idempotency_records_result CHECK (
        result IS NULL OR jsonb_typeof(result) = 'object'
    ),
    CONSTRAINT idempotency_records_lifecycle CHECK (
        (state = 'pending' AND result_status IS NULL AND result IS NULL AND completed_at IS NULL) OR
        (state = 'completed' AND result_status IS NOT NULL AND result IS NOT NULL AND completed_at IS NOT NULL)
    ),
    CONSTRAINT idempotency_records_times CHECK (
        expires_at > created_at AND
        (completed_at IS NULL OR completed_at BETWEEN created_at AND expires_at)
    )
);

CREATE INDEX idempotency_records_expiry
    ON idenqa.idempotency_records (expires_at);

ALTER TABLE idenqa.capture_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_profiles FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_profile_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_profile_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_profile_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_profile_audit FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.idempotency_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.idempotency_records FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.capture_profiles
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.capture_profile_revisions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.capture_profile_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.idempotency_records
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.capture_profiles FROM PUBLIC;
REVOKE ALL ON idenqa.capture_profile_revisions FROM PUBLIC;
REVOKE ALL ON idenqa.capture_profile_audit FROM PUBLIC;
REVOKE ALL ON idenqa.idempotency_records FROM PUBLIC;
