CREATE TABLE idenqa.provider_async_operations (
 tenant_id text NOT NULL,
 attempt_id text NOT NULL,
 request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
 provider_job_id text CHECK (provider_job_id ~ '^[A-Za-z0-9_-]{1,128}$'),
 fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
 lease_expires_at timestamptz,
 next_poll_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,attempt_id),
 FOREIGN KEY (tenant_id,attempt_id) REFERENCES idenqa.provider_dispatches(tenant_id,attempt_id)
);

ALTER TABLE idenqa.provider_async_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.provider_async_operations FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_async_tenant ON idenqa.provider_async_operations
 USING (tenant_id = current_setting('idenqa.tenant_id',true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id',true));

CREATE FUNCTION idenqa.guard_provider_async() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.tenant_id,NEW.attempt_id,NEW.request_digest) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.attempt_id,OLD.request_digest)
 OR (OLD.provider_job_id IS NOT NULL AND NEW.provider_job_id IS DISTINCT FROM OLD.provider_job_id)
 OR NEW.fence < OLD.fence THEN RAISE EXCEPTION 'provider async identity is immutable'; END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER provider_async_guard BEFORE UPDATE ON idenqa.provider_async_operations
 FOR EACH ROW EXECUTE FUNCTION idenqa.guard_provider_async();
