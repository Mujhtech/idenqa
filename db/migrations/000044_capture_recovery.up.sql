CREATE TABLE idenqa.capture_recoveries (
 tenant_id text NOT NULL,
 verification_id text NOT NULL,
 old_token_id text NOT NULL,
 new_token_id text NOT NULL,
 recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,new_token_id),
 UNIQUE(tenant_id,old_token_id),
 FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,old_token_id) REFERENCES idenqa.capture_tokens(tenant_id,id),
 FOREIGN KEY(tenant_id,new_token_id) REFERENCES idenqa.capture_tokens(tenant_id,id),
 CHECK(old_token_id<>new_token_id)
);

CREATE TABLE idenqa.capture_recovery_uploads (
 tenant_id text NOT NULL,
 new_token_id text NOT NULL,
 upload_id text NOT NULL,
 disposition text NOT NULL CHECK(disposition IN ('retained','abandoned')),
 PRIMARY KEY(tenant_id,new_token_id,upload_id),
 FOREIGN KEY(tenant_id,new_token_id) REFERENCES idenqa.capture_recoveries(tenant_id,new_token_id),
 FOREIGN KEY(tenant_id,upload_id) REFERENCES idenqa.evidence_upload_intents(tenant_id,id)
);

CREATE TRIGGER capture_recovery_immutable BEFORE UPDATE OR DELETE ON idenqa.capture_recoveries FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

CREATE TRIGGER capture_recovery_upload_immutable BEFORE UPDATE OR DELETE ON idenqa.capture_recovery_uploads FOR EACH ROW EXECUTE FUNCTION idenqa.reject_immutable_authority_row();

ALTER TABLE idenqa.capture_recoveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_recoveries FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_recovery_uploads ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.capture_recovery_uploads FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.capture_recoveries USING(tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),'')) WITH CHECK(tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),''));
CREATE POLICY tenant_scope ON idenqa.capture_recovery_uploads USING(tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),'')) WITH CHECK(tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),''));

REVOKE ALL ON idenqa.capture_recoveries,idenqa.capture_recovery_uploads FROM PUBLIC;
