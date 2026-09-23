ALTER TABLE idenqa.generative_model_registry
    ADD COLUMN logical_model_id text;

ALTER TABLE idenqa.generative_model_registry
    ADD CONSTRAINT generative_model_registry_logical_model_id_valid
    CHECK (
        logical_model_id IS NULL OR
        logical_model_id ~ '^[a-z][a-z0-9._:-]{0,63}$'
    );

COMMENT ON COLUMN idenqa.generative_model_registry.logical_model_id IS
    'Provider-neutral route identifier. NULL marks a pre-binding record that cannot authorize generation.';
