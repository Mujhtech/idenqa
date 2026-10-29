DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.admission_resource_sources) THEN RAISE EXCEPTION 'cannot discard populated dependent admission history'; END IF;
END $$;
DROP TRIGGER admission_upload_retry_safe ON idenqa.evidence_upload_intents;
DROP FUNCTION idenqa.admission_upload_retry_safe();
DROP TRIGGER admission_attempt_closed ON idenqa.verification_attempts;
DROP TRIGGER admission_review_closed ON idenqa.review_cases;
DROP TRIGGER admission_upload_closed ON idenqa.evidence_upload_intents;
DROP TRIGGER admission_object_cleaned ON idenqa.evidence_object_reconciliations;
DROP TRIGGER admission_evidence_deleted ON idenqa.evidence_assets;
DROP FUNCTION idenqa.admission_evidence_cleanup_proven(text,text),idenqa.admission_dependent_closed(),idenqa.admission_evidence_deleted(),idenqa.admission_resource_release(text,text,text,boolean);
DROP TABLE idenqa.admission_resource_sources;
