DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.admission_snapshots) OR EXISTS(SELECT 1 FROM idenqa.admission_operations) THEN
  RAISE EXCEPTION 'cannot discard populated admission history';
 END IF;
END $$;
DROP TRIGGER admission_verification_closed ON idenqa.verification_sessions;
DROP FUNCTION idenqa.admission_verification_closed();
DROP TABLE idenqa.admission_events,idenqa.admission_reservations,idenqa.admission_counters,idenqa.admission_operations,idenqa.admission_pending,idenqa.admission_heads,idenqa.admission_snapshots,idenqa.admission_limit_definitions;
DROP FUNCTION idenqa.admission_append_only(),idenqa.admission_operation_immutable(),idenqa.admission_reservation_immutable(),idenqa.admission_head_monotonic(),idenqa.admission_pending_protected();
