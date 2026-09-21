package experience

import (
	"context"
	"errors"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
)

func TestServiceLifecycleAndIdempotency(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()

	created := mustCreateDraft(t, environment)
	if created.State != StateDraft || created.LatestVersion != 1 || created.Revision != 1 {
		t.Fatalf("created = %+v", created)
	}

	// Editing invalidates approval and advances the immutable version.
	approved, err := environment.service.Approve(ctx, environment.authority, created.ID, created.Revision)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if approved.State != StateApproved || approved.ApprovedVersion != 1 {
		t.Fatalf("approved = %+v", approved)
	}
	updated, err := environment.service.UpdateDraft(ctx, environment.authority, created.ID, approved.Revision, validDraftRequest())
	if err != nil {
		t.Fatalf("UpdateDraft() error = %v", err)
	}
	if updated.State != StateDraft || updated.LatestVersion != 2 || updated.ApprovedVersion != 0 {
		t.Fatalf("updated = %+v", updated)
	}

	// Publication requires the explicit approval of the latest revision.
	if _, err := environment.service.Publish(ctx, environment.authority, updated.ID, updated.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("Publish() without approval error = %v, want ErrConflict", err)
	}
	approved, err = environment.service.Approve(ctx, environment.authority, updated.ID, updated.Revision)
	if err != nil {
		t.Fatalf("Approve() second error = %v", err)
	}
	if replay, err := environment.service.Approve(ctx, environment.authority, updated.ID, approved.Revision); err != nil || replay.Revision != approved.Revision {
		t.Fatalf("Approve() replay = (%+v, %v), want idempotent", replay, err)
	}
	published, err := environment.service.Publish(ctx, environment.authority, updated.ID, approved.Revision)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if published.State != StatePublished || published.PublishedVersion != 2 {
		t.Fatalf("published = %+v", published)
	}
	if replay, err := environment.service.Publish(ctx, environment.authority, updated.ID, published.Revision); err != nil || replay.PublishedVersion != 2 {
		t.Fatalf("Publish() replay = (%+v, %v), want idempotent", replay, err)
	}

	// A stale expected revision fails closed.
	if _, err := environment.service.Publish(ctx, environment.authority, updated.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Publish() error = %v, want ErrConflict", err)
	}

	// Kill switch and rollback.
	revoked, err := environment.service.Revoke(ctx, environment.authority, updated.ID, published.Revision, "incident")
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if revoked.State != StateRevoked {
		t.Fatalf("revoked = %+v", revoked)
	}
	if replay, err := environment.service.Revoke(ctx, environment.authority, updated.ID, revoked.Revision, "incident"); err != nil || replay.State != StateRevoked {
		t.Fatalf("Revoke() replay = (%+v, %v), want idempotent", replay, err)
	}
	if _, err := environment.service.UpdateDraft(ctx, environment.authority, updated.ID, revoked.Revision, validDraftRequest()); !errors.Is(err, ErrRevoked) {
		t.Fatalf("UpdateDraft() after revoke error = %v, want ErrRevoked", err)
	}
	rolledBack, err := environment.service.Rollback(ctx, environment.authority, updated.ID, revoked.Revision, 1, "restore")
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if rolledBack.State != StatePublished || rolledBack.PublishedVersion != 1 {
		t.Fatalf("rolled back = %+v", rolledBack)
	}
	revision, err := environment.repository.Revision(ctx, environment.authority.TenantScope(), updated.ID, 2)
	if err != nil || revision.State != RevisionSuperseded {
		t.Fatalf("superseded revision = (%+v, %v)", revision, err)
	}
}

func TestServiceAuthorisationAndValidation(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()

	if _, err := environment.service.Create(ctx, access.Context{}, validDraftRequest()); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Create() without authority error = %v", err)
	}
	readOnly := newTestAuthority(t, environment.now, access.Pattern("experiences:read"))
	if _, err := environment.service.Create(ctx, readOnly, validDraftRequest()); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Create() with read-only authority error = %v", err)
	}
	writer := newTestAuthority(t, environment.now, access.Pattern("experiences:write"))
	created, err := environment.service.Create(ctx, writer, validDraftRequest())
	if err != nil {
		t.Fatalf("Create() with write authority error = %v", err)
	}
	if _, err := environment.service.Publish(ctx, writer, created.ID, created.Revision); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Publish() with write-only authority error = %v", err)
	}
	if _, err := environment.service.Get(ctx, authorityForTenant(t, environment, readOnly), created.ID); err != nil {
		t.Fatalf("Get() with read authority error = %v", err)
	}

	unknown := validDraftRequest()
	unknown.MandatoryCopyVersion = "mc-1999-01-01"
	if _, err := environment.service.Create(ctx, environment.authority, unknown); !errors.Is(err, ErrUnknownMandatoryCopy) {
		t.Fatalf("Create() unknown mandatory error = %v, want ErrUnknownMandatoryCopy", err)
	}
	invalid := validDraftRequest()
	invalid.DefaultLocale = "de"
	if _, err := environment.service.Create(ctx, environment.authority, invalid); !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("Create() invalid document error = %v, want contract.ErrInvalid", err)
	}

	environment.assets.failKey = "logo"
	withAsset := validDraftRequest()
	withAsset.Assets = []contract.Asset{{
		Key: "logo", Kind: "image", MIME: "image/png", Size: 1024,
		Digest:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ObjectKey: "experiences/acme/logo.png", ObjectVersion: "v1",
	}}
	withAsset.Theme.LogoAssetKey = "logo"
	if _, err := environment.service.Create(ctx, environment.authority, withAsset); !errors.Is(err, ErrAssetUnavailable) {
		t.Fatalf("Create() unverifiable asset error = %v, want ErrAssetUnavailable", err)
	}
}

func authorityForTenant(t *testing.T, environment *testEnvironment, source access.Context) access.Context {
	t.Helper()
	// The read-only authority already authenticates the same tenant because all
	// test authorities derive from the same deterministic identifier stream.
	if source.TenantScope().ID().String() != environment.authority.TenantScope().ID().String() {
		t.Fatal("test authority tenant mismatch")
	}
	return source
}
