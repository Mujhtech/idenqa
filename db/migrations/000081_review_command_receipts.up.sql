CREATE TABLE idenqa.review_command_receipts (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    command_id text NOT NULL,
    actor_id text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    operation text NOT NULL,
    expected_version bigint NOT NULL,
    request_digest text NOT NULL,
    consumed_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, command_id),
    CONSTRAINT review_command_receipts_target_kind CHECK (target_kind = 'review_case'),
    CONSTRAINT review_command_receipts_operation CHECK (operation IN ('claim', 'submit_finding')),
    CONSTRAINT review_command_receipts_expected_version CHECK (expected_version >= 1),
    CONSTRAINT review_command_receipts_digest CHECK (request_digest ~ '^[0-9a-f]{64}$')
);

CREATE INDEX review_command_receipts_target
    ON idenqa.review_command_receipts (tenant_id, target_kind, target_id, consumed_at DESC);

CREATE TRIGGER review_command_receipts_append_only
    BEFORE UPDATE OR DELETE ON idenqa.review_command_receipts
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();

ALTER TABLE idenqa.review_command_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_command_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY review_command_receipts_tenant ON idenqa.review_command_receipts
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
REVOKE ALL ON idenqa.review_command_receipts FROM PUBLIC;
