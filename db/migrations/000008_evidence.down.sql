DROP TRIGGER IF EXISTS evidence_asset_audit_immutable ON idenqa.evidence_asset_audit;
DROP TRIGGER IF EXISTS evidence_assets_protected ON idenqa.evidence_assets;
DROP TABLE IF EXISTS idenqa.evidence_asset_audit;
DROP TABLE IF EXISTS idenqa.evidence_assets;
DROP FUNCTION IF EXISTS idenqa.protect_evidence_asset();
ALTER TABLE idenqa.subjects
    DROP CONSTRAINT IF EXISTS subjects_tenant_subject_verification_unique;
