CREATE TABLE idenqa.evidence_object_reconciliations (
    tenant_id text NOT NULL,
    upload_id text NOT NULL,
    evidence_id text NOT NULL,
    upload_attempt integer NOT NULL,
    object_key text NOT NULL,
    object_version text NOT NULL,
    object_size bigint NOT NULL,
    object_checksum text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    claim integer NOT NULL,
    available_at timestamptz NOT NULL,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id, upload_id, upload_attempt),
    UNIQUE (tenant_id, object_key, object_version),
    CONSTRAINT evidence_object_reconciliations_upload_fk
        FOREIGN KEY (tenant_id, upload_id)
        REFERENCES idenqa.evidence_upload_intents (tenant_id, id),
    CONSTRAINT evidence_object_reconciliations_identity CHECK (
        evidence_id ~ '^evd_[0-9A-HJKMNP-TV-Z]{26}$' AND
        upload_attempt BETWEEN 1 AND 2147483647
    ),
    CONSTRAINT evidence_object_reconciliations_object CHECK (
        char_length(object_key) BETWEEN 1 AND 1024 AND
        char_length(object_version) BETWEEN 1 AND 200 AND
        object_size >= 0 AND
        object_checksum ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT evidence_object_reconciliations_versions CHECK (
        version > 0 AND claim BETWEEN 0 AND 2147483647
    ),
    CONSTRAINT evidence_object_reconciliations_times CHECK (
        available_at >= created_at AND updated_at >= created_at AND
        (lease_expires_at IS NULL OR lease_expires_at > updated_at) AND
        (resolved_at IS NULL OR resolved_at = updated_at)
    ),
    CONSTRAINT evidence_object_reconciliations_lifecycle CHECK (
        (state = 'pending' AND lease_expires_at IS NULL AND resolved_at IS NULL) OR
        (state = 'claimed' AND claim > 0 AND lease_expires_at IS NOT NULL AND
            resolved_at IS NULL) OR
        (state IN ('retained', 'deleted') AND lease_expires_at IS NULL AND
            resolved_at IS NOT NULL)
    )
);

CREATE INDEX evidence_object_reconciliations_ready
    ON idenqa.evidence_object_reconciliations (tenant_id, available_at, upload_id, upload_attempt)
    WHERE state = 'pending';
CREATE INDEX evidence_object_reconciliations_expired_claim
    ON idenqa.evidence_object_reconciliations (tenant_id, lease_expires_at, upload_id, upload_attempt)
    WHERE state = 'claimed';

CREATE TABLE idenqa.evidence_object_reconciliation_audit (
    tenant_id text NOT NULL,
    upload_id text NOT NULL,
    upload_attempt integer NOT NULL,
    aggregate_version bigint NOT NULL,
    claim integer NOT NULL,
    action text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, upload_id, upload_attempt, aggregate_version),
    CONSTRAINT evidence_object_reconciliation_audit_obligation_fk
        FOREIGN KEY (tenant_id, upload_id, upload_attempt)
        REFERENCES idenqa.evidence_object_reconciliations (
            tenant_id, upload_id, upload_attempt
        ),
    CONSTRAINT evidence_object_reconciliation_audit_values CHECK (
        aggregate_version > 0 AND claim BETWEEN 0 AND 2147483647 AND
        action IN ('create', 'claim', 'retry', 'retain', 'delete')
    )
);

CREATE FUNCTION idenqa.protect_evidence_object_reconciliation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    transition_valid boolean;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state <> 'pending' OR NEW.version <> 1 OR NEW.claim <> 0 OR
           NEW.updated_at <> NEW.created_at OR NOT EXISTS (
               SELECT 1
               FROM idenqa.evidence_upload_intents AS upload
               WHERE upload.tenant_id = NEW.tenant_id
                 AND upload.id = NEW.upload_id
                 AND upload.evidence_id = NEW.evidence_id
                 AND upload.attempt >= NEW.upload_attempt
                 AND (
                     (upload.attempt = NEW.upload_attempt AND
                         ((upload.state = 'uploading' AND
                           GREATEST(upload.lease_expires_at, NEW.created_at) = NEW.available_at) OR
                          (upload.state IN ('issued', 'rejected', 'expired') AND
                           NEW.available_at >= NEW.created_at))) OR
                     (upload.attempt > NEW.upload_attempt AND
                      NEW.available_at >= NEW.created_at)
                 )
           ) THEN
            RAISE EXCEPTION 'invalid initial evidence object reconciliation state';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence object reconciliations cannot be deleted';
    END IF;
    IF ROW(NEW.tenant_id, NEW.upload_id, NEW.evidence_id, NEW.upload_attempt,
           NEW.object_key, NEW.object_version, NEW.object_size,
           NEW.object_checksum, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.tenant_id, OLD.upload_id, OLD.evidence_id, OLD.upload_attempt,
           OLD.object_key, OLD.object_version, OLD.object_size,
           OLD.object_checksum, OLD.created_at) OR
       NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid evidence object reconciliation transition';
    END IF;

    transition_valid :=
        (NEW.state = 'claimed' AND NEW.claim = OLD.claim + 1 AND
            NEW.lease_expires_at IS NOT NULL AND NEW.resolved_at IS NULL AND
            ((OLD.state = 'pending' AND OLD.available_at <= NEW.updated_at) OR
             (OLD.state = 'claimed' AND OLD.lease_expires_at <= NEW.updated_at))) OR
        (OLD.state = 'claimed' AND NEW.state = 'pending' AND
            NEW.claim = OLD.claim AND NEW.available_at > NEW.updated_at AND
            NEW.lease_expires_at IS NULL AND NEW.resolved_at IS NULL) OR
        (OLD.state IN ('pending', 'claimed') AND NEW.state IN ('retained', 'deleted') AND
            NEW.claim = OLD.claim AND NEW.lease_expires_at IS NULL AND
            NEW.resolved_at = NEW.updated_at);

    IF NOT transition_valid THEN
        RAISE EXCEPTION 'invalid evidence object reconciliation transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_object_reconciliations_protected
BEFORE INSERT OR UPDATE OR DELETE ON idenqa.evidence_object_reconciliations
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_evidence_object_reconciliation();

CREATE TRIGGER evidence_object_reconciliation_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_object_reconciliation_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.evidence_object_reconciliations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_object_reconciliations FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_object_reconciliations_tenant_scope
    ON idenqa.evidence_object_reconciliations
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_object_reconciliation_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_object_reconciliation_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_object_reconciliation_audit_tenant_scope
    ON idenqa.evidence_object_reconciliation_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.evidence_object_reconciliations FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_object_reconciliation_audit FROM PUBLIC;
