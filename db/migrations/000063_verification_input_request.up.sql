CREATE TABLE idenqa.verification_input_requests (
    id text NOT NULL,
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    case_id text,
    reason_codes text[] NOT NULL,
    actor_id text NOT NULL,
    requested_at timestamptz NOT NULL,
    superseded_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT verification_input_requests_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT verification_input_requests_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT verification_input_requests_case_fk
        FOREIGN KEY (tenant_id, case_id)
        REFERENCES idenqa.review_cases (tenant_id, id),
    CONSTRAINT verification_input_requests_id
        CHECK (id ~ '^inp_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_input_requests_actor
        CHECK (actor_id ~ '^[a-z]{3}_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_input_requests_case
        CHECK (case_id IS NULL OR case_id ~ '^rvc_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT verification_input_requests_reasons CHECK (
        cardinality(reason_codes) BETWEEN 1 AND 8
        AND array_position(reason_codes, NULL) IS NULL
        AND array_to_string(reason_codes, ',') ~ '^[a-z][a-z0-9._:-]{0,63}(,[a-z][a-z0-9._:-]{0,63}){0,7}$'
    ),
    CONSTRAINT verification_input_requests_times
        CHECK (superseded_at IS NULL OR superseded_at >= requested_at)
);

CREATE UNIQUE INDEX verification_input_requests_active
    ON idenqa.verification_input_requests (tenant_id, verification_id)
    WHERE superseded_at IS NULL;

CREATE INDEX verification_input_requests_history
    ON idenqa.verification_input_requests (tenant_id, verification_id, requested_at DESC, id DESC);

ALTER TABLE idenqa.verification_input_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.verification_input_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY verification_input_requests_tenant ON idenqa.verification_input_requests
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.verification_input_requests FROM PUBLIC;

CREATE FUNCTION idenqa.protect_verification_input_request()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'verification input request is immutable';
    END IF;
    IF OLD.superseded_at IS NOT NULL
       OR NEW.superseded_at IS NULL
       OR ROW(NEW.id, NEW.tenant_id, NEW.verification_id, NEW.case_id,
              NEW.reason_codes, NEW.actor_id, NEW.requested_at)
          IS DISTINCT FROM ROW(OLD.id, OLD.tenant_id, OLD.verification_id, OLD.case_id,
              OLD.reason_codes, OLD.actor_id, OLD.requested_at) THEN
        RAISE EXCEPTION 'verification input request only allows a one-way supersede';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER verification_input_request_immutable
    BEFORE UPDATE OR DELETE ON idenqa.verification_input_requests
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_verification_input_request();

REVOKE ALL ON FUNCTION idenqa.protect_verification_input_request() FROM PUBLIC;
