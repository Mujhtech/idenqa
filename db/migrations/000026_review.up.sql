CREATE TABLE idenqa.review_cases (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    verification_id text NOT NULL,
    challenged_decision_id text NOT NULL,
    superseding_decision_id text,
    region text NOT NULL,
    required_certification text NOT NULL,
    oversight text NOT NULL,
    state text NOT NULL,
    assigned_reviewer text,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, verification_id) REFERENCES idenqa.verification_sessions (tenant_id, id),
    FOREIGN KEY (tenant_id, challenged_decision_id) REFERENCES idenqa.verification_decisions (tenant_id, id),
    FOREIGN KEY (tenant_id, superseding_decision_id) REFERENCES idenqa.verification_decisions (tenant_id, id),
    CONSTRAINT review_case_id CHECK (id ~ '^rvc_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT review_case_region CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT review_case_oversight CHECK (oversight IN ('single', 'dual')),
    CONSTRAINT review_case_state CHECK (state IN ('open', 'claimed', 'awaiting_second', 'resolved')),
    CONSTRAINT review_case_version CHECK (version > 0),
    CONSTRAINT review_case_times CHECK (updated_at >= created_at)
);

CREATE TABLE idenqa.review_findings (
    tenant_id text NOT NULL,
    case_id text NOT NULL,
    id text NOT NULL,
    reviewer_id text NOT NULL,
    resolution text NOT NULL,
    reason_code text NOT NULL,
    evidence_grant_ids jsonb NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, case_id) REFERENCES idenqa.review_cases (tenant_id, id),
    CONSTRAINT review_finding_id CHECK (id ~ '^fnd_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT review_finding_resolution CHECK (resolution IN ('satisfy', 'not_satisfy', 'request_input')),
    CONSTRAINT review_finding_grants CHECK (jsonb_typeof(evidence_grant_ids) = 'array')
);

CREATE TABLE idenqa.appeals (
    tenant_id text NOT NULL,
    id text NOT NULL,
    case_id text NOT NULL,
    challenged_decision_id text NOT NULL,
    original_reviewers jsonb NOT NULL,
    assigned_reviewer text,
    state text NOT NULL,
    outcome text,
    reason_code text,
    superseding_decision_id text,
    deadline timestamptz NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, case_id) REFERENCES idenqa.review_cases (tenant_id, id),
    FOREIGN KEY (tenant_id, challenged_decision_id) REFERENCES idenqa.verification_decisions (tenant_id, id),
    FOREIGN KEY (tenant_id, superseding_decision_id) REFERENCES idenqa.verification_decisions (tenant_id, id),
    CONSTRAINT appeal_id CHECK (id ~ '^apl_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT appeal_reviewers CHECK (jsonb_typeof(original_reviewers) = 'array'),
    CONSTRAINT appeal_state CHECK (state IN ('requested', 'independent_review', 'resolved', 'withdrawn', 'expired')),
    CONSTRAINT appeal_outcome CHECK (outcome IS NULL OR outcome IN ('upheld', 'overturned', 'more_input')),
    CONSTRAINT appeal_resolution CHECK (
        (state <> 'resolved' AND outcome IS NULL AND reason_code IS NULL AND superseding_decision_id IS NULL) OR
        (state = 'resolved' AND outcome IS NOT NULL AND reason_code IS NOT NULL AND
            ((outcome = 'overturned' AND superseding_decision_id IS NOT NULL) OR
             (outcome <> 'overturned' AND superseding_decision_id IS NULL)))
    ),
    CONSTRAINT appeal_version CHECK (version > 0),
    CONSTRAINT appeal_times CHECK (deadline > created_at AND updated_at >= created_at)
);

CREATE FUNCTION idenqa.protect_review_finding()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'review findings are immutable';
END;
$$;

CREATE TRIGGER review_findings_immutable BEFORE UPDATE OR DELETE ON idenqa.review_findings
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_review_finding();

ALTER TABLE idenqa.review_cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_cases FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_findings ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_findings FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.appeals ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.appeals FORCE ROW LEVEL SECURITY;

CREATE POLICY review_cases_tenant ON idenqa.review_cases
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY review_findings_tenant ON idenqa.review_findings
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
CREATE POLICY appeals_tenant ON idenqa.appeals
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.review_cases, idenqa.review_findings, idenqa.appeals FROM PUBLIC;
