DROP TRIGGER IF EXISTS capture_journey_event_append_only ON idenqa.capture_journey_events;
DROP FUNCTION IF EXISTS idenqa.reject_capture_journey_event_change();
DROP TABLE IF EXISTS idenqa.capture_journey_events;
