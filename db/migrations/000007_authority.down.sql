DROP TRIGGER IF EXISTS authority_audit_immutable ON idenqa.authority_audit;
DROP TRIGGER IF EXISTS subject_responses_immutable ON idenqa.subject_responses;
DROP TRIGGER IF EXISTS subjects_immutable ON idenqa.subjects;
DROP TRIGGER IF EXISTS notice_versions_immutable ON idenqa.notice_versions;
DROP TRIGGER IF EXISTS notice_version_audit_immutable ON idenqa.notice_version_audit;
DROP TRIGGER IF EXISTS processing_authority_declaration_immutable ON idenqa.processing_authorities;
DROP TRIGGER IF EXISTS verification_authority_binding_immutable ON idenqa.verification_sessions;

DELETE FROM idenqa.idempotency_records
WHERE principal_type = 'capture_token';

ALTER TABLE idenqa.idempotency_records
    DROP CONSTRAINT IF EXISTS idempotency_records_principal_type,
    DROP COLUMN IF EXISTS principal_type,
    ADD CONSTRAINT idempotency_records_principal_fk
        FOREIGN KEY (tenant_id, principal_id)
        REFERENCES idenqa.api_keys (tenant_id, id);

ALTER TABLE idenqa.verification_sessions
    DROP CONSTRAINT IF EXISTS verification_sessions_notice_fk,
    DROP CONSTRAINT IF EXISTS verification_sessions_authority_fk,
    DROP CONSTRAINT IF EXISTS verification_sessions_subject_fk,
    DROP CONSTRAINT IF EXISTS verification_sessions_authority_binding,
    DROP COLUMN IF EXISTS notice_id,
    DROP COLUMN IF EXISTS authority_id,
    DROP COLUMN IF EXISTS subject_id;

DROP TABLE IF EXISTS idenqa.authority_audit;
DROP TABLE IF EXISTS idenqa.subject_responses;
DROP TABLE IF EXISTS idenqa.processing_authorities;
DROP TABLE IF EXISTS idenqa.subjects;
DROP TABLE IF EXISTS idenqa.notice_version_audit;
DROP TABLE IF EXISTS idenqa.notice_versions;

DROP FUNCTION IF EXISTS idenqa.reject_immutable_authority_row();
DROP FUNCTION IF EXISTS idenqa.protect_processing_authority();
DROP FUNCTION IF EXISTS idenqa.protect_authority_binding();
