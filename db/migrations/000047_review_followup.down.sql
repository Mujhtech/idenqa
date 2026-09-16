DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.review_appeal_history) OR EXISTS(SELECT 1 FROM idenqa.review_correction_intakes) OR EXISTS(SELECT 1 FROM idenqa.review_arbitrations) OR EXISTS(SELECT 1 FROM idenqa.review_correction_evaluations) OR EXISTS(SELECT 1 FROM idenqa.appeals WHERE requested_by IS NOT NULL) THEN RAISE EXCEPTION 'cannot remove review follow-up history';END IF;
END $$;
DROP TRIGGER record_review_appeal_history ON idenqa.appeals;
DROP FUNCTION idenqa.record_review_appeal_history();
DROP TABLE idenqa.review_appeal_history;
DROP FUNCTION idenqa.list_expired_review_appeals(timestamptz,integer);
DROP INDEX idenqa.one_open_appeal_per_case;
ALTER TABLE idenqa.appeals DROP CONSTRAINT appeal_state;
ALTER TABLE idenqa.appeals ADD CONSTRAINT appeal_state CHECK(state IN ('requested','independent_review','resolved','withdrawn','expired'));
ALTER TABLE idenqa.appeals DROP CONSTRAINT appeal_resolution;
ALTER TABLE idenqa.appeals ADD CONSTRAINT appeal_resolution CHECK((state<>'resolved' AND outcome IS NULL AND reason_code IS NULL AND superseding_decision_id IS NULL) OR (state='resolved' AND outcome IS NOT NULL AND reason_code IS NOT NULL AND ((outcome='overturned' AND superseding_decision_id IS NOT NULL) OR (outcome<>'overturned' AND superseding_decision_id IS NULL))));
ALTER TABLE idenqa.appeals DROP COLUMN requested_by;
DROP TABLE idenqa.review_correction_intakes,idenqa.review_correction_evaluations,idenqa.review_arbitrations;
