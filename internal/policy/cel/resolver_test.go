package policycel_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type revisionReader struct {
	mu        sync.Mutex
	revisions map[string]policy.Revision
	err       error
	started   chan struct{}
	release   chan struct{}
	calls     atomic.Int32
}

func (reader *revisionReader) FindRevision(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	revision uint32,
) (policy.Revision, error) {
	reader.calls.Add(1)
	if reader.started != nil {
		select {
		case reader.started <- struct{}{}:
		default:
		}
	}
	if reader.release != nil {
		select {
		case <-ctx.Done():
			return policy.Revision{}, ctx.Err()
		case <-reader.release:
		}
	}
	if reader.err != nil {
		return policy.Revision{}, reader.err
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	result, exists := reader.revisions[revisionKey(scope.ID(), policyID, revision)]
	if !exists {
		return policy.Revision{}, policy.ErrRevisionNotFound
	}
	return result, nil
}

func TestResolverEvaluatesExactRevisionAndCachesCompilation(t *testing.T) {
	t.Parallel()
	reader, snapshot := resolverFixture(t, baseDocument())
	resolver, err := policycel.NewResolver(reader, 2)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		output, err := resolver.Evaluate(t.Context(), snapshot)
		if err != nil || len(output.Results) != 1 || output.Results[0].Candidate != policy.DirectiveCompleteVerified {
			t.Fatalf("Evaluate() = %+v, %v", output, err)
		}
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("revision reads = %d, want 1", reader.calls.Load())
	}
}

func TestResolverDeduplicatesConcurrentColdResolution(t *testing.T) {
	t.Parallel()
	reader, snapshot := resolverFixture(t, baseDocument())
	reader.started = make(chan struct{}, 1)
	reader.release = make(chan struct{})
	resolver, err := policycel.NewResolver(reader, 2)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, evaluateErr := resolver.Evaluate(t.Context(), snapshot)
			errorsSeen <- evaluateErr
		}()
	}
	<-reader.started
	close(reader.release)
	wait.Wait()
	close(errorsSeen)
	for evaluateErr := range errorsSeen {
		if evaluateErr != nil {
			t.Fatal(evaluateErr)
		}
	}
	if reader.calls.Load() != 1 {
		t.Fatalf("concurrent revision reads = %d, want 1", reader.calls.Load())
	}
}

func TestResolverFollowerCancellationDoesNotCancelLeader(t *testing.T) {
	t.Parallel()
	reader, snapshot := resolverFixture(t, baseDocument())
	reader.started = make(chan struct{}, 1)
	reader.release = make(chan struct{})
	resolver, err := policycel.NewResolver(reader, 1)
	if err != nil {
		t.Fatal(err)
	}
	leaderResult := make(chan error, 1)
	go func() {
		_, evaluateErr := resolver.Evaluate(t.Context(), snapshot)
		leaderResult <- evaluateErr
	}()
	<-reader.started
	follower, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver.Evaluate(follower, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("follower error = %v", err)
	}
	close(reader.release)
	if err := <-leaderResult; err != nil {
		t.Fatalf("leader error = %v", err)
	}
}

func TestResolverLeaderCancellationAllowsFollowerRetry(t *testing.T) {
	t.Parallel()
	reader, snapshot := resolverFixture(t, baseDocument())
	reader.started = make(chan struct{}, 2)
	reader.release = make(chan struct{})
	resolver, err := policycel.NewResolver(reader, 1)
	if err != nil {
		t.Fatal(err)
	}
	leader, cancelLeader := context.WithCancel(t.Context())
	leaderResult := make(chan error, 1)
	go func() {
		_, evaluateErr := resolver.Evaluate(leader, snapshot)
		leaderResult <- evaluateErr
	}()
	<-reader.started
	followerResult := make(chan error, 1)
	go func() {
		_, evaluateErr := resolver.Evaluate(t.Context(), snapshot)
		followerResult <- evaluateErr
	}()
	cancelLeader()
	if err := <-leaderResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v", err)
	}
	<-reader.started
	close(reader.release)
	if err := <-followerResult; err != nil {
		t.Fatalf("follower retry error = %v", err)
	}
	if reader.calls.Load() != 2 {
		t.Fatalf("revision reads = %d, want 2", reader.calls.Load())
	}
}

