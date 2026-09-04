DROP INDEX IF EXISTS idenqa.capture_tokens_native_application;

ALTER TABLE idenqa.capture_tokens
    DROP CONSTRAINT IF EXISTS capture_tokens_native_binding_time,
    DROP CONSTRAINT IF EXISTS capture_tokens_native_binding_complete,
    DROP COLUMN IF EXISTS native_bound_at,
    DROP COLUMN IF EXISTS native_proof_key_digest,
    DROP COLUMN IF EXISTS native_application_id;

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
    RETURN NEW;
END;
$$;
