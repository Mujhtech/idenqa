package policy_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestReaderSeparatesReadAndExportAuthority(t *testing.T) {
	t.Parallel()
	decision := verifiedDecision(t)
	tests := []struct {
		name    string
		pattern access.Pattern
		read    bool
		export  bool
	}{
		{name: "read only", pattern: "decisions:read", read: true},
		{name: "export only", pattern: "decisions:export", export: true},
		{name: "both", pattern: "decisions:*", read: true, export: true},
		{name: "neither", pattern: "tenant:read"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authority := readerAuthority(t, test.pattern)
			repository := &decisionRepositoryStub{decision: decision}
			reader, err := policy.NewReader(repository)
			if err != nil {
				t.Fatal(err)
			}
			report, readErr := reader.Find(t.Context(), authority, decision.ID())
			if (readErr == nil) != test.read {
				t.Fatalf("Find() report = %+v, error = %v", report, readErr)
			}
			bundle, _, exportErr := reader.Export(t.Context(), authority, decision.ID())
			if (exportErr == nil) != test.export {
				t.Fatalf("Export() bundle digest = %q, error = %v", bundle.Digest(), exportErr)
			}
			if !test.read && !errors.Is(readErr, access.ErrInsufficientScope) {
				t.Fatalf("Find() denial = %v", readErr)
			}
			if !test.export && !errors.Is(exportErr, access.ErrInsufficientScope) {
				t.Fatalf("Export() denial = %v", exportErr)
			}
		})
	}
}

func TestReaderFindAndFindLatestPropagateScopeContextAndReproduce(t *testing.T) {
	t.Parallel()
	decision := verifiedDecision(t)
	repository := &decisionRepositoryStub{decision: decision}
	reader, err := policy.NewReader(repository)
	if err != nil {
		t.Fatal(err)
	}
	authority := readerAuthority(t, "decisions:read")
	ctx := context.WithValue(t.Context(), readerContextKey{}, "unchanged")
	report, err := reader.Find(ctx, authority, decision.ID())
	if err != nil {
		t.Fatal(err)
	}
	latest, err := reader.FindLatest(ctx, authority, decision.Snapshot().VerificationID())
	if err != nil {
		t.Fatal(err)
	}
	if report != latest || !report.Reproduced || report.DecisionDigest != decision.Digest() {
		t.Fatal("read report does not preserve reproduced decision meaning")
	}
	if repository.contextValue != "unchanged" || repository.scope.ID().String() != authority.TenantScope().ID().String() ||
		repository.decisionID.String() != decision.ID().String() ||
		repository.verificationID.String() != decision.Snapshot().VerificationID().String() {
		t.Fatal("reader did not propagate exact context, tenant scope, or identifier")
	}
}

func TestReaderPreservesClassifiedFailuresAndRejectsInvalidState(t *testing.T) {
	t.Parallel()
	decision := verifiedDecision(t)
	authority := readerAuthority(t, "decisions:*")
	tests := []struct {
		name       string
		repository policy.Repository
		want       error
	}{
		{name: "not found", repository: &decisionRepositoryStub{err: policy.ErrDecisionNotFound}, want: policy.ErrDecisionNotFound},
		{name: "invalid restored decision", repository: &decisionRepositoryStub{}, want: policy.ErrReproduction},
		{name: "repository failure", repository: &decisionRepositoryStub{err: errors.New("unavailable")}, want: errors.New("unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader, err := policy.NewReader(test.repository)
			if err != nil {
				t.Fatal(err)
			}
			_, err = reader.Find(t.Context(), authority, decision.ID())
			if test.name == "repository failure" {
				if err == nil || !strings.Contains(err.Error(), "read policy decision: unavailable") {
					t.Fatalf("Find() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Find() error = %v, want %v", err, test.want)
			}
		})
	}
	var reader *policy.Reader
	if _, err := reader.Find(t.Context(), authority, decision.ID()); err == nil {
		t.Fatal("nil Reader.Find() error = nil")
	}
}

func TestNewReaderRejectsNilRepository(t *testing.T) {
	t.Parallel()
	if _, err := policy.NewReader(nil); err == nil {
		t.Fatal("NewReader(nil) error = nil")
	}
}

func TestReaderRejectsZeroIdentifiersBeforePersistence(t *testing.T) {
	t.Parallel()
	repository := &decisionRepositoryStub{err: errors.New("repository must not be called")}
	reader, err := policy.NewReader(repository)
	if err != nil {
		t.Fatal(err)
	}
	authority := readerAuthority(t, "decisions:*")
	if _, err := reader.Find(t.Context(), authority, id.Decision{}); !errors.Is(err, policy.ErrDecisionNotFound) {
		t.Fatalf("Find(zero) error = %v", err)
	}
	if _, err := reader.FindLatest(t.Context(), authority, id.Verification{}); !errors.Is(err, policy.ErrDecisionNotFound) {
		t.Fatalf("FindLatest(zero) error = %v", err)
	}
	if _, _, err := reader.Export(t.Context(), authority, id.Decision{}); !errors.Is(err, policy.ErrDecisionNotFound) {
		t.Fatalf("Export(zero) error = %v", err)
	}
	if repository.called {
		t.Fatal("zero identifier reached repository")
	}
}

type readerContextKey struct{}

type decisionRepositoryStub struct {
	decision       policy.Decision
	err            error
	contextValue   string
	called         bool
	scope          tenant.Scope
	decisionID     id.Decision
	verificationID id.Verification
}

func (repository *decisionRepositoryStub) Append(context.Context, tenant.Scope, policy.Decision) error {
	return errors.New("unexpected append")
}

func (repository *decisionRepositoryStub) Find(
	ctx context.Context,
	scope tenant.Scope,
	decisionID id.Decision,
) (policy.Decision, error) {
	repository.contextValue, _ = ctx.Value(readerContextKey{}).(string)
	repository.called, repository.scope, repository.decisionID = true, scope, decisionID
	return repository.decision, repository.err
}

func (repository *decisionRepositoryStub) FindLatest(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
) (policy.Decision, error) {
	repository.contextValue, _ = ctx.Value(readerContextKey{}).(string)
	repository.called, repository.scope, repository.verificationID = true, scope, verificationID
	return repository.decision, repository.err
}

type readerClock struct{ now time.Time }

func (source readerClock) Now() time.Time { return source.now }

type readerVerificationRepository struct{ record access.VerificationRecord }

func (repository readerVerificationRepository) FindForVerification(
	context.Context,
	id.Tenant,
	id.APIKey,
) (access.VerificationRecord, error) {
	return repository.record, nil
}

func readerAuthority(t testing.TB, pattern access.Pattern) access.Context {
	t.Helper()
	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(readerClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{7}, 256)))
	if err != nil {
		t.Fatal(err)
	}
	tenantID, _ := identifiers.NewTenant()
	keyID, _ := identifiers.NewAPIKey()
	generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{8}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := generator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatal(err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{9}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatal(err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "policy reader", Digest: digest,
		PepperVersion: version, Grant: grant, Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(readerVerificationRepository{record: record}, peppers, readerClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := authenticator.Authenticate(t.Context(), presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	return authority
}
