ALTER TABLE idenqa.webhook_endpoints
    ADD COLUMN event_types text[] NOT NULL DEFAULT ARRAY['verification.completed']::text[],
    ADD CONSTRAINT webhook_endpoint_event_types_bound CHECK (
        cardinality(event_types) BETWEEN 1 AND 64
        AND array_position(event_types, '') IS NULL
    );

CREATE TABLE idenqa.webhook_events (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    event_type text NOT NULL,
    schema_version text NOT NULL,
    dedupe_key text NOT NULL,
    body bytea NOT NULL,
    body_digest text NOT NULL,
    state text NOT NULL DEFAULT 'pending',
    cursor text NOT NULL DEFAULT '',
    delivered_count integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, dedupe_key),
    CONSTRAINT webhook_event_id_format CHECK (id ~ '^evt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT webhook_event_dedupe_bound CHECK (octet_length(dedupe_key) BETWEEN 1 AND 512),
    CONSTRAINT webhook_event_body_bound CHECK (octet_length(body) BETWEEN 1 AND 65536),
    CONSTRAINT webhook_event_digest_format CHECK (body_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT webhook_event_state CHECK (state IN ('pending', 'completed')),
    CONSTRAINT webhook_event_completion CHECK ((state = 'completed') = (completed_at IS NOT NULL)),
    CONSTRAINT webhook_event_count CHECK (delivered_count >= 0)
);

CREATE INDEX webhook_events_pending ON idenqa.webhook_events (tenant_id, created_at, id)
    WHERE state = 'pending';

CREATE FUNCTION idenqa.protect_webhook_event_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.event_type <> OLD.event_type OR
       NEW.schema_version <> OLD.schema_version OR NEW.dedupe_key <> OLD.dedupe_key OR
       NEW.body <> OLD.body OR NEW.body_digest <> OLD.body_digest OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'webhook event identity and canonical body are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER webhook_event_identity_immutable
    BEFORE UPDATE ON idenqa.webhook_events
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_webhook_event_identity();

ALTER TABLE idenqa.webhook_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.webhook_events FORCE ROW LEVEL SECURITY;

CREATE POLICY webhook_events_tenant ON idenqa.webhook_events
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.webhook_events FROM PUBLIC;

CREATE FUNCTION idenqa.list_ready_webhook_events(observed_at timestamptz, batch_size integer)
RETURNS TABLE (tenant_id text, id text)
LANGUAGE sql
SECURITY DEFINER
SET search_path = idenqa
AS $$
    SELECT events.tenant_id, events.id
    FROM idenqa.webhook_events AS events
    WHERE events.state = 'pending' AND events.cursor = '' AND events.created_at <= observed_at
    ORDER BY events.created_at, events.id
    LIMIT batch_size
    FOR UPDATE SKIP LOCKED;
$$;

REVOKE ALL ON FUNCTION idenqa.list_ready_webhook_events(timestamptz, integer) FROM PUBLIC;
