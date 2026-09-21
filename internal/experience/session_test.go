package experience

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func verificationIDForTest(t *testing.T, value int) id.Verification {
	t.Helper()
	identifier, err := id.ParseVerification(fmt.Sprintf("ver_%026d", value))
	if err != nil {
		t.Fatalf("ParseVerification() error = %v", err)
	}
	return identifier
}

func TestResolveForSessionPinsAndPreservesVersions(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()
	scope := environment.authority.TenantScope()

	first := mustPublish(t, environment, mustCreateDraft(t, environment))
	request := ResolutionRequest{Workflow: "capture.identity", Country: "NG", Locale: "en"}
	session := verificationIDForTest(t, 1)
	resolution, err := environment.service.ResolveForSession(ctx, scope, session, request)
	if err != nil {
		t.Fatalf("ResolveForSession() error = %v", err)
	}
	if resolution.Fallback || resolution.Pinned.ExperienceID != first.ID.String() || resolution.Pinned.Source != PinSourcePublished {
		t.Fatalf("resolution = %+v", resolution)
	}
	if resolution.Pinned.TenantCopyVersion != "tc_acme_v1" || resolution.Pinned.MandatoryCopyVersion != DefaultMandatoryVersion {
		t.Fatalf("pinned copy versions = %+v", resolution.Pinned)
	}
	if len(resolution.MandatoryCopy.Entries) == 0 || resolution.MandatoryCopy.Digest == "" {
		t.Fatal("mandatory copy was not served")
	}

	// A later, more specific publication must not move an existing session.
	moreSpecific := validDraftRequest()
	moreSpecific.Name = "Second"
	moreSpecific.Targeting = []contract.Target{{
		Workflow: "capture.identity", Countries: []string{"NG"}, ApplicationIDs: []string{"dev.acme.app"},
	}}
	created, err := environment.service.Create(ctx, environment.authority, moreSpecific)
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	mustPublish(t, environment, created)
	resumed, err := environment.service.ResolveForSession(ctx, scope, session, ResolutionRequest{
		Workflow: "capture.identity", Country: "NG", ApplicationID: "dev.acme.app",
	})
	if err != nil {
		t.Fatalf("ResolveForSession() resume error = %v", err)
	}
	if resumed.Pinned.ExperienceID != first.ID.String() || resumed.Pinned.Source != PinSourcePinned {
		t.Fatalf("resume moved the pin: %+v", resumed.Pinned)
	}

	// A different session resolves the newer most-specific publication.
	otherSession := verificationIDForTest(t, 2)
	second, err := environment.service.ResolveForSession(ctx, scope, otherSession, ResolutionRequest{
		Workflow: "capture.identity", Country: "NG", ApplicationID: "dev.acme.app",
	})
	if err != nil {
		t.Fatalf("ResolveForSession() second error = %v", err)
	}
	if second.Pinned.ExperienceID == first.ID.String() {
		t.Fatalf("second session did not resolve the newer publication: %+v", second.Pinned)
	}

	// Kill switch forces the signed default for the pinned session.
	if _, err := environment.service.Revoke(ctx, environment.authority, first.ID, first.Revision, "incident"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	fallback, err := environment.service.ResolveForSession(ctx, scope, session, request)
	if err != nil {
		t.Fatalf("ResolveForSession() fallback error = %v", err)
	}
	if !fallback.Fallback || fallback.Pinned.ExperienceID != SafeDefaultExperienceID || fallback.Pinned.Source != PinSourceDefault {
		t.Fatalf("fallback = %+v", fallback)
	}
	if fallback.Manifest.KeyID == "" || fallback.Manifest.Signature == "" {
		t.Fatal("fallback was not signed")
	}
}

func TestResolveWithoutMatchPinsSignedDefault(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()
	scope := environment.authority.TenantScope()
	mustPublish(t, environment, mustCreateDraft(t, environment))

	session := verificationIDForTest(t, 3)
	resolution, err := environment.service.ResolveForSession(ctx, scope, session, ResolutionRequest{Country: "ZA"})
	if err != nil {
		t.Fatalf("ResolveForSession() error = %v", err)
	}
	if resolution.Pinned.ExperienceID != SafeDefaultExperienceID || !resolution.Fallback {
		t.Fatalf("no-match resolution = %+v", resolution)
	}
	resumed, err := environment.service.ResolveForSession(ctx, scope, session, ResolutionRequest{Country: "ZA"})
	if err != nil || resumed.Pinned.Source != PinSourceDefault {
		t.Fatalf("default pin resume = (%+v, %v)", resumed.Pinned, err)
	}
}

func TestResolveAmbiguityFallsBackToDefault(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()
	scope := environment.authority.TenantScope()

	for index := 0; index < 2; index++ {
		request := validDraftRequest()
		request.Name = fmt.Sprintf("Ambiguous %d", index)
		created, err := environment.service.Create(ctx, environment.authority, request)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		mustPublish(t, environment, created)
	}
	resolution, err := environment.service.ResolveForSession(
		ctx, scope, verificationIDForTest(t, 4), ResolutionRequest{Workflow: "capture.identity", Country: "NG"},
	)
	if err != nil {
		t.Fatalf("ResolveForSession() error = %v", err)
	}
	if !resolution.Fallback || resolution.Pinned.ExperienceID != SafeDefaultExperienceID {
		t.Fatalf("ambiguous resolution = %+v", resolution)
	}
}

func TestSafeDefaultIsSignedAndStable(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	resolution := environment.service.SafeDefault(environment.now)
	if !resolution.Fallback || resolution.Manifest.Document.ExperienceID != SafeDefaultExperienceID {
		t.Fatalf("safe default = %+v", resolution)
	}
	if _, err := contract.VerifyManifest(resolution.Manifest, map[string]ed25519.PublicKey{}); err == nil {
		t.Fatal("VerifyManifest accepted an unknown key")
	}
	public := environment.signer.publicKey()
	if _, err := contract.VerifyManifest(resolution.Manifest, map[string]ed25519.PublicKey{resolution.Manifest.KeyID: public}); err != nil {
		t.Fatalf("safe default signature did not verify: %v", err)
	}
	if resolution.Pinned.Locale != SafeDefaultLocale || resolution.MandatoryCopy.Version != DefaultMandatoryVersion {
		t.Fatalf("safe default pin = %+v", resolution.Pinned)
	}
}

func TestSafeDefaultDocumentIsValid(t *testing.T) {
	t.Parallel()
	if err := contract.ValidateDocument(SafeDefaultDocument()); err != nil {
		t.Fatalf("SafeDefaultDocument() invalid: %v", err)
	}
}
