-- Do not discard accepted review lineage on downgrade.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.review_evaluation_requests) THEN RAISE EXCEPTION 'review evaluation requests prevent downgrade'; END IF;
END; $$;
DROP FUNCTION idenqa.list_ready_review_evaluations(timestamptz,integer);
DROP TABLE idenqa.review_evaluations;
DROP TABLE idenqa.review_evaluation_requests;
DROP TRIGGER protect_review_finding_rules ON idenqa.review_cases;
DROP FUNCTION idenqa.protect_review_finding_rules();
ALTER TABLE idenqa.review_cases DROP COLUMN permitted_findings;
ALTER TABLE idenqa.review_cases DROP CONSTRAINT review_case_state;
ALTER TABLE idenqa.review_cases ADD CONSTRAINT review_case_state CHECK (state IN ('open','claimed','awaiting_second','resolved'));
