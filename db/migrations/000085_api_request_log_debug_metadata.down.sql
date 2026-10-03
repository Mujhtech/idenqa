ALTER TABLE idenqa.api_request_logs
    DROP CONSTRAINT IF EXISTS api_request_logs_body_parameters_object,
    DROP CONSTRAINT IF EXISTS api_request_logs_query_parameters_object,
    DROP COLUMN IF EXISTS body_parameters,
    DROP COLUMN IF EXISTS query_parameters,
    DROP COLUMN IF EXISTS client_ip;
