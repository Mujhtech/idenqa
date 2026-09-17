ALTER TABLE idenqa.webhook_delivery_attempts
    ADD COLUMN response_body text,
    ADD COLUMN response_truncated boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT webhook_attempt_response_bound CHECK (
        (response_body IS NULL OR octet_length(response_body) <= 4096)
        AND (response_truncated = false OR response_body IS NOT NULL)
    );
