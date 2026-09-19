ALTER TABLE idenqa.webhook_events
    ADD COLUMN stream_sequence bigint GENERATED ALWAYS AS IDENTITY;

CREATE UNIQUE INDEX webhook_events_stream ON idenqa.webhook_events (tenant_id, stream_sequence);
