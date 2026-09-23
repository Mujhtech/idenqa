//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepg "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpg "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestCaptureDocumentSelectionBranchesAndReplay(t *testing.T) {
	for _, documentType := range []string{"passport", "driver_license"} {
		t.Run(documentType, func(t *testing.T) {
			runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
				sessions, err := verificationpg.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
				if err != nil {
					t.Fatal(err)
				}
				source := fixedIntegrationClock{now: f.now}
				store, err := verificationpg.NewDocumentSelectionStore(sessions, source)
				if err != nil {
					t.Fatal(err)
				}
				service, err := verification.NewDocumentSelectionService(store, f.ids, source, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				keyring, err := access.NewCaptureTokenKeyring(f.creation.Credential.KeyVersion(), map[access.CaptureTokenKeyVersion][]byte{f.creation.Credential.KeyVersion(): bytes.Repeat([]byte{42}, 32)})
				if err != nil {
					t.Fatal(err)
				}
				signer, err := access.NewCaptureTokenSigner(keyring, source)
				if err != nil {
					t.Fatal(err)
				}
				token, err := signer.Sign(f.creation.Credential)
				if err != nil {
					t.Fatal(err)
				}
				authenticator, err := verification.NewCaptureAuthenticator(sessions, signer, source)
				if err != nil {
					t.Fatal(err)
				}
				authenticate := func() verification.CaptureContext {
					t.Helper()
					capture, err := authenticator.Authenticate(t.Context(), token.Reveal())
					if err != nil {
						t.Fatal(err)
					}
					return capture
				}
				capture := authenticate()
				uploads, err := evidencepg.NewWithClock(f.runtime, integrationProtector{}, f.catalog, source)
				if err != nil {
					t.Fatal(err)
				}
				issuer, err := authority.NewUploadService(f.authorities, uploads, f.ids, source, f.catalog, evidence.DefaultUploadPolicy(), time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				issueInput := authority.UploadRequest{RequirementKey: "document", Artefact: evidence.ArtefactDocumentFront, AcquisitionMethod: evidence.MethodLiveCamera, ExpectedBytes: 8, ExpectedDigest: "sha256:" + string(bytes.Repeat([]byte{'a'}, 64)), MediaType: evidence.MediaTypeJPEG, Region: f.declaration.Record().Regions[0]}
				if _, err := issuer.Issue(t.Context(), capture, "unselected", issueInput); !errors.Is(err, authority.ErrProcessingNotPermitted) {
					t.Fatal("unselected issuance", err)
				}
				var before string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT requirements::text FROM idenqa.verification_sessions WHERE id=$1`, f.creation.Session.ID().String()).Scan(&before); err != nil {
					t.Fatal(err)
				}
				input := verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: documentType, ExpectedVersion: 1}
				selected, err := service.Select(t.Context(), capture, "choose-document", input)
				if err != nil {
					t.Fatal(err)
				}
				if selected.Version() != 2 || selected.DocumentSelections()["document"] != documentType {
					t.Fatal("selection not recorded")
				}
				capture = authenticate()
				if capture.Session().DocumentSelections()["document"] != documentType {
					t.Fatal("capture restoration lost choice")
				}
				createdReplay, err := sessions.Create(t.Context(), f.scope, f.creationMutation)
				if err != nil || createdReplay.Session.Version() != 1 || len(createdReplay.Session.DocumentSelections()) != 0 {
					t.Fatal("creation replay mixed later choices with original version", err)
				}
				replayed, err := service.Select(t.Context(), capture, "choose-document", input)
				if err != nil || replayed.Version() != 2 {
					t.Fatal("lost response replay", err)
				}
				if _, err := service.Select(t.Context(), capture, "stale-choice", input); !errors.Is(err, verification.ErrSessionConflict) {
					t.Fatal("stale choice accepted", err)
				}
				if documentType == "passport" {
					issueInput.Artefact = evidence.ArtefactDocumentBack
					if _, err := issuer.Issue(t.Context(), capture, "outside-branch", issueInput); !errors.Is(err, authority.ErrProcessingNotPermitted) {
						t.Fatal("passport back allowed", err)
					}
				}
				f.creation.Session = selected
				prepared, acceptances := f.prepare(t)
				if len(acceptances) != map[string]int{"passport": 1, "driver_license": 2}[documentType] {
					t.Fatal("wrong effective steps")
				}
				if documentType == "driver_license" {
					_, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions SET document_selections='{"document":"passport"}',version=version+1 WHERE id=$1`, selected.ID().String())
					expectDocumentSelectionGuard(t, err, "document selection is locked by upload history")
				}
				other := "driver_license"
				if documentType == other {
					other = "passport"
				}
				if _, err := service.Select(t.Context(), capture, "switch-after-intent", verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: other, ExpectedVersion: 2}); !errors.Is(err, verification.ErrSessionConflict) {
					t.Fatal("switched after upload intent", err)
				}
				for index, acceptance := range acceptances {
					if _, err := prepared.AcceptUpload(t.Context(), f.scope, acceptance); err != nil {
						t.Fatal(err)
					}
					var complete bool
					if err := f.admin.Native().QueryRow(t.Context(), `SELECT capture_completed_at IS NOT NULL FROM idenqa.verification_sessions WHERE id=$1`, selected.ID().String()).Scan(&complete); err != nil {
						t.Fatal(err)
					}
					if complete != (index == len(acceptances)-1) {
						t.Fatal("wrong capture completion", index, complete)
					}
				}
				var after string
				var audits, events int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT requirements::text,(SELECT count(*) FROM idenqa.capture_document_selection_audit WHERE verification_id=s.id),(SELECT count(*) FROM idenqa.outbox_events WHERE aggregate_id=s.id AND event_type='verification.document_selected.v1') FROM idenqa.verification_sessions s WHERE id=$1`, selected.ID().String()).Scan(&after, &audits, &events); err != nil {
					t.Fatal(err)
				}
				if before != after || audits != 1 || events != 1 {
					t.Fatal("snapshot or atomic command receipts changed", audits, events)
				}
				if _, err := service.Select(t.Context(), authenticate(), "same-choice-after-completion", verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: documentType, ExpectedVersion: 2}); !errors.Is(err, verification.ErrSessionConflict) {
					t.Fatal("completed capture accepted same-choice version increment", err)
				}
				if replay, err := service.Select(t.Context(), authenticate(), "choose-document", input); err != nil || replay.Version() != 2 {
					t.Fatal("completion blocked exact selection replay", err)
				}
				var version int64
				var complete bool
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT version,capture_completed_at IS NOT NULL FROM idenqa.verification_sessions WHERE id=$1`, selected.ID().String()).Scan(&version, &complete); err != nil || version != 2 || !complete {
					t.Fatal("completed capture metadata changed", version, complete, err)
				}
				issueInput.Artefact = evidence.ArtefactDocumentFront
				pending, err := issuer.Issue(t.Context(), capture, "failed-intent", issueInput)
				if err != nil {
					t.Fatal(err)
				}
				claimed, err := uploads.ClaimUploadAttempt(t.Context(), f.scope, capture.TokenID(), pending.ID(), pending.Version(), f.now)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := uploads.FailUploadAttempt(t.Context(), f.scope, capture.TokenID(), pending.ID(), claimed.Version(), claimed.Attempt(), f.now); err != nil {
					t.Fatal(err)
				}
				if _, err := service.Select(t.Context(), capture, "switch-after-failure", verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: other, ExpectedVersion: 2}); !errors.Is(err, verification.ErrSessionConflict) {
					t.Fatal("failed intent unlocked branch", err)
				}
				// Exercise the persistence gate directly: a forged application mutation
				// cannot issue a back artefact under a passport selection.
				if documentType == "passport" {
					original, err := prepared.FindUpload(t.Context(), f.scope, acceptances[0].UploadID)
					if err != nil {
						t.Fatal(err)
					}
					record := original.Record()
					record.ID, err = f.ids.NewUpload()
					if err != nil {
						t.Fatal(err)
					}
					record.EvidenceID, err = f.ids.NewEvidence()
					if err != nil {
						t.Fatal(err)
					}
					record.Artefact = evidence.ArtefactDocumentBack
					record.State = evidence.UploadStateIssued
					record.Version = 1
					record.Attempt = 0
					record.AcceptedAt = nil
					record.LeaseExpiresAt = nil
					forged, err := evidence.RestoreUpload(record, f.registry)
					if err != nil {
						t.Fatal(err)
					}
					body, err := json.Marshal(input)
					if err != nil {
						t.Fatal(err)
					}
					retry, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), evidence.OperationCreateUpload, "forged-branch", body, f.now, time.Hour)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := prepared.CreateUpload(t.Context(), f.scope, evidence.UploadCreateMutation{Upload: forged, Idempotency: retry}); !errors.Is(err, evidence.ErrUploadConflict) {
						t.Fatal("transaction accepted forged branch", err)
					}
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
				var renewed verification.SessionCreation
				err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
					var err error
					renewed, err = sessions.RenewCaptureWithin(ctx, f.scope, tx, selected.ID(), verification.CaptureRenewal{ExpectedToken: f.creation.Credential.ID(), Token: next, Actor: actor, KeyVersion: f.creation.Credential.KeyVersion(), At: at, ExpiresAt: at.Add(time.Minute)}, fixedIntegrationClock{now: at})
					return err
				})
				if err != nil || renewed.Session.DocumentSelections()["document"] != documentType || renewed.Session.Version() != 2 {
					t.Fatal("renewal lost document selection", err)
				}
				recoveredStore, err := verificationpg.NewDocumentSelectionStore(sessions, fixedIntegrationClock{now: at})
				if err != nil {
					t.Fatal(err)
				}
				oldBody, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				oldRetry, err := idempotency.NewRequest(f.scope.ID(), capture.TokenID(), verification.OperationSelectDocument, "choose-document", oldBody, at, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := recoveredStore.SelectDocument(t.Context(), f.scope, verification.DocumentSelectionMutation{Input: input, VerificationID: selected.ID(), CaptureTokenID: capture.TokenID(), EventID: mustCaptureEvent(t, f.ids), Retry: oldRetry}); !errors.Is(err, access.ErrInvalidCaptureToken) {
					t.Fatal("revoked credential replay bypassed transactional authentication", err)
				}
				body, err := json.Marshal(verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: other, ExpectedVersion: 2})
				if err != nil {
					t.Fatal(err)
				}
				retry, err := idempotency.NewRequest(f.scope.ID(), next, verification.OperationSelectDocument, "recovered-switch", body, at, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := recoveredStore.SelectDocument(t.Context(), f.scope, verification.DocumentSelectionMutation{Input: verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: other, ExpectedVersion: 2}, VerificationID: selected.ID(), CaptureTokenID: next, EventID: mustCaptureEvent(t, f.ids), Retry: retry}); !errors.Is(err, verification.ErrSessionConflict) {
					t.Fatal("recovery unlocked branch", err)
				}
			}, "local", "tenant.region.ng", func(profile *verification.Profile) {
				r := profile.Requirements[0]
				r.Key = "document"
				r.EvidenceType = evidence.EvidenceDocumentImage
				r.Artefacts = []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}
				r.DocumentOptions = []verification.DocumentOption{{ID: "passport", Label: "Passport", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}}, {ID: "driver_license", Label: "Driver licence", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}}}
				profile.Requirements = []verification.Requirement{r}
			})
		})
	}
}
