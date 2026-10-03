//go:build integration

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/runtimepermissions"
	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLFoundation(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()

	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	report, err := migrator.Preflight(ctx)
	if err != nil {
		t.Fatalf("Preflight() empty database error = %v", err)
	}
	if report.Current != 0 || !report.Pending || report.Latest != migrations.LatestVersion {
		t.Fatalf("empty report = %+v", report)
	}
	report, err = migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if report.Current != migrations.LatestVersion || report.Pending || report.Dirty {
		t.Fatalf("migrated report = %+v", report)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	pool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("Open() pool error = %v", err)
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	rollbackCause := errors.New("force rollback")
	err = pool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{Isolation: idenqapostgres.IsolationSerializable},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			if _, err := transaction.Exec(ctx, "CREATE TABLE idenqa.transaction_rollback_probe (value bigint)"); err != nil {
				return fmt.Errorf("create rollback probe: %w", err)
			}

			return rollbackCause
		},
	)
	if !errors.Is(err, rollbackCause) {
		t.Fatalf("WithinTransaction() error = %v, want rollback cause", err)
	}
	connection, err := pgx.Connect(ctx, database.url)
	if err != nil {
		t.Fatalf("connect for rollback assertion: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close rollback assertion connection: %v", err)
		}
	}()
	var relation *string
	if err := connection.QueryRow(ctx, "SELECT to_regclass('idenqa.transaction_rollback_probe')::text").Scan(&relation); err != nil {
		t.Fatalf("query rollback probe: %v", err)
	}
	if relation != nil {
		t.Fatalf("rolled-back relation = %q, want nil", *relation)
	}

	migrator, err = idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("reopen migrator: %v", err)
	}
	if _, err := migrator.DownOne(ctx, idenqapostgres.RollbackGuard{
		Environment: "production",
		Confirmed:   true,
	}); !errors.Is(err, idenqapostgres.ErrRollbackForbidden) {
		t.Fatalf("production DownOne() error = %v", err)
	}
	report, err = migrator.DownOne(ctx, idenqapostgres.RollbackGuard{
		Environment: "test",
		Confirmed:   true,
	})
	if err != nil {
		t.Fatalf("test DownOne() error = %v", err)
	}
	if report.Current != migrations.LatestVersion-1 || !report.Pending || report.Dirty {
		t.Fatalf("rolled-back report = %+v", report)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close rollback migrator: %v", err)
	}
	if err := pool.Check(ctx, migrations.LatestVersion); !errors.Is(err, idenqapostgres.ErrSchemaIncompatible) {
		t.Fatalf("Check() after rollback error = %v, want ErrSchemaIncompatible", err)
	}
}

func TestPostgreSQLReadinessFailureAndRecovery(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	pool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		t.Fatalf("initial Check() error = %v", err)
	}

	database.setConnectionsAllowed(t, false)
	failureContext, cancelFailureCheck := context.WithTimeout(ctx, 2*time.Second)
	err = pool.Check(failureContext, migrations.LatestVersion)
	cancelFailureCheck()
	if !errors.Is(err, idenqapostgres.ErrUnavailable) {
		t.Fatalf("Check() while unavailable error = %v, want ErrUnavailable", err)
	}
	database.setConnectionsAllowed(t, true)

	deadline := time.Now().Add(5 * time.Second)
	for {
		err := pool.Check(ctx, migrations.LatestVersion)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Check() did not recover: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type isolatedDatabase struct {
	admin *pgx.Conn
	name  string
	url   string
	roles []string
}

func createIsolatedDatabase(t *testing.T) *isolatedDatabase {
	t.Helper()

	adminURL := os.Getenv("DATABASE_TEST_URL")
	if adminURL == "" {
		t.Skip("DATABASE_TEST_URL is not configured")
	}
	admin, err := pgx.Connect(t.Context(), adminURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL test administrator: %v", err)
	}
	name := "idenqa_test_" + randomSuffix(t)
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+identifier); err != nil {
		_ = admin.Close(context.Background())
		t.Fatalf("create isolated database: %v", err)
	}
	parsed, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	parsed.Path = "/" + name
	database := &isolatedDatabase{admin: admin, name: name, url: parsed.String()}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		database.setConnectionsAllowed(t, true)
		_, _ = admin.Exec(cleanupContext, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", name)
		if _, err := admin.Exec(cleanupContext, "DROP DATABASE IF EXISTS "+identifier); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		for _, role := range database.roles {
			if _, err := admin.Exec(cleanupContext, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("drop isolated database role: %v", err)
			}
		}
		if err := admin.Close(cleanupContext); err != nil {
			t.Errorf("close PostgreSQL test administrator: %v", err)
		}
	})

	return database
}

func (database *isolatedDatabase) createRuntimeRole(t *testing.T) string {
	t.Helper()

	role := "idenqa_runtime_" + randomSuffix(t)
	report, err := runtimepermissions.Apply(t.Context(), database.url, role, 30*time.Second)
	if err != nil {
		t.Fatalf("reconcile isolated runtime role: %v", err)
	}
	if report.Version != runtimepermissions.Version || report.Role != role {
		t.Fatalf("unexpected runtime permission report: %#v", report)
	}
	database.roles = append(database.roles, role)

	return role
}

func (database *isolatedDatabase) grantHeadgateRuntime(t *testing.T, role string) {
	t.Helper()
	connection, err := pgx.Connect(t.Context(), database.url)
	if err != nil {
		t.Fatalf("connect to isolated database for Headgate grants: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close isolated Headgate grant connection: %v", err)
		}
	}()
	identifier := pgx.Identifier{role}.Sanitize()
	statements := []string{
		"GRANT USAGE ON SCHEMA headgate TO " + identifier,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA headgate TO " + identifier,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA headgate TO " + identifier,
	}
	for _, statement := range statements {
		if _, err := connection.Exec(t.Context(), statement); err != nil {
			t.Fatalf("grant isolated Headgate runtime role: %v", err)
		}
	}
}

func (database *isolatedDatabase) setConnectionsAllowed(t *testing.T, allowed bool) {
	t.Helper()

	operationContext, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()

	identifier := pgx.Identifier{database.name}.Sanitize()
	if _, err := database.admin.Exec(operationContext, fmt.Sprintf("ALTER DATABASE %s WITH ALLOW_CONNECTIONS %t", identifier, allowed)); err != nil {
		t.Fatalf("set isolated database connection policy: %v", err)
	}
	if !allowed {
		if _, err := database.admin.Exec(
			operationContext,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			database.name,
		); err != nil {
			t.Fatalf("terminate isolated database connections: %v", err)
		}
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()

	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatalf("generate database suffix: %v", err)
	}

	return hex.EncodeToString(buffer)
}

func migrationConfig(databaseURL string) idenqapostgres.MigrationConfig {
	return idenqapostgres.MigrationConfig{
		URL:              databaseURL,
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 30 * time.Second,
	}
}

func poolConfig(databaseURL string) idenqapostgres.Config {
	return idenqapostgres.Config{
		URL:                 databaseURL,
		MaxConnections:      4,
		MinConnections:      0,
		MaxConnectionAge:    time.Minute,
		MaxConnectionIdle:   time.Minute,
		HealthCheckInterval: time.Second,
		ConnectTimeout:      5 * time.Second,
	}
}
