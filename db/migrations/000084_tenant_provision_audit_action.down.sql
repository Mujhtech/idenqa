DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM idenqa.tenant_admin_audit
        WHERE action = 'provision'
    ) THEN
        RAISE EXCEPTION 'cannot downgrade tenant provision audit action while provision history exists';
    END IF;
END
$$;

ALTER TABLE idenqa.tenant_admin_audit
    DROP CONSTRAINT tenant_admin_audit_action,
    ADD CONSTRAINT tenant_admin_audit_action
        CHECK (action IN ('create', 'inspect', 'disable'));
