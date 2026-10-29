-- Preserve the tenant API-key FK for ordinary declarations while allowing only
-- an explicitly identified managed workload as the other declaration actor.
ALTER TABLE idenqa.processing_authorities
    DROP CONSTRAINT processing_authorities_actor_fk,
    ADD COLUMN created_by_api_key text GENERATED ALWAYS AS (
        CASE WHEN created_by ~ '^key_' THEN created_by ELSE NULL END
    ) STORED,
    ADD CONSTRAINT processing_authorities_actor_format CHECK (
        created_by ~ '^key_[0-9A-HJKMNP-TV-Z]{26}$' OR
        created_by ~ '^wld_[A-Za-z0-9_-]{4,124}$'
    ),
    ADD CONSTRAINT processing_authorities_actor_fk
        FOREIGN KEY (tenant_id, created_by_api_key)
        REFERENCES idenqa.api_keys (tenant_id, id);

ALTER TABLE idenqa.authority_audit
    DROP CONSTRAINT authority_audit_actor_type,
    ADD CONSTRAINT authority_audit_actor_type CHECK (
        actor_type = 'api_key' OR
        (actor_type = 'managed_workload' AND action = 'declare' AND
         actor_id ~ '^wld_[A-Za-z0-9_-]{4,124}$')
    );

ALTER TABLE idenqa.verification_session_audit
    DROP CONSTRAINT verification_session_audit_delegated_actor,
    ADD COLUMN actor_api_key text GENERATED ALWAYS AS (
        CASE WHEN actor_id ~ '^key_' THEN actor_id ELSE NULL END
    ) STORED,
    ADD CONSTRAINT verification_session_audit_actor_fk
        FOREIGN KEY (tenant_id, actor_api_key)
        REFERENCES idenqa.api_keys (tenant_id, id),
    ADD CONSTRAINT verification_session_audit_delegated_actor CHECK (
        (actor_id ~ '^key_' AND delegated_actor_id IS NULL) OR
        (actor_id ~ '^wld_' AND delegated_actor_id IS NOT NULL AND
         delegated_actor_id ~ '^usr_[A-Za-z0-9_-]{1,124}$')
    );
