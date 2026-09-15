-- Identifier-only installation discovery. Every effect re-enters tenant scope,
-- locks the parent session and rechecks current processing authority.
CREATE INDEX verification_sessions_ready_capture
    ON idenqa.verification_sessions (capture_completed_at, tenant_id, id)
    WHERE state = 'collecting' AND capture_completed_at IS NOT NULL;

CREATE FUNCTION idenqa.list_ready_verification_captures(observed_at timestamptz, batch_size integer)
RETURNS TABLE (tenant_id text, verification_id text)
LANGUAGE sql SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT sessions.tenant_id, sessions.id
    FROM idenqa.verification_sessions AS sessions
    JOIN idenqa.processing_authorities AS authorities
      ON authorities.tenant_id = sessions.tenant_id AND authorities.id = sessions.authority_id
    JOIN LATERAL (
        SELECT response.id, response.action, response.recorded_at
        FROM idenqa.subject_responses AS response
        WHERE response.tenant_id = sessions.tenant_id
          AND response.authority_id = sessions.authority_id
        ORDER BY response.recorded_at DESC, response.id DESC
        LIMIT 1
    ) AS latest_response ON true
    WHERE observed_at IS NOT NULL AND batch_size BETWEEN 1 AND 100
      AND sessions.state = 'collecting'
      AND sessions.capture_completed_at <= observed_at
      AND sessions.expires_at > observed_at
      AND sessions.policy_id IS NOT NULL AND sessions.decision_id IS NOT NULL
      AND latest_response.recorded_at <= observed_at
      AND (latest_response.action = 'consent' OR
          (NOT authorities.consent_required AND latest_response.action = 'acknowledge'))
      AND EXISTS (
          SELECT 1 FROM idenqa.evidence_upload_intents AS uploads
          WHERE uploads.tenant_id = sessions.tenant_id AND uploads.verification_id = sessions.id
            AND uploads.state = 'accepted'
      )
      AND NOT EXISTS (
          SELECT 1 FROM idenqa.evidence_upload_intents AS uploads
          WHERE uploads.tenant_id = sessions.tenant_id AND uploads.verification_id = sessions.id
            AND uploads.state = 'accepted' AND uploads.response_id <> latest_response.id
      )
      AND authorities.state = 'active'
      AND authorities.valid_from <= observed_at AND authorities.expires_at > observed_at
      AND NOT EXISTS (
          SELECT 1 FROM idenqa.verification_checks AS checks
          WHERE checks.tenant_id = sessions.tenant_id AND checks.verification_id = sessions.id
      )
    ORDER BY sessions.capture_completed_at, sessions.tenant_id, sessions.id
    LIMIT LEAST(batch_size, 100);
$$;
REVOKE ALL ON FUNCTION idenqa.list_ready_verification_captures(timestamptz, integer) FROM PUBLIC;
