//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestCaptureRecoveryRetainsProgressWithFreshAuthorization(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		uploads, mutations := f.prepare(t)
		first, err := uploads.AcceptUpload(t.Context(), f.scope, mutations[0])
		if err != nil {
			t.Fatal(err)
		}
		oldRecord := first.Record()
		originalSnapshot, err := f.authorities.CaptureSnapshot(t.Context(), f.scope, f.creation.Session.ID())
		if err != nil {
			t.Fatal(err)
		}
		sessions, err := verificationpostgres.NewSessionStore(f.runtime, f.catalog)
		if err != nil {
			t.Fatal(err)
		}
		at := f.creation.Credential.ExpiresAt().Add(time.Second)
		next, err := f.ids.NewCaptureToken()
		if err != nil {
			t.Fatal(err)
		}
		actor, err := f.ids.NewAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		input := verification.CaptureRenewal{ExpectedToken: f.creation.Credential.ID(), Token: next, Actor: actor, KeyVersion: f.creation.Credential.KeyVersion(), At: at, ExpiresAt: at.Add(10 * time.Minute)}
		var renewed verification.SessionCreation
		rollback := errors.New("injected recovery rollback")
		renew := func(fail bool) error {
			return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
				var err error
				renewed, err = sessions.RenewCaptureWithin(ctx, f.scope, tx, f.creation.Session.ID(), input, fixedIntegrationClock{now: at})
				if err != nil {
					return err
				}
				if fail {
					return rollback
				}
				return nil
			})
		}
		if err := renew(true); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		var count int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.capture_recoveries WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&count); err != nil || count != 0 {
			t.Fatal("recovery escaped rollback", err)
		}
		if err := renew(false); err != nil {
			t.Fatal(err)
		}
		var retained, abandoned int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE disposition='retained'),count(*) FILTER(WHERE disposition='abandoned') FROM idenqa.capture_recovery_uploads WHERE tenant_id=$1 AND new_token_id=$2`, f.scope.ID().String(), next.String()).Scan(&retained, &abandoned); err != nil || retained != 1 || abandoned != 1 {
			t.Fatal("incorrect recovered work", retained, abandoned, err)
		}
		reader, err := evidence.NewProgressReader(uploads)
		if err != nil {
			t.Fatal(err)
		}
		progress, err := reader.Find(t.Context(), evidence.UploadPrincipal{Scope: f.scope, CaptureTokenID: next, VerificationID: f.creation.Session.ID()})
		if err != nil || len(progress.Completions) != 1 || progress.Completions[0].UploadID != oldRecord.ID {
			t.Fatal("accepted progress lost", err)
		}
		current, err := uploads.FindUpload(t.Context(), f.scope, oldRecord.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Record().CaptureTokenID != oldRecord.CaptureTokenID || current.Record().ResponseID != oldRecord.ResponseID {
			t.Fatal("original provenance changed")
		}
		currentUploads, err := evidencepostgres.NewWithClock(f.runtime, f.catalog, fixedIntegrationClock{now: at})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := currentUploads.AcceptUpload(t.Context(), f.scope, mutations[1]); err == nil {
			t.Fatal("old pending upload committed")
		}
		// A replacement response is appended; the old response is never rewritten.
		authorities, err := authoritypostgres.NewWithClock(f.runtime, fixedIntegrationClock{now: at})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := authorities.CaptureSnapshot(t.Context(), f.scope, f.creation.Session.ID())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Response != nil {
			t.Fatal("recovery reused old authorization")
		}
		response := originalSnapshot.Response.Record()
		response.ID, err = f.ids.NewAcknowledgement()
		if err != nil {
			t.Fatal(err)
		}
		response.CaptureTokenID = next
		response.RecordedAt = at
		fresh, err := authority.NewResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := idempotency.NewRequest(f.scope.ID(), next, "authorities.respond", "recovery-response", []byte(`{"action":"consent"}`), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{Response: fresh, EventID: mustCaptureEvent(t, f.ids), Idempotency: retry}); err != nil {
			t.Fatal(err)
		}
		resumed := f
		resumed.creation = renewed
		resumed.now = at
		resumed.authorities = authorities
		newUploads, newMutations := resumed.prepare(t)
		if _, err := newUploads.AcceptUpload(t.Context(), f.scope, newMutations[1]); err != nil {
			t.Fatal("remaining artefact failed", err)
		}
		progress, err = reader.Find(t.Context(), evidence.UploadPrincipal{Scope: f.scope, CaptureTokenID: next, VerificationID: f.creation.Session.ID()})
		if err != nil || len(progress.Completions) != 2 {
			t.Fatal("combined recovery progress incomplete", err)
		}
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
			return authoritypostgres.ValidateProcessingWithin(ctx, tx, f.scope, f.creation.Session.ID(), at, fixedIntegrationClock{now: at}, verification.SessionStateCollecting)
		}); err != nil {
			t.Fatal("retained evidence could not be processed", err)
		}
		secondAt := renewed.Credential.ExpiresAt().Add(time.Second)
		third, err := f.ids.NewCaptureToken()
		if err != nil {
			t.Fatal(err)
		}
		input.ExpectedToken, input.Token, input.At, input.ExpiresAt = next, third, secondAt, secondAt.Add(10*time.Minute)
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			_, err := sessions.RenewCaptureWithin(ctx, f.scope, tx, f.creation.Session.ID(), input, fixedIntegrationClock{now: secondAt})
			return err
		}); err != nil {
			t.Fatal("second recovery", err)
		}
		progress, err = reader.Find(t.Context(), evidence.UploadPrincipal{Scope: f.scope, CaptureTokenID: third, VerificationID: f.creation.Session.ID()})
		if err != nil || len(progress.Completions) != 2 {
			t.Fatal("multi-generation progress lost or duplicated", err)
		}
		snapshot, err = authorities.CaptureSnapshot(t.Context(), f.scope, f.creation.Session.ID())
		if err != nil || snapshot.Response != nil {
			t.Fatal("second recovery reused prior consent", err)
		}

	})
}
