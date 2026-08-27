package access_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestNewVerificationRecord(t *testing.T) {
	t.Parallel()

	fixture := newAuthenticationFixture(t)
	if _, err := access.NewVerificationRecord(fixture.key, tenant.StateActive); err != nil {
		t.Fatalf("NewVerificationRecord(active) error = %v", err)
	}
	if _, err := access.NewVerificationRecord(access.Key{}, tenant.StateActive); err == nil {
		t.Error("NewVerificationRecord(zero key) error = nil")
	}
	if _, err := access.NewVerificationRecord(fixture.key, tenant.State("unknown")); err == nil {
		t.Error("NewVerificationRecord(unknown state) error = nil")
	}
}

func TestAuthenticatorAuthenticatesAndAuthorisesExactGrant(t *testing.T) {
	t.Parallel()

	fixture := newAuthenticationFixture(t)
	repository := &verificationRepositoryStub{record: fixture.record}
	authenticator, err := access.NewAuthenticator(repository, fixture.peppers, fixedAuthenticationClock{now: fixture.now})
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}

	accessContext, err := authenticator.Authenticate(t.Context(), fixture.presented.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if accessContext.Principal().KeyID().String() != fixture.presented.ID().String() {
		t.Fatalf("principal key = %q", accessContext.Principal().KeyID())
	}
	if accessContext.TenantScope().ID().String() != fixture.presented.TenantHint().String() {
		t.Fatalf("tenant scope = %q", accessContext.TenantScope().ID())
	}
	if err := accessContext.Require(access.PermissionTenantRead); err != nil {
		t.Fatalf("Require(tenant:read) error = %v", err)
	}
	if err := accessContext.Require(access.PermissionCaptureProfilesWrite); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Require(ungranted) error = %v", err)
	}
	if repository.tenantHint.String() != fixture.presented.TenantHint().String() ||
		repository.keyID.String() != fixture.presented.ID().String() {
		t.Fatal("verification repository did not receive both credential lookup hints")
	}
}

func TestAuthenticatorCollapsesExpectedFailures(t *testing.T) {
	t.Parallel()

	fixture := newAuthenticationFixture(t)
	expired := fixture.recordFor(t, func(record *access.KeyRecord) {
		expiresAt := fixture.now
		record.ExpiresAt = &expiresAt
	})
	revoked := fixture.recordFor(t, func(record *access.KeyRecord) {
		revokedAt := fixture.now.Add(-time.Minute)
		record.UpdatedAt = revokedAt
		record.RevokedAt = &revokedAt
	})
	retired := fixture.recordFor(t, func(record *access.KeyRecord) {
		scheduledAt := fixture.now.Add(-time.Minute)
		retiredAt := fixture.now
		record.UpdatedAt = scheduledAt
		record.RetiredAt = &retiredAt
	})
	disabled, err := access.NewVerificationRecord(fixture.key, tenant.StateDisabled)
	if err != nil {
		t.Fatalf("NewVerificationRecord(disabled) error = %v", err)
	}
	wrongSecret := testPresentedKey(t, 0xbc)

	tests := []struct {
		name       string
		encoded    string
		repository *verificationRepositoryStub
	}{
		{name: "malformed", encoded: "malformed", repository: &verificationRepositoryStub{}},
		{name: "unknown", encoded: fixture.presented.Reveal(), repository: &verificationRepositoryStub{err: access.ErrKeyNotFound}},
		{name: "mismatched secret", encoded: wrongSecret.Reveal(), repository: &verificationRepositoryStub{record: fixture.record}},
		{name: "expired", encoded: fixture.presented.Reveal(), repository: &verificationRepositoryStub{record: expired}},
		{name: "revoked", encoded: fixture.presented.Reveal(), repository: &verificationRepositoryStub{record: revoked}},
		{name: "retired", encoded: fixture.presented.Reveal(), repository: &verificationRepositoryStub{record: retired}},
		{name: "disabled tenant", encoded: fixture.presented.Reveal(), repository: &verificationRepositoryStub{record: disabled}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authenticator, err := access.NewAuthenticator(test.repository, fixture.peppers, fixedAuthenticationClock{now: fixture.now})
			if err != nil {
				t.Fatalf("NewAuthenticator() error = %v", err)
			}
			got, err := authenticator.Authenticate(t.Context(), test.encoded)
			if !errors.Is(err, access.ErrInvalidCredential) || err.Error() != access.ErrInvalidCredential.Error() {
				t.Fatalf("Authenticate() error = %v, want only ErrInvalidCredential", err)
			}
			if !got.TenantScope().ID().IsZero() || !got.Principal().KeyID().IsZero() {
				t.Fatal("failed authentication returned authority")
			}
			if test.name == "malformed" && test.repository.calls != 0 {
				t.Fatal("malformed credential reached the verification repository")
			}
		})
	}
}

