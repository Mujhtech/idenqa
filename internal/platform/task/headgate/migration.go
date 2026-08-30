package headgate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mujhtech/headgate/go/headgatemigrate"
)

// MigrationReport is the stable operational projection printed by Idenqa's CLI.
type MigrationReport struct {
	State    string
	Current  int
	Latest   int
	Pending  bool
	Healthy  bool
	Messages []string
	Steps    int
}

// Migrator owns one administrative PostgreSQL connection.
type Migrator struct {
	connection *pgx.Conn
	schema     string
}

// CheckPoolSchema validates compatibility through the caller-owned runtime
// pool, avoiding a second connection lifecycle in readiness polling.
func CheckPoolSchema(
	ctx context.Context,
	pool *pgxpool.Pool,
	schema string,
) (MigrationReport, error) {
	if pool == nil || !identifierPattern.MatchString(schema) {
		return MigrationReport{}, fmt.Errorf("%w: Headgate schema check", task.ErrInvalid)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("acquire Headgate schema connection: %w", err)
	}
	defer connection.Release()
	validation, err := headgatemigrate.ValidatePostgresInSchema(ctx, connection.Conn(), schema)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("validate Headgate schema: %w", err)
	}
	report := reportOf(validation)
	if !validation.OK() {
		return report, errors.New("headgate schema is incompatible")
	}
	return report, nil
}

// OpenMigrator connects with an explicit operational credential. It never runs
// migrations implicitly.
func OpenMigrator(
	ctx context.Context,
	url, schema string,
	connectTimeout, statementTimeout time.Duration,
) (*Migrator, error) {
	if url == "" || !identifierPattern.MatchString(schema) || connectTimeout <= 0 || statementTimeout <= 0 {
		return nil, fmt.Errorf("%w: Headgate migration configuration", task.ErrInvalid)
	}
	configuration, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse Headgate migration database URL: %w", err)
	}
	configuration.RuntimeParams["statement_timeout"] = strconv.FormatInt(statementTimeout.Milliseconds(), 10)
	connectContext, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	connection, err := pgx.ConnectConfig(connectContext, configuration)
	if err != nil {
		return nil, fmt.Errorf("connect Headgate migrator: %w", err)
	}
	return &Migrator{connection: connection, schema: schema}, nil
}

// Preflight verifies exact schema compatibility without mutating PostgreSQL.
func (migrator *Migrator) Preflight(ctx context.Context) (MigrationReport, error) {
	if migrator == nil || migrator.connection == nil {
		return MigrationReport{}, fmt.Errorf("%w: Headgate migrator", task.ErrInvalid)
	}
	validation, err := headgatemigrate.ValidatePostgresInSchema(ctx, migrator.connection, migrator.schema)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("validate Headgate schema: %w", err)
	}
	report := reportOf(validation)
	if !validation.OK() {
		return report, errors.New("headgate schema is incompatible")
	}
	return report, nil
}

// Up creates only the selected namespace and applies all pinned Headgate migrations.
func (migrator *Migrator) Up(ctx context.Context) (MigrationReport, error) {
	if migrator == nil || migrator.connection == nil {
		return MigrationReport{}, fmt.Errorf("%w: Headgate migrator", task.ErrInvalid)
	}
	if _, err := migrator.connection.Exec(
		ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{migrator.schema}.Sanitize(),
	); err != nil {
		return MigrationReport{}, fmt.Errorf("create Headgate schema: %w", err)
	}
	result, err := headgatemigrate.MigratePostgresInSchema(
		ctx, migrator.connection, migrator.schema, headgatemigrate.Up, headgatemigrate.Options{},
	)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("migrate Headgate schema up: %w", err)
	}
	report, err := migrator.Preflight(ctx)
	report.Steps = len(result.Steps)
	return report, err
}

// DownOne rolls back exactly one migration. The CLI supplies the environment guard.
func (migrator *Migrator) DownOne(ctx context.Context) (MigrationReport, error) {
	if migrator == nil || migrator.connection == nil {
		return MigrationReport{}, fmt.Errorf("%w: Headgate migrator", task.ErrInvalid)
	}
	maximum := 1
	result, err := headgatemigrate.MigratePostgresInSchema(
		ctx, migrator.connection, migrator.schema, headgatemigrate.Down,
		headgatemigrate.Options{MaxSteps: &maximum},
	)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("migrate Headgate schema down: %w", err)
	}
	validation, validationErr := headgatemigrate.ValidatePostgresInSchema(
		ctx, migrator.connection, migrator.schema,
	)
	report := reportOf(validation)
	report.Steps = len(result.Steps)
	return report, validationErr
}

// Close releases the administrative connection.
func (migrator *Migrator) Close(ctx context.Context) error {
	if migrator == nil || migrator.connection == nil {
		return nil
	}
	err := migrator.connection.Close(ctx)
	migrator.connection = nil
	return err
}

func reportOf(validation headgatemigrate.PostgresValidation) MigrationReport {
	return MigrationReport{
		State: string(validation.State), Current: validation.CurrentVersion,
		Latest: validation.LatestVersion, Pending: validation.CurrentVersion != validation.LatestVersion,
		Healthy: validation.OK(), Messages: append([]string(nil), validation.Messages...),
	}
}
