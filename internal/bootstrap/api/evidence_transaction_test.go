package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

type evidenceTransactionTestDatabase struct {
	calls  int
	commit func(int) error
}

func (database *evidenceTransactionTestDatabase) WithinTransaction(ctx context.Context, options pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	database.calls++
	if options.Isolation != pg.IsolationSerializable {
		return errors.New("evidence transaction lost serializable isolation")
	}
	if err := work(ctx, nil); err != nil {
		return err
	}
	if database.commit != nil {
		return database.commit(database.calls)
	}
	return nil
}

func TestBufferedEvidenceTransactionRechecksAuthorityAndWipesRolledBackPlaintext(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"session lock", "nested grant repository", "commit"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			conflict := &pgconn.PgError{Code: "40001"}
			database := &evidenceTransactionTestDatabase{}
			if stage == "commit" {
				database.commit = func(call int) error {
					if call == 1 {
						return conflict
					}
					return nil
				}
			}
			var plaintext, discarded []byte
			checks := 0
			discard := func() {
				discarded = plaintext
				clear(plaintext)
				plaintext = nil
			}
			err := withBufferedEvidenceTransaction(t.Context(), database, discard, func(ctx context.Context, tx *evidenceTransaction) error {
				checks++
				if checks == 2 && (!bytes.Equal(discarded, make([]byte, len(discarded))) || plaintext != nil) {
					t.Fatal("retry retained plaintext from the aborted transaction")
				}
				plaintext = []byte("synthetic controlled plaintext")
				if checks == 1 {
					switch stage {
					case "session lock":
						return conflict
					case "nested grant repository":
						// Reader deliberately hides repository details as a denied
						// controlled read. The owning transaction must retain the
						// original database abort and repeat every authority check.
						_ = tx.WithinTransaction(ctx, pg.TransactionOptions{}, func(context.Context, pg.Transaction) error {
							return conflict
						})
					}
				}
				return nil
			})
			defer clear(plaintext)
			if err != nil || checks != 2 || database.calls != 2 || len(plaintext) == 0 {
				t.Fatalf("checks=%d transactions=%d retained=%d error=%v", checks, database.calls, len(plaintext), err)
			}
		})
	}
}

func TestBufferedEvidenceTransactionNeverReplaysUnknownCommitOrAuthorityDenial(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		err       error
		wantCalls int
	}{
		{name: "authority denied", err: evidence.ErrReadDenied, wantCalls: 1},
		{name: "unknown commit", err: pg.ErrCommitOutcomeUnknown, wantCalls: 1},
		{name: "conflict budget exhausted", err: &pgconn.PgError{Code: "40001"}, wantCalls: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := &evidenceTransactionTestDatabase{commit: func(int) error { return test.err }}
			var plaintext []byte
			var allocated [][]byte
			err := withBufferedEvidenceTransaction(t.Context(), database, func() {
				clear(plaintext)
				plaintext = nil
			}, func(context.Context, *evidenceTransaction) error {
				plaintext = []byte("synthetic controlled plaintext")
				allocated = append(allocated, plaintext)
				return nil
			})
			if !errors.Is(err, test.err) || database.calls != test.wantCalls || plaintext != nil {
				t.Fatalf("transactions=%d retained=%d error=%v", database.calls, len(plaintext), err)
			}
			for _, buffer := range allocated {
				if !bytes.Equal(buffer, make([]byte, len(buffer))) {
					t.Fatal("failed evidence transaction retained plaintext")
				}
			}
		})
	}
}

func TestEvidenceUnavailableDistinguishesExhaustedConflictsFromAuthorityDenial(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCalls  int
	}{
		{name: "serialization conflict", err: fmt.Errorf("read authority: %w", &pgconn.PgError{Code: "40001"}), wantStatus: http.StatusServiceUnavailable, wantCalls: 3},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, wantStatus: http.StatusServiceUnavailable, wantCalls: 3},
		{name: "authority denied", err: evidence.ErrReadDenied, wantStatus: http.StatusForbidden, wantCalls: 1},
		{name: "unknown commit", err: errors.Join(pg.ErrCommitOutcomeUnknown, &pgconn.PgError{Code: "40001"}), wantStatus: http.StatusForbidden, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := &evidenceTransactionTestDatabase{commit: func(int) error { return test.err }}
			var plaintext []byte
			err := withBufferedEvidenceTransaction(t.Context(), database, func() {
				clear(plaintext)
				plaintext = nil
			}, func(context.Context, *evidenceTransaction) error {
				plaintext = []byte("synthetic controlled plaintext")
				return nil
			})
			response := httptest.NewRecorder()
			writeEvidenceUnavailable(response, err)
			if response.Code != test.wantStatus || database.calls != test.wantCalls || plaintext != nil {
				t.Fatalf("status=%d transactions=%d retained=%d error=%v", response.Code, database.calls, len(plaintext), err)
			}
			if response.Body.String() != "evidence unavailable\n" {
				t.Fatalf("response disclosed transaction details: %q", response.Body.String())
			}
		})
	}
}
