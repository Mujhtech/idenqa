DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM idenqa.evidence_object_reconciliations) THEN
        RAISE EXCEPTION 'cannot roll back evidence reconciliation schema after an obligation has been recorded';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS evidence_object_reconciliation_audit_immutable
    ON idenqa.evidence_object_reconciliation_audit;
DROP TRIGGER IF EXISTS evidence_object_reconciliations_protected
    ON idenqa.evidence_object_reconciliations;
DROP TABLE IF EXISTS idenqa.evidence_object_reconciliation_audit;
DROP TABLE IF EXISTS idenqa.evidence_object_reconciliations;
DROP FUNCTION IF EXISTS idenqa.protect_evidence_object_reconciliation();
