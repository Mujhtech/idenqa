ALTER TABLE idenqa.provider_requests
    ADD COLUMN callback_token_digest text
    CHECK (callback_token_digest IS NULL OR callback_token_digest ~ '^[0-9a-f]{64}$');

CREATE UNIQUE INDEX provider_requests_callback_token
    ON idenqa.provider_requests (callback_token_digest)
    WHERE callback_token_digest IS NOT NULL;

CREATE FUNCTION idenqa.resolve_provider_callback(p_digest text)
RETURNS TABLE (
    tenant_id text,
    attempt_id text,
    verification_id text,
    check_id text,
    attempt_deadline timestamptz,
    attempt_state text,
    check_state text
)
LANGUAGE sql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT requests.tenant_id, requests.attempt_id, requests.verification_id, requests.check_id,
           attempts.deadline, attempts.state, checks.state
    FROM idenqa.provider_requests AS requests
    JOIN idenqa.verification_attempts AS attempts
      ON attempts.tenant_id = requests.tenant_id AND attempts.id = requests.attempt_id
    JOIN idenqa.verification_checks AS checks
      ON checks.tenant_id = requests.tenant_id AND checks.id = requests.check_id
    WHERE p_digest IS NOT NULL AND length(p_digest) = 64
      AND requests.callback_token_digest = p_digest
    LIMIT 1;
$$;

REVOKE ALL ON FUNCTION idenqa.resolve_provider_callback(text) FROM PUBLIC;

CREATE TABLE idenqa.provider_callback_receipts (
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    provider_job_id text CHECK (provider_job_id IS NULL OR provider_job_id ~ '^[A-Za-z0-9_-]{1,128}$'),
    provider_replay_id text NOT NULL CHECK (provider_replay_id ~ '^[A-Za-z0-9_.:-]{1,200}$'),
    configuration_digest text NOT NULL CHECK (configuration_digest ~ '^[0-9a-f]{64}$'),
    progress_digest text NOT NULL CHECK (progress_digest ~ '^[0-9a-f]{64}$'),
    result_digest text CHECK (result_digest IS NULL OR result_digest ~ '^[0-9a-f]{64}$'),
    progress_body jsonb NOT NULL CHECK (jsonb_typeof(progress_body) = 'object' AND octet_length(progress_body::text) <= 262144),
    received_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, attempt_id, provider_replay_id),
    FOREIGN KEY (tenant_id, attempt_id) REFERENCES idenqa.provider_requests(tenant_id, attempt_id),
    FOREIGN KEY (tenant_id, verification_id, check_id) REFERENCES idenqa.verification_checks(tenant_id, verification_id, id)
);

ALTER TABLE idenqa.provider_callback_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_callback_receipts FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_callback_receipts_tenant ON idenqa.provider_callback_receipts
 USING (tenant_id = current_setting('idenqa.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

CREATE FUNCTION idenqa.guard_provider_callback_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE'
 OR ROW(NEW.tenant_id,NEW.attempt_id,NEW.verification_id,NEW.check_id,NEW.provider_replay_id,NEW.configuration_digest)
    IS DISTINCT FROM ROW(OLD.tenant_id,OLD.attempt_id,OLD.verification_id,OLD.check_id,OLD.provider_replay_id,OLD.configuration_digest)
 OR (OLD.provider_job_id IS NOT NULL AND NEW.provider_job_id IS DISTINCT FROM OLD.provider_job_id)
 OR (OLD.result_digest IS NOT NULL AND NEW.result_digest IS DISTINCT FROM OLD.result_digest) THEN
  RAISE EXCEPTION 'provider callback receipt is immutable';
 END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER provider_callback_receipt_guard
 BEFORE UPDATE OR DELETE ON idenqa.provider_callback_receipts
 FOR EACH ROW EXECUTE FUNCTION idenqa.guard_provider_callback_receipt();
