CREATE TABLE idenqa.privacy_requests (
    tenant_id text NOT NULL REFERENCES idenqa.tenants (id),
    id text NOT NULL,
    type text NOT NULL,
    state text NOT NULL,
    channel text NOT NULL,
    subject_id text,
    verification_id text,
    region text NOT NULL,
    payload jsonb NOT NULL,
    reason_code text,
    failure_class text,
    effect_kind text,
    effect_reference text,
    effect_digest text,
    expires_at timestamptz NOT NULL,
    requested_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    version bigint NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT privacy_requests_tenant_fk
        FOREIGN KEY (tenant_id) REFERENCES idenqa.tenants (id),
    CONSTRAINT privacy_requests_verification_fk
        FOREIGN KEY (tenant_id, verification_id)
        REFERENCES idenqa.verification_sessions (tenant_id, id),
    CONSTRAINT privacy_requests_id CHECK (id ~ '^prq_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT privacy_requests_type
        CHECK (type IN ('access', 'portability', 'correction', 'restriction', 'objection', 'erasure')),
    CONSTRAINT privacy_requests_state
        CHECK (state IN ('requested', 'in_review', 'approved', 'partially_approved', 'denied', 'executing', 'completed', 'failed', 'withdrawn', 'expired')),
    CONSTRAINT privacy_requests_channel CHECK (channel IN ('tenant_api', 'subject_outcome')),
    CONSTRAINT privacy_requests_subject CHECK (subject_id IS NULL OR subject_id ~ '^[!-~]{1,200}$'),
    CONSTRAINT privacy_requests_region CHECK (region ~ '^[a-z0-9_-]{1,64}$'),
    CONSTRAINT privacy_requests_payload CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT privacy_requests_reason CHECK (reason_code IS NULL OR reason_code ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT privacy_requests_failure CHECK (failure_class IS NULL OR failure_class ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT privacy_requests_effect CHECK (
        (effect_kind IS NULL AND effect_reference IS NULL AND effect_digest IS NULL) OR
        (effect_kind IS NOT NULL AND effect_reference IS NOT NULL AND effect_digest IS NOT NULL AND
         effect_kind ~ '^[a-z][a-z0-9._-]{0,63}$' AND length(effect_reference) BETWEEN 1 AND 512 AND
         length(effect_digest) BETWEEN 1 AND 128)
    ),
    CONSTRAINT privacy_requests_version CHECK (version > 0),
    CONSTRAINT privacy_requests_times CHECK (
        updated_at >= requested_at AND expires_at > requested_at AND
        (state <> 'expired' OR updated_at >= expires_at)
    ),
    CONSTRAINT privacy_requests_channel_verification CHECK (channel <> 'subject_outcome' OR verification_id IS NOT NULL)
);

CREATE INDEX privacy_requests_subject_page
    ON idenqa.privacy_requests (tenant_id, subject_id, id);
CREATE INDEX privacy_requests_state_page
    ON idenqa.privacy_requests (tenant_id, state, id);
CREATE INDEX privacy_requests_subject_channel
    ON idenqa.privacy_requests (tenant_id, verification_id, id)
    WHERE channel = 'subject_outcome';
CREATE INDEX privacy_requests_due
    ON idenqa.privacy_requests (expires_at, id)
    WHERE state IN ('requested', 'in_review');

CREATE FUNCTION idenqa.protect_privacy_request_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.type <> OLD.type OR
       NEW.channel <> OLD.channel OR NEW.requested_at <> OLD.requested_at OR
       NEW.expires_at <> OLD.expires_at OR NEW.region <> OLD.region OR
       NEW.payload IS DISTINCT FROM OLD.payload THEN
        RAISE EXCEPTION 'privacy request identity and instruction are immutable';
    END IF;
    IF OLD.state IN ('denied', 'completed', 'withdrawn', 'expired') AND
       (NEW.state IS DISTINCT FROM OLD.state OR NEW.reason_code IS DISTINCT FROM OLD.reason_code OR
        NEW.effect_kind IS DISTINCT FROM OLD.effect_kind OR NEW.effect_reference IS DISTINCT FROM OLD.effect_reference OR
        NEW.effect_digest IS DISTINCT FROM OLD.effect_digest) THEN
        RAISE EXCEPTION 'terminal privacy request states are immutable';
    END IF;
    IF NEW.version <> OLD.version + 1 AND NEW.state IS DISTINCT FROM OLD.state THEN
        RAISE EXCEPTION 'privacy request transitions must advance the version';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER privacy_request_record_protected
BEFORE UPDATE ON idenqa.privacy_requests
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_privacy_request_record();

CREATE TABLE idenqa.privacy_request_events (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    sequence bigint NOT NULL,
    event_type text NOT NULL,
    from_state text NOT NULL,
    to_state text NOT NULL,
    reason_code text,
    actor_digest text,
    detail text,
    digest text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, request_id, sequence),
    FOREIGN KEY (tenant_id, request_id) REFERENCES idenqa.privacy_requests (tenant_id, id),
    CONSTRAINT privacy_request_events_sequence CHECK (sequence > 0),
    CONSTRAINT privacy_request_events_type CHECK (event_type ~ '^privacy\.(request|restriction|disclosure|processor)\.[a-z0-9_.]{1,64}$'),
    CONSTRAINT privacy_request_events_from CHECK (from_state IN ('requested', 'in_review', 'approved', 'partially_approved', 'denied', 'executing', 'completed', 'failed', 'withdrawn', 'expired')),
    CONSTRAINT privacy_request_events_to CHECK (to_state IN ('requested', 'in_review', 'approved', 'partially_approved', 'denied', 'executing', 'completed', 'failed', 'withdrawn', 'expired')),
    CONSTRAINT privacy_request_events_digest CHECK (length(digest) BETWEEN 1 AND 128),
    CONSTRAINT privacy_request_events_actor CHECK (actor_digest IS NULL OR length(actor_digest) BETWEEN 1 AND 128)
);

CREATE FUNCTION idenqa.reject_privacy_request_event_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'privacy request events are append-only';
END;
$$;

CREATE TRIGGER privacy_request_events_append_only
BEFORE UPDATE OR DELETE ON idenqa.privacy_request_events
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_privacy_request_event_change();

CREATE TABLE idenqa.privacy_request_decisions (
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    id text NOT NULL,
    outcome text NOT NULL,
    reason_code text NOT NULL,
    actor_id text NOT NULL,
    decided_at timestamptz NOT NULL,
    version bigint NOT NULL,
    PRIMARY KEY (tenant_id, request_id, id),
    FOREIGN KEY (tenant_id, request_id) REFERENCES idenqa.privacy_requests (tenant_id, id),
    CONSTRAINT privacy_request_decisions_id CHECK (id ~ '^prd_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT privacy_request_decisions_outcome CHECK (outcome IN ('approved', 'partially_approved', 'denied')),
    CONSTRAINT privacy_request_decisions_reason CHECK (reason_code ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT privacy_request_decisions_version CHECK (version > 0)
);

CREATE FUNCTION idenqa.reject_privacy_request_decision_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'privacy request decisions are immutable';
END;
$$;

CREATE TRIGGER privacy_request_decisions_append_only
BEFORE UPDATE OR DELETE ON idenqa.privacy_request_decisions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_privacy_request_decision_change();

CREATE TABLE idenqa.privacy_restrictions (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    subject_id text NOT NULL,
    scope text NOT NULL,
    purpose text,
    reason_code text NOT NULL,
    region text NOT NULL,
    state text NOT NULL,
    starts_at timestamptz NOT NULL,
    lifted_at timestamptz,
    lift_reason_code text,
    version bigint NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, request_id) REFERENCES idenqa.privacy_requests (tenant_id, id),
    CONSTRAINT privacy_restrictions_id CHECK (id ~ '^prs_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT privacy_restrictions_scope CHECK (scope IN ('subject', 'purpose')),
    CONSTRAINT privacy_restrictions_purpose CHECK (
        (scope = 'subject' AND purpose IS NULL) OR
        (scope = 'purpose' AND purpose IS NOT NULL AND purpose ~ '^[!-~]{1,128}$')
    ),
    CONSTRAINT privacy_restrictions_subject CHECK (subject_id ~ '^[!-~]{1,200}$'),
    CONSTRAINT privacy_restrictions_region CHECK (region ~ '^[a-z0-9_-]{1,64}$'),
    CONSTRAINT privacy_restrictions_state CHECK (state IN ('active', 'lifted')),
    CONSTRAINT privacy_restrictions_reason CHECK (reason_code ~ '^[a-z][a-z0-9._-]{0,63}$'),
    CONSTRAINT privacy_restrictions_lift CHECK (
        (state = 'active' AND lifted_at IS NULL AND lift_reason_code IS NULL) OR
        (state = 'lifted' AND lifted_at IS NOT NULL AND lift_reason_code IS NOT NULL AND lifted_at >= starts_at)
    ),
    CONSTRAINT privacy_restrictions_version CHECK (version > 0)
);

CREATE INDEX privacy_restrictions_active
    ON idenqa.privacy_restrictions (tenant_id, subject_id, id)
    WHERE state = 'active' AND scope = 'subject';

CREATE FUNCTION idenqa.protect_privacy_restriction_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.request_id <> OLD.request_id OR
       NEW.subject_id <> OLD.subject_id OR NEW.scope <> OLD.scope OR
       NEW.purpose IS DISTINCT FROM OLD.purpose OR NEW.reason_code <> OLD.reason_code OR
       NEW.region <> OLD.region OR NEW.starts_at <> OLD.starts_at THEN
        RAISE EXCEPTION 'privacy restriction identity is immutable';
    END IF;
    IF OLD.state = 'lifted' AND (NEW.state IS DISTINCT FROM OLD.state OR NEW.lifted_at IS DISTINCT FROM OLD.lifted_at) THEN
        RAISE EXCEPTION 'lifted privacy restrictions are immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER privacy_restrictions_protected
BEFORE UPDATE ON idenqa.privacy_restrictions
FOR EACH ROW EXECUTE FUNCTION idenqa.protect_privacy_restriction_record();

CREATE TABLE idenqa.privacy_disclosures (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_id text NOT NULL,
    recipient text NOT NULL,
    purpose text NOT NULL,
    data_class text NOT NULL,
    legal_basis text NOT NULL,
    region text NOT NULL,
    reference text NOT NULL,
    disclosed_at timestamptz NOT NULL,
    version bigint NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, request_id) REFERENCES idenqa.privacy_requests (tenant_id, id),
    CONSTRAINT privacy_disclosures_id CHECK (id ~ '^pdc_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT privacy_disclosures_class
        CHECK (data_class IN ('subject_export', 'identity_successor', 'deletion_request', 'restriction', 'objection')),
    CONSTRAINT privacy_disclosures_region CHECK (region ~ '^[a-z0-9_-]{1,64}$'),
    CONSTRAINT privacy_disclosures_version CHECK (version = 1),
    CONSTRAINT privacy_disclosures_reference CHECK (length(reference) BETWEEN 1 AND 128)
);

CREATE FUNCTION idenqa.reject_privacy_disclosure_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'privacy disclosure records are immutable';
END;
$$;

CREATE TRIGGER privacy_disclosures_immutable
BEFORE UPDATE OR DELETE ON idenqa.privacy_disclosures
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_privacy_disclosure_change();

CREATE TABLE idenqa.processor_inventory (
    tenant_id text NOT NULL,
    id text NOT NULL,
    name text NOT NULL,
    role text NOT NULL,
    purpose text NOT NULL,
    data_classes jsonb NOT NULL,
    regions jsonb NOT NULL,
    transfer_mechanism text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT processor_inventory_id CHECK (id ~ '^prc_[0-9A-HJKMNP-TV-Z]{26}$'),
    CONSTRAINT processor_inventory_role CHECK (role IN ('processor', 'subprocessor', 'recipient')),
    CONSTRAINT processor_inventory_classes CHECK (jsonb_typeof(data_classes) = 'array' AND jsonb_array_length(data_classes) BETWEEN 1 AND 8),
    CONSTRAINT processor_inventory_regions CHECK (jsonb_typeof(regions) = 'array' AND jsonb_array_length(regions) BETWEEN 1 AND 16),
    CONSTRAINT processor_inventory_version CHECK (version > 0),
    CONSTRAINT processor_inventory_times CHECK (updated_at >= created_at),
    CONSTRAINT processor_inventory_name CHECK (length(name) BETWEEN 1 AND 200)
);

CREATE TABLE idenqa.processor_inventory_revisions (
    tenant_id text NOT NULL,
    id text NOT NULL,
    version bigint NOT NULL,
    name text NOT NULL,
    role text NOT NULL,
    purpose text NOT NULL,
    data_classes jsonb NOT NULL,
    regions jsonb NOT NULL,
    transfer_mechanism text NOT NULL,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id, version),
    FOREIGN KEY (tenant_id, id) REFERENCES idenqa.processor_inventory (tenant_id, id),
    CONSTRAINT processor_inventory_revisions_version CHECK (version > 0)
);

CREATE FUNCTION idenqa.reject_processor_inventory_revision_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'processor inventory revisions are append-only';
END;
$$;

CREATE TRIGGER processor_inventory_revisions_append_only
BEFORE UPDATE OR DELETE ON idenqa.processor_inventory_revisions
FOR EACH ROW EXECUTE FUNCTION idenqa.reject_processor_inventory_revision_change();

ALTER TABLE idenqa.privacy_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_request_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_request_events FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_request_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_request_decisions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_restrictions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_restrictions FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_disclosures ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.privacy_disclosures FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.processor_inventory ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.processor_inventory FORCE ROW LEVEL SECURITY;
ALTER TABLE idenqa.processor_inventory_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.processor_inventory_revisions FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_scope ON idenqa.privacy_requests
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.privacy_request_events
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.privacy_request_decisions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.privacy_restrictions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.privacy_disclosures
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.processor_inventory
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));
CREATE POLICY tenant_scope ON idenqa.processor_inventory_revisions
    USING (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('idenqa.tenant_id', true), ''));

REVOKE ALL ON idenqa.privacy_requests FROM PUBLIC;
REVOKE ALL ON idenqa.privacy_request_events FROM PUBLIC;
REVOKE ALL ON idenqa.privacy_request_decisions FROM PUBLIC;
REVOKE ALL ON idenqa.privacy_restrictions FROM PUBLIC;
REVOKE ALL ON idenqa.privacy_disclosures FROM PUBLIC;
REVOKE ALL ON idenqa.processor_inventory FROM PUBLIC;
REVOKE ALL ON idenqa.processor_inventory_revisions FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_privacy_request_record() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_privacy_request_event_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_privacy_request_decision_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.protect_privacy_restriction_record() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_privacy_disclosure_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION idenqa.reject_processor_inventory_revision_change() FROM PUBLIC;
