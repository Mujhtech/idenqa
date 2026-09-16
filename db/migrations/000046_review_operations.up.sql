CREATE TABLE idenqa.review_operator_assignments (
 tenant_id text NOT NULL,api_key_id text NOT NULL,operator_id text NOT NULL,version bigint NOT NULL CHECK(version>0),
 assignment jsonb NOT NULL,revoked boolean NOT NULL DEFAULT false,updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,api_key_id),FOREIGN KEY(tenant_id,api_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TABLE idenqa.review_policy_settings (
 tenant_id text NOT NULL,policy_id text NOT NULL,policy_revision bigint NOT NULL,policy_digest text NOT NULL,
 version bigint NOT NULL CHECK(version>0),configuration jsonb NOT NULL,updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,policy_id,policy_revision),FOREIGN KEY(tenant_id,policy_id,policy_revision,policy_digest) REFERENCES idenqa.policy_revisions(tenant_id,policy_id,revision,digest)
);

CREATE TABLE idenqa.review_administration_history (
 tenant_id text NOT NULL,kind text NOT NULL,reference text NOT NULL,version bigint NOT NULL,
 actor_key_id text NOT NULL,configuration jsonb NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,kind,reference,version),FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TABLE idenqa.review_case_settings (
 tenant_id text NOT NULL,case_id text NOT NULL,configuration jsonb NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id),FOREIGN KEY(tenant_id,case_id) REFERENCES idenqa.review_cases(tenant_id,id)
);

CREATE TABLE idenqa.review_case_operations (
 tenant_id text NOT NULL,case_id text NOT NULL,version bigint NOT NULL CHECK(version>0),priority integer NOT NULL CHECK(priority BETWEEN 0 AND 9),
 due_at timestamptz NOT NULL,language text NOT NULL,reason text NOT NULL,assurance text NOT NULL,risk text NOT NULL,
 sampled boolean NOT NULL,updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id),FOREIGN KEY(tenant_id,case_id) REFERENCES idenqa.review_cases(tenant_id,id)
);

CREATE INDEX review_operations_queue ON idenqa.review_case_operations(tenant_id,priority DESC,due_at,case_id);

CREATE TABLE idenqa.review_evidence_access (
 tenant_id text NOT NULL,grant_id text NOT NULL,redemption_id text NOT NULL,case_id text NOT NULL,case_version bigint NOT NULL,
 evidence_id text NOT NULL,reviewer_id text NOT NULL,redactions jsonb NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,grant_id),UNIQUE(tenant_id,redemption_id),
 FOREIGN KEY(tenant_id,grant_id) REFERENCES idenqa.evidence_processing_grants(tenant_id,id),
 FOREIGN KEY(tenant_id,case_id) REFERENCES idenqa.review_cases(tenant_id,id),
 FOREIGN KEY(tenant_id,evidence_id) REFERENCES idenqa.evidence_assets(tenant_id,id)
);

DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['review_operator_assignments','review_policy_settings','review_administration_history','review_case_settings','review_case_operations','review_evidence_access'] LOOP
 EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',n);
 EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',n);
 EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',n);
 EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',n);
 END LOOP;
 FOREACH n IN ARRAY ARRAY['review_administration_history','review_case_settings','review_evidence_access'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_review_record BEFORE UPDATE OR DELETE ON idenqa.%I FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change()',n);
 END LOOP;
END $$;
