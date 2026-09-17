ALTER TABLE idenqa.accepted_commands
    DROP CONSTRAINT accepted_commands_exactly_one_anchor,
    DROP CONSTRAINT accepted_commands_policy_id_format,
    DROP COLUMN policy_id,
    ALTER COLUMN verification_id SET NOT NULL;

ALTER TABLE idenqa.proposals
    DROP CONSTRAINT proposals_exactly_one_anchor,
    DROP CONSTRAINT proposals_policy_id_format,
    DROP CONSTRAINT proposals_policy_id_fkey,
    DROP COLUMN policy_id,
    ALTER COLUMN verification_id SET NOT NULL;
