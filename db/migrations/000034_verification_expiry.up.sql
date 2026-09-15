ALTER TABLE idenqa.verification_sessions ADD COLUMN expiry_discovered_at timestamptz;

CREATE INDEX verification_sessions_due_expiry
    ON idenqa.verification_sessions (expiry_discovered_at NULLS FIRST, expires_at, tenant_id, id)
    WHERE state IN ('collecting', 'awaiting_input', 'processing', 'awaiting_external', 'manual_review');

-- Discovery rotates only scheduling metadata. Every effect re-enters tenant
-- scope and reloads the session under its parent lock and live task fence.
CREATE FUNCTION idenqa.list_due_verification_expirations(observed_at timestamptz, batch_size integer)
RETURNS TABLE (tenant_id text, verification_id text)
LANGUAGE sql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    WITH ready AS (
        SELECT sessions.tenant_id, sessions.id
        FROM idenqa.verification_sessions AS sessions
        WHERE observed_at IS NOT NULL AND batch_size BETWEEN 1 AND 100
          AND sessions.state IN ('collecting', 'awaiting_input', 'processing', 'awaiting_external', 'manual_review')
          AND sessions.expires_at <= observed_at
        ORDER BY sessions.expiry_discovered_at NULLS FIRST, sessions.expires_at, sessions.tenant_id, sessions.id
        LIMIT LEAST(batch_size, 100)
        FOR UPDATE SKIP LOCKED
    )
    UPDATE idenqa.verification_sessions AS sessions
    SET expiry_discovered_at = observed_at
    FROM ready
    WHERE sessions.tenant_id = ready.tenant_id AND sessions.id = ready.id
    RETURNING sessions.tenant_id, sessions.id;
$$;
REVOKE ALL ON FUNCTION idenqa.list_due_verification_expirations(timestamptz, integer) FROM PUBLIC;
