ALTER TABLE idenqa.webhook_deliveries ADD COLUMN last_discovered_at timestamptz;

CREATE INDEX webhook_deliveries_pending_due
    ON idenqa.webhook_deliveries (last_discovered_at NULLS FIRST, next_attempt_at, tenant_id, id)
    WHERE state = 'pending';

-- Rotating a durable discovery cursor prevents an unavailable endpoint or an
-- infrastructure-exhausted task from permanently starving later tenant work.
-- A crash between discovery and enqueue only delays this row until the next
-- bounded pass; it never removes delivery eligibility.
CREATE FUNCTION idenqa.list_ready_webhook_deliveries(observed_at timestamptz, batch_size integer)
RETURNS TABLE (tenant_id text, delivery_id text, attempt_number integer, due_at timestamptz, created_at timestamptz)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    WITH ready AS (
        SELECT deliveries.tenant_id, deliveries.id
        FROM idenqa.webhook_deliveries AS deliveries
        WHERE observed_at IS NOT NULL
          AND batch_size BETWEEN 1 AND 100
          AND deliveries.state = 'pending'
          AND deliveries.attempt_count < deliveries.max_attempts
          AND deliveries.next_attempt_at <= observed_at
        ORDER BY deliveries.last_discovered_at NULLS FIRST,
                 deliveries.next_attempt_at, deliveries.tenant_id, deliveries.id
        LIMIT LEAST(batch_size, 100)
        FOR UPDATE SKIP LOCKED
    )
    UPDATE idenqa.webhook_deliveries AS deliveries
    SET last_discovered_at = observed_at
    FROM ready
    WHERE deliveries.tenant_id = ready.tenant_id AND deliveries.id = ready.id
    RETURNING deliveries.tenant_id, deliveries.id, deliveries.attempt_count + 1,
              deliveries.next_attempt_at, deliveries.created_at;
$$;

REVOKE ALL ON FUNCTION idenqa.list_ready_webhook_deliveries(timestamptz, integer) FROM PUBLIC;
