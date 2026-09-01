CREATE TABLE idenqa.webhook_endpoints (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    url text NOT NULL,
    secret_version bigint NOT NULL,
    previous_secret_version bigint,
    previous_secret_valid_until timestamptz,
    disabled_at timestamptz,
    disabled_reason text,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT webhook_endpoint_id_format CHECK (id ~ '^whk_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT webhook_endpoint_url_bound CHECK (octet_length(url) BETWEEN 1 AND 2048),
    CONSTRAINT webhook_endpoint_secret_version_positive CHECK (secret_version > 0),
    CONSTRAINT webhook_endpoint_previous_secret_consistent CHECK (
        (previous_secret_version IS NULL) = (previous_secret_valid_until IS NULL)
        AND (previous_secret_version IS NULL OR previous_secret_version < secret_version)
    ),
    CONSTRAINT webhook_endpoint_disablement_consistent CHECK (
        (disabled_at IS NULL) = (disabled_reason IS NULL)
        AND (disabled_reason IS NULL OR octet_length(disabled_reason) BETWEEN 1 AND 128)
    )
);

CREATE TABLE idenqa.webhook_secrets (
    tenant_id text NOT NULL,
    endpoint_id text NOT NULL,
    version bigint NOT NULL,
    provider text NOT NULL,
    reference text NOT NULL,
    key_version text NOT NULL,
    algorithm text NOT NULL,
    ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, endpoint_id, version),
    FOREIGN KEY (tenant_id, endpoint_id) REFERENCES idenqa.webhook_endpoints (tenant_id, id),
    CONSTRAINT webhook_secret_version_positive CHECK (version > 0),
    CONSTRAINT webhook_secret_ciphertext_bound CHECK (octet_length(ciphertext) BETWEEN 1 AND 65536)
);

CREATE TABLE idenqa.webhook_deliveries (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    endpoint_id text NOT NULL,
    event_id text NOT NULL,
    event_type text NOT NULL,
    body bytea NOT NULL,
    body_digest text NOT NULL,
    state text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    delivered_at timestamptz,
    replay_of text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, endpoint_id, event_id),
    FOREIGN KEY (tenant_id, endpoint_id) REFERENCES idenqa.webhook_endpoints (tenant_id, id),
    FOREIGN KEY (tenant_id, replay_of) REFERENCES idenqa.webhook_deliveries (tenant_id, id),
    CONSTRAINT webhook_delivery_id_format CHECK (id ~ '^dlv_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT webhook_delivery_event_id_format CHECK (event_id ~ '^evt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT webhook_delivery_body_bound CHECK (octet_length(body) BETWEEN 1 AND 1048576),
    CONSTRAINT webhook_delivery_digest_format CHECK (body_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT webhook_delivery_state CHECK (state IN ('pending', 'delivered', 'exhausted', 'cancelled')),
    CONSTRAINT webhook_delivery_attempts CHECK (attempt_count BETWEEN 0 AND max_attempts AND max_attempts BETWEEN 1 AND 20),
    CONSTRAINT webhook_delivery_terminal_time CHECK ((state = 'delivered') = (delivered_at IS NOT NULL))
);

CREATE TABLE idenqa.webhook_delivery_attempts (
    tenant_id text NOT NULL,
    delivery_id text NOT NULL,
    attempt_number integer NOT NULL,
    secret_version bigint NOT NULL,
    signature_timestamp bigint NOT NULL,
    status_code integer NOT NULL,
    error_class text NOT NULL,
    retry_after_ms bigint NOT NULL,
    completed_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, delivery_id, attempt_number),
    FOREIGN KEY (tenant_id, delivery_id) REFERENCES idenqa.webhook_deliveries (tenant_id, id),
    CONSTRAINT webhook_attempt_number_positive CHECK (attempt_number > 0),
    CONSTRAINT webhook_attempt_status_bound CHECK (status_code BETWEEN 0 AND 599),
    CONSTRAINT webhook_attempt_class_bound CHECK (octet_length(error_class) BETWEEN 1 AND 64),
    CONSTRAINT webhook_attempt_retry_bound CHECK (retry_after_ms BETWEEN 0 AND 86400000)
);

CREATE FUNCTION idenqa.protect_webhook_immutable_rows()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'webhook secret and attempt history is append-only';
END;
$$;

CREATE TRIGGER webhook_secrets_append_only
    BEFORE UPDATE OR DELETE ON idenqa.webhook_secrets
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_webhook_immutable_rows();
CREATE TRIGGER webhook_delivery_attempts_append_only
    BEFORE UPDATE OR DELETE ON idenqa.webhook_delivery_attempts
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_webhook_immutable_rows();

ALTER TABLE idenqa.webhook_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_endpoints FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_delivery_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_delivery_attempts FORCE ROW LEVEL SECURITY;

CREATE POLICY webhook_endpoints_tenant ON idenqa.webhook_endpoints
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY webhook_secrets_tenant ON idenqa.webhook_secrets
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY webhook_deliveries_tenant ON idenqa.webhook_deliveries
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY webhook_delivery_attempts_tenant ON idenqa.webhook_delivery_attempts
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.webhook_endpoints, idenqa.webhook_secrets,
    idenqa.webhook_deliveries, idenqa.webhook_delivery_attempts FROM PUBLIC;
