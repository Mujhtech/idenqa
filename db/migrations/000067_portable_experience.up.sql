CREATE TABLE idenqa.experiences (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    state text NOT NULL,
    revision bigint NOT NULL,
    latest_version integer NOT NULL,
    approved_version integer NOT NULL DEFAULT 0,
    published_version integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT experiences_id CHECK (id ~ '^exp_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT experiences_state CHECK (state IN ('draft', 'approved', 'published', 'revoked')),
    CONSTRAINT experiences_revision CHECK (revision > 0),
    CONSTRAINT experiences_latest CHECK (latest_version > 0),
    CONSTRAINT experiences_approved CHECK (approved_version >= 0 AND approved_version <= latest_version),
    CONSTRAINT experiences_published CHECK (published_version >= 0 AND published_version <= latest_version),
    CONSTRAINT experiences_no_approval CHECK (state <> 'approved' OR approved_version = latest_version),
    CONSTRAINT experiences_no_publication CHECK (state <> 'published' OR published_version > 0),
    CONSTRAINT experiences_times CHECK (updated_at >= created_at)
);

CREATE TABLE idenqa.experience_revisions (
    tenant_id text NOT NULL,
    experience_id text NOT NULL,
    version integer NOT NULL,
    state text NOT NULL,
    document jsonb NOT NULL,
    digest text NOT NULL,
    key_id text NOT NULL,
    signature text NOT NULL,
    actor_id text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, experience_id, version),
    FOREIGN KEY (tenant_id, experience_id) REFERENCES idenqa.experiences (tenant_id, id),
    CONSTRAINT experience_revisions_version CHECK (version > 0),
    CONSTRAINT experience_revisions_state CHECK (state IN ('draft', 'approved', 'published', 'superseded', 'revoked')),
    CONSTRAINT experience_revisions_document CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT experience_revisions_digest CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT experience_revisions_signature CHECK (signature ~ '^[0-9a-f]{128}$'),
    CONSTRAINT experience_revisions_key CHECK (key_id ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT experience_revisions_actor CHECK (length(actor_id) BETWEEN 1 AND 200)
);

CREATE TABLE idenqa.experience_targeting (
    tenant_id text NOT NULL,
    experience_id text NOT NULL,
    version integer NOT NULL,
    rule_index integer NOT NULL,
    workflow text,
    countries jsonb NOT NULL,
    application_ids jsonb NOT NULL,
    origins jsonb NOT NULL,
    sdk_version_min text,
    sdk_version_max text,
    specificity integer NOT NULL,
    PRIMARY KEY (tenant_id, experience_id, version, rule_index),
    FOREIGN KEY (tenant_id, experience_id, version)
        REFERENCES idenqa.experience_revisions (tenant_id, experience_id, version),
    CONSTRAINT experience_targeting_index CHECK (rule_index >= 0),
    CONSTRAINT experience_targeting_countries CHECK (jsonb_typeof(countries) = 'array'),
    CONSTRAINT experience_targeting_applications CHECK (jsonb_typeof(application_ids) = 'array'),
    CONSTRAINT experience_targeting_origins CHECK (jsonb_typeof(origins) = 'array'),
    CONSTRAINT experience_targeting_specificity CHECK (specificity >= 0),
    CONSTRAINT experience_targeting_workflow CHECK (
        workflow IS NULL OR workflow ~ '^[a-z][a-z0-9._-]{0,127}$'
    ),
    CONSTRAINT experience_targeting_sdk CHECK (
        (sdk_version_min IS NULL AND sdk_version_max IS NULL) OR
        (sdk_version_min ~ '^[0-9]+\.[0-9]+\.[0-9]+$' AND sdk_version_max ~ '^[0-9]+\.[0-9]+\.[0-9]+$')
    )
);

CREATE TABLE idenqa.experience_events (
    tenant_id text NOT NULL,
    experience_id text NOT NULL,
    sequence bigint NOT NULL,
    operation text NOT NULL,
    from_state text,
    to_state text NOT NULL,
    version integer NOT NULL,
    target_version integer NOT NULL DEFAULT 0,
    actor_id text NOT NULL,
    reason text,
    digest text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, experience_id, sequence),
    FOREIGN KEY (tenant_id, experience_id) REFERENCES idenqa.experiences (tenant_id, id),
    CONSTRAINT experience_events_sequence CHECK (sequence > 0),
    CONSTRAINT experience_events_operation CHECK (operation IN ('create', 'update', 'approve', 'publish', 'revoke', 'rollback', 'import')),
    CONSTRAINT experience_events_from CHECK (from_state IS NULL OR from_state IN ('draft', 'approved', 'published', 'revoked')),
    CONSTRAINT experience_events_to CHECK (to_state IN ('draft', 'approved', 'published', 'revoked')),
    CONSTRAINT experience_events_version CHECK (version > 0),
    CONSTRAINT experience_events_target CHECK (target_version >= 0),
    CONSTRAINT experience_events_digest CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT experience_events_actor CHECK (length(actor_id) BETWEEN 1 AND 200)
);

