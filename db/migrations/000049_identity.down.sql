-- A rollback must not silently discard persistent identity or decision provenance.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM idenqa.identity_subjects) OR EXISTS(SELECT 1 FROM idenqa.identity_configurations) OR EXISTS(SELECT 1 FROM idenqa.identity_receipts) THEN
 RAISE EXCEPTION 'cannot remove populated identity model';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION idenqa.list_expired_fraud_tenants(observed_at timestamptz,batch_size integer)
RETURNS TABLE(tenant_id text) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET row_security=off AS $$
 SELECT l.tenant_id FROM idenqa.fraud_links l WHERE  $2 BETWEEN 1 AND 100 AND l.expires_at<=$1
 AND NOT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=l.tenant_id AND h.aggregate_id=l.verification_id AND h.starts_at<=$1 AND h.released_at IS NULL)
 GROUP BY l.tenant_id ORDER BY min(l.expires_at),l.tenant_id LIMIT LEAST($2,100);
$$;
REVOKE ALL ON FUNCTION idenqa.list_expired_fraud_tenants(timestamptz,integer) FROM PUBLIC;

DROP FUNCTION idenqa.list_expired_identity_tenants(timestamptz,integer);
DROP TRIGGER erase_identity_evidence ON idenqa.evidence_assets;
DROP FUNCTION idenqa.erase_identity_evidence();
DROP TABLE idenqa.identity_receipts,idenqa.identity_configurations,idenqa.identity_identifier_tokens,idenqa.identity_current,
 idenqa.identity_record_evidence,idenqa.identity_record_edges,idenqa.identity_record_values,idenqa.identity_records,
 idenqa.identity_subject_verifications,idenqa.identity_keys,idenqa.identity_subjects;

ALTER TABLE idenqa.deletion_requests DROP COLUMN backup_retention_seconds;
