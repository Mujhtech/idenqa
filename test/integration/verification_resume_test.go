//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestVerificationResumeReusesOrReplacesCredentialAndReplaysReferences(t *testing.T) {
	for _, test := range []struct {
		name    string
		revoked bool
	}{
		{name: "reuse live credential"},
		{name: "replace revoked credential", revoked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				store, err := verificationpostgres.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
				if err != nil {
					t.Fatal(err)
				}
				lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				var actorValue string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT created_by FROM idenqa.processing_authorities WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&actorValue); err != nil {
					t.Fatal(err)
				}
				actor, err := id.ParseAPIKey(actorValue)
				if err != nil {
					t.Fatal(err)
				}
				awaitEvent, _ := f.ids.NewEvent()
				awaitAt := f.now.Add(-time.Minute)
				if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{EventID: awaitEvent, VerificationID: f.creation.Session.ID(), ExpectedVersion: 1, Target: verification.SessionStateAwaitingInput, ActorID: actor.String(), OccurredAt: awaitAt}); err != nil {
					t.Fatal(err)
				}

				responseID, _ := f.ids.NewAcknowledgement()
				responseEvent, _ := f.ids.NewEvent()
				response, err := authority.NewResponse(authority.ResponseRecord{
					ID: responseID, TenantID: f.scope.ID(), AuthorityID: f.declaration.ID(), NoticeID: f.declaration.NoticeID(),
					SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), CaptureTokenID: f.creation.Credential.ID(),
					Action: authority.ResponseConsent, Locale: "en-NG", RenderedExperienceVersion: "capture.resume.v1", RecordedAt: f.now,
				})
				if err != nil {
					t.Fatal(err)
				}
				responseRequest, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), "authorities.respond", "resume-fresh-authority", []byte(`{"action":"consent"}`), f.now, 24*time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{Response: response, EventID: responseEvent, Idempotency: responseRequest}); err != nil {
					t.Fatal(err)
				}
				if test.revoked {
					if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.capture_tokens SET revoked_at=$3 WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Credential.ID().String(), f.now); err != nil {
						t.Fatal(err)
					}
				}

				replacement, _ := f.ids.NewCaptureToken()
				resumeEvent, _ := f.ids.NewEvent()
				canonical, _ := json.Marshal(struct {
					VerificationID  string `json:"verification_id"`
					ExpectedVersion int64  `json:"expected_version"`
				}{f.creation.Session.ID().String(), 2})
				retry, err := idempotency.NewRequest(f.scope.ID(), actor, verification.OperationResumeVerification, "resume-session", canonical, f.now, 24*time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				mutation := verification.ResumeMutation{VerificationID: f.creation.Session.ID(), ExpectedVersion: 2, ReplacementID: replacement, EventID: resumeEvent, Actor: actor, KeyVersion: 1, At: f.now, TokenExpiresAt: f.now.Add(30 * time.Minute), Idempotency: retry}
				resumed, err := store.Resume(t.Context(), f.scope, mutation)
				if err != nil {
					t.Fatal(err)
				}
				wantToken := f.creation.Credential.ID()
				if test.revoked {
					wantToken = replacement
				}
				if resumed.Session.State() != verification.SessionStateCollecting || resumed.Session.Version() != 3 || resumed.Replaced != test.revoked || resumed.Replayed || resumed.Credential.ID() != wantToken || resumed.Credential.ExpiresAt().After(resumed.Session.ExpiresAt()) {
					t.Fatalf("resume = %+v credential=%s", resumed, resumed.Credential.ID())
				}

				processingEvent, _ := f.ids.NewEvent()
				if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{EventID: processingEvent, VerificationID: f.creation.Session.ID(), ExpectedVersion: 3, Target: verification.SessionStateProcessing, ActorID: actor.String(), OccurredAt: f.now}); err != nil {
					t.Fatal(err)
				}
				replayed, err := store.Resume(context.Background(), f.scope, mutation)
				if err != nil {
					t.Fatal(err)
				}
				if !replayed.Replayed || replayed.Session.State() != verification.SessionStateCollecting || replayed.Session.Version() != 3 || replayed.Credential.ID() != wantToken {
					t.Fatalf("replay = %+v", replayed)
				}
			})
		})
	}
}
