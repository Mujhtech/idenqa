ALTER TABLE idenqa.verification_sessions
    ADD COLUMN region text;

ALTER TABLE idenqa.verification_sessions
    ADD CONSTRAINT verification_sessions_region CHECK (
        region IS NULL OR (
            char_length(region) BETWEEN 1 AND 63 AND
            region = btrim(region) AND
            region ~ '^[a-z][a-z0-9-]*$'
        )
    );

CREATE OR REPLACE FUNCTION idenqa.protect_verification_session_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR
       NEW.source_profile_id <> OLD.source_profile_id OR
       NEW.source_profile_revision <> OLD.source_profile_revision OR
       NEW.source_profile_digest <> OLD.source_profile_digest OR
       NEW.requirements <> OLD.requirements OR
       NEW.region IS DISTINCT FROM OLD.region OR
       NEW.created_at <> OLD.created_at OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'verification session identity, snapshot, placement, and lifetime are immutable';
    END IF;
    RETURN NEW;
END;
$$;
