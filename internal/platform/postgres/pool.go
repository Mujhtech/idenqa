// Package postgres owns PostgreSQL pools, readiness checks, and transaction
// primitives shared by PostgreSQL adapters.
package postgres

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var rolePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

var (
	// ErrInvalidConfiguration means the PostgreSQL connection settings cannot
	// be parsed safely.
	ErrInvalidConfiguration = errors.New("invalid PostgreSQL configuration")
	// ErrUnavailable means PostgreSQL could not be reached within the caller's
	// deadline.
	ErrUnavailable = errors.New("PostgreSQL is unavailable")
	// ErrSchemaMissing means embedded migrations have not initialized the
	// database.
	ErrSchemaMissing = errors.New("PostgreSQL schema is not initialized")
	// ErrSchemaDirty means a migration stopped before recording a clean state.
	ErrSchemaDirty = errors.New("PostgreSQL schema migration is dirty")
	// ErrSchemaIncompatible means the database and binary schema versions differ.
	ErrSchemaIncompatible = errors.New("PostgreSQL schema version is incompatible")
)

// Config contains process-owned PostgreSQL pool settings.
type Config struct {
	URL                 string
	Role                string
	MaxConnections      int32
	MinConnections      int32
	MaxConnectionAge    time.Duration
	MaxConnectionIdle   time.Duration
	HealthCheckInterval time.Duration
	ConnectTimeout      time.Duration
}

// Pool is the owned PostgreSQL connection pool boundary.
type Pool struct {
	pool *pgxpool.Pool
}

// Native returns the process-owned pgx pool for provider adapters that require
// pgx-native capabilities such as transactional queue integration. It must not
// cross into domain or application packages.
func (pool *Pool) Native() *pgxpool.Pool {
	if pool == nil {
		return nil
	}
	return pool.pool
}

// NotificationListener owns one acquired PostgreSQL connection dedicated to
// LISTEN/NOTIFY. Notifications are wake-ups only and never authoritative data.
type NotificationListener struct{ connection *pgxpool.Conn }

// OpenNotificationListener acquires one connection and subscribes to a safely
// quoted channel selected by application composition.
func (pool *Pool) OpenNotificationListener(
	ctx context.Context,
	channel string,
) (*NotificationListener, error) {
	if pool == nil || pool.pool == nil || !rolePattern.MatchString(channel) {
		return nil, ErrInvalidConfiguration
	}
	connection, err := pool.pool.Acquire(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	if _, err := connection.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		connection.Release()
		return nil, ErrUnavailable
	}
	return &NotificationListener{connection: connection}, nil
}

// Wait blocks until PostgreSQL delivers the next notification payload.
func (listener *NotificationListener) Wait(ctx context.Context) (string, error) {
	if listener == nil || listener.connection == nil {
		return "", ErrUnavailable
	}
	notification, err := listener.connection.Conn().WaitForNotification(ctx)
	if err != nil {
		return "", err
	}
	return notification.Payload, nil
}

// Close releases the dedicated connection.
func (listener *NotificationListener) Close() {
	if listener != nil && listener.connection != nil {
		listener.connection.Release()
		listener.connection = nil
	}
}

// Open validates, creates, and connects a PostgreSQL pool.
func Open(ctx context.Context, configuration Config) (*Pool, error) {
	if err := configuration.validate(); err != nil {
		return nil, err
	}

	poolConfig, err := pgxpool.ParseConfig(configuration.URL)
	if err != nil {
		return nil, ErrInvalidConfiguration
	}
	poolConfig.MaxConns = configuration.MaxConnections
	poolConfig.MinConns = configuration.MinConnections
	poolConfig.MaxConnLifetime = configuration.MaxConnectionAge
	poolConfig.MaxConnIdleTime = configuration.MaxConnectionIdle
	poolConfig.HealthCheckPeriod = configuration.HealthCheckInterval
	if configuration.Role != "" {
		role := pgx.Identifier{configuration.Role}.Sanitize()
		poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
			// Security: Role is allow-listed and quoted before it enters this
			// configuration-only statement; request data never reaches it.
			_, err := connection.Exec(ctx, "SET ROLE "+role)

			return err
		}
	}

	connectContext, cancel := context.WithTimeout(ctx, configuration.ConnectTimeout)
	defer cancel()

	connectionPool, err := pgxpool.NewWithConfig(connectContext, poolConfig)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := connectionPool.Ping(connectContext); err != nil {
		connectionPool.Close()

		return nil, ErrUnavailable
	}

	return &Pool{pool: connectionPool}, nil
}

func (configuration Config) validate() error {
	if configuration.URL == "" || (configuration.Role != "" && !rolePattern.MatchString(configuration.Role)) ||
		configuration.MaxConnections <= 0 || configuration.MinConnections < 0 ||
		configuration.MinConnections > configuration.MaxConnections || configuration.MaxConnectionAge <= 0 ||
		configuration.MaxConnectionIdle <= 0 || configuration.HealthCheckInterval <= 0 ||
		configuration.ConnectTimeout <= 0 {
		return ErrInvalidConfiguration
	}

	return nil
}

// Ping verifies that PostgreSQL accepts a round trip.
func (pool *Pool) Ping(ctx context.Context) error {
	if _, err := sqlgen.New(pool.pool).CheckConnection(ctx); err != nil {
		return ErrUnavailable
	}

	return nil
}

// Check verifies connectivity and exact migration compatibility without
// mutating the database.
func (pool *Pool) Check(ctx context.Context, latest uint) error {
	var version int64
	var dirty bool
	err := pool.pool.QueryRow(
		ctx,
		"SELECT version, dirty FROM public.schema_migrations LIMIT 1",
	).Scan(&version, &dirty)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSchemaMissing
	}
	if err != nil {
		return classifySchemaCheckError(err)
	}
	if dirty {
		return ErrSchemaDirty
	}
	if version < 0 || uint(version) != latest {
		return ErrSchemaIncompatible
	}

	return nil
}

func classifySchemaCheckError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.SQLState() == "42P01" {
		return ErrSchemaMissing
	}

	return ErrUnavailable
}

// Close releases all PostgreSQL connections and blocks until they return.
func (pool *Pool) Close() {
	pool.pool.Close()
}
