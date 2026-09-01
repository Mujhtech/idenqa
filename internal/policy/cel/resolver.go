package policycel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maximumCachedEvaluators = 4096

var (
	// ErrResolve means an exact compiled policy evaluator could not be resolved.
	ErrResolve = errors.New("policy cel: evaluator resolution failed")
	// ErrRevisionMismatch means durable revision meaning differs from the snapshot.
	ErrRevisionMismatch = errors.New("policy cel: durable revision mismatch")
)

// RevisionReader is the narrow immutable catalog boundary consumed by Resolver.
type RevisionReader interface {
	FindRevision(context.Context, tenant.Scope, id.Policy, uint32) (policy.Revision, error)
}

type evaluatorKey struct {
	tenantID       id.Tenant
	policyID       string
	revision       uint32
	schemaMajor    uint16
	schemaMinor    uint16
	policyDigest   string
	evaluatorMajor uint16
	evaluatorMinor uint16
	evaluatorHash  string
}

type cacheEntry struct {
	key       evaluatorKey
	evaluator *Evaluator
	newer     *cacheEntry
	older     *cacheEntry
}

type resolution struct {
	done      chan struct{}
	evaluator *Evaluator
	err       error
}

// Resolver is a bounded concurrency-safe exact-revision CEL evaluator. It
// implements policy.Evaluator without exposing CEL or cache types upstream.
type Resolver struct {
	revisions RevisionReader
	capacity  int

	mu       sync.Mutex
	entries  map[evaluatorKey]*cacheEntry
	inflight map[evaluatorKey]*resolution
	newest   *cacheEntry
	oldest   *cacheEntry
}

var _ policy.Evaluator = (*Resolver)(nil)

// NewResolver constructs a bounded exact-revision evaluator cache. Capacity
// is required explicitly so composition owns its memory budget.
func NewResolver(revisions RevisionReader, capacity int) (*Resolver, error) {
	if revisions == nil || capacity < 1 || capacity > maximumCachedEvaluators {
		return nil, errors.New("policy cel resolver: revision reader and valid capacity are required")
	}
	return &Resolver{
		revisions: revisions,
		capacity:  capacity,
		entries:   make(map[evaluatorKey]*cacheEntry, capacity),
		inflight:  make(map[evaluatorKey]*resolution),
	}, nil
}

// Reference pins the selected CEL implementation independently of a policy.
func (resolver *Resolver) Reference() policy.EvaluatorReference {
	if resolver == nil {
		return policy.EvaluatorReference{}
	}
	return evaluatorReference()
}

// Evaluate resolves and compiles the exact immutable revision pinned by the
// snapshot. It never follows the mutable active-policy pointer.
func (resolver *Resolver) Evaluate(
	ctx context.Context,
	snapshot policy.Snapshot,
) (policy.EvaluatorOutput, error) {
	if resolver == nil {
		return policy.EvaluatorOutput{}, ErrResolve
	}
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorOutput{}, err
	}
	if snapshot.Evaluator() != resolver.Reference() {
		return policy.EvaluatorOutput{}, ErrPolicyMismatch
	}
	key := keyFor(snapshot)
	evaluator, err := resolver.resolve(ctx, key, snapshot.Policy())
	if err != nil {
		return policy.EvaluatorOutput{}, err
	}
	return evaluator.Evaluate(ctx, snapshot)
}

func (resolver *Resolver) resolve(
	ctx context.Context,
	key evaluatorKey,
	reference policy.Reference,
) (*Evaluator, error) {
	for {
		resolver.mu.Lock()
		if entry := resolver.entries[key]; entry != nil {
			resolver.promote(entry)
			evaluator := entry.evaluator
			resolver.mu.Unlock()
			return evaluator, nil
		}
		if pending := resolver.inflight[key]; pending != nil {
			done := pending.done
			resolver.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
			}
			if pending.err == nil {
				return pending.evaluator, nil
			}
			if (errors.Is(pending.err, context.Canceled) ||
				errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				continue
			}
			return nil, pending.err
		}
		pending := &resolution{done: make(chan struct{})}
		resolver.inflight[key] = pending
		resolver.mu.Unlock()

		evaluator, err := resolver.load(ctx, key, reference)

		resolver.mu.Lock()
		pending.evaluator, pending.err = evaluator, err
		if err == nil {
			resolver.insert(key, evaluator)
		}
		delete(resolver.inflight, key)
		close(pending.done)
		resolver.mu.Unlock()
		return evaluator, err
	}
}

func (resolver *Resolver) load(
	ctx context.Context,
	key evaluatorKey,
	reference policy.Reference,
) (*Evaluator, error) {
	scope, err := tenant.NewScope(key.tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant scope: %w", ErrResolve, err)
	}
	revision, err := resolver.revisions.FindRevision(ctx, scope, reference.ID, reference.Revision)
	if err != nil {
		return nil, fmt.Errorf("%w: find policy revision: %w", ErrResolve, err)
	}
	if revision.Reference() != reference || revision.Evaluator() != resolver.Reference() {
		return nil, ErrRevisionMismatch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	evaluator, err := ParseCanonical(revision.Canonical())
	if err != nil {
		return nil, fmt.Errorf("%w: compile durable policy revision: %w", ErrResolve, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if evaluator.PolicyDigest() != key.policyDigest || evaluator.Reference() != resolver.Reference() {
		return nil, ErrRevisionMismatch
	}
	return evaluator, nil
}

func keyFor(snapshot policy.Snapshot) evaluatorKey {
	reference, evaluator := snapshot.Policy(), snapshot.Evaluator()
	return evaluatorKey{
		tenantID: snapshot.TenantID(), policyID: reference.ID.String(),
		revision: reference.Revision, schemaMajor: reference.SchemaMajor,
		schemaMinor: reference.SchemaMinor, policyDigest: reference.Digest,
		evaluatorMajor: evaluator.Major, evaluatorMinor: evaluator.Minor,
		evaluatorHash: evaluator.Digest,
	}
}

func (resolver *Resolver) insert(key evaluatorKey, evaluator *Evaluator) {
	if existing := resolver.entries[key]; existing != nil {
		existing.evaluator = evaluator
		resolver.promote(existing)
		return
	}
	entry := &cacheEntry{key: key, evaluator: evaluator, older: resolver.newest}
	if resolver.newest != nil {
		resolver.newest.newer = entry
	}
	resolver.newest = entry
	if resolver.oldest == nil {
		resolver.oldest = entry
	}
	resolver.entries[key] = entry
	if len(resolver.entries) <= resolver.capacity {
		return
	}
	evicted := resolver.oldest
	resolver.oldest = evicted.newer
	resolver.oldest.older = nil
	delete(resolver.entries, evicted.key)
}

func (resolver *Resolver) promote(entry *cacheEntry) {
	if entry == resolver.newest {
		return
	}
	if entry.older != nil {
		entry.older.newer = entry.newer
	}
	if entry.newer != nil {
		entry.newer.older = entry.older
	}
	if entry == resolver.oldest {
		resolver.oldest = entry.newer
	}
	entry.older = resolver.newest
	entry.newer = nil
	resolver.newest.newer = entry
	resolver.newest = entry
}
