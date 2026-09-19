ALTER TABLE idenqa.webhook_endpoints
    ADD COLUMN schema_version text NOT NULL DEFAULT '1.0',
    ADD CONSTRAINT webhook_endpoint_schema_version_supported CHECK (schema_version = '1.0');
