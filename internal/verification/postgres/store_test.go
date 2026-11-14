package postgres

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

func TestFindRevisionChecksStorageBoundary(t *testing.T) {
	t.Parallel()
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	profileID, err := id.ParseProfile("prf_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []uint32{0, 1, math.MaxInt32, math.MaxInt32 + 1, math.MaxUint32} {
		t.Run(strconv.FormatUint(uint64(revision), 10), func(t *testing.T) {
			runner := &revisionRunner{}
			store := &Store{pool: runner}
			_, err := store.FindRevision(t.Context(), scope, profileID, revision)
			if !errors.Is(err, verification.ErrProfileNotFound) {
				t.Fatalf("error=%v", err)
			}
			if revision == 0 || revision > math.MaxInt32 {
				if runner.calls != 0 {
					t.Fatal("invalid revision reached PostgreSQL")
				}
				return
			}
			if runner.calls != 1 || int64(runner.revision) != int64(revision) {
				t.Fatalf("bound revision=%d transactions=%d", runner.revision, runner.calls)
			}
		})
	}
}

type revisionRunner struct {
	platformpostgres.Transaction
	calls    int
	revision int32
}

func (runner *revisionRunner) WithinTransaction(ctx context.Context, _ platformpostgres.TransactionOptions, work func(context.Context, platformpostgres.Transaction) error) error {
	runner.calls++
	return work(ctx, runner)
}

func (runner *revisionRunner) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	if strings.Contains(query, "set_config") {
		return revisionRow{scope: true}
	}
	runner.revision = args[2].(int32)
	return revisionRow{}
}

type revisionRow struct{ scope bool }

func (row revisionRow) Scan(values ...any) error {
	if !row.scope {
		return pgx.ErrNoRows
	}
	*values[0].(*string) = "tenant scope"
	return nil
}
