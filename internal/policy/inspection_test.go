package policy_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestCatalogInspectorPagesRevisionAndActivationMetadata(t *testing.T) {
	t.Parallel()
	scope, _, policyID := catalogIdentity(t)
	repository := inspectionRepository{
		revisions: []policy.RevisionMetadata{
			revisionMetadataFixture(t, policyID, 3),
			revisionMetadataFixture(t, policyID, 2),
			revisionMetadataFixture(t, policyID, 1),
		},
		activations: []policy.ActivationMetadata{
			activationMetadataFixture(t, policyID, 3, 3, 2),
			activationMetadataFixture(t, policyID, 2, 2, 1),
			activationMetadataFixture(t, policyID, 1, 1, 0),
		},
	}
	inspector, err := policy.NewCatalogInspector(&repository)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := inspector.ListRevisions(t.Context(), scope, policyID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !revisions.HasMore() || revisions.NextBefore() != 2 || len(revisions.Items()) != 2 ||
		revisions.Items()[0].Reference().Revision != 3 {
		t.Fatalf("revision page = %+v", revisionNumbers(revisions.Items()))
	}
	lastRevisions, err := inspector.ListRevisions(
		t.Context(), scope, policyID, revisions.NextBefore(), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lastRevisions.HasMore() || lastRevisions.NextBefore() != 0 ||
		!slices.Equal(revisionNumbers(lastRevisions.Items()), []uint32{1}) {
		t.Fatalf("last revision page = %+v", revisionNumbers(lastRevisions.Items()))
	}

	activations, err := inspector.ListActivations(t.Context(), scope, policyID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !activations.HasMore() || activations.NextBefore() != 2 ||
		!slices.Equal(activationVersions(activations.Items()), []int64{3, 2}) {
		t.Fatalf("activation page = %+v", activationVersions(activations.Items()))
	}
	lastActivations, err := inspector.ListActivations(
		t.Context(), scope, policyID, activations.NextBefore(), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lastActivations.HasMore() || lastActivations.NextBefore() != 0 ||
		!slices.Equal(activationVersions(lastActivations.Items()), []int64{1}) {
		t.Fatalf("last activation page = %+v", activationVersions(lastActivations.Items()))
	}
}

func TestCatalogInspectorFailsClosedAtBoundaries(t *testing.T) {
	t.Parallel()
	scope, _, policyID := catalogIdentity(t)
	otherID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	repositoryFailure := errors.New("catalog unavailable")
	tests := []struct {
		name             string
		repository       policy.CatalogInspectionRepository
		revisions        bool
		beforeActivation int64
		limit            int
		is               error
	}{
		{name: "zero limit", repository: &inspectionRepository{}, revisions: true, is: policy.ErrInvalid},
		{name: "oversized limit", repository: &inspectionRepository{}, revisions: true, limit: policy.MaximumCatalogPageSize + 1, is: policy.ErrInvalid},
		{name: "negative activation boundary", repository: &inspectionRepository{}, beforeActivation: -1, limit: 1, is: policy.ErrInvalid},
		{name: "revision repository failure", repository: &inspectionRepository{err: repositoryFailure}, revisions: true, limit: 1, is: repositoryFailure},
		{name: "activation repository failure", repository: &inspectionRepository{err: repositoryFailure}, limit: 1, is: repositoryFailure},
		{name: "wrong revision policy", repository: &inspectionRepository{revisions: []policy.RevisionMetadata{revisionMetadataFixture(t, otherID, 1)}}, revisions: true, limit: 1, is: policy.ErrRevisionConflict},
		{name: "wrong activation policy", repository: &inspectionRepository{activations: []policy.ActivationMetadata{activationMetadataFixture(t, otherID, 1, 1, 0)}}, limit: 1, is: policy.ErrActivationConflict},
		{name: "unordered revisions", repository: &inspectionRepository{revisions: []policy.RevisionMetadata{revisionMetadataFixture(t, policyID, 1), revisionMetadataFixture(t, policyID, 2)}}, revisions: true, limit: 2, is: policy.ErrRevisionConflict},
		{name: "unordered activations", repository: &inspectionRepository{activations: []policy.ActivationMetadata{activationMetadataFixture(t, policyID, 1, 1, 0), activationMetadataFixture(t, policyID, 2, 2, 1)}}, limit: 2, is: policy.ErrActivationConflict},
		{name: "oversized repository response", repository: &inspectionRepository{revisions: []policy.RevisionMetadata{revisionMetadataFixture(t, policyID, 3), revisionMetadataFixture(t, policyID, 2), revisionMetadataFixture(t, policyID, 1)}, ignoreLimit: true}, revisions: true, limit: 1, is: policy.ErrRevisionConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			inspector, err := policy.NewCatalogInspector(test.repository)
			if err != nil {
				t.Fatal(err)
			}
			if test.revisions {
				_, err = inspector.ListRevisions(t.Context(), scope, policyID, 0, test.limit)
			} else {
				_, err = inspector.ListActivations(t.Context(), scope, policyID, test.beforeActivation, test.limit)
			}
			if !errors.Is(err, test.is) {
				t.Fatalf("inspection error = %v, want %v", err, test.is)
			}
		})
	}
}

func TestCatalogInspectorPropagatesCancellationWithoutRepositoryRead(t *testing.T) {
	t.Parallel()
	scope, _, policyID := catalogIdentity(t)
	repository := &inspectionRepository{}
	inspector, err := policy.NewCatalogInspector(repository)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspector.ListRevisions(ctx, scope, policyID, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRevisions() error = %v", err)
	}
	if _, err := inspector.ListActivations(ctx, scope, policyID, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListActivations() error = %v", err)
	}
	if repository.Calls() != 0 {
		t.Fatalf("repository calls = %d", repository.Calls())
	}
}

func TestCatalogInspectionPagesOwnReturnedSlices(t *testing.T) {
	t.Parallel()
	scope, _, policyID := catalogIdentity(t)
	repository := &inspectionRepository{
		revisions:   []policy.RevisionMetadata{revisionMetadataFixture(t, policyID, 1)},
		activations: []policy.ActivationMetadata{activationMetadataFixture(t, policyID, 1, 1, 0)},
	}
	inspector, err := policy.NewCatalogInspector(repository)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := inspector.ListRevisions(t.Context(), scope, policyID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	items := revisions.Items()
	items[0] = policy.RevisionMetadata{}
	if revisions.Items()[0].Reference().ID.IsZero() {
		t.Fatal("revision page exposed mutable slice")
	}
	activations, err := inspector.ListActivations(t.Context(), scope, policyID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	activationItems := activations.Items()
	activationItems[0] = policy.ActivationMetadata{}
	if activations.Items()[0].Version() == 0 {
		t.Fatal("activation page exposed mutable slice")
	}
}

func TestRestoreInspectionMetadataRejectsMalformedDurableMeaning(t *testing.T) {
	t.Parallel()
	_, actor, policyID := catalogIdentity(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	reference := policy.Reference{
		ID: policyID, Revision: 1, SchemaMajor: 1, SchemaMinor: 0,
		Digest: strings.Repeat("a", 64),
	}
	evaluator := policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("b", 64)}
	metadata, err := policy.RestoreRevisionMetadata(reference, evaluator, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.RestoreRevisionMetadata(policy.Reference{}, evaluator, now); !errors.Is(err, policy.ErrRevisionConflict) {
		t.Fatalf("invalid revision error = %v", err)
	}
	if _, err := policy.RestoreActivationMetadata(metadata, 0, 0, actor, now); !errors.Is(err, policy.ErrActivationConflict) {
		t.Fatalf("invalid activation error = %v", err)
	}
	if _, err := policy.RestoreActivationMetadata(metadata, 1, 1, actor, now); !errors.Is(err, policy.ErrActivationConflict) {
		t.Fatalf("self-previous activation error = %v", err)
	}
}

func TestNewCatalogInspectorRequiresRepository(t *testing.T) {
	t.Parallel()
	if _, err := policy.NewCatalogInspector(nil); err == nil {
		t.Fatal("NewCatalogInspector(nil) succeeded")
	}
	var inspector *policy.CatalogInspector
	if _, err := inspector.ListRevisions(t.Context(), tenant.Scope{}, id.Policy{}, 0, 1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("nil ListRevisions() error = %v", err)
	}
}

type inspectionRepository struct {
	mu          sync.Mutex
	revisions   []policy.RevisionMetadata
	activations []policy.ActivationMetadata
	err         error
	calls       int
	ignoreLimit bool
}

func (repository *inspectionRepository) ListRevisionMetadata(
	_ context.Context,
	_ tenant.Scope,
	_ id.Policy,
	before uint32,
	limit int,
) ([]policy.RevisionMetadata, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls++
	if repository.err != nil {
		return nil, repository.err
	}
	if repository.ignoreLimit {
		return slices.Clone(repository.revisions), nil
	}
	return takeRevisionMetadata(repository.revisions, before, limit), nil
}

func (repository *inspectionRepository) ListActivationMetadata(
	_ context.Context,
	_ tenant.Scope,
	_ id.Policy,
	before int64,
	limit int,
) ([]policy.ActivationMetadata, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls++
	if repository.err != nil {
		return nil, repository.err
	}
	return takeActivationMetadata(repository.activations, before, limit), nil
}

func (repository *inspectionRepository) Calls() int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.calls
}

func takeRevisionMetadata(
	values []policy.RevisionMetadata,
	before uint32,
	limit int,
) []policy.RevisionMetadata {
	result := make([]policy.RevisionMetadata, 0, limit)
	for _, value := range values {
		if before != 0 && value.Reference().Revision >= before {
			continue
		}
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func takeActivationMetadata(
	values []policy.ActivationMetadata,
	before int64,
	limit int,
) []policy.ActivationMetadata {
	result := make([]policy.ActivationMetadata, 0, limit)
	for _, value := range values {
		if before != 0 && value.Version() >= before {
			continue
		}
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func revisionMetadataFixture(
	t testing.TB,
	policyID id.Policy,
	revision uint32,
) policy.RevisionMetadata {
	t.Helper()
	metadata, err := policy.RestoreRevisionMetadata(
		policy.Reference{
			ID: policyID, Revision: revision, SchemaMajor: 1, SchemaMinor: 0,
			Digest: strings.Repeat("a", 64),
		},
		policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("e", 64)},
		time.Date(2026, time.September, 1, 12, int(revision), 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}

func activationMetadataFixture(
	t testing.TB,
	policyID id.Policy,
	version int64,
	revision uint32,
	previous uint32,
) policy.ActivationMetadata {
	t.Helper()
	actor, err := id.ParseAPIKey("key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	revisionMetadata := revisionMetadataFixture(t, policyID, revision)
	metadata, err := policy.RestoreActivationMetadata(
		revisionMetadata, version, previous, actor,
		revisionMetadata.CreatedAt().Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}

func revisionNumbers(values []policy.RevisionMetadata) []uint32 {
	result := make([]uint32, len(values))
	for index, value := range values {
		result[index] = value.Reference().Revision
	}
	return result
}

func activationVersions(values []policy.ActivationMetadata) []int64 {
	result := make([]int64, len(values))
	for index, value := range values {
		result[index] = value.Version()
	}
	return result
}
