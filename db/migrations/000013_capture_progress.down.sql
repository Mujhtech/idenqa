DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM idenqa.evidence_upload_intents
        WHERE fallback_condition IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back capture progress binding after a fallback upload has been recorded';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS evidence_upload_fallback_condition_immutable
    ON idenqa.evidence_upload_intents;
DROP FUNCTION IF EXISTS idenqa.protect_evidence_upload_fallback_condition();
ALTER TABLE idenqa.evidence_upload_intents
    DROP CONSTRAINT IF EXISTS evidence_upload_intents_fallback_condition;
ALTER TABLE idenqa.evidence_upload_intents
    DROP COLUMN IF EXISTS fallback_condition;
