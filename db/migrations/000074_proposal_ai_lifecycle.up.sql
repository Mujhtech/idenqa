ALTER TABLE idenqa.proposal_mode_configs
    ADD COLUMN model_registry_id text,
    ADD COLUMN model_registry_version bigint,
    ADD COLUMN prompt_registry_version bigint,
    ADD COLUMN activation_revision bigint;

ALTER TABLE idenqa.proposal_mode_configs
    ADD CONSTRAINT proposal_mode_generation_pins_complete CHECK (
        (model_registry_id IS NULL AND model_registry_version IS NULL AND prompt_id IS NULL AND prompt_registry_version IS NULL AND activation_revision IS NULL)
        OR
        (model_registry_id ~ '^mdl_[0-9A-HJKMNP-TV-Z]{26}$' AND model_registry_version >= 1 AND prompt_id ~ '^prm_[0-9A-HJKMNP-TV-Z]{26}$' AND prompt_registry_version >= 1 AND activation_revision >= 1)
    ) NOT VALID;

CREATE TABLE idenqa.proposal_generation_activations (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    workflow text NOT NULL CHECK (octet_length(workflow) BETWEEN 1 AND 64),
    revision bigint NOT NULL CHECK (revision >= 1),
    state text NOT NULL CHECK (state IN ('active', 'retired')),
    model_registry_id text NOT NULL CHECK (model_registry_id ~ '^mdl_[0-9A-HJKMNP-TV-Z]{26}$'),
    model_registry_version bigint NOT NULL CHECK (model_registry_version >= 1),
    prompt_registry_id text NOT NULL CHECK (prompt_registry_id ~ '^prm_[0-9A-HJKMNP-TV-Z]{26}$'),
    prompt_registry_version bigint NOT NULL CHECK (prompt_registry_version >= 1),
    logical_model_id text NOT NULL CHECK (logical_model_id ~ '^[a-z][a-z0-9._:-]{0,63}$'),
    upstream_model_version text NOT NULL CHECK (octet_length(upstream_model_version) BETWEEN 1 AND 64),
    prompt_version text NOT NULL CHECK (octet_length(prompt_version) BETWEEN 1 AND 64),
    action text NOT NULL CHECK (action IN ('activated', 'retired', 'rolled_back')),
    source_revision bigint CHECK (source_revision IS NULL OR source_revision >= 1),
    updated_at timestamptz NOT NULL,
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 200),
    reason text NOT NULL DEFAULT '' CHECK (octet_length(reason) <= 256),
    PRIMARY KEY (tenant_id, workflow)
);

CREATE TABLE idenqa.proposal_generation_activation_history (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    workflow text NOT NULL CHECK (octet_length(workflow) BETWEEN 1 AND 64),
    revision bigint NOT NULL CHECK (revision >= 1),
    state text NOT NULL CHECK (state IN ('active', 'retired')),
    model_registry_id text NOT NULL CHECK (model_registry_id ~ '^mdl_[0-9A-HJKMNP-TV-Z]{26}$'),
    model_registry_version bigint NOT NULL CHECK (model_registry_version >= 1),
    prompt_registry_id text NOT NULL CHECK (prompt_registry_id ~ '^prm_[0-9A-HJKMNP-TV-Z]{26}$'),
    prompt_registry_version bigint NOT NULL CHECK (prompt_registry_version >= 1),
    logical_model_id text NOT NULL CHECK (logical_model_id ~ '^[a-z][a-z0-9._:-]{0,63}$'),
    upstream_model_version text NOT NULL CHECK (octet_length(upstream_model_version) BETWEEN 1 AND 64),
    prompt_version text NOT NULL CHECK (octet_length(prompt_version) BETWEEN 1 AND 64),
    action text NOT NULL CHECK (action IN ('activated', 'retired', 'rolled_back')),
    source_revision bigint CHECK (source_revision IS NULL OR source_revision >= 1),
    reason text NOT NULL DEFAULT '' CHECK (octet_length(reason) <= 256),
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 200),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, workflow, revision)
);

ALTER TABLE idenqa.proposal_generation_activations ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.proposal_generation_activations FORCE ROW LEVEL SECURITY;
CREATE POLICY proposal_generation_activations_tenant ON idenqa.proposal_generation_activations
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

ALTER TABLE idenqa.proposal_generation_activation_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.proposal_generation_activation_history FORCE ROW LEVEL SECURITY;
CREATE POLICY proposal_generation_activation_history_tenant ON idenqa.proposal_generation_activation_history
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.proposal_generation_activations FROM PUBLIC;
REVOKE ALL ON idenqa.proposal_generation_activation_history FROM PUBLIC;

ALTER TABLE idenqa.proposal_generation_usage
    ADD COLUMN outcome text NOT NULL DEFAULT 'succeeded' CHECK (outcome IN ('succeeded', 'failed', 'rejected', 'invalid_output')),
    ADD COLUMN usage_reported boolean NOT NULL DEFAULT true,
    ADD COLUMN error_code text NOT NULL DEFAULT '' CHECK (octet_length(error_code) <= 64);
