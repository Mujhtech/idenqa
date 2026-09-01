CREATE TABLE idenqa.audit_heads (
    tenant_id text PRIMARY KEY REFERENCES idenqa.tenants (id),
    last_sequence bigint NOT NULL DEFAULT 0,
    last_hash text NOT NULL DEFAULT repeat('0', 64),
    CONSTRAINT audit_head_sequence_nonnegative CHECK (last_sequence >= 0),
    CONSTRAINT audit_head_hash_format CHECK (last_hash ~ '^[0-9a-f]{64}$')
);

CREATE TABLE idenqa.audit_records (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    sequence bigint NOT NULL,
    event_id text NOT NULL,
    event_type text NOT NULL,
    aggregate_id text NOT NULL,
    actor_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    event_digest text NOT NULL,
    previous_hash text NOT NULL,
    hash text NOT NULL,
    PRIMARY KEY (tenant_id, sequence),
    UNIQUE (tenant_id, event_id),
    CONSTRAINT audit_record_sequence_positive CHECK (sequence > 0),
    CONSTRAINT audit_record_digest_format CHECK (event_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT audit_record_previous_hash_format CHECK (previous_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT audit_record_hash_format CHECK (hash ~ '^[0-9a-f]{64}$')
);

CREATE TABLE idenqa.audit_keys (
    key_id text PRIMARY KEY,
    public_key bytea NOT NULL,
    valid_from timestamptz NOT NULL,
    retired_at timestamptz,
    CONSTRAINT audit_public_key_size CHECK (octet_length(public_key) = 32),
    CONSTRAINT audit_key_lifetime CHECK (retired_at IS NULL OR retired_at > valid_from)
);

CREATE TABLE idenqa.audit_checkpoints (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    through_sequence bigint NOT NULL,
    chain_hash text NOT NULL,
    key_id text NOT NULL REFERENCES idenqa.audit_keys (key_id),
    created_at timestamptz NOT NULL,
    signature text NOT NULL,
    PRIMARY KEY (tenant_id, through_sequence),
    FOREIGN KEY (tenant_id, through_sequence) REFERENCES idenqa.audit_records (tenant_id, sequence),
    CONSTRAINT audit_checkpoint_hash_format CHECK (chain_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT audit_checkpoint_signature_format CHECK (signature ~ '^[0-9a-f]{128}$')
);

CREATE FUNCTION idenqa.protect_audit_history()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit history is append-only';
END;
$$;

CREATE TRIGGER audit_records_append_only BEFORE UPDATE OR DELETE ON idenqa.audit_records
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();
CREATE TRIGGER audit_checkpoints_append_only BEFORE UPDATE OR DELETE ON idenqa.audit_checkpoints
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();
CREATE TRIGGER audit_keys_no_delete BEFORE DELETE ON idenqa.audit_keys
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();

ALTER TABLE idenqa.audit_heads ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.audit_heads FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.audit_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.audit_records FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.audit_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.audit_checkpoints FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_heads_tenant ON idenqa.audit_heads
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY audit_records_tenant ON idenqa.audit_records
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY audit_checkpoints_tenant ON idenqa.audit_checkpoints
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.audit_heads, idenqa.audit_records, idenqa.audit_keys,
    idenqa.audit_checkpoints FROM PUBLIC;
