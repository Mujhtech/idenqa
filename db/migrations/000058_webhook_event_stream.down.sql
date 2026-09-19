DROP INDEX IF EXISTS idenqa.webhook_events_stream;

ALTER TABLE idenqa.webhook_events
    DROP COLUMN IF EXISTS stream_sequence;
