ALTER TABLE idenqa.api_request_logs
    ADD COLUMN client_ip inet NOT NULL DEFAULT '0.0.0.0'::inet,
    ADD COLUMN query_parameters jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN body_parameters jsonb;

ALTER TABLE idenqa.api_request_logs
    ALTER COLUMN client_ip DROP DEFAULT,
    ADD CONSTRAINT api_request_logs_query_parameters_object CHECK (jsonb_typeof(query_parameters) = 'object'),
    ADD CONSTRAINT api_request_logs_body_parameters_object CHECK (body_parameters IS NULL OR jsonb_typeof(body_parameters) = 'object');
