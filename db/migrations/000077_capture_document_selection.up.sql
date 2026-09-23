ALTER TABLE idenqa.verification_sessions
    ADD COLUMN document_selections jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT verification_document_selections_object CHECK (jsonb_typeof(document_selections) = 'object');

CREATE TABLE idenqa.capture_document_selection_audit (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 1),
    capture_token_id text NOT NULL,
    requirement_key text NOT NULL,
    document_type text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, verification_id, aggregate_version),
    FOREIGN KEY (tenant_id, verification_id) REFERENCES idenqa.verification_sessions (tenant_id, id),
    FOREIGN KEY (tenant_id, capture_token_id) REFERENCES idenqa.capture_tokens (tenant_id, id)
);
ALTER TABLE idenqa.capture_document_selection_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_document_selection_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON idenqa.capture_document_selection_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
REVOKE ALL ON idenqa.capture_document_selection_audit FROM PUBLIC;

CREATE FUNCTION idenqa.protect_capture_document_selection()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- This trigger runs even for an explicitly assigned, unchanged choice.
    -- A new selection command cannot advance a completed capture's version.
    IF OLD.state <> 'collecting' OR NEW.state <> 'collecting' OR
       OLD.capture_completed_at IS NOT NULL OR NEW.capture_completed_at IS NOT NULL THEN
        RAISE EXCEPTION 'document selection requires an incomplete collecting session';
    END IF;
    IF NEW.document_selections IS DISTINCT FROM OLD.document_selections THEN
        IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
            RAISE EXCEPTION 'document selection requires the next version and monotonic time';
        END IF;
        IF EXISTS (
            SELECT 1 FROM idenqa.evidence_upload_intents AS uploads
            WHERE uploads.tenant_id = OLD.tenant_id AND uploads.verification_id = OLD.id
              AND (NEW.document_selections -> uploads.requirement_key)
                  IS DISTINCT FROM (OLD.document_selections -> uploads.requirement_key)
        ) THEN
            RAISE EXCEPTION 'document selection is locked by upload history';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER capture_document_selection_guard
    BEFORE UPDATE OF document_selections ON idenqa.verification_sessions
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_capture_document_selection();

CREATE TRIGGER capture_document_selection_audit_append_only
    BEFORE UPDATE OR DELETE ON idenqa.capture_document_selection_audit
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();
