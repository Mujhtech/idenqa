-- Filter ineligible work before applying the installation-wide batch limit.
-- The fenced decision commit remains responsible for current authorization.
CREATE OR REPLACE FUNCTION idenqa.list_ready_policy_authorships(
    observed_at timestamptz,
    batch_size integer
)
RETURNS TABLE (
    tenant_id text,
    verification_id text,
    decision_id text,
    ready_at timestamptz
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT sessions.tenant_id, sessions.id, sessions.decision_id,
           GREATEST(sessions.capture_completed_at, MAX(checks.updated_at)) AS ready_at
    FROM idenqa.verification_sessions AS sessions
    JOIN idenqa.verification_checks AS checks
      ON checks.tenant_id = sessions.tenant_id
     AND checks.verification_id = sessions.id
    JOIN idenqa.processing_authorities AS authorities
      ON authorities.tenant_id = sessions.tenant_id
     AND authorities.id = sessions.authority_id
     AND authorities.verification_id = sessions.id
     AND authorities.subject_id = sessions.subject_id
     AND authorities.notice_id = sessions.notice_id
    JOIN idenqa.notice_versions AS notices
      ON notices.tenant_id = sessions.tenant_id AND notices.id = sessions.notice_id
    JOIN LATERAL (
        SELECT response.id, response.action, response.recorded_at,
               response.subject_id, response.verification_id, response.notice_id
        FROM idenqa.subject_responses AS response
        WHERE response.tenant_id = sessions.tenant_id
          AND response.authority_id = sessions.authority_id
        ORDER BY response.recorded_at DESC, response.id DESC
        LIMIT 1
    ) AS latest_response ON true
    WHERE observed_at IS NOT NULL
      AND batch_size BETWEEN 1 AND 100
      AND sessions.state = 'processing'
      AND sessions.expires_at > observed_at
      AND authorities.state = 'active'
      AND authorities.valid_from <= observed_at AND authorities.expires_at > observed_at
      AND notices.effective_at <= observed_at
      AND latest_response.recorded_at <= observed_at
      AND latest_response.subject_id = sessions.subject_id
      AND latest_response.verification_id = sessions.id
      AND latest_response.notice_id = sessions.notice_id
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
            AND uploads.state = 'accepted'
            AND (uploads.response_id <> latest_response.id OR
                 uploads.authority_id <> authorities.id OR
                 uploads.subject_id <> authorities.subject_id OR
                 uploads.accepted_at IS NULL OR uploads.accepted_at > observed_at OR
                 NOT uploads.purpose = ANY(authorities.requirement_purposes) OR
                 NOT uploads.evidence_type = ANY(authorities.evidence_types) OR
                 NOT uploads.region = ANY(authorities.regions))
      )
      AND sessions.policy_id IS NOT NULL
      AND sessions.decision_id IS NOT NULL
      AND sessions.capture_completed_at IS NOT NULL
      AND sessions.capture_completed_at <= observed_at
      AND NOT EXISTS (
          SELECT 1
          FROM idenqa.verification_checks AS pending
          WHERE pending.tenant_id = sessions.tenant_id
            AND pending.verification_id = sessions.id
            AND pending.state NOT IN (
                'completed', 'skipped_by_policy', 'timed_out', 'cancelled', 'failed'
            )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM idenqa.verification_decisions AS decisions
          WHERE decisions.tenant_id = sessions.tenant_id
            AND decisions.id = sessions.decision_id
      )
    GROUP BY sessions.tenant_id, sessions.id, sessions.decision_id,
             sessions.capture_completed_at
    ORDER BY ready_at, sessions.tenant_id, sessions.id
    LIMIT LEAST(batch_size, 100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_ready_policy_authorships(timestamptz, integer)
    FROM PUBLIC;
