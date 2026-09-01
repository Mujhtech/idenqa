DROP TRIGGER IF EXISTS verification_decision_append_only ON idenqa.verification_decisions;
DROP TRIGGER IF EXISTS policy_evaluation_append_only ON idenqa.policy_evaluations;
DROP TRIGGER IF EXISTS policy_snapshot_append_only ON idenqa.policy_snapshots;
DROP FUNCTION IF EXISTS idenqa.reject_policy_record_change();
DROP TABLE IF EXISTS idenqa.verification_decisions;
DROP TABLE IF EXISTS idenqa.policy_evaluations;
DROP TABLE IF EXISTS idenqa.policy_snapshots;
