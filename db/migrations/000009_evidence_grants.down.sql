DROP TRIGGER IF EXISTS evidence_grant_access_attempt_audit_immutable
    ON idenqa.evidence_grant_access_attempt_audit;
DROP TRIGGER IF EXISTS evidence_processing_grant_audit_immutable
    ON idenqa.evidence_processing_grant_audit;
DROP TRIGGER IF EXISTS evidence_grant_redemption_outcomes_immutable
    ON idenqa.evidence_grant_redemption_outcomes;
DROP TRIGGER IF EXISTS evidence_grant_redemption_outcomes_validated
    ON idenqa.evidence_grant_redemption_outcomes;
DROP TRIGGER IF EXISTS evidence_grant_redemptions_validated
    ON idenqa.evidence_grant_redemptions;
DROP TRIGGER IF EXISTS evidence_grant_redemptions_immutable
    ON idenqa.evidence_grant_redemptions;
DROP TRIGGER IF EXISTS evidence_processing_grants_protected
    ON idenqa.evidence_processing_grants;
DROP FUNCTION IF EXISTS idenqa.protect_evidence_processing_grant();
DROP FUNCTION IF EXISTS idenqa.validate_evidence_grant_redemption_outcome();
DROP FUNCTION IF EXISTS idenqa.validate_evidence_grant_redemption();
DROP TABLE IF EXISTS idenqa.evidence_grant_access_attempt_audit;
DROP TABLE IF EXISTS idenqa.evidence_processing_grant_audit;
DROP TABLE IF EXISTS idenqa.evidence_grant_redemption_outcomes;
DROP TABLE IF EXISTS idenqa.evidence_grant_redemptions;
DROP TABLE IF EXISTS idenqa.evidence_processing_grants;
ALTER TABLE idenqa.evidence_assets
    DROP CONSTRAINT IF EXISTS evidence_assets_grant_binding_unique;
ALTER TABLE idenqa.subject_responses
    DROP CONSTRAINT IF EXISTS subject_responses_grant_binding_unique;
ALTER TABLE idenqa.processing_authorities
    DROP CONSTRAINT IF EXISTS processing_authorities_grant_binding_unique;
