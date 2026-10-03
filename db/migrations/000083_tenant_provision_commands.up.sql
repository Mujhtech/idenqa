CREATE TABLE idenqa.tenant_provision_commands (
    command_id text PRIMARY KEY CHECK (
        char_length(command_id) BETWEEN 16 AND 200 AND
        command_id = btrim(command_id) AND
        command_id !~ '[[:cntrl:]]'
    ),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    tenant_id text NOT NULL REFERENCES idenqa.tenants(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX tenant_provision_commands_tenant_idx
    ON idenqa.tenant_provision_commands (tenant_id);

REVOKE ALL ON idenqa.tenant_provision_commands FROM PUBLIC;
