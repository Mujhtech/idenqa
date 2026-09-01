CREATE TABLE idenqa.policies (
    tenant_id text NOT NULL,
    id text NOT NULL,
    activation_version bigint NOT NULL DEFAULT 0,
    active_revision bigint,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT policies_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT policies_id CHECK (id ~ '^pol_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT policies_activation_version CHECK (activation_version >= 0),
    CONSTRAINT policies_active_state CHECK (
        (activation_version = 0 AND active_revision IS NULL AND updated_at = created_at) OR
        (activation_version > 0 AND active_revision > 0 AND updated_at >= created_at)
    )
);

CREATE TABLE idenqa.policy_revisions (
    tenant_id text NOT NULL,
    policy_id text NOT NULL,
    revision bigint NOT NULL,
    schema_major integer NOT NULL,
    schema_minor integer NOT NULL,
    digest text NOT NULL,
    evaluator_major integer NOT NULL,
    evaluator_minor integer NOT NULL,
    evaluator_digest text NOT NULL,
    canonical text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, policy_id, revision),
    UNIQUE (tenant_id, policy_id, revision, digest),
    CONSTRAINT policy_revisions_policy_fk
        FOREIGN KEY (tenant_id, policy_id)
        REFERENCES idenqa.policies (tenant_id, id),
    CONSTRAINT policy_revisions_revision CHECK (revision > 0),
    CONSTRAINT policy_revisions_schema CHECK (schema_major = 1 AND schema_minor = 0),
    CONSTRAINT policy_revisions_evaluator CHECK (evaluator_major = 1 AND evaluator_minor = 0),
    CONSTRAINT policy_revisions_digests CHECK (
        digest ~ '^[0-9a-f]{64}$' AND evaluator_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT policy_revisions_canonical_size CHECK (
        octet_length(canonical) > 0 AND octet_length(canonical) <= 131072
    )
);

ALTER TABLE idenqa.policies
    ADD CONSTRAINT policies_active_revision_fk
        FOREIGN KEY (tenant_id, id, active_revision)
        REFERENCES idenqa.policy_revisions (tenant_id, policy_id, revision)
        DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE idenqa.policy_activations (
    tenant_id text NOT NULL,
    policy_id text NOT NULL,
    activation_version bigint NOT NULL,
    revision bigint NOT NULL,
    previous_revision bigint,
    actor_key_id text NOT NULL,
    activated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, policy_id, activation_version),
    CONSTRAINT policy_activations_policy_fk
        FOREIGN KEY (tenant_id, policy_id)
        REFERENCES idenqa.policies (tenant_id, id),
    CONSTRAINT policy_activations_revision_fk
        FOREIGN KEY (tenant_id, policy_id, revision)
        REFERENCES idenqa.policy_revisions (tenant_id, policy_id, revision),
    CONSTRAINT policy_activations_previous_revision_fk
        FOREIGN KEY (tenant_id, policy_id, previous_revision)
        REFERENCES idenqa.policy_revisions (tenant_id, policy_id, revision),
    CONSTRAINT policy_activations_actor_fk
        FOREIGN KEY (tenant_id, actor_key_id)
        REFERENCES idenqa.api_keys (tenant_id, id),
    CONSTRAINT policy_activations_version CHECK (activation_version > 0),
    CONSTRAINT policy_activations_revisions CHECK (
        revision > 0 AND (previous_revision IS NULL OR previous_revision > 0) AND
        previous_revision IS DISTINCT FROM revision
    )
);

CREATE INDEX policy_activations_time
    ON idenqa.policy_activations (tenant_id, policy_id, activated_at DESC);

CREATE FUNCTION idenqa.reject_policy_catalog_record_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'policy revision and activation records are append-only';
END;
$$;

CREATE TRIGGER policy_revision_append_only
BEFORE UPDATE OR DELETE ON idenqa.policy_revisions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_catalog_record_change();

CREATE TRIGGER policy_activation_append_only
BEFORE UPDATE OR DELETE ON idenqa.policy_activations
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_policy_catalog_record_change();

ALTER TABLE idenqa.policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policies FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_activations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.policy_activations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.policies
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.policy_revisions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.policy_activations
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.policies FROM PUBLIC;
REVOKE ALL ON idenqa.policy_revisions FROM PUBLIC;
REVOKE ALL ON idenqa.policy_activations FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_policy_catalog_record_change() FROM PUBLIC;
