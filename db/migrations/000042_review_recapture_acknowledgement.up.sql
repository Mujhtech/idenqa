ALTER TABLE idenqa.review_recaptures ADD CONSTRAINT review_recaptures_exact_child UNIQUE(tenant_id,case_id,case_version,child_verification_id);

CREATE TABLE idenqa.review_recapture_acknowledgements (
 tenant_id text NOT NULL,case_id text NOT NULL,case_version bigint NOT NULL,
 child_verification_id text NOT NULL,decision_id text NOT NULL,actor_key_id text NOT NULL,
 reviewer_id text NOT NULL CHECK(length(reviewer_id) BETWEEN 1 AND 128),recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,case_id,case_version,child_verification_id) REFERENCES idenqa.review_recaptures(tenant_id,case_id,case_version,child_verification_id),
 FOREIGN KEY(tenant_id,child_verification_id,decision_id) REFERENCES idenqa.verification_decisions(tenant_id,verification_id,id),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

ALTER TABLE idenqa.review_recapture_acknowledgements ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_recapture_acknowledgements FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.review_recapture_acknowledgements USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));

CREATE TRIGGER review_recapture_acknowledgements_immutable BEFORE UPDATE OR DELETE ON idenqa.review_recapture_acknowledgements FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

REVOKE ALL ON idenqa.review_recapture_acknowledgements FROM PUBLIC;
