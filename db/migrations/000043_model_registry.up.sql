CREATE TABLE idenqa.model_registries (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 name text NOT NULL CHECK(name ~ '^[a-z][a-z0-9._-]{0,63}$'),
 version bigint NOT NULL CHECK(version >= 0),
 state jsonb NOT NULL CHECK(jsonb_typeof(state)='object' AND octet_length(state::text)<=8192),
 PRIMARY KEY(tenant_id,name)
);

CREATE TABLE idenqa.model_registry_revisions (
 tenant_id text NOT NULL,
 name text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('model','threshold')),
 revision bigint NOT NULL CHECK(revision>0),
 digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 document jsonb NOT NULL CHECK(jsonb_typeof(document)='object' AND octet_length(document::text)<=65536),
 PRIMARY KEY(tenant_id,name,kind,revision),
 FOREIGN KEY(tenant_id,name) REFERENCES idenqa.model_registries(tenant_id,name)
);

CREATE TABLE idenqa.model_registry_history (
 tenant_id text NOT NULL,
 name text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 actor_key_id text NOT NULL,
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=98304),
 PRIMARY KEY(tenant_id,name,version),
 FOREIGN KEY(tenant_id,name) REFERENCES idenqa.model_registries(tenant_id,name),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TRIGGER model_registry_revision_append_only BEFORE UPDATE OR DELETE ON idenqa.model_registry_revisions
 FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_catalog_record_change();
CREATE TRIGGER model_registry_history_append_only BEFORE UPDATE OR DELETE ON idenqa.model_registry_history
 FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_catalog_record_change();

ALTER TABLE idenqa.model_registries ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_registries FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_registry_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_registry_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_registry_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.model_registry_history FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.model_registries USING (tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),'')) WITH CHECK (tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),''));
CREATE POLICY tenant_scope ON idenqa.model_registry_revisions USING (tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),'')) WITH CHECK (tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),''));
CREATE POLICY tenant_scope ON idenqa.model_registry_history USING (tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),'')) WITH CHECK (tenant_id=NULLIF(current_setting('idenqa.tenant_id',true),''));

REVOKE ALL ON idenqa.model_registries,idenqa.model_registry_revisions,idenqa.model_registry_history FROM PUBLIC;

ALTER TABLE idenqa.model_requests
 ADD COLUMN registry_name text,
 ADD COLUMN registry_version bigint,
 ADD COLUMN registry_selection jsonb,
 ADD CONSTRAINT model_request_registry_history_fk FOREIGN KEY(tenant_id,registry_name,registry_version)
  REFERENCES idenqa.model_registry_history(tenant_id,name,version),
 ADD CONSTRAINT model_request_registry_selection CHECK (
  (registry_name IS NULL AND registry_version IS NULL AND registry_selection IS NULL) OR
  (registry_name IS NOT NULL AND registry_version>0 AND registry_selection IS NOT NULL
   AND jsonb_typeof(registry_selection)='object' AND octet_length(registry_selection::text)<=4096)
 );
