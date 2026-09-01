DROP TRIGGER IF EXISTS policy_activation_append_only ON idenqa.policy_activations;
DROP TRIGGER IF EXISTS policy_revision_append_only ON idenqa.policy_revisions;
DROP FUNCTION IF EXISTS idenqa.reject_policy_catalog_record_change();
DROP TABLE IF EXISTS idenqa.policy_activations;
ALTER TABLE idenqa.policies DROP CONSTRAINT IF EXISTS policies_active_revision_fk;
DROP TABLE IF EXISTS idenqa.policy_revisions;
DROP TABLE IF EXISTS idenqa.policies;
