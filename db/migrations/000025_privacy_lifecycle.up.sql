CREATE TABLE idenqa.retention_bindings (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    aggregate_id text NOT NULL,
    data_class text NOT NULL,
    region text NOT NULL,
    retention_seconds bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    policy_digest text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, aggregate_id, data_class),
    CONSTRAINT retention_binding_class CHECK (data_class IN (
        'raw_evidence', 'derived_evidence', 'webhook_payload', 'workflow_metadata',
        'audit_record', 'deletion_proof', 'backup'
    )),
    CONSTRAINT retention_binding_region CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT retention_binding_duration CHECK (retention_seconds > 0),
    CONSTRAINT retention_binding_digest CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT retention_binding_time CHECK (expires_at > created_at)
);

CREATE TABLE idenqa.legal_holds (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    aggregate_id text NOT NULL,
    authority text NOT NULL,
    reason text NOT NULL,
    starts_at timestamptz NOT NULL,
    review_at timestamptz NOT NULL,
    released_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT legal_hold_id CHECK (id ~ '^hld_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT legal_hold_times CHECK (
        review_at > starts_at AND created_at <= starts_at AND
        (released_at IS NULL OR released_at >= starts_at)
    )
);

CREATE TABLE idenqa.deletion_requests (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    aggregate_id text NOT NULL,
    region text NOT NULL,
    state text NOT NULL,
    backup_expires_at timestamptz NOT NULL,
    failure_class text,
    version bigint NOT NULL,
    requested_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT deletion_request_id CHECK (id ~ '^del_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT deletion_request_region CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT deletion_request_state CHECK (state IN (
        'requested', 'blocked_by_legal_hold', 'in_progress',
        'awaiting_backup_expiry', 'completed', 'failed'
    )),
    CONSTRAINT deletion_request_version CHECK (version > 0),
    CONSTRAINT deletion_request_times CHECK (
        backup_expires_at > requested_at AND updated_at >= requested_at AND
        (completed_at IS NULL OR completed_at >= requested_at)
    )
);

CREATE TABLE idenqa.deletion_targets (
    tenant_id text NOT NULL,
    deletion_id text NOT NULL,
    kind text NOT NULL,
    reference text NOT NULL,
    region text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    deleted_at timestamptz,
    last_failure_class text,
    PRIMARY KEY (tenant_id, deletion_id, kind, reference),
    FOREIGN KEY (tenant_id, deletion_id) REFERENCES idenqa.deletion_requests (tenant_id, id),
    CONSTRAINT deletion_target_region CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT deletion_target_attempts CHECK (attempts >= 0)
);

CREATE TABLE idenqa.deletion_tombstones (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    deletion_id text NOT NULL,
    aggregate_id text NOT NULL,
    region text NOT NULL,
    target_count integer NOT NULL,
    proof_digest text NOT NULL,
    completed_at timestamptz NOT NULL,
    retain_until timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, deletion_id),
    FOREIGN KEY (tenant_id, deletion_id) REFERENCES idenqa.deletion_requests (tenant_id, id),
    CONSTRAINT deletion_tombstone_count CHECK (target_count > 0),
    CONSTRAINT deletion_tombstone_digest CHECK (proof_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT deletion_tombstone_retention CHECK (retain_until > completed_at)
);

CREATE FUNCTION idenqa.protect_privacy_history()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'privacy lifecycle history is append-only';
END;
$$;

CREATE TRIGGER retention_bindings_immutable BEFORE UPDATE OR DELETE ON idenqa.retention_bindings
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_privacy_history();
CREATE TRIGGER deletion_tombstones_immutable BEFORE UPDATE OR DELETE ON idenqa.deletion_tombstones
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_privacy_history();

ALTER TABLE idenqa.retention_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.retention_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.legal_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.legal_holds FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.deletion_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.deletion_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.deletion_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.deletion_targets FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.deletion_tombstones ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.deletion_tombstones FORCE ROW LEVEL SECURITY;

CREATE POLICY retention_bindings_tenant ON idenqa.retention_bindings
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY legal_holds_tenant ON idenqa.legal_holds
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY deletion_requests_tenant ON idenqa.deletion_requests
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY deletion_targets_tenant ON idenqa.deletion_targets
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY deletion_tombstones_tenant ON idenqa.deletion_tombstones
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.retention_bindings, idenqa.legal_holds,
    idenqa.deletion_requests, idenqa.deletion_targets,
    idenqa.deletion_tombstones FROM PUBLIC;
