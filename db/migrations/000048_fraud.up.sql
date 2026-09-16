ALTER TABLE idenqa.evidence_assets ADD CONSTRAINT evidence_fraud_verification UNIQUE(tenant_id,id,verification_id);

CREATE TABLE idenqa.fraud_configurations (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),version bigint NOT NULL CHECK(version>0),
 configuration jsonb NOT NULL,digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 actor_key_id text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,version),FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TABLE idenqa.fraud_keys (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),region text NOT NULL,key_version bigint NOT NULL DEFAULT 1 CHECK(key_version>0),wrapped_key jsonb NOT NULL,
 PRIMARY KEY(tenant_id,region)
);

CREATE TABLE idenqa.fraud_links (
 tenant_id text NOT NULL,verification_id text NOT NULL,evidence_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('subject','device','identifier','document','portrait','address','provider_event','network','capture')),
 namespace text NOT NULL,token text NOT NULL CHECK(token ~ '^[0-9a-f]{64}$'),region text NOT NULL,
 source_reference text NOT NULL,source_class text NOT NULL CHECK(source_class IN ('core','tenant_attested')),
 actor_key_id text,configuration_version bigint NOT NULL,observed_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,evidence_id,namespace,kind,source_reference),
 FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,evidence_id,verification_id) REFERENCES idenqa.evidence_assets(tenant_id,id,verification_id),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id),
 FOREIGN KEY(tenant_id,configuration_version) REFERENCES idenqa.fraud_configurations(tenant_id,version),
 CHECK(expires_at>observed_at)
);

CREATE INDEX fraud_correlate ON idenqa.fraud_links(tenant_id,region,kind,namespace,token,observed_at);
CREATE INDEX fraud_evidence_window ON idenqa.evidence_assets(tenant_id,region,created_at,id) WHERE state='available' AND integrity='verified';
CREATE INDEX fraud_expire ON idenqa.fraud_links(tenant_id,expires_at);

CREATE TABLE idenqa.fraud_receipts (
 tenant_id text NOT NULL,verification_id text NOT NULL,digest text NOT NULL,configuration_version bigint NOT NULL,
 receipt jsonb NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,digest),FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,configuration_version) REFERENCES idenqa.fraud_configurations(tenant_id,version)
);

CREATE INDEX fraud_receipts_verification ON idenqa.fraud_receipts(tenant_id,verification_id,recorded_at DESC,digest);

CREATE TABLE idenqa.fraud_proposals (
 tenant_id text NOT NULL,receipt_digest text NOT NULL,digest text NOT NULL,proposal jsonb NOT NULL,actor_key_id text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,digest),FOREIGN KEY(tenant_id,receipt_digest) REFERENCES idenqa.fraud_receipts(tenant_id,digest),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

-- Deleting ciphertext also removes its correlation material. Re-ingestion cannot
-- revive it because all writers require currently available evidence.
CREATE FUNCTION idenqa.erase_fraud_evidence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.state='deleted' AND OLD.state<>'deleted' THEN DELETE FROM idenqa.fraud_links WHERE tenant_id=NEW.tenant_id AND evidence_id=NEW.id; END IF; RETURN NEW; END $$;
CREATE TRIGGER erase_fraud_evidence AFTER UPDATE ON idenqa.evidence_assets FOR EACH ROW EXECUTE FUNCTION idenqa.erase_fraud_evidence();
DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['fraud_configurations','fraud_keys','fraud_links','fraud_receipts','fraud_proposals'] LOOP
 EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',n);
 EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',n);
 EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',n);
 EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',n);
 END LOOP;
 FOREACH n IN ARRAY ARRAY['fraud_configurations','fraud_receipts','fraud_proposals'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_fraud_record BEFORE UPDATE OR DELETE ON idenqa.%I FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change()',n);
 END LOOP;
END $$;

-- Installation-wide discovery exposes tenant IDs only, never graph material.
CREATE FUNCTION idenqa.list_expired_fraud_tenants(observed_at timestamptz,batch_size integer)
RETURNS TABLE(tenant_id text) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET row_security=off AS $$
 SELECT l.tenant_id FROM idenqa.fraud_links l WHERE  $2 BETWEEN 1 AND 100 AND l.expires_at<=$1
 AND NOT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=l.tenant_id AND h.aggregate_id=l.verification_id AND h.starts_at<=$1 AND h.released_at IS NULL)
 GROUP BY l.tenant_id ORDER BY min(l.expires_at),l.tenant_id LIMIT LEAST($2,100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_expired_fraud_tenants(timestamptz,integer) FROM PUBLIC;
