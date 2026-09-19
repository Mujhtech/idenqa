ALTER TABLE idenqa.webhook_endpoints
    DROP CONSTRAINT IF EXISTS webhook_endpoint_schema_version_supported,
    DROP COLUMN IF EXISTS schema_version;
