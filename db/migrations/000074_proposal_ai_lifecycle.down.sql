ALTER TABLE idenqa.proposal_generation_usage
    DROP COLUMN error_code,
    DROP COLUMN usage_reported,
    DROP COLUMN outcome;

DROP TABLE idenqa.proposal_generation_activation_history;
DROP TABLE idenqa.proposal_generation_activations;

ALTER TABLE idenqa.proposal_mode_configs
    DROP CONSTRAINT proposal_mode_generation_pins_complete,
    DROP COLUMN activation_revision,
    DROP COLUMN prompt_registry_version,
    DROP COLUMN model_registry_version,
    DROP COLUMN model_registry_id;
