CREATE TABLE idenqa.model_requests (
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    verification_id text NOT NULL,
    check_id text NOT NULL,
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    request_body jsonb NOT NULL CHECK (jsonb_typeof(request_body) = 'object' AND octet_length(request_body::text) <= 327680),
    PRIMARY KEY (tenant_id, attempt_id),
    FOREIGN KEY (tenant_id, attempt_id) REFERENCES idenqa.verification_attempts(tenant_id, id),
    FOREIGN KEY (tenant_id, verification_id, check_id) REFERENCES idenqa.verification_checks(tenant_id, verification_id, id)
);

CREATE TABLE idenqa.model_dispatches (
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    request_digest text NOT NULL,
    claimed_at timestamptz NOT NULL,
    result_body jsonb,
    PRIMARY KEY (tenant_id, attempt_id),
    FOREIGN KEY (tenant_id, attempt_id) REFERENCES idenqa.model_requests(tenant_id, attempt_id),
    CHECK (result_body IS NULL OR (jsonb_typeof(result_body) = 'object' AND octet_length(result_body::text) <= 262144))
);

ALTER TABLE idenqa.model_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY model_requests_tenant ON idenqa.model_requests
 USING (tenant_id = current_setting('idenqa.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

ALTER TABLE idenqa.model_dispatches ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_dispatches FORCE ROW LEVEL SECURITY;

CREATE POLICY model_dispatches_tenant ON idenqa.model_dispatches
 USING (tenant_id = current_setting('idenqa.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

CREATE FUNCTION idenqa.guard_model_runtime() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' OR TG_TABLE_NAME = 'model_requests' THEN
  RAISE EXCEPTION 'model request history is immutable';
 END IF;
 IF ROW(NEW.tenant_id, NEW.attempt_id, NEW.request_digest, NEW.claimed_at)
    IS DISTINCT FROM ROW(OLD.tenant_id, OLD.attempt_id, OLD.request_digest, OLD.claimed_at)
    OR OLD.result_body IS NOT NULL OR NEW.result_body IS NULL THEN
  RAISE EXCEPTION 'model dispatch history is immutable';
 END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER model_request_immutable BEFORE UPDATE OR DELETE ON idenqa.model_requests
 FOR EACH ROW EXECUTE FUNCTION idenqa.guard_model_runtime();
CREATE TRIGGER model_dispatch_immutable BEFORE UPDATE OR DELETE ON idenqa.model_dispatches
 FOR EACH ROW EXECUTE FUNCTION idenqa.guard_model_runtime();
