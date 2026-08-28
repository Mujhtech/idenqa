ALTER TABLE idenqa.evidence_asset_audit
    DROP CONSTRAINT evidence_asset_audit_action;

ALTER TABLE idenqa.evidence_asset_audit
    ADD CONSTRAINT evidence_asset_audit_action
        CHECK (action IN ('create', 'quarantine', 'integrity_failed', 'rewrap'));

CREATE TABLE idenqa.evidence_key_rewrap_audit (
    tenant_id text NOT NULL,
    evidence_id text NOT NULL,
    aggregate_version bigint NOT NULL,
    previous_key_provider text NOT NULL,
    previous_key_reference text NOT NULL,
    previous_key_version text NOT NULL,
    previous_key_algorithm text NOT NULL,
    new_key_provider text NOT NULL,
    new_key_reference text NOT NULL,
    new_key_version text NOT NULL,
    new_key_algorithm text NOT NULL,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    tenant_actor_type text NOT NULL,
    tenant_actor_id text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, evidence_id, aggregate_version),
    CONSTRAINT evidence_key_rewrap_audit_evidence_fk
        FOREIGN KEY (tenant_id, evidence_id)
        REFERENCES idenqa.evidence_assets (tenant_id, id),
    CONSTRAINT evidence_key_rewrap_audit_version CHECK (aggregate_version > 1),
    CONSTRAINT evidence_key_rewrap_audit_key_identity CHECK (
        char_length(previous_key_provider) BETWEEN 1 AND 512 AND
        char_length(previous_key_reference) BETWEEN 1 AND 512 AND
        char_length(previous_key_version) BETWEEN 1 AND 512 AND
        char_length(previous_key_algorithm) BETWEEN 1 AND 512 AND
        char_length(new_key_provider) BETWEEN 1 AND 512 AND
        char_length(new_key_reference) BETWEEN 1 AND 512 AND
        char_length(new_key_version) BETWEEN 1 AND 512 AND
        char_length(new_key_algorithm) BETWEEN 1 AND 512 AND
        previous_key_provider !~ '[[:space:][:cntrl:]]' AND
        previous_key_reference !~ '[[:space:][:cntrl:]]' AND
        previous_key_version !~ '[[:space:][:cntrl:]]' AND
        previous_key_algorithm !~ '[[:space:][:cntrl:]]' AND
        new_key_provider !~ '[[:space:][:cntrl:]]' AND
        new_key_reference !~ '[[:space:][:cntrl:]]' AND
        new_key_version !~ '[[:space:][:cntrl:]]' AND
        new_key_algorithm !~ '[[:space:][:cntrl:]]' AND
        ROW(previous_key_provider, previous_key_reference, previous_key_version, previous_key_algorithm)
            IS DISTINCT FROM
        ROW(new_key_provider, new_key_reference, new_key_version, new_key_algorithm)
    ),
    CONSTRAINT evidence_key_rewrap_audit_attribution CHECK (
        principal_type ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        tenant_actor_type ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        char_length(principal_id) BETWEEN 1 AND 512 AND
        char_length(tenant_actor_id) BETWEEN 1 AND 512 AND
        char_length(reason) BETWEEN 1 AND 500 AND
        principal_id !~ '[[:space:][:cntrl:]]' AND
        tenant_actor_id !~ '[[:space:][:cntrl:]]' AND
        reason !~ '[[:cntrl:]]'
    )
);

CREATE TRIGGER evidence_key_rewrap_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_key_rewrap_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.evidence_key_rewrap_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_key_rewrap_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_key_rewrap_audit_tenant_scope ON idenqa.evidence_key_rewrap_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.evidence_key_rewrap_audit FROM PUBLIC;

