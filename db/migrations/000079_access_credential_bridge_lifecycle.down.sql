ALTER TABLE idenqa.api_key_bridge_commands
    DROP CONSTRAINT IF EXISTS api_key_bridge_commands_delivery;

ALTER TABLE idenqa.api_key_bridge_commands
    ALTER COLUMN delivery_algorithm SET NOT NULL,
    ALTER COLUMN delivery_ephemeral_public_key SET NOT NULL,
    ALTER COLUMN delivery_nonce SET NOT NULL,
    ALTER COLUMN delivery_ciphertext SET NOT NULL;
