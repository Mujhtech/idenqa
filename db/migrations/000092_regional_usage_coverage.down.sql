DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.usage_source_clocks) THEN
  RAISE EXCEPTION 'usage coverage has durable state; use forward repair';
 END IF;
END $$;
DROP TABLE idenqa.usage_source_handoffs,idenqa.usage_source_clocks;
DROP FUNCTION idenqa.usage_handoff_immutable();
DROP FUNCTION idenqa.usage_source_clock_monotonic();
