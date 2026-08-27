CREATE TABLE idenqa.tenants (
    id text PRIMARY KEY,
    state text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    disabled_at timestamptz,
    CONSTRAINT tenants_id_format CHECK (id ~ '^ten_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT tenants_state CHECK (state IN ('active', 'disabled')),
    CONSTRAINT tenants_version CHECK (version > 0),
    CONSTRAINT tenants_disabled_state CHECK (
        (state = 'active' AND disabled_at IS NULL) OR
        (state = 'disabled' AND disabled_at IS NOT NULL)
    )
);

ALTER TABLE idenqa.tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.tenants FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.tenants
    USING (id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

CREATE TABLE idenqa.tenant_admin_audit (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    action text NOT NULL,
    actor text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    CONSTRAINT tenant_admin_audit_action CHECK (action IN ('create', 'inspect', 'disable')),
    CONSTRAINT tenant_admin_audit_actor CHECK (length(actor) BETWEEN 1 AND 200),
    CONSTRAINT tenant_admin_audit_reason CHECK (length(reason) BETWEEN 1 AND 500)
);

REVOKE ALL ON idenqa.tenants FROM PUBLIC;
REVOKE ALL ON idenqa.tenant_admin_audit FROM PUBLIC;
