-- Fleet KMS envelope rewrap, verified key destruction, and dual-control key
-- recovery ceremonies.
--
-- Wrapping envelopes are operational metadata: replacing a wrapping never
-- changes the protected plaintext, its aggregate identity, or its lifecycle
-- state. The three immutable-wrapping triggers are therefore narrowed to admit
-- exactly one additional transition, a recorded rewrap in which every identity
-- field is unchanged and the new envelope is written in the same statement.
-- Every other mutation still fails closed.

-- Record the instant an HMAC key version envelope was last replaced. The
-- wrapping change must be accompanied by a strictly later rewrap instant.
ALTER TABLE idenqa.hmac_keys ADD COLUMN rewrapped_at timestamptz;
ALTER TABLE idenqa.hmac_keys ADD CONSTRAINT hmac_key_rewrap_time CHECK (
    rewrapped_at IS NULL OR rewrapped_at >= created_at
);

CREATE OR REPLACE FUNCTION idenqa.protect_hmac_key_version()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    is_rewrap boolean;
BEGIN
    IF TG_OP='DELETE' THEN
        RAISE EXCEPTION 'hmac key versions are immutable';
    END IF;
    IF NEW.tenant_id<>OLD.tenant_id OR NEW.domain<>OLD.domain OR NEW.version<>OLD.version
       OR NEW.id<>OLD.id OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'hmac key versions are immutable';
    END IF;
    is_rewrap := NEW.wrapped_key<>OLD.wrapped_key
        AND NEW.state=OLD.state
        AND NEW.retired_at IS NOT DISTINCT FROM OLD.retired_at
        AND NEW.rewrapped_at IS NOT NULL
        AND NEW.rewrapped_at>=OLD.created_at
        AND (OLD.rewrapped_at IS NULL OR NEW.rewrapped_at>OLD.rewrapped_at);
    IF is_rewrap THEN
        RETURN NEW;
    END IF;
    IF NEW.wrapped_key<>OLD.wrapped_key
       OR NEW.rewrapped_at IS DISTINCT FROM OLD.rewrapped_at THEN
        RAISE EXCEPTION 'hmac key wrapping may only change through a recorded rewrap';
    END IF;
    IF NOT ((OLD.state='active' AND NEW.state='disabled')
         OR (OLD.state='disabled' AND NEW.state='retired')) THEN
        RAISE EXCEPTION 'hmac key state transition is not permitted';
    END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_immutable_rows()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'webhook_delivery_attempts'
       AND TG_OP = 'DELETE'
       AND current_setting('idenqa.webhook_retention', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_TABLE_NAME = 'webhook_secrets'
       AND TG_OP = 'UPDATE'
       AND current_setting('idenqa.key_rewrap', true) = 'on'
       AND NEW.tenant_id = OLD.tenant_id AND NEW.endpoint_id = OLD.endpoint_id
       AND NEW.version = OLD.version AND NEW.created_at = OLD.created_at
       AND (NEW.provider <> OLD.provider OR NEW.reference <> OLD.reference
            OR NEW.key_version <> OLD.key_version OR NEW.algorithm <> OLD.algorithm
            OR NEW.ciphertext <> OLD.ciphertext) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'webhook secret and attempt history is append-only';
END $$;

CREATE OR REPLACE FUNCTION idenqa.protect_webhook_event_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.event_type <> OLD.event_type OR
       NEW.schema_version <> OLD.schema_version OR NEW.dedupe_key <> OLD.dedupe_key OR
       NEW.created_at <> OLD.created_at OR NEW.aggregate_ids <> OLD.aggregate_ids OR
       NEW.payload_expires_at <> OLD.payload_expires_at OR NEW.retain_until <> OLD.retain_until THEN
        RAISE EXCEPTION 'webhook event identity and retention metadata are immutable';
    END IF;
    IF NEW.body IS NOT DISTINCT FROM OLD.body THEN
        IF NEW.body_digest <> OLD.body_digest
           OR NEW.body_provider IS DISTINCT FROM OLD.body_provider
           OR NEW.body_reference IS DISTINCT FROM OLD.body_reference
           OR NEW.body_key_version IS DISTINCT FROM OLD.body_key_version
           OR NEW.body_algorithm IS DISTINCT FROM OLD.body_algorithm THEN
            RAISE EXCEPTION 'webhook event payload is immutable outside retention expiry or key rewrap';
        END IF;
        RETURN NEW;
    END IF;
    IF current_setting('idenqa.webhook_retention', true) = 'on'
       AND OLD.body IS NOT NULL AND NEW.body IS NULL
       AND NEW.payload_expired_at IS NOT NULL
       AND NEW.body_provider IS NULL AND NEW.body_reference IS NULL
       AND NEW.body_key_version IS NULL AND NEW.body_algorithm IS NULL THEN
        RETURN NEW;
    END IF;
    IF current_setting('idenqa.key_rewrap', true) = 'on'
       AND OLD.body IS NOT NULL AND NEW.body IS NOT NULL
       AND NEW.body_digest <> OLD.body_digest
       AND num_nonnulls(NEW.body_provider, NEW.body_reference, NEW.body_key_version, NEW.body_algorithm) = 4
       AND NEW.payload_expired_at IS NOT DISTINCT FROM OLD.payload_expired_at THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'webhook event payload is immutable outside retention expiry or key rewrap';
END $$;

-- Resumable, installation-wide rewrap progress. One row per artifact class
-- carries the last durably completed cursor and the observed KMS material
-- epoch that produced the current generation.
CREATE TABLE idenqa.key_rewrap_state (
    class text PRIMARY KEY,
    epoch_key text NOT NULL,
    epoch jsonb NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 1),
    status text NOT NULL CHECK (status IN ('running', 'failed', 'completed')),
    cursor_tenant text NOT NULL DEFAULT '',
    cursor_object text NOT NULL DEFAULT '',
    processed bigint NOT NULL DEFAULT 0 CHECK (processed >= 0),
    rewrapped bigint NOT NULL DEFAULT 0 CHECK (rewrapped >= 0),
    skipped bigint NOT NULL DEFAULT 0 CHECK (skipped >= 0),
    failed bigint NOT NULL DEFAULT 0 CHECK (failed >= 0),
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    last_error text,
    next_attempt_at timestamptz,
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL,
    CHECK (class ~ '^[a-z][a-z0-9._-]{2,99}$'),
    CHECK (epoch_key ~ '^[0-9a-f]{64}$'),
    CHECK (length(cursor_tenant) <= 128 AND length(cursor_object) <= 512),
    CHECK (last_error IS NULL OR length(last_error) BETWEEN 1 AND 512),
    CHECK (updated_at >= started_at),
    CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);

