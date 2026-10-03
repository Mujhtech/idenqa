CREATE TABLE idenqa.api_key_bridge_commands (
    command_id text PRIMARY KEY CHECK (
        char_length(command_id) BETWEEN 1 AND 200 AND
        command_id = btrim(command_id) AND
        command_id !~ '[[:cntrl:]]'
    ),
    tenant_id text NOT NULL REFERENCES idenqa.tenants(id) ON DELETE CASCADE,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    operation text NOT NULL CHECK (operation IN ('issue', 'rotate', 'revoke')),
    key_id text NOT NULL,
    delivery_algorithm text NOT NULL,
    delivery_ephemeral_public_key text NOT NULL,
    delivery_nonce text NOT NULL,
    delivery_ciphertext text NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT api_key_bridge_commands_key_fk
        FOREIGN KEY (tenant_id, key_id)
        REFERENCES idenqa.api_keys(tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX api_key_bridge_commands_tenant_created_idx
    ON idenqa.api_key_bridge_commands (tenant_id, created_at DESC, command_id DESC);

ALTER TABLE idenqa.api_key_bridge_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.api_key_bridge_commands FORCE ROW LEVEL SECURITY;

CREATE POLICY api_key_bridge_commands_tenant_isolation
    ON idenqa.api_key_bridge_commands
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.api_key_bridge_commands FROM PUBLIC;
