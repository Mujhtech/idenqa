-- A downgrade must fail if workload declarations remain; never rewrite their
-- audit identity into a tenant API key.
ALTER TABLE idenqa.verification_session_audit
    DROP CONSTRAINT verification_session_audit_actor_fk,
    DROP CONSTRAINT verification_session_audit_delegated_actor,
    DROP COLUMN actor_api_key,
    ADD CONSTRAINT verification_session_audit_delegated_actor CHECK (
        (actor_id ~ '^key_' AND delegated_actor_id IS NULL) OR
        (actor_id ~ '^wld_' AND delegated_actor_id ~ '^usr_[A-Za-z0-9_-]{1,124}$')
    );

ALTER TABLE idenqa.authority_audit
    DROP CONSTRAINT authority_audit_actor_type,
    ADD CONSTRAINT authority_audit_actor_type CHECK (actor_type = 'api_key');

ALTER TABLE idenqa.processing_authorities
    DROP CONSTRAINT processing_authorities_actor_fk,
    DROP CONSTRAINT processing_authorities_actor_format,
    DROP COLUMN created_by_api_key,
    ADD CONSTRAINT processing_authorities_actor_fk
        FOREIGN KEY (tenant_id, created_by)
        REFERENCES idenqa.api_keys (tenant_id, id);
