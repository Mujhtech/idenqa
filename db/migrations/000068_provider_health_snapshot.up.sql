CREATE TABLE idenqa.provider_health_snapshots (
    tenant_id text NOT NULL,
    adapter_id text NOT NULL,
    provider_id text NOT NULL,
    registration_id text,
    region text NOT NULL,
    state text NOT NULL,
    reason_code text NOT NULL,
    breaker_state text NOT NULL,
    breaker_since timestamptz,
    window_seconds integer NOT NULL,
    completed_dispatches bigint NOT NULL,
    failed_dispatches bigint NOT NULL,
    failure_ratio double precision NOT NULL,
    failure_classes jsonb,
    async_unresolved_dispatches bigint NOT NULL,
    async_expired_dispatches bigint NOT NULL,
    callbacks_adopted bigint NOT NULL,
    observed_at timestamptz NOT NULL,
    version bigint NOT NULL,
    PRIMARY KEY (tenant_id, adapter_id, provider_id, region),
    CONSTRAINT provider_health_snapshots_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT provider_health_snapshots_adapter
        CHECK (adapter_id ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT provider_health_snapshots_provider
        CHECK (provider_id ~ '^pvd_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT provider_health_snapshots_registration
        CHECK (registration_id IS NULL OR registration_id ~ '^pvr_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT provider_health_snapshots_region
        CHECK (region ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT provider_health_snapshots_state
        CHECK (state IN ('ready', 'degraded', 'not_ready', 'unknown')),
    CONSTRAINT provider_health_snapshots_reason
        CHECK (reason_code ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT provider_health_snapshots_breaker
        CHECK (breaker_state IN ('closed', 'open', 'half_open')),
    CONSTRAINT provider_health_snapshots_breaker_since
        CHECK ((breaker_state = 'closed') = (breaker_since IS NULL)),
    CONSTRAINT provider_health_snapshots_window
        CHECK (window_seconds BETWEEN 1 AND 86400),
    CONSTRAINT provider_health_snapshots_counts
        CHECK (completed_dispatches >= 0 AND failed_dispatches >= 0
            AND async_unresolved_dispatches >= 0 AND async_expired_dispatches >= 0
            AND callbacks_adopted >= 0),
    CONSTRAINT provider_health_snapshots_ratio
        CHECK (failure_ratio >= 0 AND failure_ratio <= 1),
    CONSTRAINT provider_health_snapshots_classes CHECK (
        failure_classes IS NULL OR (
            jsonb_typeof(failure_classes) = 'array'
            AND jsonb_array_length(failure_classes) <= 16
            AND octet_length(failure_classes::text) <= 2048
        )
    ),
    CONSTRAINT provider_health_snapshots_version CHECK (version > 0)
);

ALTER TABLE idenqa.provider_health_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_health_snapshots FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_health_snapshots_tenant ON idenqa.provider_health_snapshots
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.provider_health_snapshots FROM PUBLIC;