-- Append-only batch audit. Each row is the durable evidence that one bounded
-- sweep batch advanced the cursor without skipping a failed object.
CREATE TABLE idenqa.key_rewrap_audit (
    class text NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 1),
    sequence bigint NOT NULL CHECK (sequence >= 1),
    epoch jsonb NOT NULL,
    cursor_tenant text NOT NULL,
    cursor_object text NOT NULL,
    next_tenant text NOT NULL,
    next_object text NOT NULL,
    processed integer NOT NULL CHECK (processed BETWEEN 0 AND 10000),
    rewrapped integer NOT NULL CHECK (rewrapped BETWEEN 0 AND 10000),
    skipped integer NOT NULL CHECK (skipped BETWEEN 0 AND 10000),
    failed integer NOT NULL CHECK (failed BETWEEN 0 AND 10000),
    status text NOT NULL CHECK (status IN ('running', 'failed', 'completed', 'epoch_changed')),
    error_class text,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (class, generation, sequence),
    CHECK (error_class IS NULL OR error_class ~ '^[a-z][a-z0-9._-]{2,63}$')
);

CREATE FUNCTION idenqa.reject_key_rewrap_audit_change()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'key rewrap batches are append-only';
END $$;

CREATE TRIGGER key_rewrap_audit_append_only
    BEFORE UPDATE OR DELETE ON idenqa.key_rewrap_audit
    FOR EACH ROW EXECUTE FUNCTION idenqa.reject_key_rewrap_audit_change();

