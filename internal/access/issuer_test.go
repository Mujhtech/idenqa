package access_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestExpiryAndRotationPolicyValidation(t *testing.T) {
	t.Parallel()

	zero := time.Duration(0)
	if _, err := access.NewExpiryPolicy(false, &zero); err == nil {
		t.Error("NewExpiryPolicy(zero maximum) error = nil")
	}
	if _, err := access.NewRotationPolicy(0); err == nil {
		t.Error("NewRotationPolicy(0) error = nil")
	}
}

func TestIssuerIssuePersistsDisplayOnceCredential(t *testing.T) {
	t.Parallel()

	repository := &issuanceRepositoryStub{}
	issuer, peppers := newTestIssuer(t, repository, false, 24*time.Hour, time.Hour)
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := access.ParsePattern("*:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	expiresAt := keyClock{}.Now().Add(time.Hour)
	issued, err := issuer.Issue(t.Context(), scope, access.IssueInput{
		Label: "production backend", Patterns: []access.Pattern{pattern}, Expiry: access.ExpiringAt(expiresAt),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if len(repository.created) != 1 || repository.created[0].ID().String() != issued.Key().ID().String() {
		t.Fatal("Issue() did not persist the returned key")
	}
	if issued.Credential().Reveal() == "" || !strings.HasPrefix(issued.Credential().Reveal(), "idq_v1_") {
		t.Fatal("Issue() did not return display-once credential material")
	}
	if !issued.Key().ExpiresAt().Equal(expiresAt) || !issued.Key().Grant().Allows(access.PermissionTenantRead) {
		t.Fatal("issued key did not preserve expiry and resolved scope snapshot")
	}
	valid, err := peppers.Verify(issued.Credential(), issued.Key().PepperVersion(), issued.Key().Digest())
	if err != nil || !valid {
		t.Fatalf("Verify(issued) = %t, %v", valid, err)
	}
}

func TestIssuerIssueEnforcesExplicitExpiryPolicy(t *testing.T) {
	t.Parallel()

	now := keyClock{}.Now()
	tests := []struct {
		name          string
		allowNoExpiry bool
		maximum       time.Duration
		intent        access.ExpiryIntent
		wantError     bool
	}{
		{name: "missing intent", maximum: time.Hour, wantError: true},
		{name: "past fixed expiry", maximum: time.Hour, intent: access.ExpiringAt(now), wantError: true},
		{name: "exceeds maximum", maximum: time.Hour, intent: access.ExpiringAt(now.Add(2 * time.Hour)), wantError: true},
		{name: "no expiry rejected", maximum: time.Hour, intent: access.WithoutExpiry(), wantError: true},
		{name: "no expiry allowed", allowNoExpiry: true, maximum: time.Hour, intent: access.WithoutExpiry()},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := &issuanceRepositoryStub{}
			issuer, _ := newTestIssuer(t, repository, test.allowNoExpiry, test.maximum, time.Minute)
			tenantID, _ := testKeyIDs(t)
			scope, err := tenant.NewScope(tenantID)
			if err != nil {
				t.Fatalf("NewScope() error = %v", err)
			}
			pattern, err := access.ParsePattern("tenant:read")
			if err != nil {
				t.Fatalf("ParsePattern() error = %v", err)
			}
			_, err = issuer.Issue(t.Context(), scope, access.IssueInput{
				Label: "backend", Patterns: []access.Pattern{pattern}, Expiry: test.intent,
			})
			if (err != nil) != test.wantError {
				t.Fatalf("Issue() error = %v, wantError = %t", err, test.wantError)
			}
			if test.wantError && len(repository.created) != 0 {
				t.Fatal("rejected issuance reached persistence")
			}
		})
	}
}

func TestIssuerDoesNotReturnCredentialWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	repository := &issuanceRepositoryStub{createErr: errors.New("database unavailable")}
	issuer, _ := newTestIssuer(t, repository, true, time.Hour, time.Minute)
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	issued, err := issuer.Issue(t.Context(), scope, access.IssueInput{
		Label: "backend", Patterns: []access.Pattern{pattern}, Expiry: access.WithoutExpiry(),
	})
	if err == nil {
		t.Fatal("Issue() error = nil")
	}
	if issued.Credential().Reveal() != "" || !issued.Key().ID().IsZero() {
		t.Fatal("failed persistence returned credential material")
	}
}

