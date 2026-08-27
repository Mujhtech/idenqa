CREATE TABLE idenqa.notice_versions (
    id text NOT NULL,
    tenant_id text NOT NULL,
    semantic_key text NOT NULL,
    locale text NOT NULL,
    controller_display_name text NOT NULL,
    recipient_display_name text NOT NULL,
    title text NOT NULL,
    summary text NOT NULL,
    purpose_copy text NOT NULL,
    consequence_copy text NOT NULL,
    effective_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    created_by text NOT NULL,
    digest text NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT notice_versions_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT notice_versions_actor_fk
        FOREIGN KEY (tenant_id, created_by) REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT notice_versions_id_format
        CHECK (id ~ '^ntc_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT notice_versions_key
        CHECK (char_length(semantic_key) BETWEEN 3 AND 200 AND
               semantic_key ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$'),
    CONSTRAINT notice_versions_locale
        CHECK (char_length(locale) BETWEEN 2 AND 35 AND locale ~ '^[A-Za-z0-9-]+$'),
    CONSTRAINT notice_versions_digest
        CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT notice_versions_copy CHECK (
        char_length(controller_display_name) BETWEEN 1 AND 200 AND
        char_length(recipient_display_name) BETWEEN 1 AND 200 AND
        char_length(title) BETWEEN 1 AND 8000 AND
        char_length(summary) BETWEEN 1 AND 8000 AND
        char_length(purpose_copy) BETWEEN 1 AND 8000 AND
        char_length(consequence_copy) BETWEEN 1 AND 8000
    )
);

ALTER TABLE idenqa.idempotency_records
    DROP CONSTRAINT idempotency_records_principal_fk,
    ADD COLUMN principal_type text GENERATED ALWAYS AS (
        CASE
            WHEN principal_id ~ '^key_' THEN 'api_key'
            WHEN principal_id ~ '^ctk_' THEN 'capture_token'
            ELSE 'invalid'
        END
    ) STORED,
    ADD CONSTRAINT idempotency_records_principal_type CHECK (
        (principal_type = 'api_key' AND principal_id ~ '^key_[0-9A-HJKMNP-TV-Z]{26}$') OR
        (principal_type = 'capture_token' AND principal_id ~ '^ctk_[0-9A-HJKMNP-TV-Z]{26}$')
    );

CREATE INDEX notice_versions_key_locale
    ON idenqa.notice_versions (tenant_id, semantic_key, locale, created_at DESC);

CREATE TABLE idenqa.notice_version_audit (
    tenant_id text NOT NULL,
    notice_id text NOT NULL,
    action text NOT NULL,
    actor_key_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, notice_id),
    CONSTRAINT notice_version_audit_notice_fk
        FOREIGN KEY (tenant_id, notice_id) REFERENCES idenqa.notice_versions (tenant_id, id),
    CONSTRAINT notice_version_audit_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id) REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT notice_version_audit_action CHECK (action = 'create')
);

CREATE TABLE idenqa.subjects (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, verification_id),
    CONSTRAINT subjects_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT subjects_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT subjects_id_format
        CHECK (id ~ '^sub_[0-9A-HJKMNP-TV-Z]{26}$')
);

