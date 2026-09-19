DROP TRIGGER provider_callback_receipt_guard ON idenqa.provider_callback_receipts;
DROP FUNCTION idenqa.guard_provider_callback_receipt();
DROP TABLE idenqa.provider_callback_receipts;

DROP FUNCTION idenqa.resolve_provider_callback(text);
DROP INDEX idenqa.provider_requests_callback_token;
ALTER TABLE idenqa.provider_requests DROP COLUMN callback_token_digest;
