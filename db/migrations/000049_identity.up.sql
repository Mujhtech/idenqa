ALTER TABLE idenqa.deletion_requests ADD COLUMN backup_retention_seconds bigint NOT NULL DEFAULT 0 CHECK(backup_retention_seconds BETWEEN 0 AND 3024000);
-- Persistent subjects supplement the verification-local subjects from migration 7.
-- Existing authority, evidence and decision references are deliberately preserved.
CREATE TABLE idenqa.identity_subjects (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id), id text NOT NULL,
 region text NOT NULL, state text NOT NULL CHECK(state IN ('active','suspended','deleting','deleted')),
 version bigint NOT NULL CHECK(version>0), external_cipher bytea, external_token text,
 wrapped_key jsonb, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 deletion_id text, erased_at timestamptz,
 PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,id,region),
 CHECK(id ~ '^sub_[0-9A-HJKMNP-TV-Z]{26}$'),
 CHECK(region ~ '^[a-z0-9_-]{1,64}$'), CHECK(updated_at>=created_at),
 CHECK(external_token IS NULL OR external_token ~ '^[0-9a-f]{64}$'),
 FOREIGN KEY(tenant_id,deletion_id) REFERENCES idenqa.deletion_requests(tenant_id,id)
);

CREATE UNIQUE INDEX identity_subject_external ON idenqa.identity_subjects(tenant_id,region,external_token) WHERE external_token IS NOT NULL;

CREATE TABLE idenqa.identity_keys (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),region text NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version=1),wrapped_key jsonb NOT NULL,
 PRIMARY KEY(tenant_id,region)
);

CREATE TABLE idenqa.identity_subject_verifications (
 tenant_id text NOT NULL, subject_id text NOT NULL, verification_id text NOT NULL,
 actor_key_id text NOT NULL, linked_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,verification_id), UNIQUE(tenant_id,subject_id,verification_id),
 FOREIGN KEY(tenant_id,subject_id) REFERENCES idenqa.identity_subjects(tenant_id,id),
 FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE INDEX identity_subject_sessions ON idenqa.identity_subject_verifications(tenant_id,subject_id,verification_id);

CREATE TABLE idenqa.identity_records (
 tenant_id text NOT NULL, id text NOT NULL, subject_id text NOT NULL, verification_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('observation','fact','claim','identifier')),
 name text NOT NULL, sequence bigint NOT NULL CHECK(sequence>0),series_id text NOT NULL,
 supersedes text,metadata jsonb NOT NULL,recorded_at timestamptz NOT NULL,retain_until timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,subject_id,id),UNIQUE(tenant_id,subject_id,sequence),
 UNIQUE(tenant_id,id,subject_id,verification_id),
 FOREIGN KEY(tenant_id,subject_id,verification_id) REFERENCES idenqa.identity_subject_verifications(tenant_id,subject_id,verification_id),
 FOREIGN KEY(tenant_id,subject_id,supersedes) REFERENCES idenqa.identity_records(tenant_id,subject_id,id),
 FOREIGN KEY(tenant_id,subject_id,series_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id) DEFERRABLE INITIALLY DEFERRED,
 CHECK(id ~ '^(obs|fct|clm|idi)_[0-9A-HJKMNP-TV-Z]{26}$'),
 CHECK(name ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
 CHECK(retain_until>recorded_at),CHECK(supersedes IS NULL OR supersedes<>id)
);

CREATE UNIQUE INDEX identity_record_successor ON idenqa.identity_records(tenant_id,supersedes) WHERE supersedes IS NOT NULL;
CREATE INDEX identity_record_expiry ON idenqa.identity_records(tenant_id,retain_until,subject_id,id);
CREATE INDEX identity_record_page ON idenqa.identity_records(tenant_id,subject_id,sequence,id);

CREATE TABLE idenqa.identity_record_values (
 tenant_id text NOT NULL, record_id text NOT NULL, subject_id text NOT NULL, ciphertext bytea NOT NULL,
 PRIMARY KEY(tenant_id,record_id),
 FOREIGN KEY(tenant_id,subject_id,record_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id)
);

CREATE INDEX identity_subject_values ON idenqa.identity_record_values(tenant_id,subject_id,record_id);

CREATE TABLE idenqa.identity_record_edges (
 tenant_id text NOT NULL,subject_id text NOT NULL,record_id text NOT NULL,source_id text NOT NULL,
 PRIMARY KEY(tenant_id,record_id,source_id),
 FOREIGN KEY(tenant_id,subject_id,record_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id),
 FOREIGN KEY(tenant_id,subject_id,source_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id),
 CHECK(record_id<>source_id)
);

CREATE INDEX identity_record_dependents ON idenqa.identity_record_edges(tenant_id,source_id,record_id);

CREATE TABLE idenqa.identity_record_evidence (
 tenant_id text NOT NULL,subject_id text NOT NULL,verification_id text NOT NULL,record_id text NOT NULL,evidence_id text NOT NULL,
 PRIMARY KEY(tenant_id,record_id,evidence_id),
 FOREIGN KEY(tenant_id,subject_id,record_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id),
 FOREIGN KEY(tenant_id,subject_id,verification_id) REFERENCES idenqa.identity_subject_verifications(tenant_id,subject_id,verification_id),
 FOREIGN KEY(tenant_id,evidence_id,verification_id) REFERENCES idenqa.evidence_assets(tenant_id,id,verification_id)
);

CREATE INDEX identity_evidence_records ON idenqa.identity_record_evidence(tenant_id,evidence_id,record_id);

