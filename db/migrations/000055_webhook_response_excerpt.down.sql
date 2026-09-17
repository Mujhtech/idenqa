ALTER TABLE idenqa.webhook_delivery_attempts
    DROP CONSTRAINT IF EXISTS webhook_attempt_response_bound,
    DROP COLUMN IF EXISTS response_truncated,
    DROP COLUMN IF EXISTS response_body;
