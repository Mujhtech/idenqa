package verification

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestCaptureProfileLifecyclePreservesPublishedRevisions(t *testing.T) {
	t.Parallel()

	registry := builtInRegistry(t)
	document := captureProfileDocument(t, registry, evidence.MethodFileUpload)
	profile, draft, err := NewCaptureProfile(
		mustProfileID(t),
		mustTenantID(t),
		"Standard identity",
		document,
		registry,
		profileTime(0),
	)
	if err != nil {
		t.Fatalf("NewCaptureProfile() error = %v", err)
	}
	if profile.State() != ProfileStateDraft || profile.Version() != 1 || draft.Number() != 1 {
		t.Fatalf("new profile = %+v, revision = %+v", profile, draft)
	}

	profile, firstPublished, previous, err := profile.PublishDraft(1, draft, nil, profileTime(1))
	if err != nil {
		t.Fatalf("PublishDraft(1) error = %v", err)
	}
	if previous != nil || firstPublished.State() != RevisionStatePublished || profile.State() != ProfileStateActive {
		t.Fatalf("first publication = %+v, previous = %+v, profile = %+v", firstPublished, previous, profile)
	}

	liveDocument := captureProfileDocument(t, registry, evidence.MethodLiveCamera)
	profile, secondDraft, err := profile.BeginSupersession(2, liveDocument, registry, profileTime(2))
	if err != nil {
		t.Fatalf("BeginSupersession() error = %v", err)
	}
	if profile.PublishedRevision() == nil || *profile.PublishedRevision() != 1 || secondDraft.Number() != 2 {
		t.Fatalf("supersession replaced the active revision before publication")
	}

	profile, secondPublished, superseded, err := profile.PublishDraft(
		3,
		secondDraft,
		&firstPublished,
		profileTime(3),
	)
	if err != nil {
		t.Fatalf("PublishDraft(2) error = %v", err)
	}
	if superseded == nil || superseded.State() != RevisionStateSuperseded ||
		secondPublished.State() != RevisionStatePublished || *profile.PublishedRevision() != 2 {
		t.Fatalf("second publication did not preserve supersession history")
	}

	profile, withdrawn, err := profile.Deactivate(4, nil, profileTime(4))
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if withdrawn != nil || profile.State() != ProfileStateDeactivated || profile.DeactivatedAt() == nil {
		t.Fatalf("deactivated profile = %+v, withdrawn = %+v", profile, withdrawn)
	}
}

func TestCaptureProfileAllowsOnlyOneMutableDraft(t *testing.T) {
	t.Parallel()

	registry := builtInRegistry(t)
	document := captureProfileDocument(t, registry, evidence.MethodFileUpload)
	profile, draft, err := NewCaptureProfile(
		mustProfileID(t),
		mustTenantID(t),
		"Standard identity",
		document,
		registry,
		profileTime(0),
	)
	if err != nil {
		t.Fatalf("NewCaptureProfile() error = %v", err)
	}
	if _, _, err := profile.BeginSupersession(1, document, registry, profileTime(1)); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("BeginSupersession(draft) error = %v, want ErrProfileConflict", err)
	}

	profile, published, _, err := profile.PublishDraft(1, draft, nil, profileTime(1))
	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
	profile, nextDraft, err := profile.BeginSupersession(2, document, registry, profileTime(2))
	if err != nil {
		t.Fatalf("BeginSupersession() error = %v", err)
	}
	if _, _, err := profile.BeginSupersession(3, document, registry, profileTime(3)); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("second BeginSupersession() error = %v, want ErrProfileConflict", err)
	}
	if _, _, err := profile.UpdateDraft(2, "Changed", nextDraft, document, registry, profileTime(3)); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale UpdateDraft() error = %v, want ErrProfileConflict", err)
	}
	if _, _, err := profile.UpdateDraft(3, "Changed", published, document, registry, profileTime(3)); !errors.Is(err, ErrPublishedRevisionImmutable) {
		t.Fatalf("UpdateDraft(published) error = %v, want ErrPublishedRevisionImmutable", err)
	}
}

func TestCaptureProfileDeactivationWithdrawsUnpublishedDraft(t *testing.T) {
	t.Parallel()

	registry := builtInRegistry(t)
	document := captureProfileDocument(t, registry, evidence.MethodFileUpload)
	profile, draft, err := NewCaptureProfile(
		mustProfileID(t),
		mustTenantID(t),
		"Standard identity",
		document,
		registry,
		profileTime(0),
	)
	if err != nil {
		t.Fatalf("NewCaptureProfile() error = %v", err)
	}
	profile, published, _, err := profile.PublishDraft(1, draft, nil, profileTime(1))
	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
	profile, nextDraft, err := profile.BeginSupersession(2, document, registry, profileTime(2))
	if err != nil {
		t.Fatalf("BeginSupersession() error = %v", err)
	}
	_ = published
	profile, withdrawn, err := profile.Deactivate(3, &nextDraft, profileTime(3))
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if withdrawn == nil || withdrawn.State() != RevisionStateWithdrawn || profile.DraftRevision() != nil {
		t.Fatalf("withdrawn revision = %+v, profile = %+v", withdrawn, profile)
	}
}

func captureProfileDocument(t *testing.T, registry evidence.Registry, method evidence.Name) Profile {
	t.Helper()

	profile, err := NewProfile(registry, []Requirement{
		selfieRequirement(Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{method}}, nil),
	})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}

	return profile
}

func mustProfileID(t *testing.T) id.Profile {
	t.Helper()

	identifier, err := id.ParseProfile("prf_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}

	return identifier
}

func mustTenantID(t *testing.T) id.Tenant {
	t.Helper()

	identifier, err := id.ParseTenant("ten_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}

	return identifier
}

func profileTime(minutes int) time.Time {
	return time.Date(2026, time.August, 27, 12, minutes, 0, 0, time.UTC)
}
