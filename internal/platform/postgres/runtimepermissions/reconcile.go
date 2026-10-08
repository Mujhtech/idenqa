package runtimepermissions

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidConfiguration = errors.New("invalid runtime permission configuration")
	ErrSchemaIncompatible   = errors.New("runtime permissions require the exact Core schema version")
	ErrUnsafeRole           = errors.New("runtime role has unsafe PostgreSQL attributes")
	rolePattern             = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

type Report struct {
	Version string
	Role    string
}

func Apply(ctx context.Context, databaseURL, role string, timeout time.Duration) (Report, error) {
	if databaseURL == "" || !rolePattern.MatchString(role) || timeout <= 0 {
		return Report{}, ErrInvalidConfiguration
	}
	operationContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := pgx.Connect(operationContext, databaseURL)
	if err != nil {
		return Report{}, fmt.Errorf("connect runtime permission administrator: %w", err)
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = connection.Close(cleanupContext)
	}()

	transaction, err := connection.BeginTx(operationContext, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Report{}, fmt.Errorf("begin runtime permission reconciliation: %w", err)
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = transaction.Rollback(cleanupContext)
	}()

	var schemaVersion int64
	var dirty bool
	if err := transaction.QueryRow(operationContext, "SELECT version, dirty FROM public.schema_migrations LIMIT 1").Scan(&schemaVersion, &dirty); err != nil || dirty || schemaVersion != int64(migrations.LatestVersion) {
		return Report{}, ErrSchemaIncompatible
	}
	if _, err := transaction.Exec(operationContext, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", Version+"\n"+role); err != nil {
		return Report{}, fmt.Errorf("lock runtime permission reconciliation: %w", err)
	}

	var canLogin, superuser, createRole, createDatabase, inherit, replication, bypassRLS bool
	err = transaction.QueryRow(operationContext, `
		SELECT rolcanlogin, rolsuper, rolcreaterole, rolcreatedb, rolinherit, rolreplication, rolbypassrls
		FROM pg_catalog.pg_roles WHERE rolname = $1`, role,
	).Scan(&canLogin, &superuser, &createRole, &createDatabase, &inherit, &replication, &bypassRLS)
	if errors.Is(err, pgx.ErrNoRows) {
		statement := "CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS"
		if _, err := transaction.Exec(operationContext, statement); err != nil {
			return Report{}, fmt.Errorf("create runtime role: %w", err)
		}
	} else if err != nil {
		return Report{}, fmt.Errorf("inspect runtime role: %w", err)
	} else if canLogin || superuser || createRole || createDatabase || inherit || replication || bypassRLS {
		return Report{}, ErrUnsafeRole
	}

	for _, statement := range Statements(role) {
		if _, err := transaction.Exec(operationContext, statement); err != nil {
			return Report{}, fmt.Errorf("apply runtime permission manifest: %w", err)
		}
	}
	if err := transaction.Commit(operationContext); err != nil {
		return Report{}, fmt.Errorf("commit runtime permission reconciliation: %w", err)
	}
	return Report{Version: Version, Role: role}, nil
}
