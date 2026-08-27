CREATE TABLE idenqa.api_key_admin_audit (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    key_id text,
    action text NOT NULL,
    actor text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    CONSTRAINT api_key_admin_audit_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT api_key_admin_audit_key_fk
        FOREIGN KEY (tenant_id, key_id) REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT api_key_admin_audit_action
        CHECK (action IN ('issue', 'list', 'rotate', 'revoke')),
    CONSTRAINT api_key_admin_audit_target
        CHECK ((action = 'list' AND key_id IS NULL) OR (action <> 'list' AND key_id IS NOT NULL)),
    CONSTRAINT api_key_admin_audit_actor CHECK (length(actor) BETWEEN 1 AND 200),
    CONSTRAINT api_key_admin_audit_reason CHECK (length(reason) BETWEEN 1 AND 500)
);

CREATE INDEX api_key_admin_audit_tenant_sequence
    ON idenqa.api_key_admin_audit (tenant_id, sequence DESC);

ALTER TABLE idenqa.api_key_admin_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.api_key_admin_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.api_key_admin_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.api_key_admin_audit FROM PUBLIC;
