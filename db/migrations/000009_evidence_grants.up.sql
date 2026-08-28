ALTER TABLE idenqa.processing_authorities
    ADD CONSTRAINT processing_authorities_grant_binding_unique
        UNIQUE (tenant_id, id, subject_id, verification_id);

ALTER TABLE idenqa.subject_responses
    ADD CONSTRAINT subject_responses_grant_binding_unique
        UNIQUE (tenant_id, id, authority_id, subject_id, verification_id);

ALTER TABLE idenqa.evidence_assets
    ADD CONSTRAINT evidence_assets_grant_binding_unique
        UNIQUE (tenant_id, id, subject_id, verification_id, requirement_key);

CREATE TABLE idenqa.evidence_processing_grants (
    id text NOT NULL,
    tenant_id text NOT NULL,
    subject_id text NOT NULL,
    verification_id text NOT NULL,
    evidence_id text NOT NULL,
    requirement_key text NOT NULL,
    authority_id text NOT NULL,
    response_id text NOT NULL,
    check_reference text NOT NULL,
    runner_identity text NOT NULL,
    workload_version text NOT NULL,
    purpose text NOT NULL,
    operation text NOT NULL,
    permitted_variants text[] NOT NULL,
    region text NOT NULL,
    recipient_reference text NOT NULL,
    output_destination text NOT NULL,
    policy_reference text NOT NULL,
    maximum_uses integer NOT NULL,
    uses integer NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    CONSTRAINT evidence_processing_grants_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT evidence_processing_grants_authority_fk
        FOREIGN KEY (tenant_id, authority_id, subject_id, verification_id)
        REFERENCES idenqa.processing_authorities (tenant_id, id, subject_id, verification_id),
    CONSTRAINT evidence_processing_grants_response_fk
        FOREIGN KEY (tenant_id, response_id, authority_id, subject_id, verification_id)
        REFERENCES idenqa.subject_responses (tenant_id, id, authority_id, subject_id, verification_id),
    CONSTRAINT evidence_processing_grants_evidence_fk
        FOREIGN KEY (tenant_id, evidence_id, subject_id, verification_id, requirement_key)
        REFERENCES idenqa.evidence_assets (tenant_id, id, subject_id, verification_id, requirement_key),
    CONSTRAINT evidence_processing_grants_id_format
        CHECK (id ~ '^grt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT evidence_processing_grants_requirement
        CHECK (requirement_key ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT evidence_processing_grants_scope CHECK (
        check_reference ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        purpose ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        operation = 'evidence.plaintext.read' AND
        region ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        recipient_reference ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        output_destination ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        policy_reference ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        char_length(check_reference) <= 200 AND char_length(purpose) <= 200 AND
        char_length(region) <= 200 AND char_length(recipient_reference) <= 200 AND
        char_length(output_destination) <= 200 AND char_length(policy_reference) <= 200 AND
        cardinality(permitted_variants) BETWEEN 1 AND 16 AND
        array_position(permitted_variants, NULL) IS NULL AND
        'evidence.variant.original' = ANY(permitted_variants)
    ),
    CONSTRAINT evidence_processing_grants_runner CHECK (
        char_length(runner_identity) BETWEEN 1 AND 512 AND
        char_length(workload_version) BETWEEN 1 AND 512 AND
        runner_identity !~ '[[:space:][:cntrl:]]' AND
        workload_version !~ '[[:space:][:cntrl:]]'
    ),
    CONSTRAINT evidence_processing_grants_use_bounds CHECK (
        maximum_uses BETWEEN 1 AND 32 AND uses BETWEEN 0 AND maximum_uses
    ),
    CONSTRAINT evidence_processing_grants_time_bounds CHECK (
        expires_at > created_at AND expires_at <= created_at + interval '1 hour' AND
        (revoked_at IS NULL OR revoked_at >= created_at)
    )
);

CREATE INDEX evidence_processing_grants_expiry
    ON idenqa.evidence_processing_grants (tenant_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE idenqa.evidence_grant_redemptions (
    id text NOT NULL,
    tenant_id text NOT NULL,
    grant_id text NOT NULL,
    runner_identity text NOT NULL,
    workload_version text NOT NULL,
    claimed_use integer,
    denial_reason text,
    attempted_at timestamptz NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, id, grant_id),
    CONSTRAINT evidence_grant_redemptions_grant_fk
        FOREIGN KEY (tenant_id, grant_id)
        REFERENCES idenqa.evidence_processing_grants (tenant_id, id),
    CONSTRAINT evidence_grant_redemptions_id_format
        CHECK (id ~ '^rdm_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT evidence_grant_redemptions_denial_reason CHECK (
        denial_reason IS NULL OR denial_reason IN (
            'runner_mismatch', 'not_started', 'expired', 'revoked',
            'exhausted', 'post_claim_denied'
        )
    ),
    CONSTRAINT evidence_grant_redemptions_lifecycle CHECK (
        (claimed_use IS NOT NULL AND denial_reason IS NULL) OR
        (claimed_use IS NULL AND denial_reason IS NOT NULL)
    ),
    CONSTRAINT evidence_grant_redemptions_claimed_use
        CHECK (claimed_use IS NULL OR claimed_use BETWEEN 1 AND 32),
    CONSTRAINT evidence_grant_redemptions_runner CHECK (
        char_length(runner_identity) BETWEEN 1 AND 512 AND
        char_length(workload_version) BETWEEN 1 AND 512 AND
        runner_identity !~ '[[:space:][:cntrl:]]' AND
        workload_version !~ '[[:space:][:cntrl:]]'
    )
);

CREATE UNIQUE INDEX evidence_grant_redemptions_use
    ON idenqa.evidence_grant_redemptions (tenant_id, grant_id, claimed_use)
    WHERE claimed_use IS NOT NULL;

CREATE INDEX evidence_grant_redemptions_pending
    ON idenqa.evidence_grant_redemptions (tenant_id, attempted_at, id)
    WHERE claimed_use IS NOT NULL;

CREATE TABLE idenqa.evidence_grant_redemption_outcomes (
    tenant_id text NOT NULL,
    redemption_id text NOT NULL,
    grant_id text NOT NULL,
    outcome text NOT NULL,
    denial_reason text,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, redemption_id),
    CONSTRAINT evidence_grant_redemption_outcomes_redemption_fk
        FOREIGN KEY (tenant_id, redemption_id, grant_id)
        REFERENCES idenqa.evidence_grant_redemptions (tenant_id, id, grant_id),
    CONSTRAINT evidence_grant_redemption_outcomes_value
        CHECK (outcome IN ('succeeded', 'denied', 'integrity_failed', 'failed')),
    CONSTRAINT evidence_grant_redemption_outcomes_reason CHECK (
        (outcome = 'denied' AND denial_reason IS NOT NULL) OR
        (outcome <> 'denied' AND denial_reason IS NULL)
    )
);

CREATE TABLE idenqa.evidence_processing_grant_audit (
    tenant_id text NOT NULL,
    grant_id text NOT NULL,
    aggregate_version integer NOT NULL,
    action text NOT NULL,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    tenant_actor_type text NOT NULL,
    tenant_actor_id text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, grant_id, aggregate_version),
    CONSTRAINT evidence_processing_grant_audit_grant_fk
        FOREIGN KEY (tenant_id, grant_id)
        REFERENCES idenqa.evidence_processing_grants (tenant_id, id),
    CONSTRAINT evidence_processing_grant_audit_action
        CHECK ((aggregate_version = 1 AND action = 'create') OR
               (aggregate_version = 2 AND action = 'revoke')),
    CONSTRAINT evidence_processing_grant_audit_attribution CHECK (
        principal_type ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        tenant_actor_type ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$' AND
        char_length(principal_id) BETWEEN 1 AND 512 AND
        char_length(tenant_actor_id) BETWEEN 1 AND 512 AND
        char_length(reason) BETWEEN 1 AND 500 AND
        principal_id !~ '[[:space:][:cntrl:]]' AND
        tenant_actor_id !~ '[[:space:][:cntrl:]]' AND
        reason !~ '[[:cntrl:]]'
    )
);

