CREATE TABLE idenqa.usage_receipts (
 tenant_id text NOT NULL REFERENCES idenqa.tenants(id),
 id text NOT NULL CHECK(id ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=16384),
 created_at timestamptz NOT NULL,
 delivered_at timestamptz,
 PRIMARY KEY(tenant_id,id)
);
CREATE INDEX usage_receipts_pending ON idenqa.usage_receipts(tenant_id,created_at,id) WHERE delivered_at IS NULL;
ALTER TABLE idenqa.usage_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE idenqa.usage_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY usage_receipts_tenant ON idenqa.usage_receipts USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true));
REVOKE ALL ON idenqa.usage_receipts FROM PUBLIC;

CREATE FUNCTION idenqa.usage_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.receipt IS DISTINCT FROM OLD.receipt OR NEW.created_at IS DISTINCT FROM OLD.created_at OR (OLD.delivered_at IS NOT NULL AND NEW.delivered_at IS DISTINCT FROM OLD.delivered_at) THEN RAISE EXCEPTION 'usage receipt is immutable'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER usage_receipt_immutable BEFORE UPDATE OR DELETE ON idenqa.usage_receipts FOR EACH ROW EXECUTE FUNCTION idenqa.usage_receipt_immutable();
