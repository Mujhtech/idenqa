CREATE TABLE idenqa.admission_snapshots (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 revision bigint NOT NULL CHECK(revision>0),
 id text NOT NULL,
 deployment_id text NOT NULL, region text NOT NULL,
 valid_from timestamptz NOT NULL, valid_until timestamptz NOT NULL,
 body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 installed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,revision), UNIQUE(tenant_id,id),
 CHECK(valid_until>valid_from),
 CHECK(coalesce(body->'scope'->>'tenantId'=tenant_id AND body->'scope'->>'deploymentId'=deployment_id
   AND body->'scope'->>'region'=region AND (body->>'revision')::bigint=revision AND body->>'id'=id,false))
);
CREATE INDEX admission_snapshot_effective ON idenqa.admission_snapshots(tenant_id,revision DESC) INCLUDE(valid_from);
CREATE TABLE idenqa.admission_heads (
 tenant_id text PRIMARY KEY REFERENCES idenqa.tenants(id),
 active_revision bigint,
 FOREIGN KEY(tenant_id,active_revision) REFERENCES idenqa.admission_snapshots(tenant_id,revision)
);
CREATE TABLE idenqa.admission_pending (
 tenant_id text NOT NULL, revision bigint NOT NULL, valid_from timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,revision),
 FOREIGN KEY(tenant_id,revision) REFERENCES idenqa.admission_snapshots(tenant_id,revision)
);
CREATE TABLE idenqa.admission_limit_definitions (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 kind text NOT NULL, key text NOT NULL, window_nanoseconds bigint NOT NULL CHECK(window_nanoseconds>=0),
 PRIMARY KEY(tenant_id,kind,key)
);
CREATE TABLE idenqa.admission_operations (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 id text NOT NULL, rule text NOT NULL, digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 snapshot_revision bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('active','terminal','deleted')),
 accepted_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,snapshot_revision) REFERENCES idenqa.admission_snapshots(tenant_id,revision)
);
CREATE TABLE idenqa.admission_counters (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 kind text NOT NULL, key text NOT NULL, window_key bigint NOT NULL,
 used bigint NOT NULL CHECK(used>=0), dirty boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,kind,key,window_key)
);
CREATE TABLE idenqa.admission_reservations (
 tenant_id text NOT NULL, operation_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('rate','usage','concurrency','capacity','count','storage')),
 key text NOT NULL, window_key bigint NOT NULL, quantity bigint NOT NULL CHECK(quantity>0),
 lifetime text NOT NULL CHECK(lifetime IN ('window','terminal','delete')),
 released boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,operation_id,kind,key),
 FOREIGN KEY(tenant_id,operation_id) REFERENCES idenqa.admission_operations(tenant_id,id),
 FOREIGN KEY(tenant_id,kind,key,window_key) REFERENCES idenqa.admission_counters(tenant_id,kind,key,window_key),
 CHECK(NOT released OR lifetime<>'window')
);
CREATE INDEX admission_reservation_counter ON idenqa.admission_reservations(tenant_id,kind,key,window_key) INCLUDE(quantity) WHERE NOT released;
CREATE TABLE idenqa.admission_events (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 kind text NOT NULL CHECK(kind IN ('snapshot','accepted','terminal','deleted','counter_rebuilt')),
 reference text NOT NULL, revision bigint NOT NULL,
 actor_id text, reason_reference text,
 counter_kind text, counter_key text, counter_window bigint,
 counter_before bigint, counter_after bigint, counter_was_dirty boolean,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,kind,reference),
 CHECK((kind='counter_rebuilt')=(actor_id IS NOT NULL AND reason_reference IS NOT NULL
   AND counter_kind IS NOT NULL AND counter_key IS NOT NULL AND counter_window IS NOT NULL
   AND counter_before IS NOT NULL AND counter_after IS NOT NULL AND counter_was_dirty IS NOT NULL))
);
DO $$ DECLARE name text; BEGIN
 FOREACH name IN ARRAY ARRAY['admission_snapshots','admission_heads','admission_pending','admission_limit_definitions','admission_operations','admission_counters','admission_reservations','admission_events'] LOOP
  EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY tenant_scope ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',name);
  EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',name);
 END LOOP;
