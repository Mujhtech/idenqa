ALTER TABLE idenqa.idempotency_records
    DROP CONSTRAINT idempotency_records_principal_type,
    DROP COLUMN principal_type,
    ADD COLUMN principal_type text GENERATED ALWAYS AS (
        CASE
            WHEN principal_id ~ '^key_' THEN 'api_key'
            WHEN principal_id ~ '^ctk_' THEN 'capture_token'
            WHEN principal_id ~ '^usr_' THEN 'workforce_user'
            WHEN principal_id ~ '^wld_' THEN 'managed_workload'
            ELSE 'invalid'
        END
    ) STORED,
    ADD CONSTRAINT idempotency_records_principal_type CHECK (
        (principal_type = 'api_key' AND principal_id ~ '^key_[0-9A-HJKMNP-TV-Z]{26}$') OR
        (principal_type = 'capture_token' AND principal_id ~ '^ctk_[0-9A-HJKMNP-TV-Z]{26}$') OR
        (principal_type = 'workforce_user' AND principal_id ~ '^usr_[A-Za-z0-9_-]{1,124}$') OR
        (principal_type = 'managed_workload' AND principal_id ~ '^wld_[A-Za-z0-9_-]{4,124}$')
    );

ALTER TABLE idenqa.verification_session_audit
    DROP CONSTRAINT verification_session_audit_actor_fk;

ALTER TABLE idenqa.verification_session_audit
    RENAME COLUMN actor_key_id TO actor_id;

ALTER TABLE idenqa.verification_session_audit
    ADD COLUMN delegated_actor_id text,
    ADD CONSTRAINT verification_session_audit_actor CHECK (
        actor_id ~ '^key_[0-9A-HJKMNP-TV-Z]{26}$' OR
        actor_id ~ '^wld_[A-Za-z0-9_-]{4,124}$'
    ),
    ADD CONSTRAINT verification_session_audit_delegated_actor CHECK (
        (actor_id ~ '^key_' AND delegated_actor_id IS NULL) OR
        (actor_id ~ '^wld_' AND delegated_actor_id ~ '^usr_[A-Za-z0-9_-]{1,124}$')
    );
