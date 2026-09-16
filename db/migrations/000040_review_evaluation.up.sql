ALTER TABLE idenqa.review_cases ADD COLUMN permitted_findings jsonb NOT NULL DEFAULT '[]'::jsonb
 CHECK (jsonb_typeof(permitted_findings)='array' AND jsonb_array_length(permitted_findings)<=64);
ALTER TABLE idenqa.review_cases DROP CONSTRAINT review_case_state;
ALTER TABLE idenqa.review_cases ADD CONSTRAINT review_case_state CHECK (state IN ('open','claimed','awaiting_second','resolved','escalated'));

CREATE FUNCTION idenqa.protect_review_finding_rules() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.permitted_findings IS DISTINCT FROM OLD.permitted_findings THEN RAISE EXCEPTION 'review finding rules are immutable'; END IF;
 RETURN NEW;
END; $$;

CREATE TRIGGER protect_review_finding_rules BEFORE UPDATE ON idenqa.review_cases FOR EACH ROW EXECUTE FUNCTION idenqa.protect_review_finding_rules();

REVOKE ALL ON FUNCTION idenqa.protect_review_finding_rules() FROM PUBLIC;

CREATE TABLE idenqa.review_evaluation_requests (
 tenant_id text NOT NULL, case_id text NOT NULL, case_version bigint NOT NULL CHECK(case_version>0), created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id,case_version), FOREIGN KEY(tenant_id,case_id) REFERENCES idenqa.review_cases(tenant_id,id)
);
CREATE TABLE idenqa.review_evaluations (
 tenant_id text NOT NULL,case_id text NOT NULL,case_version bigint NOT NULL,verification_id text NOT NULL,
 snapshot_digest text NOT NULL,evaluation_digest text NOT NULL,case_digest text NOT NULL CHECK(case_digest ~ '^[0-9a-f]{64}$'),
 PRIMARY KEY(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,case_id,case_version) REFERENCES idenqa.review_evaluation_requests(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,verification_id,snapshot_digest,evaluation_digest) REFERENCES idenqa.policy_evaluations(tenant_id,verification_id,snapshot_digest,evaluation_digest)
);

ALTER TABLE idenqa.review_evaluation_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_evaluation_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.review_evaluation_requests USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));

ALTER TABLE idenqa.review_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_evaluations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.review_evaluations USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));

CREATE TRIGGER review_requests_immutable BEFORE UPDATE OR DELETE ON idenqa.review_evaluation_requests FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();
CREATE TRIGGER review_evaluations_immutable BEFORE UPDATE OR DELETE ON idenqa.review_evaluations FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

REVOKE ALL ON idenqa.review_evaluation_requests,idenqa.review_evaluations FROM PUBLIC;

CREATE FUNCTION idenqa.list_ready_review_evaluations(observed_at timestamptz,batch_size integer)
RETURNS TABLE(tenant_id text,case_id text,case_version bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET row_security=off AS $$
 SELECT r.tenant_id,r.case_id,r.case_version FROM idenqa.review_evaluation_requests r
 JOIN idenqa.review_cases c ON c.tenant_id=r.tenant_id AND c.id=r.case_id AND c.version=r.case_version AND c.state='resolved'
 JOIN idenqa.verification_sessions s ON s.tenant_id=c.tenant_id AND s.id=c.verification_id
 JOIN idenqa.processing_authorities a ON a.tenant_id=s.tenant_id AND a.id=s.authority_id
 JOIN LATERAL (SELECT response.action,response.recorded_at,response.subject_id,response.verification_id,response.notice_id FROM idenqa.subject_responses response WHERE response.tenant_id=s.tenant_id AND response.authority_id=s.authority_id ORDER BY response.recorded_at DESC,response.id DESC LIMIT 1) latest ON true
 WHERE batch_size BETWEEN 1 AND 100 AND r.created_at<=observed_at AND s.state='manual_review' AND s.expires_at>observed_at
 AND latest.recorded_at<=observed_at AND latest.subject_id=s.subject_id AND latest.verification_id=s.id AND latest.notice_id=s.notice_id
 AND (latest.action='consent' OR (NOT a.consent_required AND latest.action='acknowledge'))
 AND a.state='active' AND a.valid_from<=observed_at AND a.expires_at>observed_at
 AND NOT EXISTS(SELECT 1 FROM idenqa.review_evaluations e WHERE e.tenant_id=r.tenant_id AND e.case_id=r.case_id AND e.case_version=r.case_version)
 ORDER BY r.created_at,r.tenant_id,r.case_id LIMIT batch_size;
$$;

REVOKE ALL ON FUNCTION idenqa.list_ready_review_evaluations(timestamptz,integer) FROM PUBLIC;
