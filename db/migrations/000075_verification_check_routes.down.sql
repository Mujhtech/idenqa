DROP INDEX IF EXISTS idenqa.verification_checks_route_predecessors;

ALTER TABLE idenqa.verification_checks
    DROP CONSTRAINT IF EXISTS verification_checks_route_correlation,
    DROP CONSTRAINT IF EXISTS verification_checks_route_fallback,
    DROP CONSTRAINT IF EXISTS verification_checks_route_dependencies,
    DROP CONSTRAINT IF EXISTS verification_checks_route_priority,
    DROP COLUMN IF EXISTS route_correlation_group,
    DROP COLUMN IF EXISTS route_fallback_for,
    DROP COLUMN IF EXISTS route_depends_on,
    DROP COLUMN IF EXISTS route_priority;
