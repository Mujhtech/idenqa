ALTER TABLE idenqa.capture_tokens
    ADD CONSTRAINT capture_tokens_upload_binding_unique
        UNIQUE (tenant_id, id, verification_id);

ALTER TABLE idenqa.verification_sessions
    ADD CONSTRAINT verification_sessions_upload_snapshot_unique
        UNIQUE (
            tenant_id, id, source_profile_id,
            source_profile_revision, source_profile_digest
        );

ALTER TABLE idenqa.capture_profile_revisions
    ADD CONSTRAINT capture_profile_revisions_upload_snapshot_unique
        UNIQUE (
            tenant_id, profile_id, revision, digest,
            registry_schema_version, registry_revision, registry_digest
        );

CREATE TABLE idenqa.evidence_upload_intents (
    id text NOT NULL,
    tenant_id text NOT NULL,
    capture_token_id text NOT NULL,
    subject_id text NOT NULL,
    verification_id text NOT NULL,
    evidence_id text NOT NULL,
    authority_id text NOT NULL,
    response_id text NOT NULL,
    profile_id text NOT NULL,
    profile_revision integer NOT NULL,
    profile_digest text NOT NULL,
    registry_schema_version integer NOT NULL,
    registry_revision integer NOT NULL,
    registry_digest text NOT NULL,
    requirement_key text NOT NULL,
    purpose text NOT NULL,
    evidence_type text NOT NULL,
    artefact text NOT NULL,
    acquisition_method text NOT NULL,
    assurances text[] NOT NULL,
    encryption_purpose text NOT NULL,
    allowed_media_types text[] NOT NULL,
    maximum_bytes bigint NOT NULL,
    expected_bytes bigint NOT NULL,
    expected_digest text NOT NULL,
    media_type text NOT NULL,
    region text NOT NULL,
    retention_class text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    attempt integer NOT NULL,
    attempt_timeout_milliseconds bigint NOT NULL,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    rejection_reason text,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, evidence_id),
    CONSTRAINT evidence_upload_intents_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT evidence_upload_intents_capture_token_fk
        FOREIGN KEY (tenant_id, capture_token_id, verification_id)
        REFERENCES idenqa.capture_tokens (tenant_id, id, verification_id),
    CONSTRAINT evidence_upload_intents_subject_fk
        FOREIGN KEY (tenant_id, subject_id, verification_id)
        REFERENCES idenqa.subjects (tenant_id, id, verification_id),
    CONSTRAINT evidence_upload_intents_verification_snapshot_fk
        FOREIGN KEY (
            tenant_id, verification_id, profile_id,
            profile_revision, profile_digest
        ) REFERENCES idenqa.verification_sessions (
            tenant_id, id, source_profile_id,
            source_profile_revision, source_profile_digest
        ),
    CONSTRAINT evidence_upload_intents_authority_fk
        FOREIGN KEY (tenant_id, authority_id, subject_id, verification_id)
        REFERENCES idenqa.processing_authorities (tenant_id, id, subject_id, verification_id),
    CONSTRAINT evidence_upload_intents_response_fk
        FOREIGN KEY (tenant_id, response_id, authority_id, subject_id, verification_id)
        REFERENCES idenqa.subject_responses (tenant_id, id, authority_id, subject_id, verification_id),
    CONSTRAINT evidence_upload_intents_profile_snapshot_fk
        FOREIGN KEY (
            tenant_id, profile_id, profile_revision, profile_digest,
            registry_schema_version, registry_revision, registry_digest
        ) REFERENCES idenqa.capture_profile_revisions (
            tenant_id, profile_id, revision, digest,
            registry_schema_version, registry_revision, registry_digest
        ),
    CONSTRAINT evidence_upload_intents_id_formats CHECK (
        id ~ '^upl_[0-9A-HJKMNP-TV-Z]{26}$' AND
        evidence_id ~ '^evd_[0-9A-HJKMNP-TV-Z]{26}$'
    ),
    CONSTRAINT evidence_upload_intents_snapshot CHECK (
        profile_revision > 0 AND
        profile_digest ~ '^sha256:[0-9a-f]{64}$' AND
        registry_schema_version > 0 AND registry_revision > 0 AND
        registry_digest ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT evidence_upload_intents_requirement CHECK (
        requirement_key ~ '^[a-z][a-z0-9_]{0,63}$' AND
        purpose ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        evidence_type ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        artefact ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        acquisition_method ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        encryption_purpose = 'idenqa.evidence.content' AND
        cardinality(assurances) BETWEEN 0 AND 16 AND
        array_position(assurances, NULL) IS NULL
    ),
    CONSTRAINT evidence_upload_intents_media CHECK (
        allowed_media_types IN (
            ARRAY['image/jpeg']::text[],
            ARRAY['image/png']::text[],
            ARRAY['image/jpeg', 'image/png']::text[]
        ) AND
        media_type = ANY(allowed_media_types) AND
        expected_digest ~ '^sha256:[0-9a-f]{64}$' AND
        maximum_bytes BETWEEN 1 AND 67108864 AND
        expected_bytes BETWEEN 1 AND maximum_bytes
    ),
    CONSTRAINT evidence_upload_intents_placement CHECK (
        region ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        retention_class ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        char_length(region) <= 200 AND char_length(retention_class) <= 200
    ),
    CONSTRAINT evidence_upload_intents_versions CHECK (
        version > 0 AND attempt BETWEEN 0 AND 2147483647 AND
        attempt_timeout_milliseconds BETWEEN 60000 AND 900000
    ),
    CONSTRAINT evidence_upload_intents_times CHECK (
        updated_at >= created_at AND
        expires_at > created_at AND expires_at <= created_at + interval '60 minutes' AND
        (lease_expires_at IS NULL OR
            (lease_expires_at > updated_at AND lease_expires_at <= expires_at))
    ),
    CONSTRAINT evidence_upload_intents_lifecycle CHECK (
        (state = 'issued' AND lease_expires_at IS NULL AND accepted_at IS NULL AND
            rejection_reason IS NULL AND updated_at < expires_at) OR
        (state = 'uploading' AND attempt > 0 AND lease_expires_at IS NOT NULL AND
            accepted_at IS NULL AND rejection_reason IS NULL) OR
        (state = 'accepted' AND attempt > 0 AND lease_expires_at IS NULL AND
            accepted_at = updated_at AND rejection_reason IS NULL AND updated_at < expires_at) OR
        (state = 'rejected' AND attempt > 0 AND lease_expires_at IS NULL AND
            accepted_at IS NULL AND
            rejection_reason ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
            updated_at < expires_at) OR
        (state = 'expired' AND lease_expires_at IS NULL AND accepted_at IS NULL AND
            rejection_reason IS NULL AND updated_at >= expires_at)
    )
);

