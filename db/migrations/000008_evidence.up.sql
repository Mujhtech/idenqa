ALTER TABLE idenqa.subjects
    ADD CONSTRAINT subjects_tenant_subject_verification_unique
        UNIQUE (tenant_id, id, verification_id);

CREATE TABLE idenqa.evidence_assets (
    id text NOT NULL,
    tenant_id text NOT NULL,
    subject_id text NOT NULL,
    verification_id text NOT NULL,
    requirement_key text NOT NULL,
    evidence_type text NOT NULL,
    artefact text NOT NULL,
    acquisition_method text NOT NULL,
    assurances text[] NOT NULL,
    registry_schema_version integer NOT NULL,
    registry_revision integer NOT NULL,
    registry_digest text NOT NULL,
    region text NOT NULL,
    retention_class text NOT NULL,
    content_revision integer NOT NULL,
    object_key text NOT NULL,
    object_version text NOT NULL,
    ciphertext_size bigint NOT NULL,
    ciphertext_checksum text NOT NULL,
    envelope_format_version integer NOT NULL,
    content_algorithm text NOT NULL,
    key_purpose text NOT NULL,
    key_provider text NOT NULL,
    key_reference text NOT NULL,
    key_version text NOT NULL,
    key_algorithm text NOT NULL,
    wrapped_key bytea NOT NULL,
    context_schema_version integer NOT NULL,
    context_digest text NOT NULL,
    plaintext_digest text NOT NULL,
    media_type text NOT NULL,
    integrity text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    quarantine_reason text,
    quarantined_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (object_key, object_version),
    CONSTRAINT evidence_assets_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT evidence_assets_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT evidence_assets_subject_verification_fk
        FOREIGN KEY (tenant_id, subject_id, verification_id)
        REFERENCES idenqa.subjects (tenant_id, id, verification_id),
    CONSTRAINT evidence_assets_id_format CHECK (id ~ '^evd_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT evidence_assets_classifications CHECK (
        requirement_key ~ '^[a-z][a-z0-9_]{0,63}$' AND
        region ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        retention_class ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        char_length(region) <= 200 AND
        char_length(retention_class) <= 200
    ),
    CONSTRAINT evidence_assets_registry CHECK (
        registry_schema_version > 0 AND registry_revision > 0 AND
        registry_digest ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT evidence_assets_content_versions CHECK (
        content_revision > 0 AND envelope_format_version > 0 AND context_schema_version > 0
    ),
    CONSTRAINT evidence_assets_object CHECK (
        char_length(object_key) BETWEEN 1 AND 1024 AND
        char_length(object_version) BETWEEN 1 AND 200 AND ciphertext_size >= 0 AND
        ciphertext_checksum ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT evidence_assets_envelope CHECK (
        char_length(content_algorithm) BETWEEN 1 AND 200 AND
        char_length(key_purpose) BETWEEN 1 AND 200 AND
        char_length(key_provider) BETWEEN 1 AND 512 AND
        char_length(key_reference) BETWEEN 1 AND 512 AND
        char_length(key_version) BETWEEN 1 AND 512 AND
        char_length(key_algorithm) BETWEEN 1 AND 512 AND
        octet_length(wrapped_key) BETWEEN 1 AND 65536 AND
        context_digest ~ '^sha256:[0-9a-f]{64}$' AND
        plaintext_digest ~ '^sha256:[0-9a-f]{64}$' AND
        char_length(media_type) BETWEEN 1 AND 200
    ),
    CONSTRAINT evidence_assets_lifecycle CHECK (
        version > 0 AND
        ((state = 'available' AND integrity = 'verified' AND quarantine_reason IS NULL AND quarantined_at IS NULL) OR
         (state = 'quarantined' AND integrity IN ('verified', 'failed') AND
          quarantine_reason ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
          quarantined_at = updated_at))
    ),
    CONSTRAINT evidence_assets_times CHECK (updated_at >= created_at)
);

CREATE INDEX evidence_assets_verification
    ON idenqa.evidence_assets (tenant_id, verification_id, created_at, id);

CREATE TABLE idenqa.evidence_asset_audit (
    tenant_id text NOT NULL,
    evidence_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    action text NOT NULL,
    reason text,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, evidence_id, aggregate_version),
    CONSTRAINT evidence_asset_audit_evidence_fk
        FOREIGN KEY (tenant_id, evidence_id) REFERENCES idenqa.evidence_assets (tenant_id, id),
    CONSTRAINT evidence_asset_audit_action CHECK (action IN ('create', 'quarantine', 'integrity_failed')),
    CONSTRAINT evidence_asset_audit_version CHECK (aggregate_version > 0),
    CONSTRAINT evidence_asset_audit_reason CHECK (
        (action = 'create' AND reason IS NULL) OR
        (action <> 'create' AND reason ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$')
    )
);

CREATE FUNCTION idenqa.protect_evidence_asset()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence asset rows cannot be deleted';
    END IF;
    IF ROW(NEW.id, NEW.tenant_id, NEW.subject_id, NEW.verification_id,
           NEW.requirement_key, NEW.evidence_type, NEW.artefact, NEW.acquisition_method,
           NEW.assurances, NEW.registry_schema_version, NEW.registry_revision,
           NEW.registry_digest, NEW.region, NEW.retention_class, NEW.content_revision,
           NEW.object_key, NEW.object_version, NEW.ciphertext_size, NEW.ciphertext_checksum,
           NEW.envelope_format_version, NEW.content_algorithm, NEW.key_purpose,
           NEW.key_provider, NEW.key_reference, NEW.key_version, NEW.key_algorithm,
           NEW.wrapped_key, NEW.context_schema_version, NEW.context_digest,
           NEW.plaintext_digest, NEW.media_type, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.tenant_id, OLD.subject_id, OLD.verification_id,
           OLD.requirement_key, OLD.evidence_type, OLD.artefact, OLD.acquisition_method,
           OLD.assurances, OLD.registry_schema_version, OLD.registry_revision,
           OLD.registry_digest, OLD.region, OLD.retention_class, OLD.content_revision,
           OLD.object_key, OLD.object_version, OLD.ciphertext_size, OLD.ciphertext_checksum,
           OLD.envelope_format_version, OLD.content_algorithm, OLD.key_purpose,
           OLD.key_provider, OLD.key_reference, OLD.key_version, OLD.key_algorithm,
           OLD.wrapped_key, OLD.context_schema_version, OLD.context_digest,
           OLD.plaintext_digest, OLD.media_type, OLD.created_at)
       OR OLD.state <> 'available' OR NEW.state <> 'quarantined'
       OR NEW.version <> OLD.version + 1 OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid evidence asset transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_assets_protected
BEFORE UPDATE OR DELETE ON idenqa.evidence_assets
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_evidence_asset();

CREATE TRIGGER evidence_asset_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_asset_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.evidence_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_assets FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_assets_tenant_scope ON idenqa.evidence_assets
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_asset_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_asset_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_asset_audit_tenant_scope ON idenqa.evidence_asset_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.evidence_assets FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_asset_audit FROM PUBLIC;
