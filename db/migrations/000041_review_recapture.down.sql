DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.review_recaptures) THEN RAISE EXCEPTION 'linked recapture history prevents downgrade'; END IF;
END; $$;
DROP TABLE idenqa.review_recaptures;
