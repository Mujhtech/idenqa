//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestPolicyDiscoveryExcludesIneligibleProcessingBeforeBatchLimit(t *testing.T) {
	for _, scenario := range []string{"withdrawal", "refusal", "new_consent", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				evidenceStore, mutations := f.prepare(t)
				for _, mutation := range mutations {
					if _, err := evidenceStore.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
						t.Fatal(err)
					}
				}
				checkID, err := f.ids.NewCheck()
				if err != nil {
					t.Fatal(err)
				}
				// Only discovery is under test: seed a terminal check, without
				// fabricating provider observations or a policy decision.
				if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.verification_checks
(tenant_id,id,verification_id,name,state,version,created_at,updated_at)
VALUES ($1,$2,$3,'synthetic.discovery','cancelled',1,$4,$4)`, f.scope.ID().String(), checkID.String(), f.creation.Session.ID().String(), f.now); err != nil {
					t.Fatal(err)
				}
				store, err := policypostgres.New(f.runtime, integrationProtector{})
				if err != nil {
					t.Fatal(err)
				}
				assertTargets := func(at time.Time, want int) {
					t.Helper()
					targets, err := store.ListReadyAuthorships(t.Context(), at, 1)
					if err != nil || len(targets) != want {
						t.Fatalf("discovery targets=%d want=%d err=%v", len(targets), want, err)
					}
					if want == 1 && (targets[0].TenantID != f.scope.ID() || targets[0].VerificationID != f.creation.Session.ID()) {
						t.Fatal("discovery returned a different scope")
					}
				}
				assertTargets(f.now, 0)
				lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				transition := func(target verification.SessionState, version int64) {
					t.Helper()
					if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{
						EventID: mustCaptureEvent(t, f.ids), VerificationID: f.creation.Session.ID(), ExpectedVersion: version,
						Target: target, ActorID: f.declaration.Record().CreatedBy.String(), OccurredAt: f.now,
					}); err != nil {
						t.Fatal(err)
					}
				}
				transition(verification.SessionStateProcessing, 1)
				assertTargets(f.now, 1)
				observedAt := f.now
				switch scenario {
				case "withdrawal", "refusal":
					revokeProcessingAuthority(t, f, scenario)
				case "new_consent":
					appendNewProcessingConsent(t, f)
				case "expired":
					observedAt = f.creation.Session.ExpiresAt()
				case "cancelled":
					transition(verification.SessionStateCancelled, 2)
				}
				assertTargets(observedAt, 0)
			})
		})
	}
}

func appendNewProcessingConsent(t *testing.T, f captureAcceptanceFixture) {
	t.Helper()
	responseID, err := f.ids.NewAcknowledgement()
	if err != nil {
		t.Fatal(err)
	}
	response, err := authority.NewResponse(authority.ResponseRecord{
		ID: responseID, TenantID: f.scope.ID(), AuthorityID: f.declaration.ID(), NoticeID: f.declaration.Record().NoticeID,
		SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), CaptureTokenID: f.creation.Credential.ID(),
		Action: authority.ResponseConsent, Locale: "en-NG", RecordedAt: f.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), "authorities.respond", "processing-new-consent", []byte(`{}`), f.now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{
		Response: response, EventID: mustCaptureEvent(t, f.ids), Idempotency: request,
	}); err != nil {
		t.Fatal(err)
	}
}
