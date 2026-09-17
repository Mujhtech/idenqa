CREATE TABLE idenqa.proposal_mode_configs (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 workflow text NOT NULL CHECK(octet_length(workflow) BETWEEN 1 AND 64),
 mode text NOT NULL CHECK(mode IN ('disabled','assist','recommend','guardrailed_auto','human_required')),
 allow_list_version text NOT NULL DEFAULT '' CHECK(octet_length(allow_list_version) <= 64),
 allowed_kinds jsonb NOT NULL DEFAULT '[]'::jsonb,
 cost_daily_limit integer NOT NULL DEFAULT 1000 CHECK(cost_daily_limit BETWEEN 0 AND 100000),
 prompt_id text,
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 9223372036854775807),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id, workflow)
);

CREATE TABLE idenqa.proposals (
 id text PRIMARY KEY CHECK(id ~ '^prp_[0-9A-HJKMNP-TV-Z]{26}$'),
 UNIQUE (tenant_id, id),
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 verification_id text NOT NULL CHECK(verification_id ~ '^ver_[0-9A-HJKMNP-TV-Z]{26}$'),
 mode text NOT NULL CHECK(mode IN ('disabled','assist','recommend','guardrailed_auto','human_required')),
 status text NOT NULL CHECK(status IN ('pending','approved','rejected','expired','cancelled','superseded')),
 actions jsonb NOT NULL CHECK(octet_length(actions::text) BETWEEN 2 AND 65536),
 evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
 signal_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
 model_id text NOT NULL CHECK(octet_length(model_id) BETWEEN 1 AND 64),
 model_version text NOT NULL CHECK(octet_length(model_version) BETWEEN 1 AND 64),
 prompt_version text NOT NULL CHECK(octet_length(prompt_version) BETWEEN 1 AND 64),
 context_digest text NOT NULL CHECK(context_digest ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,
 supersedes text CHECK(supersedes ~ '^prp_[0-9A-HJKMNP-TV-Z]{26}$'),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 9223372036854775807),
 actor_id text NOT NULL,
 reason text CHECK(octet_length(reason) <= 512),
 FOREIGN KEY(tenant_id, verification_id) REFERENCES idenqa.verification_sessions(tenant_id, id),
 CHECK(expires_at > created_at),
 CHECK(supersedes IS NULL OR supersedes <> id)
);

CREATE TABLE idenqa.accepted_commands (
 id text PRIMARY KEY CHECK(id ~ '^acc_[0-9A-HJKMNP-TV-Z]{26}$'),
 proposal_id text NOT NULL REFERENCES idenqa.proposals(id),
 tenant_id text NOT NULL,
 verification_id text NOT NULL CHECK(verification_id ~ '^ver_[0-9A-HJKMNP-TV-Z]{26}$'),
 kind text NOT NULL CHECK(kind IN ('review.copilot.summarize','routing.adaptive.suggest','policy.draft.generate','policy.diff.generate','policy.adversarial.generate','experience.accessibility.propose','experience.exception.propose','document.layout.propose')),
 args jsonb NOT NULL CHECK(octet_length(args::text) BETWEEN 2 AND 8192),
 model_id text NOT NULL,
 model_version text NOT NULL,
 prompt_version text NOT NULL,
 created_at timestamptz NOT NULL,
 executed_at timestamptz,
 UNIQUE(proposal_id, kind),
 FOREIGN KEY(tenant_id, proposal_id) REFERENCES idenqa.proposals(tenant_id, id)
);

CREATE TABLE idenqa.prompt_registry (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 prompt_id text NOT NULL CHECK(prompt_id ~ '^prm_[0-9A-HJKMNP-TV-Z]{26}$'),
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 9223372036854775807),
 content text NOT NULL CHECK(octet_length(content) BETWEEN 1 AND 16384),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 model_id text NOT NULL,
 created_at timestamptz NOT NULL,
 actor_id text NOT NULL,
 PRIMARY KEY(tenant_id, prompt_id, version)
);

CREATE TABLE idenqa.generative_model_registry (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 model_id text NOT NULL CHECK(model_id ~ '^mdl_[0-9A-HJKMNP-TV-Z]{26}$'),
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 9223372036854775807),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 actor_id text NOT NULL,
 PRIMARY KEY(tenant_id, model_id, version)
);

CREATE TABLE idenqa.impact_assessments (
 id text PRIMARY KEY,
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 kind text NOT NULL,
 assessment text NOT NULL CHECK(octet_length(assessment) BETWEEN 1 AND 8192),
 risk_level text NOT NULL CHECK(risk_level IN ('low','medium','high','critical')),
 created_at timestamptz NOT NULL,
 actor_id text NOT NULL
);

DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['proposal_mode_configs','proposals','accepted_commands','prompt_registry','generative_model_registry','impact_assessments'] LOOP
  EXECUTE format('ALTER TABLE idenqa.%I ENABLE ROW LEVEL SECURITY', n);
  EXECUTE format('ALTER TABLE idenqa.%I FORCE ROW LEVEL SECURITY', n);
  EXECUTE format('CREATE POLICY tenant_isolation ON idenqa.%I USING(tenant_id=current_setting(''idenqa.tenant_id'',true)) WITH CHECK(tenant_id=current_setting(''idenqa.tenant_id'',true))', n);
  EXECUTE format('REVOKE ALL ON idenqa.%I FROM PUBLIC', n);
 END LOOP;
END $$;

CREATE INDEX proposals_tenant_verification_idx ON idenqa.proposals(tenant_id, verification_id, created_at);
CREATE INDEX accepted_commands_tenant_proposal_idx ON idenqa.accepted_commands(tenant_id, proposal_id);
