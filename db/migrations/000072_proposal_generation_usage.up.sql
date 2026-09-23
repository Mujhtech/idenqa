CREATE TABLE idenqa.proposal_generation_usage (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    proposal_id text NOT NULL CHECK (proposal_id ~ '^prp_[0-9A-HJKMNP-TV-Z]{26}$'),
    model_id text NOT NULL CHECK (octet_length(model_id) BETWEEN 1 AND 64),
    model_version text NOT NULL CHECK (octet_length(model_version) BETWEEN 1 AND 64),
    prompt_version text NOT NULL CHECK (octet_length(prompt_version) BETWEEN 1 AND 64),
    provider_request_id text NOT NULL DEFAULT '' CHECK (octet_length(provider_request_id) <= 256),
    input_tokens bigint NOT NULL CHECK (input_tokens BETWEEN 0 AND 1000000000),
    output_tokens bigint NOT NULL CHECK (output_tokens BETWEEN 0 AND 1000000000),
    estimated_cost_micros bigint NOT NULL CHECK (estimated_cost_micros >= 0),
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, proposal_id)
);

CREATE INDEX proposal_generation_usage_tenant_recorded
    ON idenqa.proposal_generation_usage (tenant_id, recorded_at, proposal_id);

ALTER TABLE idenqa.proposal_generation_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.proposal_generation_usage FORCE ROW LEVEL SECURITY;

CREATE POLICY proposal_generation_usage_tenant ON idenqa.proposal_generation_usage
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.proposal_generation_usage FROM PUBLIC;
