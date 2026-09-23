CREATE TABLE idenqa.evidence_temporal_frames (
    tenant_id text NOT NULL,
    verification_id text NOT NULL,
    upload_id text NOT NULL,
    evidence_id text NOT NULL,
    sequence_digest text NOT NULL,
    frame_index integer NOT NULL,
    frame_count integer NOT NULL,
    challenge_id text NOT NULL,
    captured_at timestamptz NOT NULL,
    previous_digest text,
    content_digest text NOT NULL,
    PRIMARY KEY (tenant_id, upload_id),
    UNIQUE (tenant_id, verification_id, sequence_digest, frame_index),
    UNIQUE (tenant_id, evidence_id),
    FOREIGN KEY (tenant_id, upload_id)
        REFERENCES idenqa.evidence_upload_intents (tenant_id, id),
    FOREIGN KEY (tenant_id, evidence_id)
        REFERENCES idenqa.evidence_upload_intents (tenant_id, evidence_id),
    CONSTRAINT evidence_temporal_frames_sequence CHECK (sequence_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT evidence_temporal_frames_position CHECK (frame_count BETWEEN 2 AND 32 AND frame_index BETWEEN 0 AND frame_count - 1),
    CONSTRAINT evidence_temporal_frames_challenge CHECK (challenge_id ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)+$'),
    CONSTRAINT evidence_temporal_frames_chain CHECK (
        content_digest ~ '^sha256:[0-9a-f]{64}$' AND
        ((frame_index = 0 AND previous_digest IS NULL) OR
         (frame_index > 0 AND previous_digest ~ '^sha256:[0-9a-f]{64}$'))
    )
);

ALTER TABLE idenqa.evidence_temporal_frames ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.evidence_temporal_frames FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON idenqa.evidence_temporal_frames
    USING (tenant_id = current_setting('idenqa.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('idenqa.tenant_id', true));
REVOKE ALL ON idenqa.evidence_temporal_frames FROM PUBLIC;
