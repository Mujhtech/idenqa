package verification

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestNewSessionSnapshotsActivePublishedProfile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	registry, profile, published, verificationID := publishedProfileFixture(t, now)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	session, err := NewSession(
		verificationID,
		profile.TenantID(),
		profile,
		published,
		registry,
		"local",
		policyID,
		now.Add(time.Minute),
		now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if session.State() != SessionStateCollecting || session.Version() != 1 ||
		session.ProfileID().String() != profile.ID().String() ||
		session.ProfileRevision() != published.Number() || session.ProfileDigest() != published.Digest() ||
		session.Region() != "local" {
		t.Fatalf("session = %+v", session)
	}
	if !session.AcceptsCaptureAt(now.Add(time.Hour)) || session.AcceptsCaptureAt(session.ExpiresAt()) {
		t.Fatal("session capture lifetime is incorrect")
	}
}

func TestNewSessionRejectsInvalidProcessingRegion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	registry, profile, published, verificationID := publishedProfileFixture(t, now)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	for _, region := range []string{"", "EU West", "-local", "local_1"} {
		_, err := NewSession(
			verificationID, profile.TenantID(), profile, published, registry,
			region, policyID, now.Add(time.Minute), now.Add(time.Hour),
		)
		if err == nil {
			t.Fatalf("NewSession(region %q) succeeded", region)
		}
	}
}

func TestNewSessionRejectsDraftOrInactiveProfile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	registry, profile, published, verificationID := publishedProfileFixture(t, now)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	draftProfile, draft, err := NewCaptureProfile(
		profile.ID(),
		profile.TenantID(),
		"Draft",
		published.Document(),
		registry,
		now,
	)
	if err != nil {
		t.Fatalf("NewCaptureProfile() error = %v", err)
	}

	tests := []struct {
		name     string
		profile  CaptureProfile
		revision Revision
	}{
		{name: "draft", profile: draftProfile, revision: draft},
		{name: "mismatched revision", profile: profile, revision: draft},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewSession(
				verificationID,
				profile.TenantID(),
				test.profile,
				test.revision,
				registry,
				"local",
				policyID,
				now.Add(time.Minute),
				now.Add(time.Hour),
			)
			if !errors.Is(err, ErrProfileUnavailable) {
				t.Fatalf("NewSession() error = %v, want ErrProfileUnavailable", err)
			}
		})
	}
}

func TestSessionSnapshotSurvivesProfileSupersessionAndDeactivation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	registry, profile, published, verificationID := publishedProfileFixture(t, now)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	session, err := NewSession(
		verificationID,
		profile.TenantID(),
		profile,
		published,
		registry,
		"local",
		policyID,
		now.Add(time.Minute),
		now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	originalDigest := session.ProfileDigest()

	profile, draft, err := profile.BeginSupersession(
		profile.Version(),
		published.Document(),
		registry,
		now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("BeginSupersession() error = %v", err)
	}
	profile, _, _, err = profile.PublishDraft(
		profile.Version(),
		draft,
		&published,
		now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
	if _, _, err := profile.Deactivate(profile.Version(), nil, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if session.ProfileRevision() != 1 || session.ProfileDigest() != originalDigest ||
		session.Requirements().Registry != published.Document().Registry {
		t.Fatal("session snapshot changed with its source profile")
	}
}

type sessionClock struct{ now time.Time }

func (source sessionClock) Now() time.Time { return source.now }

func publishedProfileFixture(
	t *testing.T,
	now time.Time,
) (evidence.Registry, CaptureProfile, Revision, id.Verification) {
	t.Helper()

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	generator, err := id.NewGenerator(sessionClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{0x19}, 512)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	profileID, err := generator.NewProfile()
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	verificationID, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}
	document, err := NewProfile(registry, []Requirement{{
		Key:          "selfie",
		Purpose:      evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceSelfieImage,
		Artefacts:    []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition: Acquisition{
			Strategy: StrategyAnyOf,
			Methods:  []evidence.Name{evidence.MethodFileUpload},
		},
	}})
	if err != nil {
		t.Fatalf("NewProfile(document) error = %v", err)
	}
	profile, draft, err := NewCaptureProfile(profileID, tenantID, "Standard", document, registry, now)
	if err != nil {
		t.Fatalf("NewCaptureProfile() error = %v", err)
	}
	profile, published, _, err := profile.PublishDraft(profile.Version(), draft, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}

	return registry, profile, published, verificationID
}
