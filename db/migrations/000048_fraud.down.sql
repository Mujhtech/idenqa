DROP INDEX idenqa.fraud_evidence_window;
DROP FUNCTION idenqa.list_expired_fraud_tenants(timestamptz,integer);
DROP TRIGGER erase_fraud_evidence ON idenqa.evidence_assets;
DROP FUNCTION idenqa.erase_fraud_evidence();
DROP TABLE idenqa.fraud_proposals,idenqa.fraud_receipts,idenqa.fraud_links,idenqa.fraud_keys,idenqa.fraud_configurations;
ALTER TABLE idenqa.evidence_assets DROP CONSTRAINT evidence_fraud_verification;
