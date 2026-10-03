package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewbrowser"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestStoreConsumePersistsRegionInBootstrapAndSession(t *testing.T) {
	t.Parallel()

	tenantID, err := id.ParseTenant("ten_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	caseID, err := id.ParseReviewCase("rvc_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseReviewCase() error = %v", err)
	}
	tx := &recordingTransaction{}
	store, err := New(transactionFixture{tx: tx})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	session := reviewbrowser.Session{
		BootstrapID: "rbs_1", Scope: scope, Actor: review.Actor{ID: "usr_reviewer"}, CaseID: caseID,
		Version: 4, Region: "eu-west-1", Origin: "https://console.example", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err := store.Consume(t.Context(), session, [32]byte{1}, [32]byte{2}, [32]byte{3}); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if len(tx.calls) != 2 {
		t.Fatalf("Exec calls = %d, want 2", len(tx.calls))
	}
	for index, call := range tx.calls {
		if !strings.Contains(call.query, "region") {
			t.Fatalf("Exec call %d does not persist region: %s", index, call.query)
		}
		if !containsArgument(call.arguments, session.Region) {
			t.Fatalf("Exec call %d arguments do not contain region", index)
		}
	}
}

func TestStoreAuthenticateDistinguishesExpiredSession(t *testing.T) {
	t.Parallel()

	tenantID, err := id.ParseTenant("ten_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	store, err := New(transactionFixture{tx: &authenticationTransaction{expiresAt: now.Add(-time.Second)}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := store.Authenticate(t.Context(), scope, [32]byte{1}, "https://console.example", now); !errors.Is(err, reviewbrowser.ErrExpired) {
		t.Fatalf("Authenticate() error = %v, want ErrExpired", err)
	}
}

type transactionFixture struct{ tx platformpostgres.Transaction }

func (fixture transactionFixture) WithinTransaction(ctx context.Context, _ platformpostgres.TransactionOptions, work func(context.Context, platformpostgres.Transaction) error) error {
	return work(ctx, fixture.tx)
}

type execCall struct {
	query     string
	arguments []any
}

type recordingTransaction struct{ calls []execCall }

func (tx *recordingTransaction) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	tx.calls = append(tx.calls, execCall{query: query, arguments: arguments})
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (*recordingTransaction) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (*recordingTransaction) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return scopeRow{}
}

type scopeRow struct{}

func (scopeRow) Scan(destinations ...any) error {
	*(destinations[0].(*string)) = "ten_01M3NRK3Z6BA1MMMR66QMM1NRN"
	return nil
}

type authenticationTransaction struct {
	queryCount int
	expiresAt  time.Time
}

func (*authenticationTransaction) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (*authenticationTransaction) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (tx *authenticationTransaction) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	tx.queryCount++
	if tx.queryCount == 1 {
		return scopeRow{}
	}
	return authenticationRow{expiresAt: tx.expiresAt}
}

type authenticationRow struct{ expiresAt time.Time }

func (row authenticationRow) Scan(destinations ...any) error {
	*(destinations[0].(*string)) = "rbs_1"
	*(destinations[1].(*string)) = "usr_reviewer"
	*(destinations[2].(*string)) = "rvc_01M3NRK3Z6BA1MMMR66QMM1NRN"
	*(destinations[3].(*int64)) = 4
	*(destinations[4].(*string)) = "eu-west-1"
	*(destinations[5].(*string)) = "https://console.example"
	*(destinations[6].(*time.Time)) = row.expiresAt.Add(-time.Minute)
	*(destinations[7].(*time.Time)) = row.expiresAt
	return nil
}

func containsArgument(arguments []any, expected string) bool {
	for _, argument := range arguments {
		if value, ok := argument.(string); ok && value == expected {
			return true
		}
	}
	return false
}