CREATE OR REPLACE FUNCTION idenqa.protect_evidence_asset()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    is_quarantine boolean;
    is_rewrap boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence asset rows cannot be deleted';
    END IF;

    is_quarantine :=
        ROW(NEW.id, NEW.tenant_id, NEW.subject_id, NEW.verification_id,
            NEW.requirement_key, NEW.evidence_type, NEW.artefact, NEW.acquisition_method,
            NEW.assurances, NEW.registry_schema_version, NEW.registry_revision,
            NEW.registry_digest, NEW.region, NEW.retention_class, NEW.content_revision,
            NEW.object_key, NEW.object_version, NEW.ciphertext_size, NEW.ciphertext_checksum,
            NEW.envelope_format_version, NEW.content_algorithm, NEW.key_purpose,
            NEW.key_provider, NEW.key_reference, NEW.key_version, NEW.key_algorithm,
            NEW.wrapped_key, NEW.context_schema_version, NEW.context_digest,
            NEW.plaintext_digest, NEW.media_type, NEW.created_at)
        IS NOT DISTINCT FROM
        ROW(OLD.id, OLD.tenant_id, OLD.subject_id, OLD.verification_id,
            OLD.requirement_key, OLD.evidence_type, OLD.artefact, OLD.acquisition_method,
            OLD.assurances, OLD.registry_schema_version, OLD.registry_revision,
            OLD.registry_digest, OLD.region, OLD.retention_class, OLD.content_revision,
            OLD.object_key, OLD.object_version, OLD.ciphertext_size, OLD.ciphertext_checksum,
            OLD.envelope_format_version, OLD.content_algorithm, OLD.key_purpose,
            OLD.key_provider, OLD.key_reference, OLD.key_version, OLD.key_algorithm,
            OLD.wrapped_key, OLD.context_schema_version, OLD.context_digest,
            OLD.plaintext_digest, OLD.media_type, OLD.created_at)
        AND OLD.state = 'available' AND NEW.state = 'quarantined';

    is_rewrap :=
        ROW(NEW.id, NEW.tenant_id, NEW.subject_id, NEW.verification_id,
            NEW.requirement_key, NEW.evidence_type, NEW.artefact, NEW.acquisition_method,
            NEW.assurances, NEW.registry_schema_version, NEW.registry_revision,
            NEW.registry_digest, NEW.region, NEW.retention_class, NEW.content_revision,
            NEW.object_key, NEW.object_version, NEW.ciphertext_size, NEW.ciphertext_checksum,
            NEW.envelope_format_version, NEW.content_algorithm, NEW.key_purpose,
            NEW.context_schema_version, NEW.context_digest, NEW.plaintext_digest,
            NEW.media_type, NEW.integrity, NEW.state, NEW.created_at,
            NEW.quarantine_reason, NEW.quarantined_at)
        IS NOT DISTINCT FROM
        ROW(OLD.id, OLD.tenant_id, OLD.subject_id, OLD.verification_id,
            OLD.requirement_key, OLD.evidence_type, OLD.artefact, OLD.acquisition_method,
            OLD.assurances, OLD.registry_schema_version, OLD.registry_revision,
            OLD.registry_digest, OLD.region, OLD.retention_class, OLD.content_revision,
            OLD.object_key, OLD.object_version, OLD.ciphertext_size, OLD.ciphertext_checksum,
            OLD.envelope_format_version, OLD.content_algorithm, OLD.key_purpose,
            OLD.context_schema_version, OLD.context_digest, OLD.plaintext_digest,
            OLD.media_type, OLD.integrity, OLD.state, OLD.created_at,
            OLD.quarantine_reason, OLD.quarantined_at)
        AND ROW(NEW.key_provider, NEW.key_reference, NEW.key_version, NEW.key_algorithm)
            IS DISTINCT FROM
            ROW(OLD.key_provider, OLD.key_reference, OLD.key_version, OLD.key_algorithm);

    IF (NOT is_quarantine AND NOT is_rewrap)
       OR NEW.version <> OLD.version + 1 OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid evidence asset transition';
    END IF;
    RETURN NEW;
END;
$$;
