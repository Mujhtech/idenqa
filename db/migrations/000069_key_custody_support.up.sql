-- HMAC key lifecycle and delegated/emergency support access.
--
-- HMAC key versions are immutable: a version's wrapped material, identifier,
-- and creation instant can never change, and the only permitted transitions
-- are active -> disabled -> retired. Domains keep a monotonic generation and
-- an explicit issuance pointer so disabling the active version fails closed.
CREATE TABLE idenqa.hmac_key_domains (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 domain text NOT NULL,
 generation bigint NOT NULL CHECK(generation>=0),
 active_version bigint,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,domain),
 CHECK(domain ~ '^[a-z][a-z0-9._-]{2,199}$'),
 CHECK(updated_at>=created_at),
 CONSTRAINT hmac_domain_active_positive CHECK(active_version IS NULL OR active_version BETWEEN 1 AND 64)
);

CREATE TABLE idenqa.hmac_keys (
 tenant_id text NOT NULL,
 domain text NOT NULL,
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 64),
 id text NOT NULL,
 state text NOT NULL CHECK(state IN ('active','disabled','retired')),
 wrapped_key jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 retired_at timestamptz,
 PRIMARY KEY(tenant_id,domain,version),
 UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,domain) REFERENCES idenqa.hmac_key_domains(tenant_id,domain),
 CHECK(id ~ '^hmk_[0-9A-HJKMNP-TV-Z]{26}$'),
 CHECK((state='retired')=(retired_at IS NOT NULL))
);

CREATE INDEX hmac_keys_active ON idenqa.hmac_keys(tenant_id,domain,state,version);

CREATE FUNCTION idenqa.protect_hmac_key_version()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'hmac key versions are immutable';
    END IF;
    IF NEW.tenant_id<>OLD.tenant_id OR NEW.domain<>OLD.domain OR NEW.version<>OLD.version
       OR NEW.id<>OLD.id OR NEW.wrapped_key<>OLD.wrapped_key OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'hmac key versions are immutable';
    END IF;
    IF NOT ((OLD.state='active' AND NEW.state='disabled')
         OR (OLD.state='disabled' AND NEW.state='retired')) THEN
        RAISE EXCEPTION 'hmac key state transition is not permitted';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER hmac_keys_transition BEFORE UPDATE OR DELETE ON idenqa.hmac_keys
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_hmac_key_version();

CREATE FUNCTION idenqa.protect_hmac_key_domain()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'hmac key domains are durable';
    END IF;
    IF NEW.tenant_id<>OLD.tenant_id OR NEW.domain<>OLD.domain OR NEW.created_at<>OLD.created_at
       OR NEW.generation<OLD.generation THEN
        RAISE EXCEPTION 'hmac key domain identity is immutable';
    END IF;
    IF OLD.active_version IS NOT NULL AND NEW.active_version IS NOT NULL
       AND NEW.active_version<OLD.active_version THEN
        RAISE EXCEPTION 'hmac key domain active version cannot move backwards';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER hmac_key_domains_transition BEFORE UPDATE OR DELETE ON idenqa.hmac_key_domains
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_hmac_key_domain();

-- Delegated support access. Grants are immutable except for the single
-- active -> revoked transition.
CREATE TABLE idenqa.support_grants (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 id text NOT NULL,
 grantee text NOT NULL,
 patterns jsonb NOT NULL,
 permissions jsonb NOT NULL,
 reason text NOT NULL,
 granted_by text NOT NULL,
 state text NOT NULL CHECK(state IN ('active','revoked')),
 version bigint NOT NULL CHECK(version>=1),
 starts_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 revoked_at timestamptz,
 revoked_by text,
 revocation_reason text,
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,granted_by) REFERENCES idenqa.api_keys(tenant_id,id),
 CHECK(id ~ '^spt_[0-9A-HJKMNP-TV-Z]{26}$'),
 CHECK(expires_at>starts_at),
 CHECK(updated_at>=created_at),
 CHECK((state='revoked')=(revoked_at IS NOT NULL)),
 CHECK((state='revoked')=(revoked_by IS NOT NULL)),
 CHECK(grantee ~ '^[a-z0-9][a-z0-9._-]{0,127}$')
);

CREATE INDEX support_grants_grantee ON idenqa.support_grants(tenant_id,grantee,state,expires_at);