CREATE TABLE idenqa.identity_current (
 tenant_id text NOT NULL,subject_id text NOT NULL,kind text NOT NULL,name text NOT NULL,series_id text NOT NULL,record_id text NOT NULL,
 PRIMARY KEY(tenant_id,subject_id,series_id),
 FOREIGN KEY(tenant_id,subject_id,record_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id)
);

CREATE INDEX identity_current_name ON idenqa.identity_current(tenant_id,subject_id,kind,name,record_id);

CREATE TABLE idenqa.identity_identifier_tokens (
 tenant_id text NOT NULL,subject_id text NOT NULL,record_id text NOT NULL,region text NOT NULL,
 namespace text NOT NULL,issuer text NOT NULL,key_version bigint NOT NULL CHECK(key_version=1),token text NOT NULL CHECK(token ~ '^[0-9a-f]{64}$'),
 PRIMARY KEY(tenant_id,record_id),
 FOREIGN KEY(tenant_id,subject_id,record_id) REFERENCES idenqa.identity_records(tenant_id,subject_id,id)
);

CREATE INDEX identity_identifier_lookup ON idenqa.identity_identifier_tokens(tenant_id,region,namespace,issuer,key_version,token,subject_id);

CREATE TABLE idenqa.identity_configurations (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),region text NOT NULL,version bigint NOT NULL CHECK(version>0),
 configuration jsonb NOT NULL,digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),actor_key_id text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,region,version),CHECK(region=configuration->>'region'),FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TABLE idenqa.identity_receipts (
 tenant_id text NOT NULL,verification_id text NOT NULL,subject_id text,digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 receipt jsonb NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,digest),FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),FOREIGN KEY(tenant_id,subject_id,verification_id) REFERENCES idenqa.identity_subject_verifications(tenant_id,subject_id,verification_id)
);

CREATE INDEX identity_receipts_session ON idenqa.identity_receipts(tenant_id,verification_id,recorded_at,digest);

DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['identity_subjects','identity_keys','identity_subject_verifications','identity_records','identity_record_values','identity_record_edges','identity_record_evidence','identity_current','identity_identifier_tokens','identity_configurations','identity_receipts'] LOOP
 EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',n);
 EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',n);
 EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',n);
 EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',n);
 END LOOP;
 FOREACH n IN ARRAY ARRAY['identity_records','identity_record_edges','identity_record_evidence','identity_configurations','identity_receipts','identity_subject_verifications'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_identity_record BEFORE UPDATE OR DELETE ON idenqa.%I FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change()',n);
 END LOOP;
END $$;
-- Exact evidence erasure also removes encrypted derived identity values and lookup
-- indexes. Immutable reference-only provenance remains available to old decisions.
CREATE FUNCTION idenqa.erase_identity_evidence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.state='deleted' AND OLD.state<>'deleted' THEN
 DELETE FROM idenqa.identity_identifier_tokens t WHERE t.tenant_id=NEW.tenant_id AND t.record_id IN
 (SELECT record_id FROM idenqa.identity_record_evidence WHERE tenant_id=NEW.tenant_id AND evidence_id=NEW.id);
 DELETE FROM idenqa.identity_record_values v WHERE v.tenant_id=NEW.tenant_id AND v.record_id IN
 (SELECT record_id FROM idenqa.identity_record_evidence WHERE tenant_id=NEW.tenant_id AND evidence_id=NEW.id);
 END IF; RETURN NEW; END $$;

CREATE TRIGGER erase_identity_evidence AFTER UPDATE ON idenqa.evidence_assets FOR EACH ROW EXECUTE FUNCTION idenqa.erase_identity_evidence();

CREATE FUNCTION idenqa.list_expired_identity_tenants(observed_at timestamptz,batch_size integer)
RETURNS TABLE(tenant_id text) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET row_security=off AS $$
 SELECT r.tenant_id FROM idenqa.identity_records r JOIN idenqa.identity_record_values v ON v.tenant_id=r.tenant_id AND v.record_id=r.id
 WHERE $2 BETWEEN 1 AND 100 AND r.retain_until<=$1
 AND NOT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=r.tenant_id AND (h.aggregate_id=r.subject_id OR h.aggregate_id IN(SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=r.tenant_id AND subject_id=r.subject_id)) AND h.starts_at<=$1 AND h.released_at IS NULL)
 GROUP BY r.tenant_id ORDER BY min(r.retain_until),r.tenant_id LIMIT LEAST($2,100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_expired_identity_tenants(timestamptz,integer) FROM PUBLIC;

CREATE OR REPLACE FUNCTION idenqa.list_expired_fraud_tenants(observed_at timestamptz,batch_size integer)
RETURNS TABLE(tenant_id text) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET row_security=off AS $$
 SELECT l.tenant_id FROM idenqa.fraud_links l WHERE  $2 BETWEEN 1 AND 100 AND l.expires_at<=$1
 AND NOT EXISTS(SELECT 1 FROM idenqa.legal_holds h WHERE h.tenant_id=l.tenant_id AND (h.aggregate_id=l.verification_id OR h.aggregate_id IN(SELECT subject_id FROM idenqa.identity_subject_verifications WHERE tenant_id=l.tenant_id AND verification_id=l.verification_id)) AND h.starts_at<=$1 AND h.released_at IS NULL)
 GROUP BY l.tenant_id ORDER BY min(l.expires_at),l.tenant_id LIMIT LEAST($2,100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_expired_fraud_tenants(timestamptz,integer) FROM PUBLIC;
