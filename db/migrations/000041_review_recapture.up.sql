CREATE TABLE idenqa.review_recaptures (
 tenant_id text NOT NULL, case_id text NOT NULL, case_version bigint NOT NULL,
 parent_verification_id text NOT NULL, child_verification_id text NOT NULL, capture_token_id text NOT NULL,
 policy_snapshot_digest text NOT NULL, created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id,case_version), UNIQUE(tenant_id,child_verification_id),
 CHECK(parent_verification_id<>child_verification_id),
 FOREIGN KEY(tenant_id,case_id,case_version) REFERENCES idenqa.review_evaluations(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,capture_token_id) REFERENCES idenqa.capture_tokens(tenant_id,id),
 FOREIGN KEY(tenant_id,parent_verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,child_verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,policy_snapshot_digest) REFERENCES idenqa.policy_snapshots(tenant_id,snapshot_digest)
);

ALTER TABLE idenqa.review_recaptures ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_recaptures FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.review_recaptures USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));

CREATE TRIGGER review_recaptures_immutable BEFORE UPDATE OR DELETE ON idenqa.review_recaptures FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

REVOKE ALL ON idenqa.review_recaptures FROM PUBLIC;
