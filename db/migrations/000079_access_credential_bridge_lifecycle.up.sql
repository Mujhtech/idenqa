ALTER TABLE idenqa.api_key_bridge_commands
    ALTER COLUMN delivery_algorithm DROP NOT NULL,
    ALTER COLUMN delivery_ephemeral_public_key DROP NOT NULL,
    ALTER COLUMN delivery_nonce DROP NOT NULL,
    ALTER COLUMN delivery_ciphertext DROP NOT NULL;

ALTER TABLE idenqa.api_key_bridge_commands
    ADD CONSTRAINT api_key_bridge_commands_delivery CHECK (
        (operation IN ('issue', 'rotate') AND delivery_algorithm IS NOT NULL
            AND delivery_ephemeral_public_key IS NOT NULL AND delivery_nonce IS NOT NULL
            AND delivery_ciphertext IS NOT NULL)
        OR (operation = 'revoke' AND delivery_algorithm IS NULL
            AND delivery_ephemeral_public_key IS NULL AND delivery_nonce IS NULL
            AND delivery_ciphertext IS NULL)
    );
