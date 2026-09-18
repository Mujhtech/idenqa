ALTER TABLE idenqa.webhook_events
    ADD COLUMN body_provider text,
    ADD COLUMN body_reference text,
    ADD COLUMN body_key_version text,
    ADD COLUMN body_algorithm text,
    ADD CONSTRAINT webhook_event_body_wrapping CHECK (
        num_nonnulls(body_provider, body_reference, body_key_version, body_algorithm) IN (0, 4)
    );

ALTER TABLE idenqa.webhook_deliveries
    ADD COLUMN body_provider text,
    ADD COLUMN body_reference text,
    ADD COLUMN body_key_version text,
    ADD COLUMN body_algorithm text,
    ADD CONSTRAINT webhook_delivery_body_wrapping CHECK (
        num_nonnulls(body_provider, body_reference, body_key_version, body_algorithm) IN (0, 4)
    );
