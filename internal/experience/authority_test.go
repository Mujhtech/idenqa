package experience

import (
	"bytes"
	"context"
	"testing"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type testAuthorityClock struct{ now time.Time }

func (clock testAuthorityClock) Now() time.Time { return clock.now }

type testVerificationRepository struct{ record access.VerificationRecord }

func (repository testVerificationRepository) FindForVerification(
	context.Context,
	id.Tenant,
	id.APIKey,
) (access.VerificationRecord, error) {
	return repository.record, nil
}

type testEnvironment struct {
	service    *Service
	repository *fakeRepository
	signer     *fakeSigner
	assets     *fakeAssets
	authority  access.Context
	now        time.Time
}

func newTestEnvironment(t *testing.T) *testEnvironment {
	t.Helper()
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	repository := newFakeRepository()
	signer := newFakeSigner()
	mandatory, err := NewDefaultMandatoryCatalogue()
	if err != nil {
		t.Fatalf("NewDefaultMandatoryCatalogue() error = %v", err)
	}
	assets := &fakeAssets{}
	service, err := NewService(Deps{
		Repository: repository,
		Pins:       repository,
		Signer:     signer,
		Verifier:   newFakeVerifier(signer),
		Assets:     assets,
		Mandatory:  mandatory,
		IDs:        &fakeIDs{},
		Clock:      fakeClock{now: now},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &testEnvironment{
		service: service, repository: repository, signer: signer, assets: assets,
		authority: newTestAuthority(t, now, "experiences:*"), now: now,
	}
}

func newTestAuthority(t *testing.T, now time.Time, patterns ...access.Pattern) access.Context {
	t.Helper()
	identifiers, err := id.NewGenerator(testAuthorityClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{1}, 512)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	keyID, err := identifiers.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	keyGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x52}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := keyGenerator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x62}, 32)})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	digest, pepperVersion, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(patterns...)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "Experience service test", Digest: digest,
		PepperVersion: pepperVersion, Grant: grant, Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatalf("NewVerificationRecord() error = %v", err)
	}
	authenticator, err := access.NewAuthenticator(
		testVerificationRepository{record: record},
		peppers,
		testAuthorityClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	authority, err := authenticator.Authenticate(context.Background(), presented.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	return authority
}

func validDraftRequest() DraftRequest {
	return DraftRequest{
		Name: "Acme capture",
		Copy: contract.Copy{
			Version: "tc_acme_v1",
			Locales: []contract.LocaleCopy{{
				Locale: "en",
				Entries: []contract.CopyEntry{
					{Key: "capture.title", Value: "Verify your identity"},
					{Key: "capture.instruction", Value: "Take a clear selfie."},
				},
			}},
		},
		MandatoryCopyVersion: DefaultMandatoryVersion,
		DefaultLocale:        "en",
		Targeting: []contract.Target{
			{Workflow: "capture.identity", Countries: []string{"NG"}},
		},
		Links: contract.Links{
			Support: "https://support.acme.example/help",
			Privacy: "https://acme.example/privacy",
			Terms:   "https://acme.example/terms",
		},
		Theme: contract.Theme{
			PrimaryColor: "#1f6feb", AccentColor: "#0b3d91",
			BackgroundColor: "#ffffff", TextColor: "#1b1f23",
		},
	}
}

func mustCreateDraft(t *testing.T, environment *testEnvironment) Experience {
	t.Helper()
	created, err := environment.service.Create(context.Background(), environment.authority, validDraftRequest())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}

func mustPublish(t *testing.T, environment *testEnvironment, created Experience) Experience {
	t.Helper()
	approved, err := environment.service.Approve(context.Background(), environment.authority, created.ID, created.Revision)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	published, err := environment.service.Publish(context.Background(), environment.authority, created.ID, approved.Revision)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	return published
}