CREATE TABLE idenqa.evidence_grant_access_attempt_audit (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    presented_grant_id text NOT NULL,
    redemption_id text NOT NULL,
    runner_identity text NOT NULL,
    workload_version text NOT NULL,
    decision text NOT NULL,
    denial_reason text,
    occurred_at timestamptz NOT NULL,
    CONSTRAINT evidence_grant_access_attempt_audit_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT evidence_grant_access_attempt_audit_grant_format
        CHECK (presented_grant_id ~ '^grt_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT evidence_grant_access_attempt_audit_redemption_format
        CHECK (redemption_id ~ '^rdm_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT evidence_grant_access_attempt_audit_decision CHECK (
        decision IN ('claimed', 'replayed_pending', 'replayed_terminal', 'denied')
    ),
    CONSTRAINT evidence_grant_access_attempt_audit_denial CHECK (
        (decision = 'denied' AND denial_reason IS NOT NULL) OR
        (decision <> 'denied' AND denial_reason IS NULL)
    ),
    CONSTRAINT evidence_grant_access_attempt_audit_runner CHECK (
        char_length(runner_identity) BETWEEN 1 AND 512 AND
        char_length(workload_version) BETWEEN 1 AND 512 AND
        runner_identity !~ '[[:space:][:cntrl:]]' AND
        workload_version !~ '[[:space:][:cntrl:]]'
    )
);

CREATE INDEX evidence_grant_access_attempt_audit_tenant_sequence
    ON idenqa.evidence_grant_access_attempt_audit (tenant_id, sequence DESC);

CREATE FUNCTION idenqa.protect_evidence_processing_grant()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.uses <> 0 OR NEW.revoked_at IS NOT NULL THEN
            RAISE EXCEPTION 'invalid initial evidence processing grant state';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence processing grants cannot be deleted';
    END IF;
    IF ROW(NEW.id, NEW.tenant_id, NEW.subject_id, NEW.verification_id,
           NEW.evidence_id, NEW.requirement_key, NEW.authority_id, NEW.response_id,
           NEW.check_reference, NEW.runner_identity, NEW.workload_version,
           NEW.purpose, NEW.operation, NEW.permitted_variants, NEW.region,
           NEW.recipient_reference, NEW.output_destination, NEW.policy_reference,
           NEW.maximum_uses, NEW.created_at, NEW.expires_at)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.tenant_id, OLD.subject_id, OLD.verification_id,
           OLD.evidence_id, OLD.requirement_key, OLD.authority_id, OLD.response_id,
           OLD.check_reference, OLD.runner_identity, OLD.workload_version,
           OLD.purpose, OLD.operation, OLD.permitted_variants, OLD.region,
           OLD.recipient_reference, OLD.output_destination, OLD.policy_reference,
           OLD.maximum_uses, OLD.created_at, OLD.expires_at)
       OR NEW.uses < OLD.uses OR NEW.uses > OLD.uses + 1
       OR (NEW.uses <> OLD.uses AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at)
       OR (OLD.revoked_at IS NOT NULL AND NEW.uses <> OLD.uses)
       OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN
        RAISE EXCEPTION 'invalid evidence processing grant transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_processing_grants_protected
