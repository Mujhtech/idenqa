CREATE TABLE idenqa.review_evidence_session_bootstraps (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    bootstrap_id text NOT NULL,
    nonce_hash bytea NOT NULL,
    claims_hash bytea NOT NULL,
    actor_id text NOT NULL,
    case_id text NOT NULL,
    case_version bigint NOT NULL,
    region text NOT NULL,
    origin text NOT NULL,
    consumed_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, bootstrap_id),
    UNIQUE (tenant_id, nonce_hash),
    CONSTRAINT review_evidence_session_bootstraps_nonce_hash CHECK (octet_length(nonce_hash) = 32),
    CONSTRAINT review_evidence_session_bootstraps_claims_hash CHECK (octet_length(claims_hash) = 32),
    CONSTRAINT review_evidence_session_bootstraps_case_version CHECK (case_version >= 1),
    CONSTRAINT review_evidence_session_bootstraps_origin CHECK (length(origin) BETWEEN 1 AND 512)
);

CREATE TABLE idenqa.review_evidence_sessions (
    tenant_id text NOT NULL,
    token_hash bytea NOT NULL,
    bootstrap_id text NOT NULL,
    actor_id text NOT NULL,
    case_id text NOT NULL,
    case_version bigint NOT NULL,
    region text NOT NULL,
    origin text NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, token_hash),
    FOREIGN KEY (tenant_id, bootstrap_id) REFERENCES idenqa.review_evidence_session_bootstraps (tenant_id, bootstrap_id),
    CONSTRAINT review_evidence_sessions_token_hash CHECK (octet_length(token_hash) = 32),
    CONSTRAINT review_evidence_sessions_case_version CHECK (case_version >= 1),
    CONSTRAINT review_evidence_sessions_region CHECK (length(region) BETWEEN 1 AND 128),
    CONSTRAINT review_evidence_sessions_origin CHECK (length(origin) BETWEEN 1 AND 512),
    CONSTRAINT review_evidence_sessions_lifetime CHECK (expires_at > created_at)
);

CREATE INDEX review_evidence_sessions_expiry
    ON idenqa.review_evidence_sessions (tenant_id, expires_at);

CREATE TRIGGER review_evidence_session_bootstraps_append_only
    BEFORE UPDATE OR DELETE ON idenqa.review_evidence_session_bootstraps
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();

ALTER TABLE idenqa.review_evidence_session_bootstraps ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_evidence_session_bootstraps FORCE ROW LEVEL SECURITY;
CREATE POLICY review_evidence_session_bootstraps_tenant ON idenqa.review_evidence_session_bootstraps
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

ALTER TABLE idenqa.review_evidence_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.review_evidence_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY review_evidence_sessions_tenant ON idenqa.review_evidence_sessions
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));

REVOKE ALL ON idenqa.review_evidence_session_bootstraps, idenqa.review_evidence_sessions FROM PUBLIC;
