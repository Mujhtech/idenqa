CREATE TABLE idenqa.api_keys (
    id text NOT NULL,
    tenant_id text NOT NULL,
    label text NOT NULL,
    digest bytea NOT NULL,
    pepper_version integer NOT NULL,
    requested_scopes text[] NOT NULL,
    resolved_scopes text[] NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz,
    revoked_at timestamptz,
    retired_at timestamptz,
    replaces_key_id text,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT api_keys_tenant_fk FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT api_keys_replaces_fk FOREIGN KEY (tenant_id, replaces_key_id)
        REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT api_keys_id_format CHECK (id ~ '^key_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT api_keys_tenant_id_format CHECK (tenant_id ~ '^ten_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT api_keys_label CHECK (
        char_length(label) BETWEEN 1 AND 100 AND
        label = btrim(label) AND
        label !~ '[[:cntrl:]]'
    ),
    CONSTRAINT api_keys_digest CHECK (octet_length(digest) = 32),
    CONSTRAINT api_keys_pepper_version CHECK (pepper_version BETWEEN 1 AND 65535),
    CONSTRAINT api_keys_requested_scopes CHECK (
        cardinality(requested_scopes) > 0 AND
        array_position(requested_scopes, NULL) IS NULL AND
        array_to_string(requested_scopes, ',') ~
            '^([a-z][a-z0-9_]{0,63}|\*):([a-z][a-z0-9_]{0,63}|\*)(,([a-z][a-z0-9_]{0,63}|\*):([a-z][a-z0-9_]{0,63}|\*))*$'
    ),
    CONSTRAINT api_keys_resolved_scopes CHECK (
        cardinality(resolved_scopes) > 0 AND
        array_position(resolved_scopes, NULL) IS NULL AND
        array_to_string(resolved_scopes, ',') ~
            '^[a-z][a-z0-9_]{0,63}:[a-z][a-z0-9_]{0,63}(,[a-z][a-z0-9_]{0,63}:[a-z][a-z0-9_]{0,63})*$'
    ),
    CONSTRAINT api_keys_version CHECK (version > 0),
    CONSTRAINT api_keys_times CHECK (
        updated_at >= created_at AND
        (expires_at IS NULL OR expires_at > created_at) AND
        (revoked_at IS NULL OR revoked_at = updated_at) AND
        (retired_at IS NULL OR retired_at >= updated_at)
    ),
    CONSTRAINT api_keys_replacement CHECK (replaces_key_id IS NULL OR replaces_key_id <> id)
);

CREATE UNIQUE INDEX api_keys_single_successor
    ON idenqa.api_keys (tenant_id, replaces_key_id)
    WHERE replaces_key_id IS NOT NULL;

CREATE INDEX api_keys_tenant_created
    ON idenqa.api_keys (tenant_id, created_at DESC, id DESC);

ALTER TABLE idenqa.api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.api_keys FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.api_keys
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.api_keys FROM PUBLIC;
