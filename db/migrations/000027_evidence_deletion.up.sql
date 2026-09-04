ALTER TABLE idenqa.evidence_assets DROP CONSTRAINT evidence_assets_lifecycle;
ALTER TABLE idenqa.evidence_assets ADD CONSTRAINT evidence_assets_lifecycle CHECK (
    version > 0 AND
    ((state = 'available' AND integrity = 'verified' AND quarantine_reason IS NULL AND quarantined_at IS NULL) OR
     (state = 'quarantined' AND integrity IN ('verified', 'failed') AND
      quarantine_reason ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND quarantined_at = updated_at) OR
     (state = 'deleted' AND integrity IN ('verified', 'failed')))
);

ALTER TABLE idenqa.evidence_asset_audit DROP CONSTRAINT evidence_asset_audit_action;
ALTER TABLE idenqa.evidence_asset_audit ADD CONSTRAINT evidence_asset_audit_action
    CHECK (action IN ('create', 'quarantine', 'integrity_failed', 'rewrap', 'delete'));

CREATE OR REPLACE FUNCTION idenqa.protect_evidence_asset()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    immutable_content boolean;
    is_quarantine boolean;
    is_rewrap boolean;
    is_delete boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence asset rows cannot be deleted';
    END IF;

    immutable_content :=
        ROW(NEW.id, NEW.tenant_id, NEW.subject_id, NEW.verification_id,
            NEW.requirement_key, NEW.evidence_type, NEW.artefact, NEW.acquisition_method,
            NEW.assurances, NEW.registry_schema_version, NEW.registry_revision,
            NEW.registry_digest, NEW.region, NEW.retention_class, NEW.content_revision,
            NEW.object_key, NEW.object_version, NEW.ciphertext_size, NEW.ciphertext_checksum,
            NEW.envelope_format_version, NEW.content_algorithm, NEW.key_purpose,
            NEW.context_schema_version, NEW.context_digest, NEW.plaintext_digest,
            NEW.media_type, NEW.created_at)
        IS NOT DISTINCT FROM
        ROW(OLD.id, OLD.tenant_id, OLD.subject_id, OLD.verification_id,
            OLD.requirement_key, OLD.evidence_type, OLD.artefact, OLD.acquisition_method,
            OLD.assurances, OLD.registry_schema_version, OLD.registry_revision,
            OLD.registry_digest, OLD.region, OLD.retention_class, OLD.content_revision,
            OLD.object_key, OLD.object_version, OLD.ciphertext_size, OLD.ciphertext_checksum,
            OLD.envelope_format_version, OLD.content_algorithm, OLD.key_purpose,
            OLD.context_schema_version, OLD.context_digest, OLD.plaintext_digest,
            OLD.media_type, OLD.created_at);

    is_quarantine := immutable_content AND
        ROW(NEW.key_provider, NEW.key_reference, NEW.key_version, NEW.key_algorithm, NEW.wrapped_key)
            IS NOT DISTINCT FROM
        ROW(OLD.key_provider, OLD.key_reference, OLD.key_version, OLD.key_algorithm, OLD.wrapped_key)
        AND OLD.state = 'available' AND NEW.state = 'quarantined';

    is_rewrap := immutable_content AND
        ROW(NEW.integrity, NEW.state, NEW.quarantine_reason, NEW.quarantined_at)
            IS NOT DISTINCT FROM
        ROW(OLD.integrity, OLD.state, OLD.quarantine_reason, OLD.quarantined_at)
        AND ROW(NEW.key_provider, NEW.key_reference, NEW.key_version, NEW.key_algorithm, NEW.wrapped_key)
            IS DISTINCT FROM
        ROW(OLD.key_provider, OLD.key_reference, OLD.key_version, OLD.key_algorithm, OLD.wrapped_key);

    is_delete := immutable_content AND
        ROW(NEW.key_provider, NEW.key_reference, NEW.key_version, NEW.key_algorithm, NEW.wrapped_key,
            NEW.integrity, NEW.quarantine_reason, NEW.quarantined_at)
            IS NOT DISTINCT FROM
        ROW(OLD.key_provider, OLD.key_reference, OLD.key_version, OLD.key_algorithm, OLD.wrapped_key,
            OLD.integrity, OLD.quarantine_reason, OLD.quarantined_at)
        AND OLD.state IN ('available', 'quarantined') AND NEW.state = 'deleted';

    IF (NOT is_quarantine AND NOT is_rewrap AND NOT is_delete)
       OR NEW.version <> OLD.version + 1 OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid evidence asset transition';
    END IF;
    RETURN NEW;
END;
$$;