CREATE FUNCTION idenqa.protect_support_grant()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'support grants are durable';
    END IF;
    IF NEW.tenant_id<>OLD.tenant_id OR NEW.id<>OLD.id OR NEW.grantee<>OLD.grantee
       OR NEW.patterns<>OLD.patterns OR NEW.permissions<>OLD.permissions OR NEW.reason<>OLD.reason
       OR NEW.granted_by<>OLD.granted_by OR NEW.starts_at<>OLD.starts_at OR NEW.expires_at<>OLD.expires_at
       OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'support grant identity is immutable';
    END IF;
    IF NOT (OLD.state='active' AND NEW.state='revoked') THEN
        RAISE EXCEPTION 'support grant transition is not permitted';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER support_grants_transition BEFORE UPDATE OR DELETE ON idenqa.support_grants
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_support_grant();

-- Break-glass requests. Approval and denial are recorded with the approving
-- actor; uses are append-only.
CREATE TABLE idenqa.break_glass_requests (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 id text NOT NULL,
 requester text NOT NULL,
 reason text NOT NULL,
 permissions jsonb NOT NULL,
 duration_seconds bigint NOT NULL CHECK(duration_seconds BETWEEN 300 AND 14400),
 state text NOT NULL CHECK(state IN ('requested','approved','denied','revoked')),
 version bigint NOT NULL CHECK(version>=1),
 requested_at timestamptz NOT NULL,
 approval_expires_at timestamptz NOT NULL,
 approved_at timestamptz,
 approved_by text,
 usable_until timestamptz,
 denied_at timestamptz,
 denied_by text,
 revoked_at timestamptz,
 revoked_by text,
 revocation_reason text,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CHECK(id ~ '^bge_[0-9A-HJKMNP-TV-Z]{26}$'),
 CHECK(approval_expires_at>requested_at),
 CHECK(updated_at>=created_at),
 CHECK((state='approved')=(approved_at IS NOT NULL)),
 CHECK((state='approved')=(approved_by IS NOT NULL)),
 CHECK((state='approved')=(usable_until IS NOT NULL)),
 CHECK((state='denied')=(denied_at IS NOT NULL)),
 CHECK((state='revoked')=(revoked_at IS NOT NULL))
);

CREATE INDEX break_glass_requests_state ON idenqa.break_glass_requests(tenant_id,state,approval_expires_at);

CREATE FUNCTION idenqa.protect_break_glass_request()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'break-glass requests are durable';
    END IF;
    IF NEW.tenant_id<>OLD.tenant_id OR NEW.id<>OLD.id OR NEW.requester<>OLD.requester
       OR NEW.reason<>OLD.reason OR NEW.permissions<>OLD.permissions OR NEW.duration_seconds<>OLD.duration_seconds
       OR NEW.requested_at<>OLD.requested_at OR NEW.approval_expires_at<>OLD.approval_expires_at
       OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'break-glass request identity is immutable';
    END IF;
    IF NOT ((OLD.state='requested' AND NEW.state IN ('approved','denied'))
         OR (OLD.state IN ('requested','approved') AND NEW.state='revoked')) THEN
        RAISE EXCEPTION 'break-glass request transition is not permitted';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER break_glass_requests_transition BEFORE UPDATE OR DELETE ON idenqa.break_glass_requests
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_break_glass_request();

CREATE TABLE idenqa.break_glass_uses (
 tenant_id text NOT NULL,
 request_id text NOT NULL,
 sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 256),
 permission text NOT NULL,
 target text NOT NULL,
 actor text NOT NULL,
 used_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,request_id,sequence),
 FOREIGN KEY(tenant_id,request_id) REFERENCES idenqa.break_glass_requests(tenant_id,id),
 CHECK(length(permission) BETWEEN 3 AND 128),
 CHECK(length(target) BETWEEN 1 AND 256)
);

CREATE FUNCTION idenqa.protect_break_glass_use()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'break-glass uses are append-only';
END $$;

CREATE TRIGGER break_glass_uses_append_only BEFORE UPDATE OR DELETE ON idenqa.break_glass_uses
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_break_glass_use();

DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['hmac_key_domains','hmac_keys','support_grants','break_glass_requests','break_glass_uses'] LOOP
 EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',n);
 EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',n);
 EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',n);
 EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',n);
 END LOOP;
END $$;

REVOKE ALL ON FUNCTION idenqa.protect_hmac_key_version() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_hmac_key_domain() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_support_grant() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_break_glass_request() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_break_glass_use() FROM PUBLIC;
