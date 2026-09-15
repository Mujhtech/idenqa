DROP FUNCTION idenqa.list_ready_webhook_deliveries(timestamptz, integer);
DROP INDEX idenqa.webhook_deliveries_pending_due;
ALTER TABLE idenqa.webhook_deliveries DROP COLUMN last_discovered_at;
