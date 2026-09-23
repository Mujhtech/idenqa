DROP TRIGGER capture_document_selection_guard ON idenqa.verification_sessions;
DROP FUNCTION idenqa.protect_capture_document_selection();
DROP TABLE idenqa.capture_document_selection_audit;
ALTER TABLE idenqa.verification_sessions DROP COLUMN document_selections;
