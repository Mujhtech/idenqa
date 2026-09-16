DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.capture_recoveries) THEN RAISE EXCEPTION 'cannot discard capture recovery lineage'; END IF;
END $$;
DROP TABLE idenqa.capture_recovery_uploads;
DROP TABLE idenqa.capture_recoveries;
