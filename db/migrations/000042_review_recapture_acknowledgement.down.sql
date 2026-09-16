DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.review_recapture_acknowledgements) THEN RAISE EXCEPTION 'recapture acknowledgements prevent downgrade'; END IF;
END; $$;
DROP TABLE idenqa.review_recapture_acknowledgements;
ALTER TABLE idenqa.review_recaptures DROP CONSTRAINT review_recaptures_exact_child;
