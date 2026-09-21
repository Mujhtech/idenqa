ALTER TABLE idenqa.provider_registration_history
    DROP CONSTRAINT provider_registration_history_operation,
    ADD CONSTRAINT provider_registration_history_operation
        CHECK (operation IN ('create', 'update', 'enable', 'disable', 'rotate-credential'));

CREATE TABLE idenqa.provider_dispatch_leases (
    tenant_id text NOT NULL,
    limit_key text NOT NULL,
    lease_id text NOT NULL,
    acquired_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, limit_key, lease_id),
    CONSTRAINT provider_dispatch_leases_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT provider_dispatch_leases_key
        CHECK (limit_key ~ '^[0-9a-f]{64}$'),
    CONSTRAINT provider_dispatch_leases_id
        CHECK (lease_id ~ '^[0-9a-f]{32}$'),
    CONSTRAINT provider_dispatch_leases_window
        CHECK (expires_at > acquired_at)
);

CREATE INDEX provider_dispatch_leases_expiry
    ON idenqa.provider_dispatch_leases (tenant_id, limit_key, expires_at);

ALTER TABLE idenqa.provider_dispatch_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_dispatch_leases FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_dispatch_leases_tenant ON idenqa.provider_dispatch_leases
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.provider_dispatch_leases FROM PUBLIC;

CREATE TABLE idenqa.provider_dispatch_admissions (
    tenant_id text NOT NULL,
    limit_key text NOT NULL,
    window_start timestamptz NOT NULL,
    admitted bigint NOT NULL,
    PRIMARY KEY (tenant_id, limit_key, window_start),
    CONSTRAINT provider_dispatch_admissions_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT provider_dispatch_admissions_key
        CHECK (limit_key ~ '^[0-9a-f]{64}$'),
    CONSTRAINT provider_dispatch_admissions_count
        CHECK (admitted >= 0)
);

ALTER TABLE idenqa.provider_dispatch_admissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_dispatch_admissions FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_dispatch_admissions_tenant ON idenqa.provider_dispatch_admissions
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.provider_dispatch_admissions FROM PUBLIC;
