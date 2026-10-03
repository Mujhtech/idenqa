CREATE TABLE idenqa.capture_journey_events (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    capture_token_id text NOT NULL,
    event_id text NOT NULL,
    sequence integer NOT NULL,
    event_type text NOT NULL,
    screen text NOT NULL,
    action text,
    requirement_key text,
    artefact text,
    acquisition_method text,
    client_occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    payload_digest text NOT NULL,
    PRIMARY KEY (tenant_id, event_id),
    UNIQUE (tenant_id, capture_token_id, sequence),
    FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    FOREIGN KEY (tenant_id, capture_token_id, verification_id)
        REFERENCES idenqa.capture_tokens (tenant_id, id, verification_id),
    CONSTRAINT capture_journey_event_id CHECK (event_id ~ '^journey_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    CONSTRAINT capture_journey_sequence CHECK (sequence BETWEEN 1 AND 10000),
    CONSTRAINT capture_journey_event_type CHECK (event_type IN (
        'screen_viewed', 'action_selected', 'navigation_back', 'capture_started',
        'capture_retried', 'capture_accepted', 'capture_retake', 'recovery_started',
        'processing_started', 'completion_shown', 'error_shown'
    )),
    CONSTRAINT capture_journey_screen CHECK (screen IN (
        'intro', 'country', 'notice', 'method', 'preparation', 'capture', 'review',
        'recovery', 'processing', 'completion', 'error'
    )),
    CONSTRAINT capture_journey_action CHECK (
        action IS NULL OR action IN (
            'continue', 'back', 'select_country', 'accept_notice', 'refuse_notice',
            'select_document', 'select_method', 'start_capture', 'retake',
            'accept_capture', 'retry', 'submit'
        )
    ),
    CONSTRAINT capture_journey_tokens CHECK (
        (requirement_key IS NULL OR requirement_key ~ '^[a-z][a-z0-9_]{0,63}$') AND
        (artefact IS NULL OR artefact ~ '^[a-z][a-z0-9._:-]{0,127}$') AND
        (acquisition_method IS NULL OR acquisition_method ~ '^[a-z][a-z0-9._:-]{0,127}$')
    ),
    CONSTRAINT capture_journey_times CHECK (
        client_occurred_at >= received_at - interval '24 hours' AND
        client_occurred_at <= received_at + interval '5 minutes'
    ),
    CONSTRAINT capture_journey_digest CHECK (payload_digest ~ '^[0-9a-f]{64}$')
);

CREATE INDEX capture_journey_events_timeline
    ON idenqa.capture_journey_events (tenant_id, verification_id, received_at, event_id);

CREATE FUNCTION idenqa.reject_capture_journey_event_change()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'capture journey events are append-only';
END;
$$;

CREATE TRIGGER capture_journey_event_append_only
BEFORE UPDATE OR DELETE ON idenqa.capture_journey_events
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_capture_journey_event_change();

ALTER TABLE idenqa.capture_journey_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_journey_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON idenqa.capture_journey_events
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.capture_journey_events FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_capture_journey_event_change() FROM PUBLIC;
