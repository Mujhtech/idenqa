ALTER TABLE idenqa.webhook_deliveries
    DROP CONSTRAINT IF EXISTS webhook_delivery_body_wrapping,
    DROP COLUMN IF EXISTS body_algorithm,
    DROP COLUMN IF EXISTS body_key_version,
    DROP COLUMN IF EXISTS body_reference,
    DROP COLUMN IF EXISTS body_provider;

ALTER TABLE idenqa.webhook_events
    DROP CONSTRAINT IF EXISTS webhook_event_body_wrapping,
    DROP COLUMN IF EXISTS body_algorithm,
    DROP COLUMN IF EXISTS body_key_version,
    DROP COLUMN IF EXISTS body_reference,
    DROP COLUMN IF EXISTS body_provider;
