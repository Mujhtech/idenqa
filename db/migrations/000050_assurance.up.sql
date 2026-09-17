CREATE TABLE idenqa.assurance_profiles (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id), name text NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 4294967295), digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 canonical text NOT NULL CHECK(octet_length(canonical)<=262144), actor_key_id text NOT NULL, created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,name,revision), UNIQUE(tenant_id,name,revision,digest),
 FOREIGN KEY(tenant_id,actor_key_id) REFERENCES idenqa.api_keys(tenant_id,id)
);

CREATE TABLE idenqa.policy_assurance_assignments (
 tenant_id text NOT NULL, policy_id text NOT NULL, version bigint NOT NULL CHECK(version>0),
 profile_name text, profile_revision bigint, profile_digest text,
 PRIMARY KEY(tenant_id,policy_id),
 FOREIGN KEY(tenant_id,policy_id) REFERENCES idenqa.policies(tenant_id,id),
 FOREIGN KEY(tenant_id,profile_name,profile_revision,profile_digest) REFERENCES idenqa.assurance_profiles(tenant_id,name,revision,digest),
 CHECK((profile_name IS NULL AND profile_revision IS NULL AND profile_digest IS NULL) OR (profile_name IS NOT NULL AND profile_revision IS NOT NULL AND profile_digest IS NOT NULL))
);

CREATE TABLE idenqa.verification_assurance (
 tenant_id text NOT NULL, verification_id text NOT NULL,profile_name text,profile_revision bigint,profile_digest text,
 PRIMARY KEY(tenant_id,verification_id),
 FOREIGN KEY(tenant_id,verification_id) REFERENCES idenqa.verification_sessions(tenant_id,id),
 FOREIGN KEY(tenant_id,profile_name,profile_revision,profile_digest) REFERENCES idenqa.assurance_profiles(tenant_id,name,revision,digest),
 CHECK((profile_name IS NULL AND profile_revision IS NULL AND profile_digest IS NULL) OR (profile_name IS NOT NULL AND profile_revision IS NOT NULL AND profile_digest IS NOT NULL))
);

DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['assurance_profiles','policy_assurance_assignments','verification_assurance'] LOOP
 EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY',n);
 EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY',n);
 EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))',n);
 EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC',n);
 END LOOP;
 FOREACH n IN ARRAY ARRAY['assurance_profiles','verification_assurance'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_assurance_record BEFORE UPDATE OR DELETE ON idenqa.%I FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_record_change()',n);
 END LOOP;
END $$;
