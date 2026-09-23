ALTER TABLE idenqa.generative_model_registry
    DROP CONSTRAINT generative_model_registry_logical_model_id_valid;

ALTER TABLE idenqa.generative_model_registry
    DROP COLUMN logical_model_id;
