ALTER TABLE idenqa.webhook_events
    ADD COLUMN aggregate_ids text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN payload_expires_at timestamptz NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '7 days'),
    ADD COLUMN payload_expired_at timestamptz,
    ADD COLUMN retain_until timestamptz NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '365 days');

UPDATE idenqa.webhook_events
SET payload_expires_at = created_at + interval '7 days',
    retain_until = created_at + interval '365 days';

ALTER TABLE idenqa.webhook_events
    ALTER COLUMN body DROP NOT NULL,
    DROP CONSTRAINT webhook_event_body_bound,
    ADD CONSTRAINT webhook_event_body_bound CHECK (body IS NULL OR octet_length(body) BETWEEN 1 AND 65536),
    ADD CONSTRAINT webhook_event_retention_times CHECK (
        payload_expires_at = created_at + interval '7 days'
        AND retain_until = created_at + interval '365 days'
        AND (payload_expired_at IS NULL OR payload_expired_at >= payload_expires_at)
    );

ALTER TABLE idenqa.webhook_deliveries
    ADD COLUMN payload_expires_at timestamptz NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '7 days'),
    ADD COLUMN payload_expired_at timestamptz,
    ADD COLUMN retain_until timestamptz NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '365 days');

UPDATE idenqa.webhook_deliveries
SET payload_expires_at = created_at + interval '7 days',
    retain_until = created_at + interval '365 days';

ALTER TABLE idenqa.webhook_deliveries
    ALTER COLUMN body DROP NOT NULL,
    DROP CONSTRAINT webhook_delivery_body_bound,
    ADD CONSTRAINT webhook_delivery_body_bound CHECK (body IS NULL OR octet_length(body) BETWEEN 1 AND 1048576),
    ADD CONSTRAINT webhook_delivery_retention_times CHECK (
        payload_expires_at = created_at + interval '7 days'
        AND retain_until = created_at + interval '365 days'
        AND (payload_expired_at IS NULL OR payload_expired_at >= payload_expires_at)
    );

CREATE INDEX webhook_events_payload_retention
    ON idenqa.webhook_events (payload_expires_at, tenant_id, id)
    WHERE payload_expired_at IS NULL;