func TestAuthenticatorPreservesOperationalFailures(t *testing.T) {
	t.Parallel()

	fixture := newAuthenticationFixture(t)
	databaseErr := errors.New("database unavailable")
	authenticator, err := access.NewAuthenticator(
		&verificationRepositoryStub{err: databaseErr},
		fixture.peppers,
		fixedAuthenticationClock{now: fixture.now},
	)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	_, err = authenticator.Authenticate(t.Context(), fixture.presented.Reveal())
	if !errors.Is(err, databaseErr) || errors.Is(err, access.ErrInvalidCredential) {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestNewAuthenticatorRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	fixture := newAuthenticationFixture(t)
	repository := &verificationRepositoryStub{record: fixture.record}
	tests := []struct {
		name       string
		repository access.VerificationRepository
		peppers    *access.PepperSet
		clock      fixedAuthenticationClock
	}{
		{name: "repository", peppers: fixture.peppers, clock: fixedAuthenticationClock{now: fixture.now}},
		{name: "peppers", repository: repository, clock: fixedAuthenticationClock{now: fixture.now}},
		{name: "clock", repository: repository, peppers: fixture.peppers},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var source accessClock
			if !test.clock.now.IsZero() {
				source = test.clock
			}
			if _, err := access.NewAuthenticator(test.repository, test.peppers, source); err == nil {
				t.Error("NewAuthenticator() error = nil")
			}
		})
	}

	var nilAuthenticator *access.Authenticator
	if _, err := nilAuthenticator.Authenticate(t.Context(), ""); err == nil {
		t.Error("nil Authenticate() error = nil")
	}
}

type accessClock interface{ Now() time.Time }

type fixedAuthenticationClock struct{ now time.Time }

func (source fixedAuthenticationClock) Now() time.Time { return source.now }

type verificationRepositoryStub struct {
	record     access.VerificationRecord
	err        error
	tenantHint id.Tenant
	keyID      id.APIKey
	calls      int
}

func (repository *verificationRepositoryStub) FindForVerification(
	_ context.Context,
	tenantHint id.Tenant,
	keyID id.APIKey,
) (access.VerificationRecord, error) {
	repository.calls++
	repository.tenantHint = tenantHint
	repository.keyID = keyID

	return repository.record, repository.err
}

type authenticationFixture struct {
	now       time.Time
	presented access.PresentedKey
	peppers   *access.PepperSet
	key       access.Key
	record    access.VerificationRecord
}

func newAuthenticationFixture(t *testing.T) authenticationFixture {
	t.Helper()

	now := time.Date(2026, time.August, 27, 18, 0, 0, 0, time.UTC)
	presented := testPresentedKey(t, 0xab)
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{
		1: bytes.Repeat([]byte{0x21}, 32),
	})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: presented.ID(), TenantID: presented.TenantHint(), Label: "backend integration",
		Digest: digest, PepperVersion: version, Grant: grant, Version: 1,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatalf("NewVerificationRecord() error = %v", err)
	}

	return authenticationFixture{now: now, presented: presented, peppers: peppers, key: key, record: record}
}

func (fixture authenticationFixture) recordFor(t *testing.T, mutate func(*access.KeyRecord)) access.VerificationRecord {
	t.Helper()

	record := access.KeyRecord{
		ID: fixture.key.ID(), TenantID: fixture.key.TenantID(), Label: fixture.key.Label(),
		Digest: fixture.key.Digest(), PepperVersion: fixture.key.PepperVersion(), Grant: fixture.key.Grant(),
		Version: fixture.key.Version(), CreatedAt: fixture.key.CreatedAt(), UpdatedAt: fixture.key.UpdatedAt(),
		ExpiresAt: fixture.key.ExpiresAt(), RevokedAt: fixture.key.RevokedAt(), RetiredAt: fixture.key.RetiredAt(),
		ReplacesID: fixture.key.ReplacesID(),
	}
	mutate(&record)
	key, err := access.RestoreKey(record)
	if err != nil {
		t.Fatalf("RestoreKey(mutated) error = %v", err)
	}
	verification, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatalf("NewVerificationRecord(mutated) error = %v", err)
	}

	return verification
}
