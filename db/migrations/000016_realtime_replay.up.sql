CREATE TABLE idenqa.realtime_streams (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    latest_cursor bigint NOT NULL DEFAULT 0,
    latest_expires_at timestamptz,
    retained_from_cursor bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, verification_id),
    CONSTRAINT realtime_streams_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT realtime_streams_cursor_bounds CHECK (
        latest_cursor >= 0 AND
        retained_from_cursor >= 1 AND
        retained_from_cursor <= latest_cursor + 1 AND
        (
            (latest_cursor = 0 AND latest_expires_at IS NULL) OR
            (latest_cursor > 0 AND latest_expires_at IS NOT NULL)
        )
    )
);

CREATE TABLE idenqa.realtime_events (
    event_id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    cursor bigint NOT NULL,
    message_type text NOT NULL,
    command_id text,
    correlation_id text,
    causation_id text,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, verification_id, cursor),
    UNIQUE (tenant_id, event_id),
    CONSTRAINT realtime_events_stream_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.realtime_streams (tenant_id, verification_id),
    CONSTRAINT realtime_events_id_format
        CHECK (event_id ~ '^evt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT realtime_events_cursor CHECK (cursor > 0),
    CONSTRAINT realtime_events_message_type CHECK (message_type IN (
        'capture.command',
        'challenge.request',
        'challenge.cancelled',
        'capture.progress',
        'session.state_changed'
    )),
    CONSTRAINT realtime_events_command_binding CHECK (
        (
            message_type IN ('capture.command', 'challenge.request', 'challenge.cancelled') AND
            command_id ~ '^cmd_[0-9A-HJKMNP-TV-Z]{26}$'
        ) OR
        (
            message_type IN ('capture.progress', 'session.state_changed') AND
            command_id IS NULL
        )
    ),
    CONSTRAINT realtime_events_correlation_id CHECK (
        correlation_id IS NULL OR correlation_id ~ '^msg_[0-9A-HJKMNP-TV-Z]{26}$'
    ),
    CONSTRAINT realtime_events_causation_id CHECK (
        causation_id IS NULL OR causation_id ~ '^msg_[0-9A-HJKMNP-TV-Z]{26}$'
    ),
    CONSTRAINT realtime_events_payload CHECK (
        jsonb_typeof(payload) = 'object' AND
        octet_length(payload::text) <= 8192
    ),
    CONSTRAINT realtime_events_times CHECK (
        occurred_at < expires_at
    )
);

CREATE INDEX realtime_events_expiry
    ON idenqa.realtime_events (tenant_id, expires_at, verification_id, cursor);

CREATE TABLE idenqa.realtime_acknowledgements (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    capture_token_id text NOT NULL,
    acknowledged_cursor bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, verification_id, capture_token_id),
    CONSTRAINT realtime_acknowledgements_stream_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.realtime_streams (tenant_id, verification_id),
    CONSTRAINT realtime_acknowledgements_capture_token_fk
        FOREIGN KEY (tenant_id, capture_token_id, verification_id)
        REFERENCES idenqa.capture_tokens (tenant_id, id, verification_id),
    CONSTRAINT realtime_acknowledgements_cursor CHECK (acknowledged_cursor >= 0)
);

CREATE FUNCTION idenqa.protect_realtime_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'realtime event records are append-only';
END;
$$;

CREATE TRIGGER realtime_event_append_only
BEFORE UPDATE ON idenqa.realtime_events
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_realtime_event();

ALTER TABLE idenqa.realtime_streams ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.realtime_streams FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.realtime_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.realtime_events FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.realtime_acknowledgements ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.realtime_acknowledgements FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.realtime_streams
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

CREATE POLICY tenant_scope ON idenqa.realtime_events
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

CREATE POLICY tenant_scope ON idenqa.realtime_acknowledgements
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.realtime_streams FROM PUBLIC;
REVOKE ALL ON idenqa.realtime_events FROM PUBLIC;
REVOKE ALL ON idenqa.realtime_acknowledgements FROM PUBLIC;
