DROP FUNCTION IF EXISTS idenqa.expire_webhook_data(timestamptz, integer);

DROP INDEX IF EXISTS idenqa.webhook_events_tombstone_retention;
DROP INDEX IF EXISTS idenqa.webhook_events_payload_retention;

ALTER TABLE idenqa.webhook_deliveries
    DROP CONSTRAINT IF EXISTS webhook_delivery_retention_times,
    DROP CONSTRAINT IF EXISTS webhook_delivery_body_bound,
    ALTER COLUMN body SET NOT NULL,
    DROP COLUMN IF EXISTS retain_until,
    DROP COLUMN IF EXISTS payload_expired_at,
    DROP COLUMN IF EXISTS payload_expires_at,
    ADD CONSTRAINT webhook_delivery_body_bound CHECK (octet_length(body) BETWEEN 1 AND 1048576);

ALTER TABLE idenqa.webhook_events
    DROP CONSTRAINT IF EXISTS webhook_event_retention_times,
    DROP CONSTRAINT IF EXISTS webhook_event_body_bound,
    ALTER COLUMN body SET NOT NULL,
    DROP COLUMN IF EXISTS retain_until,
    DROP COLUMN IF EXISTS payload_expired_at,
    DROP COLUMN IF EXISTS payload_expires_at,
    DROP COLUMN IF EXISTS aggregate_ids,
    ADD CONSTRAINT webhook_event_body_bound CHECK (octet_length(body) BETWEEN 1 AND 65536);

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_event_identity()
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

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_immutable_rows()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'webhook secret and attempt history is append-only';
END;
$$;
