DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM idenqa.verification_reconciliations
        GROUP BY tenant_id, check_id, attempt_id
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION
            'migration 18 requires one verification reconciliation per attempt';
    END IF;
END;
$$;

CREATE UNIQUE INDEX verification_reconciliations_attempt
    ON idenqa.verification_reconciliations (tenant_id, check_id, attempt_id);

ALTER TABLE idenqa.realtime_events
    DROP CONSTRAINT realtime_events_message_type,
    DROP CONSTRAINT realtime_events_command_binding,
    ADD CONSTRAINT realtime_events_message_type CHECK (message_type IN (
        'capture.command',
        'challenge.request',
        'challenge.cancelled',
        'capture.progress',
        'verification.check.progress',
        'session.state_changed'
    )),
    ADD CONSTRAINT realtime_events_command_binding CHECK (
        (
            message_type IN ('capture.command', 'challenge.request', 'challenge.cancelled') AND
            command_id ~ '^cmd_[0-9A-HJKMNP-TV-Z]{26}$'
        ) OR
        (
            message_type IN (
                'capture.progress',
                'verification.check.progress',
                'session.state_changed'
            ) AND
            command_id IS NULL
        )
    );

CREATE FUNCTION idenqa.list_due_verification_reconciliations(
    observed_at timestamptz,
    batch_size integer
)
RETURNS TABLE (tenant_id text, check_id text, attempt_id text)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT reconciliations.tenant_id, reconciliations.check_id, reconciliations.attempt_id
    FROM idenqa.verification_reconciliations AS reconciliations
    WHERE observed_at IS NOT NULL
      AND batch_size BETWEEN 1 AND 100
      AND reconciliations.available_at <= observed_at
      AND (
          reconciliations.status = 'pending' OR
          (
              reconciliations.status = 'claimed' AND
              reconciliations.lease_expires_at <= observed_at
          )
      )
    ORDER BY reconciliations.available_at, reconciliations.created_at,
             reconciliations.tenant_id, reconciliations.check_id, reconciliations.attempt_id
    LIMIT LEAST(batch_size, 100);
$$;

CREATE FUNCTION idenqa.list_pending_check_progress_tenants(batch_size integer)
RETURNS TABLE (tenant_id text)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT events.tenant_id
    FROM idenqa.outbox_events AS events
    WHERE batch_size BETWEEN 1 AND 100
      AND events.event_type = 'verification.check.progress.v1'
      AND events.schema_version = 1
      AND events.published_at IS NULL
    GROUP BY events.tenant_id
    ORDER BY MIN(events.created_at), events.tenant_id
    LIMIT LEAST(batch_size, 100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_due_verification_reconciliations(timestamptz, integer)
    FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.list_pending_check_progress_tenants(integer)
    FROM PUBLIC;
