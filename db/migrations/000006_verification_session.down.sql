DROP TABLE IF EXISTS idenqa.outbox_events;
DROP TABLE IF EXISTS idenqa.verification_session_audit;
DROP TABLE IF EXISTS idenqa.capture_tokens;
DROP FUNCTION IF EXISTS idenqa.protect_capture_token_record();
DROP TABLE IF EXISTS idenqa.verification_sessions;
DROP FUNCTION IF EXISTS idenqa.protect_verification_session_snapshot();