CREATE INDEX webhook_events_tombstone_retention
    ON idenqa.webhook_events (retain_until, tenant_id, id)
    WHERE payload_expired_at IS NOT NULL;

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
END;
$$;

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_immutable_rows()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'webhook_delivery_attempts'
       AND TG_OP = 'DELETE'
       AND current_setting('idenqa.webhook_retention', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'webhook secret and attempt history is append-only';
END;
$$;

CREATE FUNCTION idenqa.expire_webhook_data(observed_at timestamptz, batch_size integer)
RETURNS TABLE (payloads_expired integer, attempts_expired integer, tombstones_purged integer)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = idenqa
AS $$
DECLARE
    direct_payloads integer;
    direct_attempts integer;
    direct_tombstones integer;
BEGIN
    IF observed_at IS NULL OR batch_size < 1 OR batch_size > 500 THEN
        RAISE EXCEPTION 'invalid webhook retention batch';
    END IF;

    PERFORM set_config('idenqa.webhook_retention', 'on', true);

    WITH candidates AS (
        SELECT events.tenant_id, events.id
        FROM idenqa.webhook_events AS events
        WHERE events.payload_expired_at IS NULL
          AND events.payload_expires_at <= observed_at
          AND NOT EXISTS (
              SELECT 1 FROM idenqa.legal_holds AS holds
              WHERE holds.tenant_id = events.tenant_id
                AND holds.aggregate_id = ANY(events.aggregate_ids)
                AND holds.starts_at <= observed_at
                AND (holds.released_at IS NULL OR holds.released_at > observed_at)
          )
        ORDER BY events.payload_expires_at, events.tenant_id, events.id
        LIMIT batch_size
        FOR UPDATE SKIP LOCKED
    ), expired AS (
        UPDATE idenqa.webhook_events AS events
        SET body = NULL, body_provider = NULL, body_reference = NULL,
            body_key_version = NULL, body_algorithm = NULL,
            payload_expired_at = observed_at,
            state = 'completed', completed_at = COALESCE(completed_at, observed_at)
        FROM candidates
        WHERE events.tenant_id = candidates.tenant_id AND events.id = candidates.id
        RETURNING events.tenant_id, events.id
    ), redacted_deliveries AS (
        UPDATE idenqa.webhook_deliveries AS deliveries
        SET body = NULL, body_provider = NULL, body_reference = NULL,
            body_key_version = NULL, body_algorithm = NULL,
            payload_expired_at = observed_at,
            state = CASE WHEN state = 'pending' THEN 'exhausted' ELSE state END,
            updated_at = GREATEST(updated_at, observed_at)
        FROM expired
        WHERE deliveries.tenant_id = expired.tenant_id
          AND deliveries.event_id = expired.id
          AND deliveries.payload_expired_at IS NULL
        RETURNING deliveries.tenant_id, deliveries.id
    ), deleted_attempts AS (
        DELETE FROM idenqa.webhook_delivery_attempts AS attempts
        USING redacted_deliveries
        WHERE attempts.tenant_id = redacted_deliveries.tenant_id
          AND attempts.delivery_id = redacted_deliveries.id
        RETURNING 1
    )
    SELECT (SELECT count(*) FROM expired), (SELECT count(*) FROM deleted_attempts)
    INTO payloads_expired, attempts_expired;

    WITH candidates AS (
        SELECT deliveries.tenant_id, deliveries.id
        FROM idenqa.webhook_deliveries AS deliveries
        WHERE deliveries.payload_expired_at IS NULL
          AND deliveries.payload_expires_at <= observed_at
          AND NOT EXISTS (
              SELECT 1
              FROM idenqa.webhook_events AS events
              JOIN idenqa.legal_holds AS holds
                ON holds.tenant_id = events.tenant_id
               AND holds.aggregate_id = ANY(events.aggregate_ids)
              WHERE events.tenant_id = deliveries.tenant_id
                AND events.id = deliveries.event_id
                AND holds.starts_at <= observed_at
                AND (holds.released_at IS NULL OR holds.released_at > observed_at)
          )
        ORDER BY deliveries.payload_expires_at, deliveries.tenant_id, deliveries.id
        LIMIT batch_size
        FOR UPDATE SKIP LOCKED
    ), redacted AS (
        UPDATE idenqa.webhook_deliveries AS deliveries
        SET body = NULL, body_provider = NULL, body_reference = NULL,
            body_key_version = NULL, body_algorithm = NULL,
            payload_expired_at = observed_at,
            state = CASE WHEN state = 'pending' THEN 'exhausted' ELSE state END,
            updated_at = GREATEST(updated_at, observed_at)
        FROM candidates
        WHERE deliveries.tenant_id = candidates.tenant_id AND deliveries.id = candidates.id
        RETURNING deliveries.tenant_id, deliveries.id
    ), deleted_attempts AS (
        DELETE FROM idenqa.webhook_delivery_attempts AS attempts
        USING redacted
        WHERE attempts.tenant_id = redacted.tenant_id
          AND attempts.delivery_id = redacted.id
        RETURNING 1
    )
    SELECT (SELECT count(*) FROM redacted), (SELECT count(*) FROM deleted_attempts)
    INTO direct_payloads, direct_attempts;

    payloads_expired := payloads_expired + direct_payloads;
    attempts_expired := attempts_expired + direct_attempts;

    WITH candidates AS (
        SELECT events.tenant_id, events.id
        FROM idenqa.webhook_events AS events
        WHERE events.payload_expired_at IS NOT NULL
          AND events.retain_until <= observed_at
          AND NOT EXISTS (
              SELECT 1 FROM idenqa.legal_holds AS holds
              WHERE holds.tenant_id = events.tenant_id
                AND holds.aggregate_id = ANY(events.aggregate_ids)
                AND holds.starts_at <= observed_at
                AND (holds.released_at IS NULL OR holds.released_at > observed_at)
          )
        ORDER BY events.retain_until, events.tenant_id, events.id
        LIMIT batch_size
        FOR UPDATE SKIP LOCKED
    ), deleted_attempts AS (
        DELETE FROM idenqa.webhook_delivery_attempts AS attempts
        USING idenqa.webhook_deliveries AS deliveries, candidates
        WHERE deliveries.tenant_id = candidates.tenant_id
          AND deliveries.event_id = candidates.id
          AND attempts.tenant_id = deliveries.tenant_id
          AND attempts.delivery_id = deliveries.id
        RETURNING 1
    ), deleted_replays AS (
        DELETE FROM idenqa.webhook_deliveries AS deliveries
        USING candidates
        WHERE deliveries.tenant_id = candidates.tenant_id
          AND deliveries.event_id = candidates.id
          AND deliveries.replay_of IS NOT NULL
        RETURNING 1
    ), deleted_roots AS (
        DELETE FROM idenqa.webhook_deliveries AS deliveries
        USING candidates
        WHERE deliveries.tenant_id = candidates.tenant_id
          AND deliveries.event_id = candidates.id
          AND (SELECT count(*) FROM deleted_replays) >= 0
        RETURNING 1
    ), deleted_events AS (
        DELETE FROM idenqa.webhook_events AS events
        USING candidates
        WHERE events.tenant_id = candidates.tenant_id AND events.id = candidates.id
          AND (SELECT count(*) FROM deleted_roots) >= 0
        RETURNING 1
    )
    SELECT count(*) INTO tombstones_purged FROM deleted_events;

    WITH candidates AS (
        SELECT deliveries.tenant_id, deliveries.id
        FROM idenqa.webhook_deliveries AS deliveries
        WHERE deliveries.payload_expired_at IS NOT NULL
          AND deliveries.retain_until <= observed_at
          AND NOT EXISTS (
              SELECT 1 FROM idenqa.webhook_events AS events
              WHERE events.tenant_id = deliveries.tenant_id AND events.id = deliveries.event_id
          )
        ORDER BY deliveries.retain_until, deliveries.tenant_id, deliveries.id
        LIMIT batch_size
        FOR UPDATE SKIP LOCKED
    ), deleted_attempts AS (
        DELETE FROM idenqa.webhook_delivery_attempts AS attempts
        USING candidates
        WHERE attempts.tenant_id = candidates.tenant_id
          AND attempts.delivery_id = candidates.id
        RETURNING 1
    ), deleted_replays AS (
        DELETE FROM idenqa.webhook_deliveries AS deliveries
        USING candidates
        WHERE deliveries.tenant_id = candidates.tenant_id
          AND deliveries.replay_of = candidates.id
        RETURNING 1
    ), deleted_roots AS (
        DELETE FROM idenqa.webhook_deliveries AS deliveries
        USING candidates
        WHERE deliveries.tenant_id = candidates.tenant_id
          AND deliveries.id = candidates.id
          AND (SELECT count(*) FROM deleted_replays) >= 0
        RETURNING 1
    )
    SELECT count(*) INTO direct_tombstones FROM deleted_roots;

    tombstones_purged := tombstones_purged + direct_tombstones;

    RETURN NEXT;
END;
$$;

REVOKE ALL ON FUNCTION idenqa.expire_webhook_data(timestamptz, integer) FROM PUBLIC;
