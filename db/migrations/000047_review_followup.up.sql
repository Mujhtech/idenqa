CREATE TABLE idenqa.review_arbitrations (
 tenant_id text NOT NULL,case_id text NOT NULL,case_version bigint NOT NULL,reviewer_id text NOT NULL,
 actor_key_id text NOT NULL,resolution text NOT NULL CHECK(resolution IN ('satisfy','not_satisfy','request_input')),
 reason_code text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,case_id,case_version) REFERENCES idenqa.review_evaluation_requests(tenant_id,case_id,case_version),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TABLE idenqa.review_correction_evaluations (
 tenant_id text NOT NULL,case_id text NOT NULL,case_version bigint NOT NULL,snapshot_digest text NOT NULL,
 evaluation_digest text NOT NULL,decision_id text,actor_key_id text NOT NULL,reviewer_id text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,case_id,case_version),FOREIGN KEY(tenant_id,case_id) REFERENCES idenqa.review_cases(tenant_id,id),
 FOREIGN KEY(tenant_id,decision_id) REFERENCES idenqa.verification_decisions(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

ALTER TABLE idenqa.appeals ADD COLUMN requested_by text;
ALTER TABLE idenqa.appeals ADD CONSTRAINT appeal_requester FOREIGN KEY(tenant_id,requested_by) REFERENCES idenqa.api_keys(tenant_id,id);

CREATE TABLE idenqa.review_correction_intakes (
 tenant_id text NOT NULL,decision_id text NOT NULL,case_id text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,decision_id),UNIQUE(tenant_id,case_id),
 FOREIGN KEY(tenant_id,case_id) REFERENCES idenqa.review_cases(tenant_id,id),
 FOREIGN KEY(tenant_id,decision_id) REFERENCES idenqa.verification_decisions(tenant_id,id)
);

DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['review_arbitrations','review_correction_evaluations','review_correction_intakes'] LOOP
 EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',n);
 EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',n);
 EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',n);
 EXECUTE format('CREATE TRIGGER immutable_review_followup BEFORE UPDATE OR DELETE ON idenqa.%I FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change()',n);
 EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',n);
 END LOOP;
END $$;

ALTER TABLE idenqa.appeals DROP CONSTRAINT appeal_state;
ALTER TABLE idenqa.appeals ADD CONSTRAINT appeal_state CHECK(state IN ('requested','independent_review','awaiting_input','resolved','withdrawn','expired'));
ALTER TABLE idenqa.appeals DROP CONSTRAINT appeal_resolution;
ALTER TABLE idenqa.appeals ADD CONSTRAINT appeal_resolution CHECK(
 (state NOT IN ('resolved','awaiting_input') AND outcome IS NULL AND reason_code IS NULL AND superseding_decision_id IS NULL)
 OR (state='awaiting_input' AND outcome='more_input' AND reason_code IS NOT NULL AND superseding_decision_id IS NULL)
 OR (state='resolved' AND outcome IS NOT NULL AND reason_code IS NOT NULL AND ((outcome='overturned' AND superseding_decision_id IS NOT NULL) OR (outcome<>'overturned' AND superseding_decision_id IS NULL)))
);

CREATE UNIQUE INDEX one_open_appeal_per_case ON idenqa.appeals(tenant_id,case_id) WHERE state IN ('requested','independent_review','awaiting_input');

CREATE FUNCTION idenqa.list_expired_review_appeals(observed_at timestamptz,batch_size integer)
RETURNS TABLE(tenant_id text,appeal_id text,version bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,pg_temp SET row_security=off AS $$
 SELECT a.tenant_id,a.id,a.version FROM idenqa.appeals a WHERE batch_size BETWEEN 1 AND 100 AND a.state IN ('requested','independent_review','awaiting_input') AND a.deadline<=observed_at ORDER BY a.deadline,a.tenant_id,a.id LIMIT batch_size;
$$;

REVOKE ALL ON FUNCTION idenqa.list_expired_review_appeals(timestamptz,integer) FROM PUBLIC;

CREATE TABLE idenqa.review_appeal_history (
 tenant_id text NOT NULL, appeal_id text NOT NULL, version bigint NOT NULL,
 record jsonb NOT NULL, recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,appeal_id,version),
 FOREIGN KEY(tenant_id,appeal_id) REFERENCES idenqa.appeals(tenant_id,id)
);

ALTER TABLE idenqa.review_appeal_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_appeal_history FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON idenqa.review_appeal_history USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));

CREATE TRIGGER immutable_review_followup BEFORE UPDATE OR DELETE ON idenqa.review_appeal_history FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change();
REVOKE ALL ON idenqa.review_appeal_history FROM PUBLIC;
CREATE FUNCTION idenqa.record_review_appeal_history() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$ BEGIN
 INSERT INTO idenqa.review_appeal_history(tenant_id,appeal_id,version,record,recorded_at) VALUES(NEW.tenant_id,NEW.id,NEW.version,to_jsonb(NEW),NEW.updated_at);
 RETURN NEW;
END $$;

REVOKE ALL ON FUNCTION idenqa.record_review_appeal_history() FROM PUBLIC;

CREATE TRIGGER record_review_appeal_history AFTER INSERT OR UPDATE ON idenqa.appeals FOR EACH ROW EXECUTE FUNCTION idenqa.record_review_appeal_history();
