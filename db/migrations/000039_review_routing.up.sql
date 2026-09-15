CREATE TABLE idenqa.policy_routing_receipts (
 tenant_id text NOT NULL,
 request_id text NOT NULL,
 verification_id text NOT NULL,
 snapshot_digest text NOT NULL,
 evaluation_digest text NOT NULL,
 requested_at timestamptz NOT NULL,
 lifecycle_event_id text NOT NULL,
 FOREIGN KEY (tenant_id,lifecycle_event_id) REFERENCES idenqa.verification_transitions (tenant_id,event_id) DEFERRABLE INITIALLY DEFERRED,
 PRIMARY KEY (tenant_id, request_id),
 UNIQUE (tenant_id, verification_id, request_id),
 FOREIGN KEY (tenant_id, verification_id, snapshot_digest, evaluation_digest)
 REFERENCES idenqa.policy_evaluations (tenant_id, verification_id, snapshot_digest, evaluation_digest),
 CHECK (request_id ~ '^dec_[0-9A-HJKMNP-TV-Z]{26}$')
);

ALTER TABLE idenqa.policy_routing_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_routing_receipts FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.policy_routing_receipts
 USING (tenant_id = current_setting('idenqa.tenant_id',true))
 WITH CHECK (tenant_id = current_setting('idenqa.tenant_id',true));
CREATE TRIGGER policy_routing_immutable BEFORE UPDATE OR DELETE ON idenqa.policy_routing_receipts
 FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();

ALTER TABLE idenqa.review_cases ALTER COLUMN challenged_decision_id DROP NOT NULL;
ALTER TABLE idenqa.review_cases ADD COLUMN routing_request_id text;
ALTER TABLE idenqa.review_cases ADD CONSTRAINT review_case_routing_fk
 FOREIGN KEY (tenant_id, verification_id, routing_request_id)
 REFERENCES idenqa.policy_routing_receipts (tenant_id, verification_id, request_id);
ALTER TABLE idenqa.review_cases ADD CONSTRAINT review_case_origin
 CHECK ((challenged_decision_id IS NOT NULL) <> (routing_request_id IS NOT NULL));

CREATE UNIQUE INDEX review_case_routing_unique ON idenqa.review_cases (tenant_id,routing_request_id)
 WHERE routing_request_id IS NOT NULL;

ALTER TABLE idenqa.review_cases DROP CONSTRAINT review_case_region;
ALTER TABLE idenqa.review_cases ADD CONSTRAINT review_case_region CHECK (region ~ '^[a-z][a-z0-9._:-]{0,63}$');

REVOKE ALL ON idenqa.policy_routing_receipts FROM PUBLIC;

CREATE FUNCTION idenqa.protect_review_origin() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.tenant_id,NEW.id,NEW.verification_id,NEW.challenged_decision_id,NEW.routing_request_id,NEW.region,NEW.required_certification,NEW.oversight)
 IS DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.verification_id,OLD.challenged_decision_id,OLD.routing_request_id,OLD.region,OLD.required_certification,OLD.oversight) THEN
  RAISE EXCEPTION 'review origin and requirements are immutable';
 END IF;
 RETURN NEW;
END;
$$;

CREATE TRIGGER protect_review_origin BEFORE UPDATE ON idenqa.review_cases
 FOR EACH ROW EXECUTE FUNCTION idenqa.protect_review_origin();

REVOKE ALL ON FUNCTION idenqa.protect_review_origin() FROM PUBLIC;
