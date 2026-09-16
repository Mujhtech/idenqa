DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.review_recapture_evaluation_requests) THEN
  RAISE EXCEPTION 'cannot remove persisted recapture evaluation lineage';
 END IF;
END $$;
DROP TABLE idenqa.review_recapture_evaluation_requests;
ALTER TABLE idenqa.review_recapture_acknowledgements DROP CONSTRAINT review_acknowledgement_exact_decision;
