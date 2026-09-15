CREATE TABLE idenqa.provider_requests (
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

CREATE TABLE idenqa.provider_dispatches (
    tenant_id text NOT NULL,
    attempt_id text NOT NULL,
    request_digest text NOT NULL,
    claimed_at timestamptz NOT NULL,
    result_body jsonb,
    PRIMARY KEY (tenant_id, attempt_id),
    FOREIGN KEY (tenant_id, attempt_id) REFERENCES idenqa.provider_requests(tenant_id, attempt_id),
    CHECK (result_body IS NULL OR (jsonb_typeof(result_body) = 'object' AND octet_length(result_body::text) <= 262144))
);

ALTER TABLE idenqa.provider_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_requests_tenant ON idenqa.provider_requests
 USING (tenant_id = current_setting('idenqa.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

ALTER TABLE idenqa.provider_dispatches ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_dispatches FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_dispatches_tenant ON idenqa.provider_dispatches
 USING (tenant_id = current_setting('idenqa.tenant_id', true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

CREATE FUNCTION idenqa.guard_provider_runtime() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' OR TG_TABLE_NAME = 'provider_requests' THEN
  RAISE EXCEPTION 'provider request history is immutable';
 END IF;
 IF ROW(NEW.tenant_id, NEW.attempt_id, NEW.request_digest, NEW.claimed_at)
    IS DISTINCT FROM ROW(OLD.tenant_id, OLD.attempt_id, OLD.request_digest, OLD.claimed_at)
    OR OLD.result_body IS NOT NULL OR NEW.result_body IS NULL THEN
  RAISE EXCEPTION 'provider dispatch history is immutable';
 END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER provider_request_immutable BEFORE UPDATE OR DELETE ON idenqa.provider_requests
 FOR EACH ROW EXECUTE FUNCTION idenqa.guard_provider_runtime();
CREATE TRIGGER provider_dispatch_immutable BEFORE UPDATE OR DELETE ON idenqa.provider_dispatches
 FOR EACH ROW EXECUTE FUNCTION idenqa.guard_provider_runtime();

CREATE FUNCTION idenqa.list_ready_provider_captures(observed_at timestamptz, batch_size integer, target_tenant text, target_policy text, target_profile text)
RETURNS TABLE (tenant_id text, verification_id text)
LANGUAGE sql SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
SET row_security = on
AS $$
    SELECT sessions.tenant_id, sessions.id
    FROM idenqa.verification_sessions AS sessions
    JOIN idenqa.processing_authorities AS authorities
      ON authorities.tenant_id = sessions.tenant_id AND authorities.id = sessions.authority_id
    JOIN LATERAL (
        SELECT response.id, response.action, response.recorded_at
        FROM idenqa.subject_responses AS response
        WHERE response.tenant_id = sessions.tenant_id
          AND response.authority_id = sessions.authority_id
        ORDER BY response.recorded_at DESC, response.id DESC
        LIMIT 1
    ) AS latest_response ON true
    WHERE observed_at IS NOT NULL AND batch_size BETWEEN 1 AND 100
      AND sessions.tenant_id = target_tenant AND sessions.policy_id = target_policy AND sessions.source_profile_digest = target_profile
      AND sessions.state = 'collecting'
      AND sessions.capture_completed_at <= observed_at
      AND sessions.expires_at > observed_at
      AND sessions.policy_id IS NOT NULL AND sessions.decision_id IS NOT NULL
      AND latest_response.recorded_at <= observed_at
      AND (latest_response.action = 'consent' OR
          (NOT authorities.consent_required AND latest_response.action = 'acknowledge'))
      AND EXISTS (
          SELECT 1 FROM idenqa.evidence_upload_intents AS uploads
          WHERE uploads.tenant_id = sessions.tenant_id AND uploads.verification_id = sessions.id
            AND uploads.state = 'accepted'
      )
      AND NOT EXISTS (
          SELECT 1 FROM idenqa.evidence_upload_intents AS uploads
          WHERE uploads.tenant_id = sessions.tenant_id AND uploads.verification_id = sessions.id
            AND uploads.state = 'accepted' AND uploads.response_id <> latest_response.id
      )
      AND authorities.state = 'active'
      AND authorities.valid_from <= observed_at AND authorities.expires_at > observed_at
      AND NOT EXISTS (
          SELECT 1 FROM idenqa.verification_checks AS checks
          WHERE checks.tenant_id = sessions.tenant_id AND checks.verification_id = sessions.id
      )
    ORDER BY sessions.capture_completed_at, sessions.tenant_id, sessions.id
    LIMIT LEAST(batch_size, 100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_ready_provider_captures(timestamptz,integer,text,text,text) FROM PUBLIC;
