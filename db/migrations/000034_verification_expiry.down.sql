DROP FUNCTION idenqa.list_due_verification_expirations(timestamptz, integer);
DROP INDEX idenqa.verification_sessions_due_expiry;
ALTER TABLE idenqa.verification_sessions DROP COLUMN expiry_discovered_at;
