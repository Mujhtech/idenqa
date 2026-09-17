ALTER TABLE idenqa.proposals
    ALTER COLUMN verification_id DROP NOT NULL,
    ADD COLUMN policy_id text,
    ADD CONSTRAINT proposals_policy_id_fkey
        FOREIGN KEY (tenant_id, policy_id) REFERENCES idenqa.policies (tenant_id, id),
    ADD CONSTRAINT proposals_policy_id_format
        CHECK (policy_id IS NULL OR policy_id ~ '^pol_[0-9A-HJKMNP-TV-Z]{26}$'),
    ADD CONSTRAINT proposals_exactly_one_anchor
        CHECK ((verification_id IS NULL) <> (policy_id IS NULL));

ALTER TABLE idenqa.accepted_commands
    ALTER COLUMN verification_id DROP NOT NULL,
    ADD COLUMN policy_id text,
    ADD CONSTRAINT accepted_commands_policy_id_format
        CHECK (policy_id IS NULL OR policy_id ~ '^pol_[0-9A-HJKMNP-TV-Z]{26}$'),
    ADD CONSTRAINT accepted_commands_exactly_one_anchor
        CHECK ((verification_id IS NULL) <> (policy_id IS NULL));
