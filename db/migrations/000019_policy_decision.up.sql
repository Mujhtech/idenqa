CREATE TABLE idenqa.policy_snapshots (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    snapshot_digest text NOT NULL,
    policy_id text NOT NULL,
    policy_revision bigint NOT NULL,
    policy_schema_major integer NOT NULL,
    policy_schema_minor integer NOT NULL,
    policy_digest text NOT NULL,
    evaluator_major integer NOT NULL,
    evaluator_minor integer NOT NULL,
    evaluator_digest text NOT NULL,
    authority_id text NOT NULL,
    acknowledgement_id text NOT NULL,
    region text NOT NULL,
    evaluated_at timestamptz NOT NULL,
    canonical text NOT NULL,
    PRIMARY KEY (tenant_id, snapshot_digest),
    UNIQUE (tenant_id, verification_id, snapshot_digest),
    CONSTRAINT policy_snapshots_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT policy_snapshots_policy_id CHECK (policy_id ~ '^pol_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT policy_snapshots_policy_version CHECK (
        policy_revision > 0 AND policy_schema_major = 1 AND policy_schema_minor = 0
    ),
    CONSTRAINT policy_snapshots_evaluator_version CHECK (evaluator_major = 1 AND evaluator_minor = 0),
    CONSTRAINT policy_snapshots_authority_id CHECK (authority_id ~ '^aut_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT policy_snapshots_acknowledgement_id CHECK (acknowledgement_id ~ '^ack_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT policy_snapshots_region CHECK (region ~ '^[a-z][a-z0-9._:-]{0,63}$'),
    CONSTRAINT policy_snapshots_digests CHECK (
        snapshot_digest ~ '^[0-9a-f]{64}$' AND
        policy_digest ~ '^[0-9a-f]{64}$' AND
        evaluator_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT policy_snapshots_canonical_size CHECK (
        octet_length(canonical) > 0 AND octet_length(canonical) <= 262144
    )
);

CREATE TABLE idenqa.policy_evaluations (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    snapshot_digest text NOT NULL,
    evaluation_digest text NOT NULL,
    selected text NOT NULL,
    outcome text,
    assurance text,
    canonical text NOT NULL,
    PRIMARY KEY (tenant_id, evaluation_digest),
    UNIQUE (tenant_id, verification_id, snapshot_digest, evaluation_digest),
    UNIQUE (tenant_id, verification_id, snapshot_digest, evaluation_digest, selected, outcome),
    CONSTRAINT policy_evaluations_snapshot_fk
        FOREIGN KEY (tenant_id, verification_id, snapshot_digest)
        REFERENCES idenqa.policy_snapshots (tenant_id, verification_id, snapshot_digest),
    CONSTRAINT policy_evaluations_digests CHECK (
        snapshot_digest ~ '^[0-9a-f]{64}$' AND evaluation_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT policy_evaluations_selected CHECK (selected IN (
        'complete_verified', 'complete_not_verified', 'complete_inconclusive',
        'request_input', 'run_check', 'retry_check', 'use_fallback',
        'route_manual_review', 'fail_workflow'
    )),
    CONSTRAINT policy_evaluations_terminal CHECK (
        (selected = 'complete_verified' AND outcome = 'verified' AND assurance IS NOT NULL) OR
        (selected = 'complete_not_verified' AND outcome = 'not_verified') OR
        (selected = 'complete_inconclusive' AND outcome = 'inconclusive') OR
        (selected NOT IN ('complete_verified', 'complete_not_verified', 'complete_inconclusive') AND outcome IS NULL)
    ),
    CONSTRAINT policy_evaluations_assurance CHECK (
        assurance IS NULL OR assurance ~ '^[a-z][a-z0-9._:-]{0,127}$'
    ),
    CONSTRAINT policy_evaluations_canonical_size CHECK (
        octet_length(canonical) > 0 AND octet_length(canonical) <= 262144
    )
);

CREATE TABLE idenqa.verification_decisions (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    snapshot_digest text NOT NULL,
    evaluation_digest text NOT NULL,
    decision_digest text NOT NULL,
    selected text NOT NULL,
    outcome text NOT NULL,
    actor text NOT NULL,
    supersedes_id text,
    decided_at timestamptz NOT NULL,
    canonical text NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, verification_id, id),
    UNIQUE (tenant_id, verification_id, decision_digest),
    CONSTRAINT verification_decisions_evaluation_fk
        FOREIGN KEY (tenant_id, verification_id, snapshot_digest, evaluation_digest, selected, outcome)
        REFERENCES idenqa.policy_evaluations (
            tenant_id, verification_id, snapshot_digest, evaluation_digest, selected, outcome
        ),
    CONSTRAINT verification_decisions_supersedes_fk
        FOREIGN KEY (tenant_id, verification_id, supersedes_id)
        REFERENCES idenqa.verification_decisions (tenant_id, verification_id, id),
    CONSTRAINT verification_decisions_id CHECK (id ~ '^dec_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_decisions_supersedes_id CHECK (
        supersedes_id IS NULL OR (supersedes_id ~ '^dec_[0-9A-HJKMNP-TV-Z]{26}$' AND supersedes_id <> id)
    ),
    CONSTRAINT verification_decisions_digests CHECK (
        snapshot_digest ~ '^[0-9a-f]{64}$' AND
        evaluation_digest ~ '^[0-9a-f]{64}$' AND
        decision_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT verification_decisions_actor CHECK (actor IN ('machine', 'human')),
    CONSTRAINT verification_decisions_terminal CHECK (
        (selected = 'complete_verified' AND outcome = 'verified') OR
        (selected = 'complete_not_verified' AND outcome = 'not_verified') OR
        (selected = 'complete_inconclusive' AND outcome = 'inconclusive')
    ),
    CONSTRAINT verification_decisions_canonical_size CHECK (
        octet_length(canonical) > 0 AND octet_length(canonical) <= 16384
    )
);

CREATE UNIQUE INDEX verification_decisions_one_root
    ON idenqa.verification_decisions (tenant_id, verification_id)
    WHERE supersedes_id IS NULL;

CREATE UNIQUE INDEX verification_decisions_one_successor
    ON idenqa.verification_decisions (tenant_id, verification_id, supersedes_id)
    WHERE supersedes_id IS NOT NULL;

CREATE INDEX verification_decisions_latest
    ON idenqa.verification_decisions (tenant_id, verification_id, decided_at DESC, id DESC);

CREATE FUNCTION idenqa.reject_policy_record_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'policy decision records are append-only';
END;
$$;

CREATE TRIGGER policy_snapshot_append_only
BEFORE UPDATE OR DELETE ON idenqa.policy_snapshots
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

CREATE TRIGGER policy_evaluation_append_only
BEFORE UPDATE OR DELETE ON idenqa.policy_evaluations
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

CREATE TRIGGER verification_decision_append_only
BEFORE UPDATE OR DELETE ON idenqa.verification_decisions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

ALTER TABLE idenqa.policy_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_snapshots FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_evaluations FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_decisions FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.policy_snapshots
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.policy_evaluations
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_decisions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.policy_snapshots FROM PUBLIC;
REVOKE ALL ON idenqa.policy_evaluations FROM PUBLIC;
REVOKE ALL ON idenqa.verification_decisions FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_policy_record_change() FROM PUBLIC;
