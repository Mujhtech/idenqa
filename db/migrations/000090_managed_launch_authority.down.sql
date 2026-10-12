ALTER TABLE idenqa.verification_session_audit
    DROP CONSTRAINT verification_session_audit_delegated_actor,
    DROP CONSTRAINT verification_session_audit_actor,
    DROP COLUMN delegated_actor_id;

ALTER TABLE idenqa.verification_session_audit
    RENAME COLUMN actor_id TO actor_key_id;

ALTER TABLE idenqa.verification_session_audit
    ADD CONSTRAINT verification_session_audit_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id)
        REFERENCES idenqa.api_keys (tenant_id, id);

ALTER TABLE idenqa.idempotency_records
    DROP CONSTRAINT idempotency_records_principal_type,
    DROP COLUMN principal_type,
    ADD COLUMN principal_type text GENERATED ALWAYS AS (
        CASE
            WHEN principal_id ~ '^key_' THEN 'api_key'
            WHEN principal_id ~ '^ctk_' THEN 'capture_token'
            ELSE 'invalid'
        END
    ) STORED,
    ADD CONSTRAINT idempotency_records_principal_type CHECK (
        (principal_type = 'api_key' AND principal_id ~ '^key_[0-9A-HJKMNP-TV-Z]{26}$') OR
        (principal_type = 'capture_token' AND principal_id ~ '^ctk_[0-9A-HJKMNP-TV-Z]{26}$')
    );
