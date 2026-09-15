-- This fails safely if retained replay history contains repeated event IDs.
ALTER TABLE idenqa.webhook_deliveries ADD CONSTRAINT webhook_deliveries_tenant_id_endpoint_id_event_id_key UNIQUE (tenant_id, endpoint_id, event_id);
DROP INDEX idenqa.webhook_deliveries_root_event;
DROP INDEX idenqa.webhook_deliveries_endpoint_list;