BEFORE INSERT OR UPDATE OR DELETE ON idenqa.evidence_processing_grants
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_evidence_processing_grant();

CREATE FUNCTION idenqa.validate_evidence_grant_redemption()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.claimed_use IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM idenqa.evidence_processing_grants AS processing_grant
        WHERE processing_grant.tenant_id = NEW.tenant_id
          AND processing_grant.id = NEW.grant_id
          AND processing_grant.runner_identity = NEW.runner_identity
          AND processing_grant.workload_version = NEW.workload_version
          AND processing_grant.uses >= NEW.claimed_use
          AND processing_grant.maximum_uses >= NEW.claimed_use
    ) THEN
        RAISE EXCEPTION 'invalid claimed evidence grant redemption';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_grant_redemptions_validated
BEFORE INSERT ON idenqa.evidence_grant_redemptions
FOR EACH ROW EXECUTE FUNCTION idenqa.validate_evidence_grant_redemption();

CREATE TRIGGER evidence_grant_redemptions_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_grant_redemptions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER evidence_grant_redemption_outcomes_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_grant_redemption_outcomes
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE FUNCTION idenqa.validate_evidence_grant_redemption_outcome()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM idenqa.evidence_grant_redemptions AS redemption
        WHERE redemption.tenant_id = NEW.tenant_id
          AND redemption.id = NEW.redemption_id
          AND redemption.grant_id = NEW.grant_id
          AND (
              (redemption.claimed_use IS NOT NULL AND
               (NEW.outcome <> 'denied' OR NEW.denial_reason = 'post_claim_denied')) OR
              (redemption.claimed_use IS NULL AND NEW.outcome = 'denied' AND
               NEW.denial_reason = redemption.denial_reason)
          )
          AND NEW.occurred_at >= redemption.attempted_at
    ) THEN
        RAISE EXCEPTION 'invalid evidence grant redemption outcome';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER evidence_grant_redemption_outcomes_validated
BEFORE INSERT ON idenqa.evidence_grant_redemption_outcomes
FOR EACH ROW EXECUTE FUNCTION idenqa.validate_evidence_grant_redemption_outcome();

CREATE TRIGGER evidence_processing_grant_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_processing_grant_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER evidence_grant_access_attempt_audit_immutable
BEFORE UPDATE OR DELETE ON idenqa.evidence_grant_access_attempt_audit
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.evidence_processing_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_processing_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_processing_grants_tenant_scope ON idenqa.evidence_processing_grants
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_grant_redemptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_grant_redemptions FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_grant_redemptions_tenant_scope ON idenqa.evidence_grant_redemptions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_grant_redemption_outcomes ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_grant_redemption_outcomes FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_grant_redemption_outcomes_tenant_scope
    ON idenqa.evidence_grant_redemption_outcomes
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_processing_grant_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_processing_grant_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_processing_grant_audit_tenant_scope ON idenqa.evidence_processing_grant_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

ALTER TABLE idenqa.evidence_grant_access_attempt_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_grant_access_attempt_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY evidence_grant_access_attempt_audit_tenant_scope
    ON idenqa.evidence_grant_access_attempt_audit
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.evidence_processing_grants FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_grant_redemptions FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_grant_redemption_outcomes FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_processing_grant_audit FROM PUBLIC;
REVOKE ALL ON idenqa.evidence_grant_access_attempt_audit FROM PUBLIC;
