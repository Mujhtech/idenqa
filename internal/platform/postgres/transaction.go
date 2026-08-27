package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const rollbackTimeout = 5 * time.Second

// ErrCommitOutcomeUnknown means PostgreSQL did not confirm whether COMMIT took
// effect. Callers must reconcile authoritative state before compensating an
// external side effect that the transaction may reference.
var ErrCommitOutcomeUnknown = errors.New("postgres: commit outcome unknown")

// Isolation is an owned transaction-isolation choice.
type Isolation uint8

const (
	// IsolationReadCommitted permits a fresh committed snapshot per statement.
	IsolationReadCommitted Isolation = iota
	// IsolationRepeatableRead keeps a stable snapshot for the transaction.
	IsolationRepeatableRead
	// IsolationSerializable rejects transactions that cannot be serialized.
	IsolationSerializable
)

// Transaction is the narrow database surface passed to PostgreSQL adapters.
// Domain and application packages must define and consume their own ports.
type Transaction interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// TransactionOptions declares behaviour that must be chosen per operation.
type TransactionOptions struct {
	Isolation Isolation
	ReadOnly  bool
}

// WithinTransaction executes work atomically. It does not retry: callers must
// make retry policy explicit for the operation being implemented.
func (pool *Pool) WithinTransaction(
	ctx context.Context,
	options TransactionOptions,
	work func(context.Context, Transaction) error,
) (result error) {
	if work == nil {
		return errors.New("transaction work is required")
	}
	pgxOptions, err := options.pgx()
	if err != nil {
		return err
	}

	transaction, err := pool.pool.BeginTx(ctx, pgxOptions)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		if rollbackErr := transaction.Rollback(rollbackContext); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("roll back PostgreSQL transaction: %w", rollbackErr))
		}
	}()

	if err := work(ctx, transaction); err != nil {
		return fmt.Errorf("execute PostgreSQL transaction: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return commitError(err)
	}
	committed = true

	return nil
}

func commitError(err error) error {
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return fmt.Errorf("commit PostgreSQL transaction: %w", err)
	}

	return fmt.Errorf(
		"commit PostgreSQL transaction: %w",
		errors.Join(ErrCommitOutcomeUnknown, err),
	)
}

func (options TransactionOptions) pgx() (pgx.TxOptions, error) {
	isolation := pgx.ReadCommitted
	switch options.Isolation {
	case IsolationReadCommitted:
	case IsolationRepeatableRead:
		isolation = pgx.RepeatableRead
	case IsolationSerializable:
		isolation = pgx.Serializable
	default:
		return pgx.TxOptions{}, errors.New("unsupported PostgreSQL transaction isolation")
	}

	accessMode := pgx.ReadWrite
	if options.ReadOnly {
		accessMode = pgx.ReadOnly
	}

	return pgx.TxOptions{IsoLevel: isolation, AccessMode: accessMode}, nil
}