CREATE INDEX evidence_upload_intents_verification
    ON idenqa.evidence_upload_intents (tenant_id, verification_id, created_at, id);
CREATE INDEX evidence_upload_intents_expiry
    ON idenqa.evidence_upload_intents (tenant_id, expires_at, id)
    WHERE state IN ('issued', 'uploading');
CREATE INDEX evidence_upload_intents_lease
    ON idenqa.evidence_upload_intents (tenant_id, lease_expires_at, id)
    WHERE state = 'uploading';

CREATE TABLE idenqa.evidence_upload_intent_audit (
    tenant_id text NOT NULL,
    upload_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    attempt integer NOT NULL,
    action text NOT NULL,
    principal_id text NOT NULL,
    reason text,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, upload_id, aggregate_version),
    CONSTRAINT evidence_upload_intent_audit_upload_fk
        FOREIGN KEY (tenant_id, upload_id)
        REFERENCES idenqa.evidence_upload_intents (tenant_id, id),
    CONSTRAINT evidence_upload_intent_audit_principal_fk
        FOREIGN KEY (tenant_id, principal_id)
        REFERENCES idenqa.capture_tokens (tenant_id, id),
    CONSTRAINT evidence_upload_intent_audit_action CHECK (
        action IN ('create', 'claim', 'retry', 'accept', 'reject', 'expire')
    ),
    CONSTRAINT evidence_upload_intent_audit_values CHECK (
        aggregate_version > 0 AND attempt BETWEEN 0 AND 2147483647 AND
        principal_id ~ '^ctk_[0-9A-HJKMNP-TV-Z]{26}$' AND
        ((action IN ('reject', 'expire') AND
            reason ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$') OR
         (action NOT IN ('reject', 'expire') AND reason IS NULL))
    )
);

