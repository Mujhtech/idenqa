ALTER TABLE idenqa.capture_profiles
    DROP CONSTRAINT capture_profiles_lifecycle,
    DROP CONSTRAINT capture_profiles_times;

ALTER TABLE idenqa.capture_profiles
    ADD CONSTRAINT capture_profiles_lifecycle CHECK (
        (state = 'draft' AND draft_revision IS NOT NULL AND published_revision IS NULL AND deactivated_at IS NULL) OR
        (state = 'active' AND published_revision IS NOT NULL AND deactivated_at IS NULL) OR
        (state = 'deactivated' AND draft_revision IS NULL AND published_revision IS NOT NULL AND deactivated_at IS NOT NULL)
    ),
    ADD CONSTRAINT capture_profiles_times CHECK (
        updated_at >= created_at AND
        (deactivated_at IS NULL OR deactivated_at = updated_at)
    );
