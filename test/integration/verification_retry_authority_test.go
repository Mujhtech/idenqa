//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestProviderRetryAuthorizesResultTimeNotFutureSchedule(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		fixture := seedProviderRecoveryFixture(t, f)
		now := f.now.Add(time.Minute)
		store, err := verificationpostgres.NewGuardedCheckStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: now})
		if err != nil {
			t.Fatal(err)
		}
		check, err := store.FindCheck(t.Context(), f.scope, fixture.checkID)
		if err != nil {
			t.Fatal(err)
		}
		expected := check.Version
		attempt := check.Attempts()[0]
		result := providerv1.Result{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String(), Outcome: providerv1.ResultOutcomeFailed, CompletedAt: now,
			Failure: &providerv1.Failure{Class: providerv1.FailureUnavailable, Code: "provider_unavailable", Retry: providerv1.RetryBackoff, RetryAfter: time.Second}}
		if _, err := verification.ApplyProviderResult(&check, result, f.ids, attempt.Fence); err != nil {
			t.Fatal(err)
		}
		next := attempt
		next.ID, err = f.ids.NewAttempt()
		if err != nil {
			t.Fatal(err)
		}
		next.Number++
		next.Fence++
		next.StartedAt = now.Add(time.Second)
		if err := check.BeginAttempt(next); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := verification.ProviderResultFingerprint(result)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := verification.NewResultReceipt(attempt.ID, fingerprint, now)
		if err != nil {
			t.Fatal(err)
		}
		commit := verification.CheckCommit{Check: check, ExpectedVersion: expected, EventID: mustCaptureEvent(t, f.ids), Receipt: &receipt}
		future := receipt
		future.ReceivedAt = now.Add(time.Second)
		invalid := commit
		invalid.Receipt = &future
		if duplicate, err := store.SaveCheck(t.Context(), f.scope, invalid); duplicate || !errors.Is(err, authority.ErrProcessingNotPermitted) {
			t.Fatalf("future result must remain forbidden: duplicate=%v error=%v", duplicate, err)
		}
		if duplicate, err := store.SaveCheck(t.Context(), f.scope, commit); err != nil || duplicate {
			t.Fatalf("schedule future retry under current authority: duplicate=%v error=%v", duplicate, err)
		}
		if duplicate, err := store.SaveCheck(t.Context(), f.scope, commit); err != nil || !duplicate {
			t.Fatalf("retry receipt replay: duplicate=%v error=%v", duplicate, err)
		}
		persisted, err := store.FindCheck(t.Context(), f.scope, fixture.checkID)
		if err != nil || len(persisted.Attempts()) != 2 || !persisted.Attempts()[1].StartedAt.Equal(next.StartedAt) {
			t.Fatalf("future retry was not preserved: %v", err)
		}
	})
}
