package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCommitErrorClassifiesOnlyUnconfirmedOutcomesAsUnknown(t *testing.T) {
	t.Parallel()

	rolledBack := commitError(pgx.ErrTxCommitRollback)
	if errors.Is(rolledBack, ErrCommitOutcomeUnknown) {
		t.Fatalf("commitError(rollback) = %v, unexpectedly unknown", rolledBack)
	}
	transportFailure := errors.New("connection lost after commit write")
	unknown := commitError(transportFailure)
	if !errors.Is(unknown, ErrCommitOutcomeUnknown) || !errors.Is(unknown, transportFailure) {
		t.Fatalf("commitError(transport) = %v, want both classifications", unknown)
	}
}

func TestTransactionOptionsPGX(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		options    TransactionOptions
		wantLevel  pgx.TxIsoLevel
		wantAccess pgx.TxAccessMode
		wantError  bool
	}{
		{name: "read committed", wantLevel: pgx.ReadCommitted, wantAccess: pgx.ReadWrite},
		{name: "repeatable read", options: TransactionOptions{Isolation: IsolationRepeatableRead}, wantLevel: pgx.RepeatableRead, wantAccess: pgx.ReadWrite},
		{name: "serializable read only", options: TransactionOptions{Isolation: IsolationSerializable, ReadOnly: true}, wantLevel: pgx.Serializable, wantAccess: pgx.ReadOnly},
		{name: "unsupported", options: TransactionOptions{Isolation: Isolation(99)}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.options.pgx()
			if test.wantError {
				if err == nil {
					t.Fatal("pgx() error = nil")
				}

				return
			}
			if err != nil {
				t.Fatalf("pgx() error = %v", err)
			}
			if got.IsoLevel != test.wantLevel || got.AccessMode != test.wantAccess {
				t.Fatalf("pgx() = %+v, want level %q access %q", got, test.wantLevel, test.wantAccess)
			}
		})
	}
}
