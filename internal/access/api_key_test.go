package access_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestRestoreKeyAndEffectiveState(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	expires := created.Add(time.Hour)
	record := testKeyRecord(t, created)
	record.ExpiresAt = &expires
	key, err := access.RestoreKey(record)
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	if key.ID().String() != record.ID.String() || key.TenantID().String() != record.TenantID.String() ||
		key.Label() != record.Label || key.Version() != 1 || key.PepperVersion() != 1 {
		t.Fatalf("restored key metadata is incorrect")
	}
	if !key.UsableAt(created) || key.StateAt(expires) != access.KeyStateExpired {
		t.Fatalf("key state before/at expiry = %q/%q", key.StateAt(created), key.StateAt(expires))
	}
	returnedExpiry := key.ExpiresAt()
	*returnedExpiry = returnedExpiry.Add(time.Hour)
	if !key.ExpiresAt().Equal(expires) {
		t.Fatal("ExpiresAt() exposed internal state")
	}
	grant := key.Grant()
	permissions := grant.Permissions()
	permissions[0] = access.PermissionCaptureProfilesWrite
	if key.Grant().Allows(access.PermissionCaptureProfilesWrite) {
		t.Fatal("Grant() exposed internal state")
	}
}

func TestKeyIrreversibleLifecycleTransitions(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		transition func(*access.Key, time.Time) error
		want       access.KeyState
	}{
		{name: "revoke", transition: (*access.Key).Revoke, want: access.KeyStateRevoked},
		{name: "retire", transition: (*access.Key).Retire, want: access.KeyStateRetired},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			key, err := access.RestoreKey(testKeyRecord(t, created))
			if err != nil {
				t.Fatalf("RestoreKey() error = %v", err)
			}
			transitionedAt := created.Add(time.Minute)
			if err := test.transition(&key, transitionedAt); err != nil {
				t.Fatalf("transition error = %v", err)
			}
			if key.StateAt(created.Add(24*time.Hour)) != test.want || key.Version() != 2 || !key.UpdatedAt().Equal(transitionedAt) {
				t.Fatalf("transitioned key = state %q version %d updated %s", key.StateAt(created), key.Version(), key.UpdatedAt())
			}
			if err := key.Revoke(transitionedAt.Add(time.Minute)); !errors.Is(err, access.ErrKeyTerminal) {
				t.Fatalf("second transition error = %v, want ErrKeyTerminal", err)
			}
			if err := key.Retire(transitionedAt.Add(time.Minute)); !errors.Is(err, access.ErrKeyTerminal) {
				t.Fatalf("opposite transition error = %v, want ErrKeyTerminal", err)
			}
		})
	}
}

func TestRestoreKeyRejectsInvalidState(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*access.KeyRecord)
	}{
		{name: "zero id", mutate: func(record *access.KeyRecord) { record.ID = id.APIKey{} }},
		{name: "zero tenant", mutate: func(record *access.KeyRecord) { record.TenantID = id.Tenant{} }},
		{name: "empty label", mutate: func(record *access.KeyRecord) { record.Label = "" }},
		{name: "label whitespace", mutate: func(record *access.KeyRecord) { record.Label = " backend " }},
		{name: "label control", mutate: func(record *access.KeyRecord) { record.Label = "backend\nkey" }},
		{name: "zero digest", mutate: func(record *access.KeyRecord) { record.Digest = access.Digest{} }},
		{name: "zero pepper", mutate: func(record *access.KeyRecord) { record.PepperVersion = 0 }},
		{name: "zero version", mutate: func(record *access.KeyRecord) { record.Version = 0 }},
		{name: "updated before created", mutate: func(record *access.KeyRecord) { record.UpdatedAt = created.Add(-time.Second) }},
		{name: "expiry at creation", mutate: func(record *access.KeyRecord) { record.ExpiresAt = &record.CreatedAt }},
		{name: "retirement before update", mutate: func(record *access.KeyRecord) {
			retired := created.Add(time.Minute)
			record.RetiredAt = &retired
			record.UpdatedAt = retired.Add(time.Minute)
		}},
		{name: "terminal time mismatch", mutate: func(record *access.KeyRecord) {
			now := created.Add(time.Minute)
			updated := now.Add(time.Minute)
			record.RevokedAt = &now
			record.UpdatedAt = updated
		}},
		{name: "self replacement", mutate: func(record *access.KeyRecord) { record.ReplacesID = record.ID }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := testKeyRecord(t, created)
			test.mutate(&record)
			if _, err := access.RestoreKey(record); err == nil {
				t.Error("RestoreKey() error = nil")
			}
		})
	}
}

func TestKeyTransitionRejectsInvalidReceiverAndTime(t *testing.T) {
	t.Parallel()

	var key *access.Key
	if err := key.Revoke(time.Now()); err == nil {
		t.Error("nil Revoke() error = nil")
	}
	created := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	value, err := access.RestoreKey(testKeyRecord(t, created))
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	if err := value.Retire(created.Add(-time.Second)); err == nil {
		t.Error("Retire(before updated) error = nil")
	}
}

func TestScheduledRetirementAllowsBoundedOverlapAndEmergencyRevocation(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	key, err := access.RestoreKey(testKeyRecord(t, created))
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	scheduledAt := created.Add(time.Minute)
	effectiveAt := scheduledAt.Add(10 * time.Minute)
	if err := key.ScheduleRetirement(scheduledAt, effectiveAt); err != nil {
		t.Fatalf("ScheduleRetirement() error = %v", err)
	}
	if !key.UsableAt(effectiveAt.Add(-time.Nanosecond)) || key.StateAt(effectiveAt) != access.KeyStateRetired {
		t.Fatalf("scheduled retirement state before/at deadline = %q/%q", key.StateAt(effectiveAt.Add(-time.Nanosecond)), key.StateAt(effectiveAt))
	}
	if err := key.Revoke(scheduledAt.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke() during overlap error = %v", err)
	}
	if key.StateAt(scheduledAt.Add(time.Minute)) != access.KeyStateRevoked || key.Version() != 3 {
		t.Fatalf("emergency revocation state = %q version %d", key.StateAt(scheduledAt.Add(time.Minute)), key.Version())
	}
}

func TestScheduleRetirementRejectsInvalidDeadline(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	key, err := access.RestoreKey(testKeyRecord(t, created))
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	if err := key.ScheduleRetirement(created.Add(time.Minute), created); err == nil {
		t.Error("ScheduleRetirement(past deadline) error = nil")
	}
}

func testKeyRecord(t *testing.T, created time.Time) access.KeyRecord {
	t.Helper()

	tenantID, keyID := testKeyIDs(t)
	digest, err := access.ParseDigest(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("ParseDigest() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	return access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "backend integration",
		Digest: digest, PepperVersion: 1, Grant: grant, Version: 1,
		CreatedAt: created, UpdatedAt: created,
	}
}