CREATE FUNCTION idenqa.protect_evidence_upload_intent()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    transition_valid boolean;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state <> 'issued' OR NEW.version <> 1 OR NEW.attempt <> 0 OR
           NEW.updated_at <> NEW.created_at THEN
            RAISE EXCEPTION 'invalid initial evidence upload intent state';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence upload intents cannot be deleted';
    END IF;
    IF ROW(NEW.id, NEW.tenant_id, NEW.capture_token_id, NEW.subject_id,
           NEW.verification_id, NEW.evidence_id, NEW.authority_id, NEW.response_id,
           NEW.profile_id, NEW.profile_revision, NEW.profile_digest,
           NEW.registry_schema_version, NEW.registry_revision, NEW.registry_digest,
           NEW.requirement_key, NEW.purpose, NEW.evidence_type, NEW.artefact,
           NEW.acquisition_method, NEW.assurances, NEW.encryption_purpose,
           NEW.allowed_media_types, NEW.maximum_bytes, NEW.expected_bytes,
           NEW.expected_digest, NEW.media_type, NEW.region, NEW.retention_class,
           NEW.attempt_timeout_milliseconds, NEW.created_at, NEW.expires_at)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.tenant_id, OLD.capture_token_id, OLD.subject_id,
           OLD.verification_id, OLD.evidence_id, OLD.authority_id, OLD.response_id,
           OLD.profile_id, OLD.profile_revision, OLD.profile_digest,
           OLD.registry_schema_version, OLD.registry_revision, OLD.registry_digest,
           OLD.requirement_key, OLD.purpose, OLD.evidence_type, OLD.artefact,
           OLD.acquisition_method, OLD.assurances, OLD.encryption_purpose,
           OLD.allowed_media_types, OLD.maximum_bytes, OLD.expected_bytes,
           OLD.expected_digest, OLD.media_type, OLD.region, OLD.retention_class,
           OLD.attempt_timeout_milliseconds, OLD.created_at, OLD.expires_at) OR
       NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid evidence upload intent transition';
    END IF;

    transition_valid :=
        (NEW.state = 'uploading' AND NEW.attempt = OLD.attempt + 1 AND
            NEW.lease_expires_at IS NOT NULL AND NEW.accepted_at IS NULL AND
            NEW.rejection_reason IS NULL AND
            (OLD.state = 'issued' OR
             (OLD.state = 'uploading' AND OLD.lease_expires_at <= NEW.updated_at))) OR
        (OLD.state = 'uploading' AND NEW.state = 'issued' AND
            NEW.attempt = OLD.attempt AND NEW.lease_expires_at IS NULL AND
            NEW.accepted_at IS NULL AND NEW.rejection_reason IS NULL) OR
        (OLD.state = 'uploading' AND NEW.state = 'accepted' AND
            NEW.attempt = OLD.attempt AND NEW.lease_expires_at IS NULL AND
            NEW.accepted_at = NEW.updated_at AND NEW.rejection_reason IS NULL AND
            NEW.updated_at < OLD.lease_expires_at) OR
        (OLD.state = 'uploading' AND NEW.state = 'rejected' AND
            NEW.attempt = OLD.attempt AND NEW.lease_expires_at IS NULL AND
            NEW.accepted_at IS NULL AND NEW.rejection_reason IS NOT NULL AND
            NEW.updated_at < OLD.lease_expires_at) OR
        (OLD.state IN ('issued', 'uploading') AND NEW.state = 'expired' AND
            NEW.attempt = OLD.attempt AND NEW.lease_expires_at IS NULL AND
            NEW.accepted_at IS NULL AND NEW.rejection_reason IS NULL AND
            NEW.updated_at >= OLD.expires_at);

    IF NOT transition_valid THEN
        RAISE EXCEPTION 'invalid evidence upload intent transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_upload_intents_protected
BEFORE INSERT OR UPDATE OR DELETE ON idenqa.evidence_upload_intents
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_evidence_upload_intent();

CREATE TRIGGER evidence_upload_intent_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_upload_intent_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.evidence_upload_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_upload_intents FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_upload_intents_tenant_scope ON idenqa.evidence_upload_intents
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_upload_intent_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_upload_intent_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_upload_intent_audit_tenant_scope ON idenqa.evidence_upload_intent_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.evidence_upload_intents FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_upload_intent_audit FROM PUBLIC;
