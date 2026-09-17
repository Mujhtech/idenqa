CREATE TABLE idenqa.outcome_tokens (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    key_version integer NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, verification_id),
    CONSTRAINT outcome_tokens_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT outcome_tokens_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT outcome_tokens_id_format
        CHECK (id ~ '^otk_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT outcome_tokens_key_version
        CHECK (key_version BETWEEN 1 AND 65535),
    CONSTRAINT outcome_tokens_times CHECK (
        expires_at > issued_at AND
        (revoked_at IS NULL OR revoked_at >= issued_at)
    )
);

CREATE INDEX outcome_tokens_expiry
    ON idenqa.outcome_tokens (expires_at)
    WHERE revoked_at IS NULL;

CREATE OR REPLACE FUNCTION idenqa.protect_outcome_token_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.verification_id <> OLD.verification_id OR
       NEW.key_version <> OLD.key_version OR NEW.issued_at <> OLD.issued_at OR
       NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'outcome token identity and signed claims are immutable';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'outcome token revocation is irreversible';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outcome_token_record_immutable
BEFORE UPDATE ON idenqa.outcome_tokens
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_outcome_token_record();

ALTER TABLE idenqa.outcome_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.outcome_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.outcome_tokens
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.outcome_tokens FROM PUBLIC;
