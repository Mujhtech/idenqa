package api

import (
	"context"
	"errors"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

type evidenceTransactionDatabase interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// evidenceTransaction also binds the evidence repositories to the owning
// transaction. Retain conflicts before an application boundary maps them to a
// controlled-read denial; a serialization abort is not an authority refusal.
type evidenceTransaction struct {
	pg.Transaction
	conflict error
}

func (transaction *evidenceTransaction) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	err := work(ctx, transaction.Transaction)
	if evidenceTransactionConflict(err) {
		transaction.conflict = err
	}
	return err
}

// withBufferedEvidenceTransaction retries only definitely aborted database
// units. Every attempt rechecks authority and grants, and discarded plaintext
// is wiped before retry or failure. The caller releases its buffer only after
// this function confirms the owning commit; an unknown commit is never retried.
func withBufferedEvidenceTransaction(ctx context.Context, database evidenceTransactionDatabase, discard func(), work func(context.Context, *evidenceTransaction) error) error {
	var err error
	for range 3 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = database.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			bound := &evidenceTransaction{Transaction: tx}
			workErr := work(ctx, bound)
			if bound.conflict != nil {
				return bound.conflict
			}
			return workErr
		})
		if err == nil {
			return nil
		}
		discard()
		if !evidenceTransactionConflict(err) {
			return err
		}
	}
	return err
}

func evidenceTransactionConflict(err error) bool {
	var conflict *pgconn.PgError
	return !errors.Is(err, pg.ErrCommitOutcomeUnknown) && errors.As(err, &conflict) &&
		(conflict.Code == "40001" || conflict.Code == "40P01")
}