END $$;
CREATE FUNCTION idenqa.admission_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'admission history is immutable'; END; $$;
CREATE TRIGGER admission_snapshot_immutable BEFORE UPDATE OR DELETE ON idenqa.admission_snapshots
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_append_only();
CREATE TRIGGER admission_limit_immutable BEFORE UPDATE OR DELETE ON idenqa.admission_limit_definitions
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_append_only();
CREATE TRIGGER admission_event_immutable BEFORE UPDATE OR DELETE ON idenqa.admission_events
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_append_only();
CREATE FUNCTION idenqa.admission_head_monotonic() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.tenant_id<>OLD.tenant_id OR
  (OLD.active_revision IS NOT NULL AND (NEW.active_revision IS NULL OR NEW.active_revision<OLD.active_revision)) THEN
  RAISE EXCEPTION 'admission head cannot move backwards';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER admission_head_monotonic BEFORE UPDATE OR DELETE ON idenqa.admission_heads
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_head_monotonic();
CREATE FUNCTION idenqa.admission_pending_protected() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' OR NOT EXISTS(SELECT 1 FROM idenqa.admission_heads WHERE tenant_id=OLD.tenant_id AND active_revision>=OLD.revision) THEN
  RAISE EXCEPTION 'admission pending revision cannot be discarded';
 END IF;
 RETURN OLD;
END; $$;
CREATE TRIGGER admission_pending_protected BEFORE UPDATE OR DELETE ON idenqa.admission_pending
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_pending_protected();
CREATE FUNCTION idenqa.admission_operation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.tenant_id<>OLD.tenant_id OR NEW.id<>OLD.id OR NEW.rule<>OLD.rule
  OR NEW.digest<>OLD.digest OR NEW.snapshot_revision<>OLD.snapshot_revision OR NEW.accepted_at<>OLD.accepted_at
  OR (OLD.state='terminal' AND NEW.state NOT IN ('terminal','deleted')) OR (OLD.state='deleted' AND NEW.state<>'deleted') THEN
  RAISE EXCEPTION 'admission operation meaning and closure are immutable';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER admission_operation_immutable BEFORE UPDATE OR DELETE ON idenqa.admission_operations
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_operation_immutable();
CREATE FUNCTION idenqa.admission_reservation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR (to_jsonb(NEW)-'released')<>(to_jsonb(OLD)-'released') OR (OLD.released AND NOT NEW.released) THEN
  RAISE EXCEPTION 'admission reservation meaning and release are immutable';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER admission_reservation_immutable BEFORE UPDATE OR DELETE ON idenqa.admission_reservations
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_reservation_immutable();
-- The source row is already locked by the owning mutation before this tenant
-- lock. Release is part of the same commit; no timer guesses an external lifetime.
CREATE FUNCTION idenqa.admission_verification_closed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE reservation record; revision bigint; deleted boolean := TG_OP='DELETE';
BEGIN
 IF NOT deleted AND NEW.state NOT IN ('completed','cancelled','expired','failed') THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM idenqa.admission_operations WHERE tenant_id=OLD.tenant_id AND id=OLD.id) THEN
  IF deleted THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('admission-v1/'||OLD.tenant_id,0));
 SELECT snapshot_revision INTO revision FROM idenqa.admission_operations WHERE tenant_id=OLD.tenant_id AND id=OLD.id;
 FOR reservation IN SELECT * FROM idenqa.admission_reservations
   WHERE tenant_id=OLD.tenant_id AND operation_id=OLD.id AND NOT released
   AND (lifetime='terminal' OR (deleted AND lifetime='delete')) ORDER BY kind,key LOOP
  UPDATE idenqa.admission_counters SET used=used-reservation.quantity
   WHERE tenant_id=OLD.tenant_id AND kind=reservation.kind AND key=reservation.key AND window_key=reservation.window_key
     AND used>=reservation.quantity AND NOT dirty;
  IF NOT FOUND THEN
   UPDATE idenqa.admission_counters SET dirty=true
    WHERE tenant_id=OLD.tenant_id AND kind=reservation.kind AND key=reservation.key AND window_key=reservation.window_key;
  END IF;
  UPDATE idenqa.admission_reservations SET released=true
   WHERE tenant_id=OLD.tenant_id AND operation_id=OLD.id AND kind=reservation.kind AND key=reservation.key;
 END LOOP;
 UPDATE idenqa.admission_operations SET state=CASE WHEN deleted THEN 'deleted' ELSE 'terminal' END
  WHERE tenant_id=OLD.tenant_id AND id=OLD.id AND (state='active' OR deleted);
 INSERT INTO idenqa.admission_events(tenant_id,kind,reference,revision)
  VALUES(OLD.tenant_id,CASE WHEN deleted THEN 'deleted' ELSE 'terminal' END,OLD.id,revision) ON CONFLICT DO NOTHING;
 IF deleted THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER admission_verification_closed AFTER UPDATE OF state OR DELETE ON idenqa.verification_sessions
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_verification_closed();
REVOKE ALL ON FUNCTION idenqa.admission_verification_closed(),idenqa.admission_append_only(),idenqa.admission_operation_immutable(),idenqa.admission_reservation_immutable(),idenqa.admission_head_monotonic(),idenqa.admission_pending_protected() FROM PUBLIC;
