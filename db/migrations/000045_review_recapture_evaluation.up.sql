ALTER TABLE idenqa.review_recapture_acknowledgements ADD CONSTRAINT review_acknowledgement_exact_decision UNIQUE(tenant_id,case_id,case_version,decision_id);

CREATE TABLE idenqa.review_recapture_evaluation_requests (
 tenant_id text NOT NULL,case_id text NOT NULL,source_version bigint NOT NULL,
 target_version bigint NOT NULL CHECK(target_version=source_version+1),
 decision_id text NOT NULL,actor_key_id text NOT NULL,reviewer_id text NOT NULL CHECK(length(reviewer_id) BETWEEN 1 AND 128),
 recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id,source_version),
 UNIQUE(tenant_id,case_id,target_version),
 FOREIGN KEY(tenant_id,case_id,source_version,decision_id) REFERENCES idenqa.review_recapture_acknowledgements(tenant_id,case_id,case_version,decision_id),
 FOREIGN KEY(tenant_id,case_id,target_version) REFERENCES idenqa.review_evaluation_requests(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,decision_id) REFERENCES idenqa.verification_decisions(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

ALTER TABLE idenqa.review_recapture_evaluation_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_recapture_evaluation_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.review_recapture_evaluation_requests USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));

CREATE TRIGGER review_recapture_evaluation_requests_immutable BEFORE UPDATE OR DELETE ON idenqa.review_recapture_evaluation_requests FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

REVOKE ALL ON idenqa.review_recapture_evaluation_requests FROM PUBLIC;
