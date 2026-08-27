package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

var (
	// ErrMigrationDirty means an interrupted migration needs operator repair.
	ErrMigrationDirty = errors.New("database migration state is dirty")
	// ErrMigrationNewer means the database was migrated by a newer binary.
	ErrMigrationNewer = errors.New("database schema is newer than this binary")
	// ErrMigrationFailed is returned without driver or connection details.
	ErrMigrationFailed = errors.New("database migration failed")
	// ErrRollbackForbidden prevents destructive migration commands outside
	// explicitly confirmed development and test environments.
	ErrRollbackForbidden = errors.New("database rollback is forbidden")
)

// MigrationConfig contains the connection and statement bounds used only by
// the operational migration command.
type MigrationConfig struct {
	URL              string
	ConnectTimeout   time.Duration
	StatementTimeout time.Duration
}

// MigrationReport describes the database schema without exposing connection
// or topology information.
type MigrationReport struct {
	Current uint
	Latest  uint
	Dirty   bool
	Pending bool
}

// RollbackGuard carries the two independent requirements for a down step.
type RollbackGuard struct {
	Environment string
	Confirmed   bool
}

// Migrator applies the embedded, reviewed migration set.
type Migrator struct {
	database  *sql.DB
	migration *migrate.Migrate
}

// OpenMigrator connects a single-purpose migration engine to PostgreSQL.
func OpenMigrator(ctx context.Context, configuration MigrationConfig) (*Migrator, error) {
	if configuration.URL == "" || configuration.ConnectTimeout <= 0 || configuration.StatementTimeout <= 0 {
		return nil, ErrInvalidConfiguration
	}

	sourceDriver, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	database, err := sql.Open("pgx/v5", configuration.URL)
	if err != nil {
		_ = sourceDriver.Close()

		return nil, ErrInvalidConfiguration
	}
	// The migrate driver retains one connection while it owns its PostgreSQL
	// instance. Keep one additional bounded connection for preflight pings.
	database.SetMaxOpenConns(2)
	database.SetMaxIdleConns(2)

	connectContext, cancel := context.WithTimeout(ctx, configuration.ConnectTimeout)
	defer cancel()
	if err := database.PingContext(connectContext); err != nil {
		_ = database.Close()
		_ = sourceDriver.Close()

		return nil, ErrUnavailable
	}
	databaseDriver, err := pgxmigrate.WithInstance(database, &pgxmigrate.Config{
		SchemaName:       "public",
		StatementTimeout: configuration.StatementTimeout,
	})
	if err != nil {
		_ = database.Close()
		_ = sourceDriver.Close()

		return nil, ErrMigrationFailed
	}
	migration, err := migrate.NewWithInstance("iofs", sourceDriver, "idenqa", databaseDriver)
	if err != nil {
		_ = databaseDriver.Close()
		_ = sourceDriver.Close()

		return nil, ErrMigrationFailed
	}

	return &Migrator{database: database, migration: migration}, nil
}

// Preflight checks connectivity, dirty state, and binary compatibility.
func (migrator *Migrator) Preflight(ctx context.Context) (MigrationReport, error) {
	if err := migrator.database.PingContext(ctx); err != nil {
		return MigrationReport{}, ErrUnavailable
	}
	report := MigrationReport{Latest: migrations.LatestVersion}
	version, dirty, err := migrator.migration.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		report.Pending = report.Latest > 0

		return report, nil
	}
	if err != nil {
		return MigrationReport{}, ErrMigrationFailed
	}
	report.Current = version
	report.Dirty = dirty
	report.Pending = version < report.Latest
	if dirty {
		return report, ErrMigrationDirty
	}
	if version > report.Latest {
		return report, ErrMigrationNewer
	}

	return report, nil
}

// Up applies all pending embedded migrations.
func (migrator *Migrator) Up(ctx context.Context) (MigrationReport, error) {
	if _, err := migrator.Preflight(ctx); err != nil {
		return MigrationReport{}, err
	}
	if err := migrator.run(ctx, migrator.migration.Up); err != nil {
		return MigrationReport{}, err
	}

	return migrator.Preflight(ctx)
}

// DownOne rolls back exactly one migration after enforcing the development or
// test environment and explicit confirmation requirements.
func (migrator *Migrator) DownOne(ctx context.Context, guard RollbackGuard) (MigrationReport, error) {
	if !guard.Confirmed || (guard.Environment != "development" && guard.Environment != "test") {
		return MigrationReport{}, ErrRollbackForbidden
	}
	if _, err := migrator.Preflight(ctx); err != nil {
		return MigrationReport{}, err
	}
	if err := migrator.run(ctx, func() error { return migrator.migration.Steps(-1) }); err != nil {
		return MigrationReport{}, err
	}

	return migrator.Preflight(ctx)
}

func (migrator *Migrator) run(ctx context.Context, action func() error) error {
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case migrator.migration.GracefulStop <- true:
			default:
			}
		case <-finished:
		}
	}()

	err := action()
	close(finished)
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	if err != nil {
		return ErrMigrationFailed
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migration context: %w", err)
	}

	return nil
}

// Close releases the migration source and database connection.
func (migrator *Migrator) Close() error {
	sourceErr, databaseErr := migrator.migration.Close()

	return errors.Join(sourceErr, databaseErr)
}
