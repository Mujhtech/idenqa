package policy_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type catalogCompiler struct {
	reference policy.EvaluatorReference
	err       error
	calls     int
}

func (compiler *catalogCompiler) CompileCanonical(
	ctx context.Context,
	_ []byte,
) (policy.EvaluatorReference, error) {
	compiler.calls++
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorReference{}, err
	}
	return compiler.reference, compiler.err
}

type catalogRepository struct {
	mu          sync.Mutex
	revisions   map[uint32]policy.Revision
	activation  policy.Activation
	appendErr   error
	activateErr error
}

func (repository *catalogRepository) AppendRevision(
	_ context.Context,
	_ tenant.Scope,
	revision policy.Revision,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.appendErr != nil {
		return repository.appendErr
	}
	repository.revisions[revision.Reference().Revision] = revision
	return nil
}

func (repository *catalogRepository) FindRevision(
	_ context.Context,
	_ tenant.Scope,
	_ id.Policy,
	revision uint32,
) (policy.Revision, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, ok := repository.revisions[revision]
	if !ok {
		return policy.Revision{}, policy.ErrRevisionNotFound
	}
	return stored, nil
}

func (repository *catalogRepository) Activate(
	_ context.Context,
	_ tenant.Scope,
	_ id.Policy,
	revision uint32,
	expectedVersion int64,
	actor id.APIKey,
	activatedAt time.Time,
) (policy.Activation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.activateErr != nil {
		return policy.Activation{}, repository.activateErr
	}
	stored, ok := repository.revisions[revision]
	if !ok {
		return policy.Activation{}, policy.ErrRevisionNotFound
	}
	previous := uint32(0)
	if repository.activation.Version() != 0 {
		if repository.activation.Version() != expectedVersion {
			return policy.Activation{}, policy.ErrActivationConflict
		}
		previous = repository.activation.Revision().Reference().Revision
	} else if expectedVersion != 0 {
		return policy.Activation{}, policy.ErrActivationConflict
	}
	activation, err := policy.RestoreActivation(stored, expectedVersion+1, previous, actor, activatedAt)
	if err == nil {
		repository.activation = activation
	}
	return activation, err
}

func (repository *catalogRepository) FindActive(
	_ context.Context,
	_ tenant.Scope,
	_ id.Policy,
) (policy.Activation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.activation.Version() == 0 {
		return policy.Activation{}, policy.ErrActivationNotFound
	}
	return repository.activation, nil
}

func TestCatalogRegisterActivateAndResolve(t *testing.T) {
	t.Parallel()
	scope, actor, policyID := catalogIdentity(t)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	compiler := &catalogCompiler{reference: policy.EvaluatorReference{
		Major: 1, Minor: 0, Digest: string(bytes.Repeat([]byte{'c'}, 64)),
	}}
	repository := &catalogRepository{revisions: make(map[uint32]policy.Revision)}
	catalog, err := policy.NewCatalog(repository, compiler)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := catalog.RegisterCanonical(t.Context(), scope, catalogCanonical(t), now)
	if err != nil || compiler.calls != 1 || revision.Reference().ID != policyID {
		t.Fatalf("register revision=%+v calls=%d error=%v", revision, compiler.calls, err)
	}
	activation, err := catalog.Activate(t.Context(), scope, policyID, 1, 0, actor, now.Add(time.Second))
	if err != nil || activation.Version() != 1 {
		t.Fatalf("activate version=%d error=%v", activation.Version(), err)
	}
	active, err := catalog.FindActive(t.Context(), scope, policyID)
	if err != nil || active.Revision().Reference() != revision.Reference() {
		t.Fatalf("active=%+v error=%v", active, err)
	}
}

func TestCatalogFailsClosedAtBoundaries(t *testing.T) {
	t.Parallel()
	scope, actor, policyID := catalogIdentity(t)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	repository := &catalogRepository{revisions: make(map[uint32]policy.Revision)}
	compiler := &catalogCompiler{reference: policy.EvaluatorReference{
		Major: 1, Minor: 0, Digest: string(bytes.Repeat([]byte{'d'}, 64)),
	}}
	catalog, err := policy.NewCatalog(repository, compiler)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := catalog.RegisterCanonical(canceled, scope, catalogCanonical(t), now); !errors.Is(err, context.Canceled) || compiler.calls != 0 {
		t.Fatalf("canceled register calls=%d error=%v", compiler.calls, err)
	}
	compiler.err = errors.New("unsafe expression")
	if _, err := catalog.RegisterCanonical(t.Context(), scope, catalogCanonical(t), now); err == nil {
		t.Fatal("compiler failure was accepted")
	}
	if len(repository.revisions) != 0 {
		t.Fatal("compiler failure reached persistence")
	}
	if _, err := catalog.Activate(t.Context(), scope, policyID, 1, -1, actor, now); !errors.Is(err, policy.ErrActivationConflict) {
		t.Fatalf("invalid activation error = %v", err)
	}
}

func catalogIdentity(t *testing.T) (tenant.Scope, id.APIKey, id.Policy) {
	t.Helper()
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := id.ParseAPIKey("key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	policyID, err := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	return scope, actor, policyID
}
