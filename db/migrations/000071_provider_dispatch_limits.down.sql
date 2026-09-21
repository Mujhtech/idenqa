DROP TABLE IF EXISTS idenqa.provider_dispatch_admissions;
DROP TABLE IF EXISTS idenqa.provider_dispatch_leases;
ALTER TABLE idenqa.provider_registration_history
    DROP CONSTRAINT provider_registration_history_operation,
    ADD CONSTRAINT provider_registration_history_operation
        CHECK (operation IN ('create', 'update', 'enable', 'disable'));
