ALTER TABLE idenqa.verification_checks
    ADD COLUMN route_priority integer NOT NULL DEFAULT 0,
    ADD COLUMN route_depends_on text[] NOT NULL DEFAULT '{}',
    ADD COLUMN route_fallback_for text,
    ADD COLUMN route_correlation_group text,
    ADD CONSTRAINT verification_checks_route_priority CHECK (route_priority BETWEEN 0 AND 65535),
    ADD CONSTRAINT verification_checks_route_dependencies CHECK (
        cardinality(route_depends_on) <= 32 AND NOT (name = ANY(route_depends_on))
    ),
    ADD CONSTRAINT verification_checks_route_fallback CHECK (
        route_fallback_for IS NULL OR (route_fallback_for ~ '^[a-z][a-z0-9._:-]{0,127}$' AND route_fallback_for <> name)
    ),
    ADD CONSTRAINT verification_checks_route_correlation CHECK (
        route_correlation_group IS NULL OR route_correlation_group ~ '^[a-z][a-z0-9._:-]{0,127}$'
    );

CREATE INDEX verification_checks_route_predecessors
    ON idenqa.verification_checks (tenant_id, verification_id, name, state);
