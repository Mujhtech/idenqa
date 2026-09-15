-- Root events retain exact deduplication; deliberate replay creates a new
-- delivery with the same signed event identity and body under command idempotency.
ALTER TABLE idenqa.webhook_deliveries DROP CONSTRAINT webhook_deliveries_tenant_id_endpoint_id_event_id_key;

CREATE UNIQUE INDEX webhook_deliveries_root_event
    ON idenqa.webhook_deliveries (tenant_id, endpoint_id, event_id)
    WHERE replay_of IS NULL;

CREATE INDEX webhook_deliveries_endpoint_list ON idenqa.webhook_deliveries (tenant_id, endpoint_id, id);