CREATE INDEX experience_events_page
    ON idenqa.experience_events (tenant_id, experience_id, sequence);

CREATE TABLE idenqa.experience_session_pins (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    experience_id text NOT NULL,
    version integer NOT NULL,
    locale text NOT NULL,
    tenant_copy_version text NOT NULL,
    mandatory_copy_version text NOT NULL,
    source text NOT NULL,
    digest text NOT NULL,
    key_id text NOT NULL,
    pinned_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, verification_id),
    FOREIGN KEY (tenant_id, verification_id) REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT experience_session_pins_experience CHECK (experience_id ~ '^exp_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT experience_session_pins_version CHECK (version > 0),
    CONSTRAINT experience_session_pins_locale CHECK (locale ~ '^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$'),
    CONSTRAINT experience_session_pins_tenant_copy CHECK (tenant_copy_version ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    CONSTRAINT experience_session_pins_mandatory CHECK (mandatory_copy_version ~ '^mc-[0-9]{4}-[0-9]{2}-[0-9]{2}(-[0-9]{1,2})?$'),
    CONSTRAINT experience_session_pins_source CHECK (source IN ('pinned', 'published', 'default')),
    CONSTRAINT experience_session_pins_digest CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT experience_session_pins_key CHECK (key_id ~ '^[a-z][a-z0-9._-]{0,63}$')
);

CREATE INDEX experience_session_pins_experience
    ON idenqa.experience_session_pins (tenant_id, experience_id, version);

CREATE FUNCTION idenqa.protect_experience_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    transition_allowed boolean;
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.experience_id <> OLD.experience_id OR
       NEW.version <> OLD.version OR NEW.document IS DISTINCT FROM OLD.document OR
       NEW.digest <> OLD.digest OR NEW.key_id <> OLD.key_id OR NEW.signature <> OLD.signature OR
       NEW.actor_id <> OLD.actor_id OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'experience revision identity and content are immutable';
    END IF;
    IF NEW.state <> OLD.state THEN
        transition_allowed :=
            (OLD.state = 'draft' AND NEW.state = 'approved') OR
            (OLD.state = 'approved' AND NEW.state = 'published') OR
            (OLD.state = 'published' AND NEW.state IN ('superseded', 'revoked')) OR
            (OLD.state = 'superseded' AND NEW.state = 'published');
        IF NOT transition_allowed THEN
            RAISE EXCEPTION 'experience revision state transition is not allowed';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER experience_revisions_immutable
BEFORE UPDATE ON idenqa.experience_revisions
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_experience_revision();

CREATE FUNCTION idenqa.reject_experience_revision_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'experience revisions are append-only';
END;
$$;

CREATE TRIGGER experience_revisions_append_only
BEFORE DELETE ON idenqa.experience_revisions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_experience_revision_delete();

CREATE FUNCTION idenqa.reject_experience_targeting_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'experience targeting projections are append-only';
END;
$$;

CREATE TRIGGER experience_targeting_append_only
BEFORE UPDATE OR DELETE ON idenqa.experience_targeting
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_experience_targeting_change();

CREATE FUNCTION idenqa.reject_experience_event_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'experience events are append-only';
END;
$$;

CREATE TRIGGER experience_events_append_only
BEFORE UPDATE OR DELETE ON idenqa.experience_events
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_experience_event_change();

CREATE FUNCTION idenqa.reject_experience_pin_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'experience session pins are immutable';
END;
$$;

CREATE TRIGGER experience_session_pins_immutable
BEFORE UPDATE OR DELETE ON idenqa.experience_session_pins
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_experience_pin_change();

CREATE FUNCTION idenqa.protect_experience_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.id <> OLD.id OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'experience identity and creation time are immutable';
    END IF;
    IF NEW.latest_version < OLD.latest_version OR NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'experience revisions must advance monotonically';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER experiences_protected
BEFORE UPDATE ON idenqa.experiences
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_experience_record();

ALTER TABLE idenqa.experiences ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experiences FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_targeting ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_targeting FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_events FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_session_pins ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.experience_session_pins FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.experiences
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.experience_revisions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.experience_targeting
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.experience_events
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.experience_session_pins
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.experiences FROM PUBLIC;
REVOKE ALL ON idenqa.experience_revisions FROM PUBLIC;
REVOKE ALL ON idenqa.experience_targeting FROM PUBLIC;
REVOKE ALL ON idenqa.experience_events FROM PUBLIC;
REVOKE ALL ON idenqa.experience_session_pins FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_experience_revision() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_experience_revision_delete() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_experience_targeting_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_experience_event_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_experience_pin_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_experience_record() FROM PUBLIC;
