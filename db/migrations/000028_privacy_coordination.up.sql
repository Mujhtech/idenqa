CREATE FUNCTION idenqa.list_due_privacy_deletions(
    observed_at timestamptz,
    batch_size integer
)
RETURNS TABLE (tenant_id text, deletion_id text, workflow_version bigint, due_at timestamptz)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT requests.tenant_id, requests.id, requests.version,
           CASE
               WHEN requests.state = 'awaiting_backup_expiry'
                   THEN requests.backup_expires_at
               ELSE requests.updated_at
           END AS due_at
    FROM idenqa.deletion_requests AS requests
    WHERE observed_at IS NOT NULL
      AND batch_size BETWEEN 1 AND 100
      AND (
          requests.state IN ('requested', 'failed') OR
          (
              requests.state = 'blocked_by_legal_hold' AND
              NOT EXISTS (
                  SELECT 1
                  FROM idenqa.legal_holds AS holds
                  WHERE holds.tenant_id = requests.tenant_id
                    AND holds.aggregate_id = requests.aggregate_id
                    AND holds.starts_at <= observed_at
                    AND (holds.released_at IS NULL OR holds.released_at > observed_at)
              )
          ) OR
          (
              requests.state = 'awaiting_backup_expiry' AND
              requests.backup_expires_at <= observed_at
          )
      )
    ORDER BY due_at, requests.tenant_id, requests.id
    LIMIT LEAST(batch_size, 100);
$$;

REVOKE ALL ON FUNCTION idenqa.list_due_privacy_deletions(timestamptz, integer)
    FROM PUBLIC;