-- Installation-wide tenant discovery for the sweep. Row-level security stays
-- forced for ordinary sessions; only this SECURITY DEFINER function exposes
-- identifier-only pages of active tenants in bounded order.
CREATE FUNCTION idenqa.list_key_rewrap_tenants(after text, batch_size integer)
RETURNS TABLE (tenant_id text)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
SET row_security = off
AS $$
    SELECT tenants.id FROM idenqa.tenants AS tenants
    WHERE $2 BETWEEN 1 AND 500 AND tenants.state <> 'deleted' AND tenants.id > $1
    ORDER BY tenants.id LIMIT $2;
$$;

REVOKE ALL ON FUNCTION idenqa.list_key_rewrap_tenants(text, integer) FROM PUBLIC;

-- Immutable verified-destruction receipts. A receipt is only written after an
-- installation-wide, single-transaction reference scan proves that no stored
-- ciphertext or ledger reference still targets the wrapping material.
CREATE TABLE idenqa.key_destruction_verifications (
    id text PRIMARY KEY,
    provider text NOT NULL,
    reference text NOT NULL,
    version text NOT NULL,
    algorithm text NOT NULL,
    state text NOT NULL CHECK (state IN ('verified', 'blocked')),
    counts jsonb NOT NULL,
    total bigint NOT NULL CHECK (total >= 0),
    verifier text NOT NULL,
    reason text NOT NULL,
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    verified_at timestamptz NOT NULL,
    CHECK (id ~ '^kdv_[0-9A-HJKMNP-TV-Z]{26}$'),
    CHECK (length(provider) BETWEEN 1 AND 512 AND provider !~ '[[:space:][:cntrl:]]'),
    CHECK (length(reference) BETWEEN 1 AND 2048 AND reference !~ '[[:space:][:cntrl:]]'),
    CHECK (length(version) BETWEEN 1 AND 512 AND version !~ '[[:space:][:cntrl:]]'),
    CHECK (length(algorithm) BETWEEN 1 AND 512 AND algorithm !~ '[[:space:][:cntrl:]]'),
    CHECK (jsonb_typeof(counts) = 'object'),
    CHECK (length(verifier) BETWEEN 1 AND 512 AND verifier !~ '[[:space:][:cntrl:]]'),
    CHECK (length(reason) BETWEEN 8 AND 500 AND reason !~ '[[:cntrl:]]'),
    CHECK ((state = 'verified') = (total = 0))
);

CREATE TRIGGER key_destruction_verifications_immutable
    BEFORE UPDATE OR DELETE ON idenqa.key_destruction_verifications
    FOR EACH ROW EXECUTE FUNCTION idenqa.reject_key_rewrap_audit_change();

-- Provider-side destruction scheduling receipts. A schedule is only accepted
-- against a verified (zero-reference) receipt.
CREATE TABLE idenqa.key_destruction_schedules (
    id text PRIMARY KEY,
    verification_id text NOT NULL REFERENCES idenqa.key_destruction_verifications (id),
    provider text NOT NULL,
    reference text NOT NULL,
    version text NOT NULL,
    algorithm text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('scheduled', 'recorded')),
    provider_deletion_at timestamptz,
    actor text NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (id ~ '^kds_[0-9A-HJKMNP-TV-Z]{26}$'),
    CHECK ((mode = 'scheduled') = (provider_deletion_at IS NOT NULL)),
    CHECK (length(actor) BETWEEN 1 AND 512 AND actor !~ '[[:space:][:cntrl:]]'),
    CHECK (length(reason) BETWEEN 8 AND 500 AND reason !~ '[[:cntrl:]]')
);

CREATE TRIGGER key_destruction_schedules_immutable
    BEFORE UPDATE OR DELETE ON idenqa.key_destruction_schedules
    FOR EACH ROW EXECUTE FUNCTION idenqa.reject_key_rewrap_audit_change();

