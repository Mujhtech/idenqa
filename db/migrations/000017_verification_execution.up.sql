CREATE TABLE idenqa.verification_checks (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    name text NOT NULL,
    state text NOT NULL,
    outcome text,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, verification_id, id),
    CONSTRAINT verification_checks_session_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT verification_checks_id_format
        CHECK (id ~ '^chk_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_checks_name
        CHECK (name ~ '^[a-z][a-z0-9._:-]{0,127}$'),
    CONSTRAINT verification_checks_state CHECK (state IN (
        'queued', 'running', 'awaiting_input', 'awaiting_provider', 'completed',
        'skipped_by_policy', 'timed_out', 'cancelled', 'failed'
    )),
    CONSTRAINT verification_checks_outcome CHECK (
        (state = 'completed' AND outcome IS NOT NULL AND outcome IN ('passed', 'not_passed', 'inconclusive')) OR
        (state <> 'completed' AND outcome IS NULL)
    ),
    CONSTRAINT verification_checks_version CHECK (version > 0),
    CONSTRAINT verification_checks_times CHECK (created_at <= updated_at)
);

CREATE INDEX verification_checks_session
    ON idenqa.verification_checks (tenant_id, verification_id, created_at, id);

CREATE TABLE idenqa.verification_attempts (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    attempt_number integer NOT NULL,
    fence bigint NOT NULL,
    runner_kind text NOT NULL,
    runner_id text NOT NULL,
    runner_version text NOT NULL,
    package_digest text NOT NULL,
    contract_major integer NOT NULL,
    contract_minor integer NOT NULL,
    request_digest text NOT NULL,
    configuration_digest text NOT NULL,
    state text NOT NULL,
    started_at timestamptz NOT NULL,
    deadline timestamptz NOT NULL,
    finished_at timestamptz,
    failure_class text,
    failure_code text,
    retry_disposition text,
    retry_after_milliseconds bigint,
    result_digest text,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, check_id, attempt_number),
    CONSTRAINT verification_attempts_check_fk
        FOREIGN KEY (tenant_id, verification_id, check_id)
        REFERENCES idenqa.verification_checks (tenant_id, verification_id, id),
    CONSTRAINT verification_attempts_id_format
        CHECK (id ~ '^atm_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_attempts_number CHECK (attempt_number > 0),
    CONSTRAINT verification_attempts_fence CHECK (fence > 0),
    CONSTRAINT verification_attempts_runner_kind CHECK (runner_kind IN ('provider', 'model')),
    CONSTRAINT verification_attempts_runner_id CHECK (runner_id ~ '^[a-z][a-z0-9._:-]{0,127}$'),
    CONSTRAINT verification_attempts_runner_version CHECK (runner_version ~ '^[a-z0-9][a-z0-9._:-]{0,63}$'),
    CONSTRAINT verification_attempts_digests CHECK (
        package_digest ~ '^[0-9a-f]{64}$' AND
        request_digest ~ '^[0-9a-f]{64}$' AND
        configuration_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT verification_attempts_contract CHECK (
        contract_major > 0 AND contract_major <= 65535 AND
        contract_minor >= 0 AND contract_minor <= 65535
    ),
    CONSTRAINT verification_attempts_state CHECK (state IN (
        'running', 'completed', 'failed', 'timed_out', 'cancelled'
    )),
    CONSTRAINT verification_attempts_times CHECK (
        started_at < deadline AND
        (finished_at IS NULL OR finished_at >= started_at)
    ),
    CONSTRAINT verification_attempts_terminal CHECK (
        (
            state = 'running' AND finished_at IS NULL AND result_digest IS NULL AND
            failure_class IS NULL AND failure_code IS NULL AND retry_disposition IS NULL AND
            retry_after_milliseconds IS NULL
        ) OR (
            state = 'completed' AND finished_at IS NOT NULL AND result_digest IS NOT NULL AND
            result_digest ~ '^[0-9a-f]{64}$' AND
            failure_class IS NULL AND failure_code IS NULL AND retry_disposition IS NULL AND
            retry_after_milliseconds IS NULL
        ) OR (
            state IN ('failed', 'timed_out', 'cancelled') AND finished_at IS NOT NULL AND
            result_digest IS NOT NULL AND result_digest ~ '^[0-9a-f]{64}$' AND
            failure_class IS NOT NULL AND failure_class ~ '^[a-z][a-z0-9._:-]{0,99}$' AND
            failure_code IS NOT NULL AND failure_code ~ '^[a-z][a-z0-9._:-]{0,99}$' AND
            retry_disposition IS NOT NULL AND retry_disposition IN ('never', 'backoff', 'reconcile') AND
            retry_after_milliseconds IS NOT NULL AND retry_after_milliseconds >= 0
        )
    )
);

CREATE INDEX verification_attempts_check
    ON idenqa.verification_attempts (tenant_id, check_id, attempt_number);

CREATE TABLE idenqa.verification_observations (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    attempt_id text NOT NULL,
    runner_kind text NOT NULL,
    runner_id text NOT NULL,
    runner_version text NOT NULL,
    package_digest text NOT NULL,
    contract_major integer NOT NULL,
    contract_minor integer NOT NULL,
    request_digest text NOT NULL,
    configuration_digest text NOT NULL,
    signal_name text NOT NULL,
    signal_outcome text NOT NULL,
    reason_codes text[] NOT NULL DEFAULT '{}',
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT verification_observations_attempt_fk
        FOREIGN KEY (tenant_id, attempt_id)
        REFERENCES idenqa.verification_attempts (tenant_id, id),
    CONSTRAINT verification_observations_check_fk
        FOREIGN KEY (tenant_id, verification_id, check_id)
        REFERENCES idenqa.verification_checks (tenant_id, verification_id, id),
    CONSTRAINT verification_observations_id_format
        CHECK (id ~ '^obs_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_observations_runner_kind CHECK (runner_kind IN ('provider', 'model')),
    CONSTRAINT verification_observations_runner_id CHECK (runner_id ~ '^[a-z][a-z0-9._:-]{0,127}$'),
    CONSTRAINT verification_observations_runner_version CHECK (runner_version ~ '^[a-z0-9][a-z0-9._:-]{0,63}$'),
    CONSTRAINT verification_observations_digests CHECK (
        package_digest ~ '^[0-9a-f]{64}$' AND
        request_digest ~ '^[0-9a-f]{64}$' AND
        configuration_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT verification_observations_contract CHECK (
        contract_major > 0 AND contract_major <= 65535 AND
        contract_minor >= 0 AND contract_minor <= 65535
    ),
    CONSTRAINT verification_observations_signal_name
        CHECK (signal_name ~ '^[a-z][a-z0-9._:-]{0,127}$'),
    CONSTRAINT verification_observations_signal_outcome
        CHECK (signal_outcome IN ('satisfied', 'not_satisfied', 'inconclusive')),
    CONSTRAINT verification_observations_reason_codes CHECK (
        cardinality(reason_codes) <= 16 AND array_position(reason_codes, NULL) IS NULL
    )
);

CREATE INDEX verification_observations_attempt
    ON idenqa.verification_observations (tenant_id, attempt_id, recorded_at, id);

CREATE TABLE idenqa.verification_attempt_diagnostics (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    attempt_id text NOT NULL,
    kind text NOT NULL,
    code text NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, check_id, attempt_id, kind, recorded_at),
    CONSTRAINT verification_attempt_diagnostics_check_fk
        FOREIGN KEY (tenant_id, verification_id, check_id)
        REFERENCES idenqa.verification_checks (tenant_id, verification_id, id),
    CONSTRAINT verification_attempt_diagnostics_attempt_id
        CHECK (attempt_id ~ '^atm_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_attempt_diagnostics_kind CHECK (kind IN ('duplicate', 'conflict', 'stale')),
    CONSTRAINT verification_attempt_diagnostics_code
        CHECK (code ~ '^[a-z][a-z0-9._:-]{0,99}$')
);

CREATE TABLE idenqa.verification_result_inbox (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    attempt_id text NOT NULL,
    result_digest text NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, attempt_id, result_digest),
    CONSTRAINT verification_result_inbox_check_fk
        FOREIGN KEY (tenant_id, verification_id, check_id)
        REFERENCES idenqa.verification_checks (tenant_id, verification_id, id),
    CONSTRAINT verification_result_inbox_attempt_fk
        FOREIGN KEY (tenant_id, attempt_id)
        REFERENCES idenqa.verification_attempts (tenant_id, id),
    CONSTRAINT verification_result_inbox_digest
        CHECK (result_digest ~ '^[0-9a-f]{64}$')
);

CREATE TABLE idenqa.verification_reconciliations (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    attempt_id text NOT NULL,
    reason text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    claim_token text,
    lease_expires_at timestamptz,
    claim_count integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id, check_id, attempt_id, reason),
    CONSTRAINT verification_reconciliations_check_fk
        FOREIGN KEY (tenant_id, verification_id, check_id)
        REFERENCES idenqa.verification_checks (tenant_id, verification_id, id),
    CONSTRAINT verification_reconciliations_attempt_id
        CHECK (attempt_id ~ '^atm_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_reconciliations_reason CHECK (reason IN ('stale', 'conflict', 'provider_requested')),
    CONSTRAINT verification_reconciliations_status CHECK (status IN ('pending', 'claimed', 'resolved')),
    CONSTRAINT verification_reconciliations_claim CHECK (
        (status = 'pending' AND claim_token IS NULL AND lease_expires_at IS NULL AND resolved_at IS NULL) OR
        (status = 'claimed' AND claim_token IS NOT NULL AND claim_token ~ '^[a-zA-Z0-9._:-]{1,128}$' AND lease_expires_at IS NOT NULL AND resolved_at IS NULL) OR
        (status = 'resolved' AND claim_token IS NULL AND lease_expires_at IS NULL AND resolved_at IS NOT NULL)
    ),
    CONSTRAINT verification_reconciliations_count CHECK (claim_count >= 0),
    CONSTRAINT verification_reconciliations_times CHECK (
        created_at <= updated_at AND created_at <= available_at AND
        (lease_expires_at IS NULL OR lease_expires_at > updated_at) AND
        (resolved_at IS NULL OR resolved_at >= created_at)
    )
);

CREATE INDEX verification_reconciliations_available
    ON idenqa.verification_reconciliations (tenant_id, available_at, created_at, check_id)
    WHERE status = 'pending';

CREATE FUNCTION idenqa.protect_verification_attempt()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'verification attempts cannot be deleted';
    END IF;
    IF OLD.state <> 'running' THEN
        RAISE EXCEPTION 'terminal verification attempts are immutable';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
       NEW.verification_id IS DISTINCT FROM OLD.verification_id OR NEW.check_id IS DISTINCT FROM OLD.check_id OR
       NEW.attempt_number IS DISTINCT FROM OLD.attempt_number OR NEW.fence IS DISTINCT FROM OLD.fence OR
       NEW.runner_kind IS DISTINCT FROM OLD.runner_kind OR NEW.runner_id IS DISTINCT FROM OLD.runner_id OR
       NEW.runner_version IS DISTINCT FROM OLD.runner_version OR NEW.package_digest IS DISTINCT FROM OLD.package_digest OR
       NEW.contract_major IS DISTINCT FROM OLD.contract_major OR NEW.contract_minor IS DISTINCT FROM OLD.contract_minor OR
       NEW.request_digest IS DISTINCT FROM OLD.request_digest OR
       NEW.configuration_digest IS DISTINCT FROM OLD.configuration_digest OR
       NEW.started_at IS DISTINCT FROM OLD.started_at OR NEW.deadline IS DISTINCT FROM OLD.deadline THEN
        RAISE EXCEPTION 'verification attempt provenance is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER verification_attempt_transition
BEFORE UPDATE OR DELETE ON idenqa.verification_attempts
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_verification_attempt();

CREATE FUNCTION idenqa.reject_verification_append_only_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'verification execution record is append-only';
END;
$$;

CREATE TRIGGER verification_observation_append_only
BEFORE UPDATE OR DELETE ON idenqa.verification_observations
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_verification_append_only_change();

CREATE TRIGGER verification_diagnostic_append_only
BEFORE UPDATE OR DELETE ON idenqa.verification_attempt_diagnostics
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_verification_append_only_change();

CREATE TRIGGER verification_result_inbox_append_only
BEFORE UPDATE OR DELETE ON idenqa.verification_result_inbox
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_verification_append_only_change();

ALTER TABLE idenqa.verification_checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_checks FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_attempts FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_observations FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_attempt_diagnostics ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_attempt_diagnostics FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_result_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_result_inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_reconciliations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_reconciliations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.verification_checks
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_attempts
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_observations
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_attempt_diagnostics
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_result_inbox
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.verification_reconciliations
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.verification_checks FROM PUBLIC;
REVOKE ALL ON idenqa.verification_attempts FROM PUBLIC;
REVOKE ALL ON idenqa.verification_observations FROM PUBLIC;
REVOKE ALL ON idenqa.verification_attempt_diagnostics FROM PUBLIC;
REVOKE ALL ON idenqa.verification_result_inbox FROM PUBLIC;
REVOKE ALL ON idenqa.verification_reconciliations FROM PUBLIC;
