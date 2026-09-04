DROP TRIGGER IF EXISTS deletion_tombstones_immutable ON idenqa.deletion_tombstones;
DROP TRIGGER IF EXISTS retention_bindings_immutable ON idenqa.retention_bindings;
DROP TABLE IF EXISTS idenqa.deletion_tombstones;
DROP TABLE IF EXISTS idenqa.deletion_targets;
DROP TABLE IF EXISTS idenqa.deletion_requests;
DROP TABLE IF EXISTS idenqa.legal_holds;
DROP TABLE IF EXISTS idenqa.retention_bindings;
DROP FUNCTION IF EXISTS idenqa.protect_privacy_history();