CREATE TABLE idenqa.processing_authorities (
    id text NOT NULL,
    tenant_id text NOT NULL,
    subject_id text NOT NULL,
    verification_id text NOT NULL,
    notice_id text NOT NULL,
    category text NOT NULL,
    purpose text NOT NULL,
    jurisdiction text NOT NULL,
    policy_pack text NOT NULL,
    consent_required boolean NOT NULL,
    requirement_purposes text[] NOT NULL,
    evidence_types text[] NOT NULL,
    recipient_reference text NOT NULL,
    recipient_display_name text NOT NULL,
    regions text[] NOT NULL,
    retention_reference text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    valid_from timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    created_by text NOT NULL,
    restricted_at timestamptz,
    withdrawn_at timestamptz,
    superseded_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, verification_id),
    CONSTRAINT processing_authorities_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT processing_authorities_subject_fk
        FOREIGN KEY (tenant_id, subject_id) REFERENCES idenqa.subjects (tenant_id, id),
    CONSTRAINT processing_authorities_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT processing_authorities_notice_fk
        FOREIGN KEY (tenant_id, notice_id) REFERENCES idenqa.notice_versions (tenant_id, id),
    CONSTRAINT processing_authorities_actor_fk
        FOREIGN KEY (tenant_id, created_by) REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT processing_authorities_id_format
        CHECK (id ~ '^aut_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT processing_authorities_state
        CHECK (state IN ('active', 'restricted', 'withdrawn', 'superseded')),
    CONSTRAINT processing_authorities_version CHECK (version > 0),
    CONSTRAINT processing_authorities_declared_codes CHECK (
        category ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        purpose ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        jurisdiction ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        policy_pack ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        recipient_reference ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        retention_reference ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        purpose = ANY(requirement_purposes) AND
        char_length(recipient_display_name) BETWEEN 1 AND 200
    ),
    CONSTRAINT processing_authorities_times CHECK (
        expires_at > valid_from AND updated_at >= created_at
    ),
    CONSTRAINT processing_authorities_scopes CHECK (
        cardinality(requirement_purposes) BETWEEN 1 AND 64 AND
        cardinality(evidence_types) BETWEEN 1 AND 64 AND
        cardinality(regions) BETWEEN 1 AND 64
    ),
    CONSTRAINT processing_authorities_transition CHECK (
        (state = 'active' AND restricted_at IS NULL AND withdrawn_at IS NULL AND superseded_at IS NULL) OR
        (state = 'restricted' AND restricted_at = updated_at AND withdrawn_at IS NULL AND superseded_at IS NULL) OR
        (state = 'withdrawn' AND restricted_at IS NULL AND withdrawn_at = updated_at AND superseded_at IS NULL) OR
        (state = 'superseded' AND restricted_at IS NULL AND withdrawn_at IS NULL AND superseded_at = updated_at)
    )
);

CREATE INDEX processing_authorities_expiry
    ON idenqa.processing_authorities (tenant_id, expires_at)
    WHERE state = 'active';

ALTER TABLE idenqa.verification_sessions
    ADD COLUMN subject_id text,
    ADD COLUMN authority_id text,
    ADD COLUMN notice_id text,
    ADD CONSTRAINT verification_sessions_subject_fk
        FOREIGN KEY (tenant_id, subject_id) REFERENCES idenqa.subjects (tenant_id, id),
    ADD CONSTRAINT verification_sessions_authority_fk
        FOREIGN KEY (tenant_id, authority_id) REFERENCES idenqa.processing_authorities (tenant_id, id),
    ADD CONSTRAINT verification_sessions_notice_fk
        FOREIGN KEY (tenant_id, notice_id) REFERENCES idenqa.notice_versions (tenant_id, id),
    ADD CONSTRAINT verification_sessions_authority_binding CHECK (
        (subject_id IS NULL AND authority_id IS NULL AND notice_id IS NULL) OR
        (subject_id IS NOT NULL AND authority_id IS NOT NULL AND notice_id IS NOT NULL)
    );

CREATE TABLE idenqa.subject_responses (
    id text NOT NULL,
    tenant_id text NOT NULL,
    authority_id text NOT NULL,
    notice_id text NOT NULL,
    subject_id text NOT NULL,
    verification_id text NOT NULL,
    capture_token_id text NOT NULL,
    action text NOT NULL,
    locale text NOT NULL,
    rendered_experience_version text,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT subject_responses_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT subject_responses_authority_fk
        FOREIGN KEY (tenant_id, authority_id) REFERENCES idenqa.processing_authorities (tenant_id, id),
    CONSTRAINT subject_responses_notice_fk
        FOREIGN KEY (tenant_id, notice_id) REFERENCES idenqa.notice_versions (tenant_id, id),
    CONSTRAINT subject_responses_subject_fk
        FOREIGN KEY (tenant_id, subject_id) REFERENCES idenqa.subjects (tenant_id, id),
    CONSTRAINT subject_responses_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT subject_responses_capture_token_fk
        FOREIGN KEY (tenant_id, capture_token_id) REFERENCES idenqa.capture_tokens (tenant_id, id),
    CONSTRAINT subject_responses_id_format
        CHECK (id ~ '^ack_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT subject_responses_action
        CHECK (action IN ('acknowledge', 'consent', 'refuse')),
    CONSTRAINT subject_responses_locale
        CHECK (char_length(locale) BETWEEN 2 AND 35 AND locale ~ '^[A-Za-z0-9-]+$')
);

CREATE INDEX subject_responses_latest
    ON idenqa.subject_responses (tenant_id, authority_id, recorded_at DESC, id DESC);

CREATE TABLE idenqa.authority_audit (
    tenant_id text NOT NULL,
    authority_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    action text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, authority_id, aggregate_version),
    CONSTRAINT authority_audit_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT authority_audit_version CHECK (aggregate_version > 0),
    CONSTRAINT authority_audit_action
        CHECK (action IN ('declare', 'restrict', 'withdraw', 'supersede')),
    CONSTRAINT authority_audit_actor_type CHECK (actor_type IN ('api_key'))
);

