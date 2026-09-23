//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpg "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpg "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestDojahDocumentPreparationUsesSelectedSides(t *testing.T) {
	for _, tt := range []struct {
		name, choice          string
		missingBack, rollback bool
		want                  int
	}{
		{"passport", "passport", false, false, 1},
		{"licence", "driver_license", false, false, 2},
		{"missing back rolls back front", "driver_license", true, false, 0},
		{"downstream rollback", "driver_license", false, true, 0},
		{"unselected", "", false, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
				sessions, err := verificationpg.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
				if err != nil {
					t.Fatal(err)
				}
				choices, err := verificationpg.NewDocumentSelectionStore(sessions, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				if tt.choice != "" {
					if _, err := choices.SelectDocument(t.Context(), f.scope, documentChoiceMutation(t, f, tt.choice, "provider-choice", 1)); err != nil {
						t.Fatal(err)
					}
					f.creation.Session, err = sessions.FindSession(t.Context(), f.scope, f.creation.Session.ID())
					if err != nil {
						t.Fatal(err)
					}
					assets, mutations := f.prepare(t)
					for i, mutation := range mutations {
						if tt.missingBack && i == 1 {
							continue
						}
						if _, err := assets.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
							t.Fatal(err)
						}
					}
				}
				manifest := dojah.Description()
				binding := provider.Binding{
					TenantID: f.scope.ID().String(), PolicyID: f.creation.Session.PolicyID().String(),
					ProfileDigest: f.creation.Session.ProfileDigest(), Requirement: "document",
					Region: "tenant.region.ng", Purpose: "idenqa.purpose.identity_verification", Recipient: "tenant.recipient.primary",
					Configuration: providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/dojah", CredentialVersion: "fixture"},
				}
				plan, err := provider.NewPlan(binding, manifest)
				if err != nil {
					t.Fatal(err)
				}
				requests, err := providerpg.NewRequestStore(f.runtime, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				preparation := providerpg.Preparation{Plan: plan, Requests: requests, IDs: f.ids, Catalog: f.catalog, Clock: fixedIntegrationClock{now: f.now}, Wrapper: integrationProtector{}}
				checkID, err := f.ids.NewCheck()
				if err != nil {
					t.Fatal(err)
				}
				attemptID, err := f.ids.NewAttempt()
				if err != nil {
					t.Fatal(err)
				}
				rollback := errors.New("fixture downstream failure")
				err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
					_, save, err := preparation.Prepare(ctx, tx, f.scope, f.creation.Session.ID(), checkID, attemptID, verification.PlannedCheck{Name: manifest.Capabilities[2].Check}, f.now, f.now.Add(time.Minute))
					if err != nil {
						return err
					}
					if save == nil {
						t.Fatal("missing prepared request writer")
					}
					if tt.rollback {
						return rollback
					}
					return nil
				})
				if tt.rollback {
					if !errors.Is(err, rollback) {
						t.Fatalf("rollback error = %v", err)
					}
				} else if tt.want == 0 {
					if !errors.Is(err, provider.ErrRequestUnavailable) {
						t.Fatalf("expected unavailable, got %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				var count int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.evidence_processing_grants WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != tt.want {
					t.Fatalf("grants = %d, want %d", count, tt.want)
				}
				if tt.want > 0 {
					var fronts, backs, invalid int
					err := f.admin.Native().QueryRow(t.Context(), `SELECT
						count(*) FILTER (WHERE uploads.artefact='idenqa.artefact.document_front'),
						count(*) FILTER (WHERE uploads.artefact='idenqa.artefact.document_back'),
						count(*) FILTER (WHERE grants.maximum_uses<>1 OR grants.uses<>0
							OR grants.runner_identity<>'dojah' OR grants.workload_version<>'0.1.2'
							OR grants.purpose<>'idenqa.purpose.identity_verification'
							OR grants.recipient_reference<>'tenant.recipient.primary')
						FROM idenqa.evidence_processing_grants grants
						JOIN idenqa.evidence_upload_intents uploads ON uploads.tenant_id=grants.tenant_id AND uploads.evidence_id=grants.evidence_id
						WHERE grants.tenant_id=$1`, f.scope.ID().String()).Scan(&fronts, &backs, &invalid)
					if err != nil || fronts != 1 || backs != tt.want-1 || invalid != 0 {
						t.Fatalf("grant binding: front=%d back=%d invalid=%d error=%v", fronts, backs, invalid, err)
					}
				}
			}, "local", "tenant.region.ng", documentChoiceProfile)
		})
	}
}
