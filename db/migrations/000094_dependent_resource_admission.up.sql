CREATE TABLE idenqa.admission_resource_sources (
 tenant_id text NOT NULL,
 operation_id text NOT NULL,
 verification_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('provider','model','evidence','review')),
 source_id text NOT NULL,
 PRIMARY KEY(tenant_id,kind,source_id),
 UNIQUE(tenant_id,operation_id),
 FOREIGN KEY(tenant_id,operation_id) REFERENCES idenqa.admission_operations(tenant_id,id),
 FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.admission_operations(tenant_id,id)
);
ALTER TABLE idenqa.admission_resource_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.admission_resource_sources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON idenqa.admission_resource_sources
 USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));
REVOKE ALL ON idenqa.admission_resource_sources FROM PUBLIC;
CREATE TRIGGER admission_resource_source_immutable BEFORE UPDATE OR DELETE ON idenqa.admission_resource_sources
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_append_only();

-- Absence of reconciliation metadata is not proof that a started object Put
-- produced no bytes. Every started attempt remains reserved until its exact
-- durable object reconciliation establishes deletion (or the accepted asset is
-- deleted for a retained object). Never infer physical cleanup from a lease.
CREATE FUNCTION idenqa.admission_evidence_cleanup_proven(p_tenant text,p_upload text)
RETURNS boolean LANGUAGE sql VOLATILE AS $$
 SELECT EXISTS(
  SELECT 1 FROM idenqa.evidence_upload_intents u
  WHERE u.tenant_id=p_tenant AND u.id=p_upload AND u.state IN ('accepted','rejected','expired')
   AND NOT EXISTS(SELECT 1 FROM idenqa.evidence_object_reconciliations r
    WHERE r.tenant_id=u.tenant_id AND r.upload_id=u.id AND r.state IN ('pending','claimed'))
   AND NOT EXISTS(SELECT 1 FROM idenqa.evidence_assets a
    WHERE a.tenant_id=u.tenant_id AND a.id=u.evidence_id AND a.state<>'deleted')
   AND (u.state<>'accepted' OR EXISTS(SELECT 1 FROM idenqa.evidence_assets a
    WHERE a.tenant_id=u.tenant_id AND a.id=u.evidence_id AND a.state='deleted'))
   AND (u.attempt=0 OR u.attempt=(SELECT count(DISTINCT r.upload_attempt)
    FROM idenqa.evidence_object_reconciliations r
    WHERE r.tenant_id=u.tenant_id AND r.upload_id=u.id AND r.upload_attempt BETWEEN 1 AND u.attempt
     AND (r.state='deleted' OR (u.state='accepted' AND r.state='retained'))))
 );
$$;

-- Called only by owning source transitions. No expiry timer infers the lifetime
-- of a provider call or an object whose physical cleanup remains uncertain.
CREATE FUNCTION idenqa.admission_resource_release(p_tenant text,p_kind text,p_source text,p_deleted boolean)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE reservation record; v_operation text; v_revision bigint;
BEGIN
 SELECT operation_id INTO v_operation FROM idenqa.admission_resource_sources
  WHERE tenant_id=p_tenant AND kind=p_kind AND source_id=p_source;
 IF NOT FOUND THEN RETURN; END IF;
 IF p_kind IN ('provider','model') AND NOT EXISTS(SELECT 1 FROM idenqa.verification_attempts WHERE tenant_id=p_tenant AND id=p_source AND runner_kind=p_kind AND state<>'running') THEN
  RAISE EXCEPTION 'dependent workload is not terminal';
 END IF;
 IF p_kind='review' AND NOT EXISTS(SELECT 1 FROM idenqa.review_cases WHERE tenant_id=p_tenant AND id=p_source AND state='resolved') THEN
  RAISE EXCEPTION 'review workload is not resolved';
 END IF;
 IF p_kind IN ('provider','model','review') AND p_deleted THEN RAISE EXCEPTION 'dependent metadata deletion is not established'; END IF;
 IF p_kind='evidence' AND NOT EXISTS(SELECT 1 FROM idenqa.evidence_upload_intents WHERE tenant_id=p_tenant AND id=p_source AND state IN ('accepted','rejected','expired')) THEN
  RAISE EXCEPTION 'evidence upload is not terminal';
 END IF;
 IF p_kind='evidence' AND p_deleted AND NOT idenqa.admission_evidence_cleanup_proven(p_tenant,p_source) THEN RAISE EXCEPTION 'physical evidence deletion is not established'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('admission-v1/'||p_tenant,0));
 SELECT snapshot_revision INTO v_revision FROM idenqa.admission_operations WHERE tenant_id=p_tenant AND id=v_operation;
 FOR reservation IN SELECT * FROM idenqa.admission_reservations
  WHERE tenant_id=p_tenant AND operation_id=v_operation AND NOT released
   AND (lifetime='terminal' OR (p_deleted AND lifetime='delete')) ORDER BY kind,key LOOP
  UPDATE idenqa.admission_counters SET used=used-reservation.quantity
   WHERE tenant_id=p_tenant AND kind=reservation.kind AND key=reservation.key AND window_key=reservation.window_key
    AND used>=reservation.quantity AND NOT dirty;
  IF NOT FOUND THEN
   UPDATE idenqa.admission_counters SET dirty=true
    WHERE tenant_id=p_tenant AND kind=reservation.kind AND key=reservation.key AND window_key=reservation.window_key;
  END IF;
  UPDATE idenqa.admission_reservations SET released=true WHERE tenant_id=p_tenant AND operation_id=v_operation AND kind=reservation.kind AND key=reservation.key;
 END LOOP;
 UPDATE idenqa.admission_operations SET state=CASE WHEN p_deleted THEN 'deleted' ELSE 'terminal' END
  WHERE tenant_id=p_tenant AND id=v_operation AND (state='active' OR p_deleted);
 INSERT INTO idenqa.admission_events(tenant_id,kind,reference,revision)
  VALUES(p_tenant,CASE WHEN p_deleted THEN 'deleted' ELSE 'terminal' END,v_operation,v_revision) ON CONFLICT DO NOTHING;
