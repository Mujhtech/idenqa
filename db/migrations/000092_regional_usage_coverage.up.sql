DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolbypassrls)) THEN
  RAISE EXCEPTION 'usage coverage migration requires the dedicated RLS-bypass migration identity';
 END IF;
END $$;
CREATE TABLE idenqa.usage_source_clocks (
 tenant_id text PRIMARY KEY REFERENCES idenqa.tenants(id),
 through timestamptz NOT NULL,
 covered_from timestamptz NOT NULL DEFAULT clock_timestamp(),
 epoch text NOT NULL DEFAULT 'core_operational_v2',
 consumer_id text CHECK(consumer_id ~ '^[a-f0-9]{64}$'),
 routing_digest text CHECK(routing_digest ~ '^[a-f0-9]{64}$'),
 CHECK((consumer_id IS NULL)=(routing_digest IS NULL))
);
INSERT INTO idenqa.usage_source_clocks(tenant_id,through)
 SELECT tenant_id,max(created_at) FROM idenqa.usage_receipts GROUP BY tenant_id;
ALTER TABLE idenqa.usage_source_clocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.usage_source_clocks FORCE ROW LEVEL SECURITY;
CREATE POLICY usage_source_clocks_tenant ON idenqa.usage_source_clocks
 USING(tenant_id=current_setting('idenqa.tenant_id',true))
 WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));
REVOKE ALL ON idenqa.usage_source_clocks FROM PUBLIC;
CREATE FUNCTION idenqa.usage_source_clock_monotonic() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.epoch IS DISTINCT FROM OLD.epoch OR NEW.covered_from IS DISTINCT FROM OLD.covered_from) THEN
  IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolbypassrls))
   OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.epoch=OLD.epoch
   OR NEW.covered_from<=OLD.through OR NEW.through<NEW.covered_from
   OR NEW.consumer_id IS NULL OR NEW.routing_digest IS NULL
   OR (EXISTS(SELECT 1 FROM idenqa.usage_receipts WHERE tenant_id=OLD.tenant_id AND delivered_at IS NULL)
       AND (coalesce(current_setting('idenqa.usage_source_pending_transfer',true),'')<>'true'
            OR (OLD.consumer_id IS NOT NULL AND NEW.consumer_id IS DISTINCT FROM OLD.consumer_id))) THEN
   RAISE EXCEPTION 'usage source epoch handoff requires privileged drained transition';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.through<OLD.through
  OR NEW.covered_from IS DISTINCT FROM OLD.covered_from OR NEW.epoch IS DISTINCT FROM OLD.epoch
  OR (OLD.consumer_id IS NOT NULL AND (NEW.consumer_id IS DISTINCT FROM OLD.consumer_id OR NEW.routing_digest IS DISTINCT FROM OLD.routing_digest)) THEN
  RAISE EXCEPTION 'usage source clock must be monotonic';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER usage_source_clock_monotonic BEFORE UPDATE OR DELETE ON idenqa.usage_source_clocks
 FOR EACH ROW EXECUTE FUNCTION idenqa.usage_source_clock_monotonic();
CREATE TABLE idenqa.usage_source_handoffs (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 epoch text NOT NULL,
 previous_consumer text, previous_routing text,
 consumer_id text NOT NULL, routing_digest text NOT NULL,
 covered_from timestamptz NOT NULL,
 previous_from timestamptz NOT NULL,
 pending_carried bigint NOT NULL CHECK(pending_carried>=0),
 pending_digest text NOT NULL CHECK(pending_digest ~ '^[a-f0-9]{64}$'),
 actor_id text NOT NULL, reason_reference text NOT NULL,
 PRIMARY KEY(tenant_id,epoch)
);
ALTER TABLE idenqa.usage_source_handoffs ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.usage_source_handoffs FORCE ROW LEVEL SECURITY;
CREATE POLICY usage_source_handoffs_tenant ON idenqa.usage_source_handoffs
 USING(tenant_id=current_setting('idenqa.tenant_id',true))
 WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));
CREATE FUNCTION idenqa.usage_handoff_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'usage handoff audit is append only'; END; $$;
CREATE TRIGGER usage_handoff_immutable BEFORE UPDATE OR DELETE ON idenqa.usage_source_handoffs
 FOR EACH ROW EXECUTE FUNCTION idenqa.usage_handoff_immutable();
REVOKE ALL ON idenqa.usage_source_handoffs FROM PUBLIC;
