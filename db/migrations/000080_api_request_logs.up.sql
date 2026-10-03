CREATE TABLE idenqa.api_request_logs (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    request_id text NOT NULL,
    actor_key_id text NOT NULL,
    method text NOT NULL,
    route_template text NOT NULL,
    status_code integer NOT NULL,
    duration_ms bigint NOT NULL,
    occurred_at timestamptz NOT NULL,
    trace_reference text,
    PRIMARY KEY (tenant_id, request_id),
    CONSTRAINT api_request_logs_route_template CHECK (route_template LIKE '/v1/%' AND position('?' IN route_template) = 0),
    CONSTRAINT api_request_logs_status CHECK (status_code BETWEEN 100 AND 599),
    CONSTRAINT api_request_logs_duration CHECK (duration_ms >= 0)
);

CREATE INDEX api_request_logs_tenant_time ON idenqa.api_request_logs (tenant_id, occurred_at DESC, request_id DESC);

CREATE TRIGGER api_request_logs_append_only BEFORE UPDATE OR DELETE ON idenqa.api_request_logs
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_audit_history();

ALTER TABLE idenqa.api_request_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.api_request_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY api_request_logs_tenant ON idenqa.api_request_logs
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
REVOKE ALL ON idenqa.api_request_logs FROM PUBLIC;
