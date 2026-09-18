DROP FUNCTION IF EXISTS idenqa.list_ready_webhook_events(timestamptz, integer);
DROP TABLE IF EXISTS idenqa.webhook_events;
DROP FUNCTION IF EXISTS idenqa.protect_webhook_event_identity();
ALTER TABLE idenqa.webhook_endpoints
    DROP CONSTRAINT IF EXISTS webhook_endpoint_event_types_bound,
    DROP COLUMN IF EXISTS event_types;
