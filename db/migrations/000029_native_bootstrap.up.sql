ALTER TABLE idenqa.capture_tokens
    ADD COLUMN native_application_id text,
    ADD COLUMN native_proof_key_digest text,
    ADD COLUMN native_bound_at timestamptz,
    ADD CONSTRAINT capture_tokens_native_binding_complete CHECK (
        (native_application_id IS NULL AND native_proof_key_digest IS NULL AND native_bound_at IS NULL) OR
        (native_application_id ~ '^[A-Za-z0-9._-]{1,255}$' AND
         native_proof_key_digest ~ '^[0-9a-f]{64}$' AND native_bound_at IS NOT NULL)
    ),
    ADD CONSTRAINT capture_tokens_native_binding_time CHECK (
        native_bound_at IS NULL OR (native_bound_at >= issued_at AND native_bound_at < expires_at)
    );

CREATE INDEX capture_tokens_native_application
    ON idenqa.capture_tokens (tenant_id, native_application_id)
    WHERE native_application_id IS NOT NULL;

CREATE OR REPLACE FUNCTION idenqa.protect_capture_token_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.verification_id <> OLD.verification_id OR
       NEW.key_version <> OLD.key_version OR NEW.issued_at <> OLD.issued_at OR
       NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'capture token identity and signed claims are immutable';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'capture token revocation is irreversible';
    END IF;
    IF OLD.native_bound_at IS NOT NULL AND
       ROW(NEW.native_application_id, NEW.native_proof_key_digest, NEW.native_bound_at)
       IS DISTINCT FROM
       ROW(OLD.native_application_id, OLD.native_proof_key_digest, OLD.native_bound_at) THEN
        RAISE EXCEPTION 'capture token native binding is immutable';
    END IF;
    IF OLD.native_bound_at IS NULL AND NEW.native_bound_at IS NOT NULL AND
       NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'capture token binding and revocation must be separate transitions';
    END IF;
    RETURN NEW;
END;
$$;
