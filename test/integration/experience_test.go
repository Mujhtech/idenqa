//go:build integration

package integration_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	ed25519signer "github.com/Mujhtech/idenqa/adapters/experience/ed25519signer"
	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/experience"
	experiencepostgres "github.com/Mujhtech/idenqa/internal/experience/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestPortableExperiencePinningAndRevocationFallback(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, generator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify portable experience"}
	firstTenant, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	secondTenant, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	firstScope, err := tenant.NewScope(firstTenant.ID())
	if err != nil {
		t.Fatalf("new first scope: %v", err)
	}
	secondScope, err := tenant.NewScope(secondTenant.ID())
	if err != nil {
		t.Fatalf("new second scope: %v", err)
	}
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x63}, 32)})
	if err != nil {
		t.Fatalf("new pepper set: %v", err)
	}
	key, presented := newIntegrationCredentialKey(
		t, generator, firstTenant.ID(), now, peppers,
		access.Pattern("verification_sessions:create"), access.Pattern("experiences:*"),
	)
	if err := accessStore.Create(ctx, firstScope, key); err != nil {
		t.Fatalf("create actor key: %v", err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	authority, err := authenticator.Authenticate(ctx, presented.Reveal())
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("built-in registry: %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	policyID := seedIntegrationPolicy(t, adminPool, firstTenant.ID(), generator, now)
	actor := newIntegrationKey(t, generator, firstTenant.ID(), now, id.APIKey{})
	if err := accessStore.Create(ctx, firstScope, actor); err != nil {
		t.Fatalf("create actor key: %v", err)
	}
	profileStore, err := verificationpostgres.New(runtimePool, catalog)
	if err != nil {
		t.Fatalf("new profile store: %v", err)
	}
	profileID, err := generator.NewProfile()
	if err != nil {
		t.Fatalf("new profile id: %v", err)
	}
	profile, draft, err := verification.NewCaptureProfile(
		profileID, firstTenant.ID(), "Experience session profile",
		integrationProfileDocument(t, registry, evidence.MethodLiveCamera), registry, now,
	)
	if err != nil {
		t.Fatalf("new capture profile: %v", err)
	}
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind: verification.MutationCreate, Profile: profile, Revision: draft, Actor: actor.ID(),
		Idempotency: integrationIdempotencyRequest(t, firstTenant.ID(), actor.ID(), verification.OperationCreateProfile, "experience-profile-create", []byte(`{"name":"Experience session profile"}`), now),
		Result:      integrationMutationResult(profile, draft), Status: 201,
	}); err != nil {
		t.Fatalf("create capture profile: %v", err)
	}
	publishedProfile, publishedRevision, _, err := profile.PublishDraft(1, draft, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind: verification.MutationPublish, Profile: publishedProfile, Revision: publishedRevision, ExpectedVersion: 1,
		Actor: actor.ID(), Idempotency: integrationIdempotencyRequest(t, firstTenant.ID(), actor.ID(), verification.OperationPublishProfile, "experience-profile-publish", []byte(`{"expected_version":1}`), now.Add(time.Minute)),
		Result: integrationMutationResult(publishedProfile, publishedRevision), Status: 200,
	}); err != nil {
		t.Fatalf("publish capture profile: %v", err)
	}

	// The owned signing keyring self-checks the safe default at construction.
	keyring, err := ed25519signer.New(1, map[uint16][]byte{1: bytes.Repeat([]byte{0x77}, 32)})
	if err != nil {
		t.Fatalf("new experience keyring: %v", err)
	}
	mandatoryCopy, err := experience.NewDefaultMandatoryCatalogue()
	if err != nil {
		t.Fatalf("new mandatory catalogue: %v", err)
	}
	experienceStore, err := experiencepostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new experience store: %v", err)
	}
	experienceService, err := experience.NewService(experience.Deps{
		Repository: experienceStore, Pins: experienceStore, Signer: keyring, Verifier: keyring,
		Assets: experience.DenyAssets{}, Mandatory: mandatoryCopy, IDs: generator, Clock: clock.System{},
	})
	if err != nil {
		t.Fatalf("new experience service: %v", err)
	}

	createdExperience, err := experienceService.Create(ctx, authority, experience.DraftRequest{
		Name: "Integration experience",
		Copy: contract.Copy{Version: "tc_integration_v1", Locales: []contract.LocaleCopy{{
			Locale:  "en",
			Entries: []contract.CopyEntry{{Key: "capture.title", Value: "Verify your identity"}},
		}}},
		MandatoryCopyVersion: experience.DefaultMandatoryVersion,
		DefaultLocale:        "en",
		Targeting: []contract.Target{{
			Workflow: "capture.identity", Countries: []string{"NG"},
		}},
		Links: contract.Links{
			Support: "https://acme.example/support", Privacy: "https://acme.example/privacy", Terms: "https://acme.example/terms",
		},
		Theme: contract.Theme{PrimaryColor: "#1f6feb", AccentColor: "#0b3d91", BackgroundColor: "#ffffff", TextColor: "#1b1f23"},
	})
	if err != nil {
		t.Fatalf("Create() experience error = %v", err)
	}
	approved, err := experienceService.Approve(ctx, authority, createdExperience.ID, createdExperience.Revision)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	published, err := experienceService.Publish(ctx, authority, createdExperience.ID, approved.Revision)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if published.State != experience.StatePublished || published.PublishedVersion != 1 {
		t.Fatalf("published = %+v", published)
	}

	sessionStore, err := verificationpostgres.NewSessionStore(runtimePool, integrationProtector{}, catalog)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	captureKeyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{1: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatalf("new capture keyring: %v", err)
	}
	captureSigner, err := access.NewCaptureTokenSigner(captureKeyring, clock.System{})
	if err != nil {
		t.Fatalf("new capture signer: %v", err)
	}
	outcomeKeyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{1: bytes.Repeat([]byte{0x72}, 32)})
	if err != nil {
		t.Fatalf("new outcome keyring: %v", err)
	}
	outcomeSigner, err := access.NewOutcomeTokenSigner(outcomeKeyring, clock.System{})
	if err != nil {
		t.Fatalf("new outcome signer: %v", err)
	}
	sessionService, err := verification.NewSessionService(
		sessionStore, generator, captureSigner, outcomeSigner, clock.System{},
		verification.SessionLifetimes{
			VerificationDefault: 24 * time.Hour, VerificationMaximum: 48 * time.Hour,
			CaptureTokenDefault: 30 * time.Minute, CaptureTokenMaximum: time.Hour,
			OutcomePostDefault: 24 * time.Hour, OutcomePostMaximum: 48 * time.Hour,
			IdempotencyRetention: 24 * time.Hour,
		},
		"local",
	)
	if err != nil {
		t.Fatalf("new session service: %v", err)
	}
	sessionService.WithExperience(experienceService)
	createdSession, err := sessionService.Create(ctx, authority, "experience-session", verification.SessionCreateInput{
		ProfileID: profileID, PolicyID: policyID, Locale: "en",
		Experience: &experience.ResolutionRequest{Workflow: "capture.identity", Country: "NG"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	pin, err := experienceStore.FindPin(ctx, firstScope, createdSession.Session.ID())
	if err != nil {
		t.Fatalf("FindPin() error = %v", err)
	}
	if pin.ExperienceID.String() != createdExperience.ID.String() || pin.Version != 1 ||
		pin.TenantCopyVersion != "tc_integration_v1" || pin.MandatoryCopyVersion != experience.DefaultMandatoryVersion ||
		pin.Locale != "en" {
		t.Fatalf("pin = %+v", pin)
	}
	resolution, err := experienceService.ResolveForSession(ctx, firstScope, createdSession.Session.ID(), experience.ResolutionRequest{})
	if err != nil {
		t.Fatalf("ResolveForSession() error = %v", err)
	}
	if resolution.Fallback || resolution.Pinned.ExperienceID != createdExperience.ID.String() ||
		resolution.Pinned.Source != experience.PinSourcePinned || resolution.Manifest.Digest != pin.Digest {
		t.Fatalf("resolution = %+v", resolution)
	}
	if _, err := experienceStore.FindPin(ctx, secondScope, createdSession.Session.ID()); !errors.Is(err, experience.ErrNotFound) {
		t.Fatalf("cross-tenant FindPin() error = %v, want ErrNotFound", err)
	}

	current, err := experienceService.Get(ctx, authority, createdExperience.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := experienceService.Revoke(ctx, authority, createdExperience.ID, current.Revision, "integration kill switch"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	fallback, err := experienceService.ResolveForSession(ctx, firstScope, createdSession.Session.ID(), experience.ResolutionRequest{})
	if err != nil {
		t.Fatalf("ResolveForSession() after revoke error = %v", err)
	}
	if !fallback.Fallback || fallback.Pinned.ExperienceID != experience.SafeDefaultExperienceID {
		t.Fatalf("fallback = %+v", fallback)
	}
	public, ok := keyring.Fingerprint(fallback.Manifest.KeyID)
	if !ok {
		t.Fatalf("fallback key id %q is not in the keyring", fallback.Manifest.KeyID)
	}
	if _, err := contract.VerifyManifest(fallback.Manifest, map[string]ed25519.PublicKey{fallback.Manifest.KeyID: public}); err != nil {
		t.Fatalf("fallback manifest did not verify: %v", err)
	}
}
