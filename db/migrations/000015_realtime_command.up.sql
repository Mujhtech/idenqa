ALTER TABLE idenqa.capture_tokens
    ADD CONSTRAINT capture_tokens_tenant_id_verification_unique
    UNIQUE (tenant_id, id, verification_id);

ALTER TABLE idenqa.websocket_connection_tickets
    ADD CONSTRAINT websocket_connection_tickets_exact_capture_token_fk
    FOREIGN KEY (tenant_id, capture_token_id, verification_id)
    REFERENCES idenqa.capture_tokens (tenant_id, id, verification_id);

CREATE TABLE idenqa.realtime_client_commands (
    command_id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    capture_token_id text NOT NULL,
    request_fingerprint bytea NOT NULL,
    message_type text NOT NULL,
    step_state text NOT NULL,
    requirement_key text NOT NULL,
    artefact text NOT NULL,
    acquisition_method text NOT NULL,
    result_code text,
    disposition text NOT NULL,
    rejection_code text,
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (command_id),
    UNIQUE (tenant_id, command_id),
    CONSTRAINT realtime_client_commands_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT realtime_client_commands_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT realtime_client_commands_capture_token_fk
        FOREIGN KEY (tenant_id, capture_token_id, verification_id)
        REFERENCES idenqa.capture_tokens (tenant_id, id, verification_id),
    CONSTRAINT realtime_client_commands_id_format
        CHECK (command_id ~ '^cmd_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT realtime_client_commands_fingerprint
        CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT realtime_client_commands_message_type
        CHECK (message_type IN (
            'capture.step.started',
            'capture.step.failed',
            'capture.step.cancelled'
        )),
    CONSTRAINT realtime_client_commands_step_state
        CHECK (
            (message_type = 'capture.step.started' AND step_state = 'started') OR
            (message_type = 'capture.step.failed' AND step_state = 'failed') OR
            (message_type = 'capture.step.cancelled' AND step_state = 'cancelled')
        ),
    CONSTRAINT realtime_client_commands_requirement_key
        CHECK (requirement_key ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT realtime_client_commands_artefact
        CHECK (artefact ~ '^[a-z][a-z0-9_.-]{0,99}$'),
    CONSTRAINT realtime_client_commands_acquisition_method
        CHECK (acquisition_method ~ '^[a-z][a-z0-9_.-]{0,99}$'),
    CONSTRAINT realtime_client_commands_result_code
        CHECK (
            (step_state = 'started' AND result_code IS NULL) OR
            (
                step_state IN ('failed', 'cancelled') AND
                result_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,99}$'
            )
        ),
    CONSTRAINT realtime_client_commands_disposition
        CHECK (
            (disposition = 'accepted' AND rejection_code IS NULL) OR
            (
                disposition = 'rejected' AND
                rejection_code IN ('authority_unavailable', 'policy_conflict')
            )
        )
);

CREATE INDEX realtime_client_commands_verification_received
    ON idenqa.realtime_client_commands (tenant_id, verification_id, received_at DESC);

CREATE FUNCTION idenqa.protect_realtime_client_command()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'realtime client command records are append-only';
END;
$$;

CREATE TRIGGER realtime_client_command_append_only
BEFORE UPDATE OR DELETE ON idenqa.realtime_client_commands
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_realtime_client_command();

ALTER TABLE idenqa.realtime_client_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.realtime_client_commands FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.realtime_client_commands
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.realtime_client_commands FROM PUBLIC;
