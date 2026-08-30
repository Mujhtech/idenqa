ALTER TABLE idenqa.evidence_upload_intents
    ADD COLUMN fallback_condition text;

ALTER TABLE idenqa.evidence_upload_intents
    ADD CONSTRAINT evidence_upload_intents_fallback_condition CHECK (
        fallback_condition IS NULL OR
        fallback_condition IN (
            'capability_unavailable',
            'method_unavailable',
            'capture_failed'
        )
    );

CREATE FUNCTION idenqa.protect_evidence_upload_fallback_condition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.fallback_condition IS DISTINCT FROM OLD.fallback_condition THEN
        RAISE EXCEPTION 'evidence upload fallback condition is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_upload_fallback_condition_immutable
BEFORE UPDATE ON idenqa.evidence_upload_intents
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_evidence_upload_fallback_condition();
