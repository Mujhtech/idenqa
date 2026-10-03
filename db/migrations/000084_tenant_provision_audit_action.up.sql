ALTER TABLE idenqa.tenant_admin_audit
    DROP CONSTRAINT tenant_admin_audit_action,
    ADD CONSTRAINT tenant_admin_audit_action
        CHECK (action IN ('create', 'inspect', 'disable', 'provision'));