func TestResolverEvictsLeastRecentlyUsedRevision(t *testing.T) {
	t.Parallel()
	documentOne := baseDocument()
	reader, snapshotOne := resolverFixture(t, documentOne)
	documentTwo := baseDocument()
	documentTwo.Revision = 4
	canonicalTwo, err := policyv1.Canonical(documentTwo)
	if err != nil {
		t.Fatal(err)
	}
	revisionTwo, err := policy.NewRevisionCanonical(
		canonicalTwo,
		(policycel.Compiler{}).Reference(),
		time.Date(2026, time.August, 31, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	evaluatorTwo, err := policycel.ParseCanonical(canonicalTwo)
	if err != nil {
		t.Fatal(err)
	}
	snapshotTwo := snapshotFor(t, evaluatorTwo, documentTwo, policy.RequirementSatisfied)
	reader.revisions[revisionKey(snapshotTwo.TenantID(), snapshotTwo.Policy().ID, snapshotTwo.Policy().Revision)] = revisionTwo
	resolver, err := policycel.NewResolver(reader, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []policy.Snapshot{snapshotOne, snapshotTwo, snapshotOne} {
		if _, err := resolver.Evaluate(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if reader.calls.Load() != 3 {
		t.Fatalf("revision reads = %d, want 3 after eviction", reader.calls.Load())
	}
}

func TestResolverCacheIsTenantScoped(t *testing.T) {
	t.Parallel()
	reader, snapshot := resolverFixture(t, baseDocument())
	otherTenant, _ := id.ParseTenant("ten_01K3P4NQF00000000000000009")
	input := snapshotInput(t, snapshot.Policy(), snapshot.Evaluator(), policy.RequirementSatisfied)
	input.TenantID = otherTenant
	otherSnapshot, err := policy.NewSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	reader.revisions[revisionKey(otherTenant, snapshot.Policy().ID, snapshot.Policy().Revision)] =
		reader.revisions[revisionKey(snapshot.TenantID(), snapshot.Policy().ID, snapshot.Policy().Revision)]
	resolver, err := policycel.NewResolver(reader, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []policy.Snapshot{snapshot, otherSnapshot} {
		if _, err := resolver.Evaluate(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	if reader.calls.Load() != 2 {
		t.Fatalf("cross-tenant revision reads = %d, want 2", reader.calls.Load())
	}
}

func TestResolverFailsClosedOnIdentityAndReaderErrors(t *testing.T) {
	t.Parallel()
	reader, snapshot := resolverFixture(t, baseDocument())
	resolver, err := policycel.NewResolver(reader, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader.err = errors.New("catalog unavailable")
	if _, err := resolver.Evaluate(t.Context(), snapshot); !errors.Is(err, policycel.ErrResolve) {
		t.Fatalf("reader error = %v", err)
	}
	reader.err = nil
	reader.mu.Lock()
	revision := reader.revisions[revisionKey(snapshot.TenantID(), snapshot.Policy().ID, snapshot.Policy().Revision)]
	delete(reader.revisions, revisionKey(snapshot.TenantID(), snapshot.Policy().ID, snapshot.Policy().Revision))
	otherDocument := baseDocument()
	otherDocument.Revision++
	canonical, canonicalErr := policyv1.Canonical(otherDocument)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	other, restoreErr := policy.NewRevisionCanonical(canonical, revision.Evaluator(), revision.CreatedAt())
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	reader.revisions[revisionKey(snapshot.TenantID(), snapshot.Policy().ID, snapshot.Policy().Revision)] = other
	reader.mu.Unlock()
	if _, err := resolver.Evaluate(t.Context(), snapshot); !errors.Is(err, policycel.ErrRevisionMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	var nilResolver *policycel.Resolver
	if nilResolver.Reference() != (policy.EvaluatorReference{}) {
		t.Fatal("nil resolver reference is non-zero")
	}
	if _, err := nilResolver.Evaluate(t.Context(), snapshot); !errors.Is(err, policycel.ErrResolve) {
		t.Fatalf("nil resolver error = %v", err)
	}
}

func TestNewResolverRequiresBoundedConfiguration(t *testing.T) {
	t.Parallel()
	reader := &revisionReader{}
	for _, capacity := range []int{-1, 0, 4097} {
		if _, err := policycel.NewResolver(reader, capacity); err == nil {
			t.Fatalf("NewResolver(capacity=%d) error = nil", capacity)
		}
	}
	if _, err := policycel.NewResolver(nil, 1); err == nil {
		t.Fatal("NewResolver(nil) error = nil")
	}
}

func resolverFixture(t testing.TB, document policyv1.Document) (*revisionReader, policy.Snapshot) {
	t.Helper()
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	revision, err := policy.NewRevisionCanonical(canonical, (policycel.Compiler{}).Reference(), createdAt)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := policycel.ParseCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotFor(t, evaluator, document, policy.RequirementSatisfied)
	reader := &revisionReader{revisions: map[string]policy.Revision{
		revisionKey(snapshot.TenantID(), snapshot.Policy().ID, snapshot.Policy().Revision): revision,
	}}
	return reader, snapshot
}

func revisionKey(tenantID id.Tenant, policyID id.Policy, revision uint32) string {
	return fmt.Sprintf("%s:%s:%d", tenantID.String(), policyID.String(), revision)
}
