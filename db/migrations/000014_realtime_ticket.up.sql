CREATE TABLE idenqa.websocket_connection_tickets (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    capture_token_id text NOT NULL,
    digest bytea NOT NULL,
    digest_version integer NOT NULL,
    client_kind text NOT NULL,
    client_identity text NOT NULL,
    region text NOT NULL,
    protocol text NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    redeemed_at timestamptz,
    connection_id text,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT websocket_connection_tickets_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT websocket_connection_tickets_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT websocket_connection_tickets_capture_token_fk
        FOREIGN KEY (tenant_id, capture_token_id)
        REFERENCES idenqa.capture_tokens (tenant_id, id),
    CONSTRAINT websocket_connection_tickets_id_format
        CHECK (id ~ '^wst_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT websocket_connection_tickets_digest
        CHECK (octet_length(digest) = 32 AND digest_version = 1),
    CONSTRAINT websocket_connection_tickets_client_kind
        CHECK (client_kind IN ('browser', 'native')),
    CONSTRAINT websocket_connection_tickets_client_identity
        CHECK (
            char_length(client_identity) BETWEEN 1 AND 2048 AND
            client_identity = btrim(client_identity) AND
            client_identity !~ '[[:cntrl:]]'
        ),
    CONSTRAINT websocket_connection_tickets_region
        CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT websocket_connection_tickets_protocol
        CHECK (protocol = 'idenqa.capture.v1'),
    CONSTRAINT websocket_connection_tickets_lifetime
        CHECK (
            expires_at >= issued_at + interval '10 seconds' AND
            expires_at <= issued_at + interval '60 seconds'
        ),
    CONSTRAINT websocket_connection_tickets_redemption
        CHECK (
            (redeemed_at IS NULL AND connection_id IS NULL) OR
            (
                redeemed_at >= issued_at AND redeemed_at < expires_at AND
                connection_id ~ '^con_[0-9A-HJKMNP-TV-Z]{26}$'
            )
        )
);

CREATE UNIQUE INDEX websocket_connection_tickets_connection
    ON idenqa.websocket_connection_tickets (connection_id)
    WHERE connection_id IS NOT NULL;
CREATE INDEX websocket_connection_tickets_expiry
    ON idenqa.websocket_connection_tickets (expires_at)
    WHERE redeemed_at IS NULL;

CREATE FUNCTION idenqa.protect_websocket_connection_ticket()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.verification_id <> OLD.verification_id OR
       NEW.capture_token_id <> OLD.capture_token_id OR
       NEW.digest <> OLD.digest OR NEW.digest_version <> OLD.digest_version OR
       NEW.client_kind <> OLD.client_kind OR
       NEW.client_identity <> OLD.client_identity OR NEW.region <> OLD.region OR
       NEW.protocol <> OLD.protocol OR NEW.issued_at <> OLD.issued_at OR
       NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'websocket connection ticket identity and bindings are immutable';
    END IF;
    IF OLD.redeemed_at IS NOT NULL OR OLD.connection_id IS NOT NULL OR
       NEW.redeemed_at IS NULL OR NEW.connection_id IS NULL THEN
        RAISE EXCEPTION 'websocket connection ticket redemption is irreversible';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER websocket_connection_ticket_immutable
BEFORE UPDATE ON idenqa.websocket_connection_tickets
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_websocket_connection_ticket();

ALTER TABLE idenqa.websocket_connection_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.websocket_connection_tickets FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.websocket_connection_tickets
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.websocket_connection_tickets FROM PUBLIC;