END; $$;

CREATE FUNCTION idenqa.admission_dependent_closed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_upload text; v_tenant text; v_evidence text; v_state text; physically_deleted boolean:=false;
BEGIN
 IF TG_TABLE_NAME='verification_attempts' THEN
  IF NEW.state<>'running' THEN PERFORM idenqa.admission_resource_release(NEW.tenant_id,NEW.runner_kind,NEW.id,false); END IF;
 ELSIF TG_TABLE_NAME='review_cases' THEN
  IF NEW.state='resolved' THEN PERFORM idenqa.admission_resource_release(NEW.tenant_id,'review',NEW.id,false); END IF;
 ELSE
  v_tenant:=NEW.tenant_id;
  IF TG_TABLE_NAME='evidence_upload_intents' THEN v_upload:=NEW.id; v_evidence:=NEW.evidence_id; v_state:=NEW.state;
  ELSE
   SELECT id,evidence_id,state INTO v_upload,v_evidence,v_state FROM idenqa.evidence_upload_intents
    WHERE tenant_id=v_tenant AND evidence_id=NEW.evidence_id;
  END IF;
  IF v_upload IS NULL OR NOT EXISTS(SELECT 1 FROM idenqa.admission_resource_sources WHERE tenant_id=v_tenant AND kind='evidence' AND source_id=v_upload) THEN RETURN NEW; END IF;
  IF v_state IN ('accepted','rejected','expired') THEN
   physically_deleted:=idenqa.admission_evidence_cleanup_proven(v_tenant,v_upload);
   PERFORM idenqa.admission_resource_release(v_tenant,'evidence',v_upload,physically_deleted);
  END IF;
 END IF;
 RETURN NEW;
END; $$;
-- Evidence assets name their primary key id, so use a tiny source-specific
-- wrapper rather than coercing a trigger record with different field names.
CREATE FUNCTION idenqa.admission_evidence_deleted() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_upload text;
BEGIN
 IF NEW.state='deleted' THEN
  SELECT id INTO v_upload FROM idenqa.evidence_upload_intents WHERE tenant_id=NEW.tenant_id AND evidence_id=NEW.id;
  IF FOUND AND EXISTS(SELECT 1 FROM idenqa.admission_resource_sources WHERE tenant_id=NEW.tenant_id AND kind='evidence' AND source_id=v_upload) THEN
   IF NOT idenqa.admission_evidence_cleanup_proven(NEW.tenant_id,v_upload) THEN RETURN NEW; END IF;
   PERFORM idenqa.admission_resource_release(NEW.tenant_id,'evidence',v_upload,true);
  END IF;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER admission_attempt_closed AFTER UPDATE OF state ON idenqa.verification_attempts FOR EACH ROW EXECUTE FUNCTION idenqa.admission_dependent_closed();
CREATE TRIGGER admission_review_closed AFTER UPDATE OF state ON idenqa.review_cases FOR EACH ROW EXECUTE FUNCTION idenqa.admission_dependent_closed();
CREATE TRIGGER admission_upload_closed AFTER UPDATE OF state ON idenqa.evidence_upload_intents FOR EACH ROW EXECUTE FUNCTION idenqa.admission_dependent_closed();
CREATE TRIGGER admission_object_cleaned AFTER UPDATE OF state ON idenqa.evidence_object_reconciliations FOR EACH ROW EXECUTE FUNCTION idenqa.admission_dependent_closed();
CREATE TRIGGER admission_evidence_deleted AFTER UPDATE OF state ON idenqa.evidence_assets FOR EACH ROW EXECUTE FUNCTION idenqa.admission_evidence_deleted();
REVOKE ALL ON FUNCTION idenqa.admission_evidence_cleanup_proven(text,text),idenqa.admission_resource_release(text,text,text,boolean),idenqa.admission_dependent_closed(),idenqa.admission_evidence_deleted() FROM PUBLIC;

-- A fixed reservation covers one complete physical object. A new managed
-- attempt cannot multiply orphan bytes under that reservation. Exact replay of
-- the existing claim does not increase attempt and therefore does not recheck.
CREATE FUNCTION idenqa.admission_upload_retry_safe() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state<>'uploading' OR NEW.attempt<=OLD.attempt OR OLD.attempt=0 THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM idenqa.admission_resource_sources
  WHERE tenant_id=NEW.tenant_id AND kind='evidence' AND source_id=NEW.id) THEN RETURN NEW; END IF;
 IF OLD.attempt<>(SELECT count(DISTINCT r.upload_attempt) FROM idenqa.evidence_object_reconciliations r
  WHERE r.tenant_id=NEW.tenant_id AND r.upload_id=NEW.id AND r.upload_attempt BETWEEN 1 AND OLD.attempt AND r.state='deleted')
  OR EXISTS(SELECT 1 FROM idenqa.evidence_object_reconciliations r
   WHERE r.tenant_id=NEW.tenant_id AND r.upload_id=NEW.id AND r.state IN ('pending','claimed')) THEN
  RAISE EXCEPTION 'managed upload requires exact prior object cleanup' USING ERRCODE='PQA01';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER admission_upload_retry_safe BEFORE UPDATE OF state,attempt ON idenqa.evidence_upload_intents
 FOR EACH ROW EXECUTE FUNCTION idenqa.admission_upload_retry_safe();
REVOKE ALL ON FUNCTION idenqa.admission_upload_retry_safe() FROM PUBLIC;
