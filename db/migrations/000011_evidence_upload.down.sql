DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM idenqa.evidence_upload_intents) THEN
        RAISE EXCEPTION 'cannot roll back evidence upload schema after an upload intent has been recorded';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS evidence_upload_intent_audit_immutable
    ON idenqa.evidence_upload_intent_audit;
DROP TRIGGER IF EXISTS evidence_upload_intents_protected
    ON idenqa.evidence_upload_intents;
DROP TABLE IF EXISTS idenqa.evidence_upload_intent_audit;
DROP TABLE IF EXISTS idenqa.evidence_upload_intents;
DROP FUNCTION IF EXISTS idenqa.protect_evidence_upload_intent();
ALTER TABLE idenqa.capture_tokens
    DROP CONSTRAINT IF EXISTS capture_tokens_upload_binding_unique;
ALTER TABLE idenqa.verification_sessions
    DROP CONSTRAINT IF EXISTS verification_sessions_upload_snapshot_unique;
ALTER TABLE idenqa.capture_profile_revisions
    DROP CONSTRAINT IF EXISTS capture_profile_revisions_upload_snapshot_unique;