-- Dual-control recovery ceremonies. Two distinct principals are required: the
-- starter cannot approve, and completion is only possible from an approved,
-- unexpired ceremony. Aborted or expired ceremonies change no key material.
CREATE TABLE idenqa.key_recovery_ceremonies (
    id text PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('rewrap', 'migrate_epoch')),
    class text NOT NULL CHECK (class ~ '^[a-z][a-z0-9._-]{2,99}$'),
    tenant_id text,
    target_provider text NOT NULL,
    target_reference text NOT NULL,
    target_version text NOT NULL,
    target_algorithm text NOT NULL,
    state text NOT NULL CHECK (state IN ('started', 'approved', 'completed', 'aborted')),
    version bigint NOT NULL CHECK (version >= 1),
    started_by text NOT NULL,
    started_at timestamptz NOT NULL,
    approve_by timestamptz NOT NULL,
    approved_by text,
    approved_at timestamptz,
    usable_until timestamptz,
    completed_by text,
    completed_at timestamptz,
    aborted_by text,
    aborted_at timestamptz,
    abort_reason text,
    receipt jsonb,
    reason text NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (id ~ '^krc_[0-9A-HJKMNP-TV-Z]{26}$'),
    CHECK (approve_by > started_at),
    CHECK (updated_at >= started_at),
    CHECK ((state IN ('approved', 'completed')) = (approved_at IS NOT NULL)),
    CHECK ((state IN ('approved', 'completed')) = (approved_by IS NOT NULL)),
    CHECK ((state IN ('approved', 'completed')) = (usable_until IS NOT NULL)),
    CHECK ((state = 'completed') = (completed_at IS NOT NULL)),
    CHECK ((state = 'completed') = (completed_by IS NOT NULL)),
    CHECK ((state = 'aborted') = (aborted_at IS NOT NULL)),
    CHECK ((state = 'aborted') = (aborted_by IS NOT NULL)),
    CHECK ((state = 'aborted') = (abort_reason IS NOT NULL)),
    CHECK (approved_by IS NULL OR approved_by <> started_by),
    CHECK (length(started_by) BETWEEN 1 AND 512 AND started_by !~ '[[:space:][:cntrl:]]'),
    CHECK (length(reason) BETWEEN 8 AND 500 AND reason !~ '[[:cntrl:]]'),
    CHECK (tenant_id IS NULL OR tenant_id ~ '^ten_[0-9A-HJKMNP-TV-Z]{26}$')
);

CREATE FUNCTION idenqa.protect_key_recovery_ceremony()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'key recovery ceremonies are durable';
    END IF;
    IF NEW.id <> OLD.id OR NEW.kind <> OLD.kind OR NEW.class <> OLD.class
       OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.target_provider <> OLD.target_provider OR NEW.target_reference <> OLD.target_reference
       OR NEW.target_version <> OLD.target_version OR NEW.target_algorithm <> OLD.target_algorithm
       OR NEW.started_by <> OLD.started_by OR NEW.started_at <> OLD.started_at
       OR NEW.approve_by <> OLD.approve_by OR NEW.reason <> OLD.reason THEN
        RAISE EXCEPTION 'key recovery ceremony identity is immutable';
    END IF;
    IF NEW.version <> OLD.version + 1 THEN
        RAISE EXCEPTION 'key recovery ceremony version must advance by one';
    END IF;
    IF NOT ((OLD.state = 'started' AND NEW.state IN ('approved', 'aborted'))
         OR (OLD.state = 'approved' AND NEW.state IN ('completed', 'aborted'))) THEN
        RAISE EXCEPTION 'key recovery ceremony transition is not permitted';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER key_recovery_ceremonies_transition
    BEFORE UPDATE OR DELETE ON idenqa.key_recovery_ceremonies
    FOR EACH ROW EXECUTE FUNCTION idenqa.protect_key_recovery_ceremony();

CREATE INDEX key_recovery_ceremonies_open
    ON idenqa.key_recovery_ceremonies (state, approve_by)
    WHERE state IN ('started', 'approved');

REVOKE ALL ON idenqa.key_rewrap_state, idenqa.key_rewrap_audit,
    idenqa.key_destruction_verifications, idenqa.key_destruction_schedules,
    idenqa.key_recovery_ceremonies FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_key_rewrap_audit_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_key_recovery_ceremony() FROM PUBLIC;
