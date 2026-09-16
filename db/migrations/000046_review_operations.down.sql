DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.review_administration_history) OR EXISTS(SELECT 1 FROM idenqa.review_evidence_access) OR EXISTS(SELECT 1 FROM idenqa.review_case_settings) THEN
 RAISE EXCEPTION 'cannot remove persisted review operations history'; END IF;
END $$;
DROP TABLE idenqa.review_evidence_access,idenqa.review_case_operations,idenqa.review_case_settings,idenqa.review_administration_history,idenqa.review_policy_settings,idenqa.review_operator_assignments;
