//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestManagedLaunchAuthorityChecksDurableActorsAndReplaysAfterExpiry(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		sessions, err := verificationpostgres.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
		if err != nil {
			t.Fatal(err)
		}
		workload, err := verification.NewWorkloadSessionPrincipal("wld_managedjourney")
		if err != nil {
			t.Fatal(err)
		}
		mutation := f.creationMutation
		mutation.SessionID, _ = f.ids.NewVerification()
		mutation.CaptureTokenID, _ = f.ids.NewCaptureToken()
		mutation.OutcomeTokenID, _ = f.ids.NewOutcomeToken()
		mutation.EventID, _ = f.ids.NewEvent()
		mutation.DecisionID, _ = f.ids.NewDecision()
		mutation.Actor, mutation.DelegatedActor = workload, "usr_operator"
		mutation.Idempotency, err = idempotency.NewRequest(f.scope.ID(), workload, verification.OperationCreateVerification, "managed-journey-create", []byte(`{"managed":"journey"}`), mutation.CreatedAt, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		created, err := sessions.Create(t.Context(), f.scope, mutation)
		if err != nil {
			t.Fatal(err)
		}
		service, err := authority.NewService(f.authorities, f.authorities, f.authorities, sessions, f.ids, fixedIntegrationClock{now: f.now}, 72*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		record := f.declaration.Record()
		input := authority.DeclarationInput{VerificationID: created.Session.ID(), NoticeID: record.NoticeID, Category: record.Category, Purpose: record.Purpose, Jurisdiction: record.Jurisdiction, PolicyPack: record.PolicyPack, IsConsentRequired: true, RecipientReference: record.RecipientReference, RecipientDisplayName: record.RecipientDisplayName, Regions: record.Regions, RetentionReference: record.RetentionReference, ValidFrom: created.Session.CreatedAt(), ExpiresAt: created.Session.ExpiresAt()}
		if _, err := service.DeclareManaged(t.Context(), f.scope, workload, "usr_wrong", "managed-journey-authority", input); !errors.Is(err, authority.ErrProcessingNotPermitted) {
			t.Fatalf("wrong delegated actor: %v", err)
		}
		otherWorkload, _ := verification.NewWorkloadSessionPrincipal("wld_wrongworkload")
		if _, err := service.DeclareManaged(t.Context(), f.scope, otherWorkload, "usr_operator", "managed-journey-authority", input); !errors.Is(err, authority.ErrProcessingNotPermitted) {
			t.Fatalf("wrong workload: %v", err)
		}
		declaration, err := service.DeclareManaged(t.Context(), f.scope, workload, "usr_operator", "managed-journey-authority", input)
		if err != nil {
			t.Fatal(err)
		}
		if !declaration.Record().CreatedBy.IsZero() || declaration.Record().WorkloadID != workload.String() {
			t.Fatal("workload was relabelled as an API key")
		}
		late, err := authority.NewService(f.authorities, f.authorities, f.authorities, sessions, f.ids, fixedIntegrationClock{now: created.Session.ExpiresAt().Add(time.Minute)}, 72*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := late.DeclareManaged(t.Context(), f.scope, workload, "usr_operator", "managed-journey-authority", input)
		if err != nil || replayed.ID() != declaration.ID() {
			t.Fatalf("lost response replay after expiry: %v", err)
		}
		if _, err := late.DeclareManaged(t.Context(), f.scope, workload, "usr_wrong", "managed-journey-authority", input); err == nil {
			t.Fatal("replay changed the delegated actor")
		}
		var count int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.authority_audit WHERE tenant_id=$1 AND authority_id=$2 AND actor_type='managed_workload' AND actor_id=$3`, f.scope.ID().String(), declaration.ID().String(), workload.String()).Scan(&count); err != nil || count != 1 {
			t.Fatalf("workload audit count=%d: %v", count, err)
		}
	})
}
