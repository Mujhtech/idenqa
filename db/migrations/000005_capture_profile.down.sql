DROP TABLE IF EXISTS idenqa.idempotency_records;
DROP TABLE IF EXISTS idenqa.capture_profile_audit;
DROP TRIGGER IF EXISTS capture_profile_revision_immutable ON idenqa.capture_profile_revisions;
DROP FUNCTION IF EXISTS idenqa.protect_capture_profile_revision();
ALTER TABLE IF EXISTS idenqa.capture_profiles
    DROP CONSTRAINT IF EXISTS capture_profiles_draft_revision_fk,
    DROP CONSTRAINT IF EXISTS capture_profiles_published_revision_fk;
DROP TABLE IF EXISTS idenqa.capture_profile_revisions;
DROP TABLE IF EXISTS idenqa.capture_profiles;
