package postgres

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/Mujhtech/idenqa/internal/audit"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func TestListPreservesSignedCursorBoundary(t *testing.T) {
	t.Parallel()
	identifier, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(identifier)
	if err != nil {
		t.Fatal(err)
	}
	for _, before := range []uint64{0, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
		t.Run(strconv.FormatUint(before, 10), func(t *testing.T) {
			runner := &cursorRunner{t: t}
			store := &Store{pool: runner}
			_, err := store.List(t.Context(), scope, before, 100)
			if before > math.MaxInt64 {
				if !errors.Is(err, audit.ErrInvalid) || runner.calls != 0 {
					t.Fatalf("invalid cursor: error=%v transactions=%d", err, runner.calls)
				}
				return
			}
			if !errors.Is(err, errCursorQuery) || runner.calls != 1 || runner.before != int64(before) {
				t.Fatalf("cursor=%d error=%v transactions=%d", runner.before, err, runner.calls)
			}
		})
	}
}

var errCursorQuery = errors.New("query observed")

type cursorRunner struct {
	platformpostgres.Transaction
	t      *testing.T
	calls  int
	before int64
}

func (runner *cursorRunner) WithinTransaction(ctx context.Context, options platformpostgres.TransactionOptions, work func(context.Context, platformpostgres.Transaction) error) error {
	runner.calls++
	if !options.ReadOnly {
		runner.t.Fatal("cursor query must remain read only")
	}
	return work(ctx, runner)
}

func (runner *cursorRunner) QueryRow(context.Context, string, ...any) pgx.Row {
	return cursorScopeRow{}
}

func (runner *cursorRunner) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	runner.before = args[1].(int64)
	return nil, errCursorQuery
}

type cursorScopeRow struct{}

func (cursorScopeRow) Scan(values ...any) error {
	*values[0].(*string) = "tenant scope"
	return nil
}
