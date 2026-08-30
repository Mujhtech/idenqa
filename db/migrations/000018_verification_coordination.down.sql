DROP FUNCTION IF EXISTS idenqa.list_pending_check_progress_tenants(integer);
DROP FUNCTION IF EXISTS idenqa.list_due_verification_reconciliations(timestamptz, integer);

ALTER TABLE idenqa.realtime_events
    DROP CONSTRAINT realtime_events_message_type,
    DROP CONSTRAINT realtime_events_command_binding,
    ADD CONSTRAINT realtime_events_message_type CHECK (message_type IN (
        'capture.command',
        'challenge.request',
        'challenge.cancelled',
        'capture.progress',
        'session.state_changed'
    )),
    ADD CONSTRAINT realtime_events_command_binding CHECK (
        (
            message_type IN ('capture.command', 'challenge.request', 'challenge.cancelled') AND
            command_id ~ '^cmd_[0-9A-HJKMNP-TV-Z]{26}$'
        ) OR
        (
            message_type IN ('capture.progress', 'session.state_changed') AND
            command_id IS NULL
        )
    );

DROP INDEX IF EXISTS idenqa.verification_reconciliations_attempt;