CREATE OR REPLACE FUNCTION idenqa.protect_authority_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.authority_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM idenqa.processing_authorities authority
        JOIN idenqa.subjects subject
          ON subject.tenant_id = authority.tenant_id
         AND subject.id = authority.subject_id
         AND subject.verification_id = authority.verification_id
        WHERE authority.tenant_id = NEW.tenant_id
          AND authority.id = NEW.authority_id
          AND authority.verification_id = NEW.id
          AND authority.subject_id = NEW.subject_id
          AND authority.notice_id = NEW.notice_id
    ) THEN
        RAISE EXCEPTION 'verification authority binding is inconsistent';
    END IF;
    IF OLD.authority_id IS NOT NULL AND (
        NEW.authority_id IS DISTINCT FROM OLD.authority_id OR
        NEW.subject_id IS DISTINCT FROM OLD.subject_id OR
        NEW.notice_id IS DISTINCT FROM OLD.notice_id
    ) THEN
        RAISE EXCEPTION 'verification authority binding is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER verification_authority_binding_immutable
BEFORE UPDATE ON idenqa.verification_sessions
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_authority_binding();

CREATE OR REPLACE FUNCTION idenqa.protect_processing_authority()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.subject_id <> OLD.subject_id OR NEW.verification_id <> OLD.verification_id OR
       NEW.notice_id <> OLD.notice_id OR NEW.category <> OLD.category OR
       NEW.purpose <> OLD.purpose OR NEW.jurisdiction <> OLD.jurisdiction OR
       NEW.policy_pack <> OLD.policy_pack OR NEW.consent_required <> OLD.consent_required OR
       NEW.requirement_purposes <> OLD.requirement_purposes OR
       NEW.evidence_types <> OLD.evidence_types OR
       NEW.recipient_reference <> OLD.recipient_reference OR
       NEW.recipient_display_name <> OLD.recipient_display_name OR
       NEW.regions <> OLD.regions OR NEW.retention_reference <> OLD.retention_reference OR
       NEW.valid_from <> OLD.valid_from OR NEW.expires_at <> OLD.expires_at OR
       NEW.created_at <> OLD.created_at OR NEW.created_by <> OLD.created_by THEN
        RAISE EXCEPTION 'processing authority declaration is immutable';
    END IF;
    IF OLD.state <> 'active' THEN
        RAISE EXCEPTION 'processing authority terminal transition is irreversible';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'processing authority lifecycle version is invalid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER processing_authority_declaration_immutable
BEFORE UPDATE ON idenqa.processing_authorities
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_processing_authority();

CREATE OR REPLACE FUNCTION idenqa.reject_immutable_authority_row()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'authority history is append-only';
END;
$$;

CREATE TRIGGER notice_versions_immutable
BEFORE UPDATE OR DELETE ON idenqa.notice_versions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER notice_version_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.notice_version_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER subjects_immutable
BEFORE UPDATE OR DELETE ON idenqa.subjects
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER subject_responses_immutable
BEFORE UPDATE OR DELETE ON idenqa.subject_responses
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER authority_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.authority_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.notice_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.notice_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.notice_version_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.notice_version_audit FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.subjects ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.subjects FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.processing_authorities ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.processing_authorities FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.subject_responses ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.subject_responses FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.authority_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.authority_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.notice_versions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.notice_version_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.subjects
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.processing_authorities
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.subject_responses
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.authority_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.notice_versions FROM PUBLIC;
REVOKE ALL ON idenqa.notice_version_audit FROM PUBLIC;
REVOKE ALL ON idenqa.subjects FROM PUBLIC;
REVOKE ALL ON idenqa.processing_authorities FROM PUBLIC;
REVOKE ALL ON idenqa.subject_responses FROM PUBLIC;
REVOKE ALL ON idenqa.authority_audit FROM PUBLIC;
