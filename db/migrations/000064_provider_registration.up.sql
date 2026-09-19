CREATE TABLE idenqa.provider_registrations (
    tenant_id text NOT NULL,
    id text NOT NULL,
    adapter_id text NOT NULL,
    region text NOT NULL,
    configuration jsonb NOT NULL,
    inputs jsonb,
    selfie_requirement text,
    restrictions jsonb,
    enabled boolean NOT NULL,
    version bigint NOT NULL,
    actor_key_id text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT provider_registrations_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT provider_registrations_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id) REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT provider_registrations_id
        CHECK (id ~ '^pvr_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT provider_registrations_adapter
        CHECK (adapter_id ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT provider_registrations_region
        CHECK (region ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT provider_registrations_configuration CHECK (
        jsonb_typeof(configuration) = 'object'
        AND octet_length(configuration::text) <= 4096
        AND configuration ? 'provider_id'
        AND configuration ? 'schema_digest'
        AND configuration ? 'secret_reference'
        AND configuration ? 'credential_version'
    ),
    CONSTRAINT provider_registrations_inputs CHECK (
        inputs IS NULL OR (
            jsonb_typeof(inputs) = 'array'
            AND jsonb_array_length(inputs) BETWEEN 1 AND 2
            AND octet_length(inputs::text) <= 4096
        )
    ),
    CONSTRAINT provider_registrations_selfie CHECK (
        selfie_requirement IS NULL OR selfie_requirement ~ '^[a-z][a-z0-9._-]{0,63}$'
    ),
    CONSTRAINT provider_registrations_restrictions CHECK (
        restrictions IS NULL OR (
            jsonb_typeof(restrictions) = 'object'
            AND octet_length(restrictions::text) <= 1024
        )
    ),
    CONSTRAINT provider_registrations_version CHECK (version > 0),
    CONSTRAINT provider_registrations_times CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX provider_registrations_enabled_scope
    ON idenqa.provider_registrations (tenant_id, adapter_id, region)
    WHERE enabled;

CREATE INDEX provider_registrations_list
    ON idenqa.provider_registrations (tenant_id, created_at DESC, id DESC);

ALTER TABLE idenqa.provider_registrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_registrations FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_registrations_tenant ON idenqa.provider_registrations
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.provider_registrations FROM PUBLIC;

CREATE TABLE idenqa.provider_registration_history (
    tenant_id text NOT NULL,
    registration_id text NOT NULL,
    version bigint NOT NULL,
    actor_key_id text NOT NULL,
    operation text NOT NULL,
    reason text NOT NULL,
    receipt jsonb NOT NULL,
    PRIMARY KEY (tenant_id, registration_id, version),
    CONSTRAINT provider_registration_history_registration_fk
        FOREIGN KEY (tenant_id, registration_id)
        REFERENCES idenqa.provider_registrations (tenant_id, id),
    CONSTRAINT provider_registration_history_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id) REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT provider_registration_history_operation
        CHECK (operation IN ('create', 'update', 'enable', 'disable')),
    CONSTRAINT provider_registration_history_reason
        CHECK (reason ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT provider_registration_history_receipt CHECK (
        jsonb_typeof(receipt) = 'object'
        AND octet_length(receipt::text) <= 16384
    )
);

CREATE TRIGGER provider_registration_history_append_only
    BEFORE UPDATE OR DELETE ON idenqa.provider_registration_history
    FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_catalog_record_change();

ALTER TABLE idenqa.provider_registration_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_registration_history FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_registration_history_tenant ON idenqa.provider_registration_history
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.provider_registration_history FROM PUBLIC;
