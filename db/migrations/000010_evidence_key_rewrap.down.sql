DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM idenqa.evidence_key_rewrap_audit) THEN
        RAISE EXCEPTION 'cannot roll back evidence key rewrap schema after a rewrap has been recorded';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS evidence_key_rewrap_audit_immutable ON idenqa.evidence_key_rewrap_audit;
DROP TABLE IF EXISTS idenqa.evidence_key_rewrap_audit;

ALTER TABLE idenqa.evidence_asset_audit
    DROP CONSTRAINT evidence_asset_audit_action;

ALTER TABLE idenqa.evidence_asset_audit
    ADD CONSTRAINT evidence_asset_audit_action
        CHECK (action IN ('create', 'quarantine', 'integrity_failed'));

CREATE OR REPLACE FUNCTION idenqa.protect_evidence_asset()
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
