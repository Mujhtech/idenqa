DROP TABLE IF EXISTS idenqa.key_recovery_ceremonies;
DROP FUNCTION IF EXISTS idenqa.protect_key_recovery_ceremony();
DROP TABLE IF EXISTS idenqa.key_destruction_schedules;
DROP TABLE IF EXISTS idenqa.key_destruction_verifications;
DROP TABLE IF EXISTS idenqa.key_rewrap_audit;
DROP FUNCTION IF EXISTS idenqa.reject_key_rewrap_audit_change();
DROP TABLE IF EXISTS idenqa.key_rewrap_state;
DROP FUNCTION IF EXISTS idenqa.list_key_rewrap_tenants(text, integer);

CREATE OR REPLACE FUNCTION idenqa.protect_hmac_key_version()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'hmac key versions are immutable';
    END IF;
    IF NEW.tenant_id<>OLD.tenant_id OR NEW.domain<>OLD.domain OR NEW.version<>OLD.version
       OR NEW.id<>OLD.id OR NEW.wrapped_key<>OLD.wrapped_key OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'hmac key versions are immutable';
    END IF;
    IF NOT ((OLD.state='active' AND NEW.state='disabled')
         OR (OLD.state='disabled' AND NEW.state='retired')) THEN
        RAISE EXCEPTION 'hmac key state transition is not permitted';
    END IF;
    RETURN NEW;
END $$;

ALTER TABLE idenqa.hmac_keys DROP CONSTRAINT IF EXISTS hmac_key_rewrap_time;
ALTER TABLE idenqa.hmac_keys DROP COLUMN IF EXISTS rewrapped_at;

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_immutable_rows()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'webhook_delivery_attempts'
       AND TG_OP = 'DELETE'
       AND current_setting('idenqa.webhook_retention', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'webhook secret and attempt history is append-only';
END $$;

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_event_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.event_type <> OLD.event_type OR
       NEW.schema_version <> OLD.schema_version OR NEW.dedupe_key <> OLD.dedupe_key OR
       NEW.body_digest <> OLD.body_digest OR NEW.created_at <> OLD.created_at OR
       NEW.aggregate_ids <> OLD.aggregate_ids OR NEW.payload_expires_at <> OLD.payload_expires_at OR
       NEW.retain_until <> OLD.retain_until THEN
        RAISE EXCEPTION 'webhook event identity and retention metadata are immutable';
    END IF;
    IF NEW.body IS DISTINCT FROM OLD.body AND NOT (
        current_setting('idenqa.webhook_retention', true) = 'on'
        AND OLD.body IS NOT NULL AND NEW.body IS NULL
        AND NEW.payload_expired_at IS NOT NULL
        AND NEW.body_provider IS NULL AND NEW.body_reference IS NULL
        AND NEW.body_key_version IS NULL AND NEW.body_algorithm IS NULL
    ) THEN
        RAISE EXCEPTION 'webhook event payload is immutable outside retention expiry';
    END IF;
    RETURN NEW;
END $$;