func TestIssuerRotateCreatesSuccessorWithBoundedOverlap(t *testing.T) {
	t.Parallel()

	repository := &issuanceRepositoryStub{}
	issuer, peppers := newTestIssuer(t, repository, false, 24*time.Hour, 30*time.Minute)
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := access.ParsePattern("verification_sessions:*")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	initial, err := issuer.Issue(t.Context(), scope, access.IssueInput{
		Label: "verification backend", Patterns: []access.Pattern{pattern},
		Expiry: access.ExpiringAt(keyClock{}.Now().Add(12 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("initial Issue() error = %v", err)
	}
	repository.found = initial.Key()
	overlap := 10 * time.Minute
	successor, err := issuer.Rotate(t.Context(), scope, access.RotateInput{
		KeyID: initial.Key().ID(), Expiry: access.ExpiringAt(keyClock{}.Now().Add(12 * time.Hour)), Overlap: overlap,
	})
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if repository.rotatedSuccessor.ID().String() != successor.Key().ID().String() ||
		successor.Key().ReplacesID().String() != initial.Key().ID().String() {
		t.Fatal("rotation successor or lineage was not persisted")
	}
	retirementAt := keyClock{}.Now().Add(overlap)
	if !repository.rotatedPredecessor.UsableAt(retirementAt.Add(-time.Nanosecond)) ||
		repository.rotatedPredecessor.StateAt(retirementAt) != access.KeyStateRetired {
		t.Fatal("predecessor does not honor the bounded overlap")
	}
	if successor.Key().Label() != initial.Key().Label() ||
		!successor.Key().Grant().Allows(access.PermissionVerificationSessionsCreate) {
		t.Fatal("rotation did not inherit label and immutable grant")
	}
	valid, err := peppers.Verify(successor.Credential(), successor.Key().PepperVersion(), successor.Key().Digest())
	if err != nil || !valid {
		t.Fatalf("Verify(successor) = %t, %v", valid, err)
	}
}

func TestIssuerRotateRejectsUnsafeTransitions(t *testing.T) {
	t.Parallel()

	now := keyClock{}.Now()
	tests := []struct {
		name    string
		overlap time.Duration
		expiry  time.Time
		mutate  func(*access.Key) error
	}{
		{name: "zero overlap", expiry: now.Add(time.Hour)},
		{name: "overlap above maximum", overlap: 31 * time.Minute, expiry: now.Add(time.Hour)},
		{name: "successor expires during overlap", overlap: 10 * time.Minute, expiry: now.Add(5 * time.Minute)},
		{name: "revoked predecessor", overlap: 10 * time.Minute, expiry: now.Add(time.Hour), mutate: func(key *access.Key) error { return key.Revoke(now) }},
		{name: "already rotating", overlap: 10 * time.Minute, expiry: now.Add(time.Hour), mutate: func(key *access.Key) error { return key.ScheduleRetirement(now, now.Add(20*time.Minute)) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := &issuanceRepositoryStub{}
			issuer, _ := newTestIssuer(t, repository, false, 24*time.Hour, 30*time.Minute)
			tenantID, keyID := testKeyIDs(t)
			scope, err := tenant.NewScope(tenantID)
			if err != nil {
				t.Fatalf("NewScope() error = %v", err)
			}
			record := testKeyRecord(t, now)
			record.ID = keyID
			record.TenantID = tenantID
			predecessor, err := access.RestoreKey(record)
			if err != nil {
				t.Fatalf("RestoreKey() error = %v", err)
			}
			if test.mutate != nil {
				if err := test.mutate(&predecessor); err != nil {
					t.Fatalf("mutate predecessor: %v", err)
				}
			}
			repository.found = predecessor
			_, err = issuer.Rotate(t.Context(), scope, access.RotateInput{
				KeyID: keyID, Expiry: access.ExpiringAt(test.expiry), Overlap: test.overlap,
			})
			if err == nil {
				t.Fatal("Rotate() error = nil")
			}
			if !repository.rotatedSuccessor.ID().IsZero() {
				t.Fatal("rejected rotation reached persistence")
			}
		})
	}
}

func TestNewIssuerRejectsMissingDependenciesAndConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := access.NewIssuer(nil, nil, nil, nil, nil, access.IssuerConfig{}); err == nil {
		t.Error("NewIssuer(missing) error = nil")
	}
}

type issuanceRepositoryStub struct {
	created            []access.Key
	found              access.Key
	rotatedPredecessor access.Key
	rotatedExpected    int64
	rotatedSuccessor   access.Key
	createErr          error
	findErr            error
	rotateErr          error
}

func (repository *issuanceRepositoryStub) Create(_ context.Context, _ tenant.Scope, key access.Key) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = append(repository.created, key)

	return nil
}

func (repository *issuanceRepositoryStub) Find(_ context.Context, _ tenant.Scope, _ id.APIKey) (access.Key, error) {
	if repository.findErr != nil {
		return access.Key{}, repository.findErr
	}

	return repository.found, nil
}

func (repository *issuanceRepositoryStub) Rotate(
	_ context.Context,
	_ tenant.Scope,
	predecessor access.Key,
	expectedVersion int64,
	successor access.Key,
) error {
	if repository.rotateErr != nil {
		return repository.rotateErr
	}
	repository.rotatedPredecessor = predecessor
	repository.rotatedExpected = expectedVersion
	repository.rotatedSuccessor = successor

	return nil
}

func newTestIssuer(
	t *testing.T,
	repository access.IssuanceRepository,
	allowNoExpiry bool,
	maximumLifetime time.Duration,
	maximumOverlap time.Duration,
) (*access.Issuer, *access.PepperSet) {
	t.Helper()

	identifiers, err := id.NewGenerator(keyClock{}, bytes.NewReader(bytes.Repeat([]byte{3}, 256)))
	if err != nil {
		t.Fatalf("id.NewGenerator() error = %v", err)
	}
	secrets, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0xaa}, 128)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{4}, 32)})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	expiryPolicy, err := access.NewExpiryPolicy(allowNoExpiry, &maximumLifetime)
	if err != nil {
		t.Fatalf("NewExpiryPolicy() error = %v", err)
	}
	rotationPolicy, err := access.NewRotationPolicy(maximumOverlap)
	if err != nil {
		t.Fatalf("NewRotationPolicy() error = %v", err)
	}
	issuer, err := access.NewIssuer(
		repository, identifiers, secrets, peppers, keyClock{},
		access.IssuerConfig{
			Registry: access.TenantRegistry(), ExpiryPolicy: expiryPolicy, RotationPolicy: rotationPolicy,
		},
	)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}

	return issuer, peppers
}
