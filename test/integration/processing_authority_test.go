//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestProcessingAuthorityInvalidatesStaleSerializableSnapshot(t *testing.T) {
	for _, scenario := range []string{"withdrawal", "refusal"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				store, mutations := f.prepare(t)
				for _, mutation := range mutations {
					if _, err := store.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
						t.Fatal(err)
					}
				}
				validate := func(ctx context.Context, tx pg.Transaction) error {
					return authoritypostgres.ValidateProcessingWithin(ctx, tx, f.scope, f.creation.Session.ID(),
						f.now, fixedIntegrationClock{now: f.now}, verification.SessionStateCollecting)
				}
				if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, validate); err != nil {
					t.Fatalf("captured session is not eligible: %v", err)
				}
				err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable},
					func(ctx context.Context, tx pg.Transaction) error {
						// Establish an older snapshot without locking the session. A
						// parent lock alone would permit stale authority reads here.
						var snapshot string
						if err := tx.QueryRow(ctx, `SELECT pg_current_snapshot()::text`).Scan(&snapshot); err != nil {
							return err
						}
						revokeProcessingAuthority(t, f, scenario)
						return validate(ctx, tx)
					})
				var serialization *pgconn.PgError
				if !errors.As(err, &serialization) || serialization.Code != "40001" {
					t.Fatalf("stale processing snapshot was not forced to retry: %v", err)
				}
				if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, validate); !errors.Is(err, authority.ErrProcessingNotPermitted) {
					t.Fatalf("fresh processing after %s = %v", scenario, err)
				}
			})
		})
	}
}

func revokeProcessingAuthority(t *testing.T, f captureAcceptanceFixture, scenario string) {
	t.Helper()
	if scenario == "withdrawal" {
		declaration := f.declaration
		if err := declaration.Withdraw(f.now); err != nil {
			t.Fatal(err)
		}
		request := integrationIdempotencyRequest(t, f.scope.ID(), declaration.Record().CreatedBy,
			"authorities.withdraw", "processing-withdraw", []byte(`{}`), f.now)
		if _, err := f.authorities.Transition(t.Context(), f.scope, authority.TransitionMutation{
			Authority: declaration, ExpectedVersion: f.declaration.Record().Version, Action: authority.StateWithdrawn,
			Actor: declaration.Record().CreatedBy, EventID: mustCaptureEvent(t, f.ids), Idempotency: request,
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	responseID, err := f.ids.NewAcknowledgement()
	if err != nil {
		t.Fatal(err)
	}
	response, err := authority.NewResponse(authority.ResponseRecord{
		ID: responseID, TenantID: f.scope.ID(), AuthorityID: f.declaration.ID(), NoticeID: f.declaration.Record().NoticeID,
		SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), CaptureTokenID: f.creation.Credential.ID(),
		Action: authority.ResponseRefuse, Locale: "en-NG", RecordedAt: f.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), "authorities.respond", "processing-refuse", []byte(`{}`), f.now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{
		Response: response, EventID: mustCaptureEvent(t, f.ids), Idempotency: request,
	}); err != nil {
		t.Fatal(err)
	}
}
