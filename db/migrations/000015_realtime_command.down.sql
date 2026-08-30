DROP TABLE IF EXISTS idenqa.realtime_client_commands;
DROP FUNCTION IF EXISTS idenqa.protect_realtime_client_command();
ALTER TABLE idenqa.websocket_connection_tickets
    DROP CONSTRAINT IF EXISTS websocket_connection_tickets_exact_capture_token_fk;
ALTER TABLE idenqa.capture_tokens
    DROP CONSTRAINT IF EXISTS capture_tokens_tenant_id_verification_unique;
