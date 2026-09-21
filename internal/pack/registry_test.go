package pack

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type memoryStore struct {
	states  []StoredState
	history []Change
	fail    bool
	applied [][]Change
}

func (store *memoryStore) LoadStates(context.Context) ([]StoredState, error) {
	return slices.Clone(store.states), nil
}

func (store *memoryStore) LoadHistory(_ context.Context, country string) ([]Change, error) {
	result := make([]Change, 0)
	for _, change := range store.history {
		if change.Country == country {
			result = append(result, change)
		}
	}
	return result, nil
}

func (store *memoryStore) Apply(_ context.Context, changes []Change) error {
	if store.fail {
		return errors.New("store unavailable")
	}
	store.applied = append(store.applied, slices.Clone(changes))
	for _, change := range changes {
		store.history = append(store.history, change)
		updated := false
		for index := range store.states {
			state := &store.states[index]
			if state.Country == change.Country && state.Revision == change.Revision {
				state.State = change.State
				state.Version = change.TransitionVersion
				state.UpdatedAt = change.RecordedAt
				updated = true
				break
			}
		}
		if !updated {
			store.states = append(store.states, StoredState{
				Country: change.Country, Revision: change.Revision, State: change.State,
				Digest: change.PackDigest, Version: change.TransitionVersion, UpdatedAt: change.RecordedAt,
			})
		}
	}
	return nil
}

func fixedNow() time.Time { return time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC) }

func newTestRegistry(t *testing.T, store Store, packs ...Pack) *Registry {
	t.Helper()
	registry, err := NewRegistry(packs, store, fixedNow)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func TestRegistryResolvesActivePackAndSupport(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t, nil, mustCanonical(t, testPack(1, LifecycleActive)))
	projection, ok := registry.Support("nga", "drivers_license")
	if !ok {
		t.Fatal("Support() did not resolve the canonical driver-licence type")
	}
	if projection.DocumentType != DocumentTypeDriverLicence || projection.SupportLevel != SupportUnsupported ||
		projection.LifecycleState != LifecycleActive || projection.LegalReview.State != LegalReviewNotReviewed {
		t.Fatalf("projection = %+v", projection)
	}
	if len(projection.RequirementSlots) == 0 || projection.Authority != "idenqa.requirement.authority" {
		t.Fatalf("projection lost requirement slots: %+v", projection)
	}
	if _, ok := registry.Support("XX", "passport"); ok {
		t.Fatal("Support() resolved an unassigned country")
	}
	if _, ok := registry.Support("NG", "residence_permit"); ok {
		t.Fatal("Support() resolved a packless document type")
	}
	if _, err := registry.Active("GH"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Active(GH) error = %v, want ErrNotFound", err)
	}
}

func TestRegistryLifecycleTransitionsRecordHistory(t *testing.T) {
	t.Parallel()

	store := &memoryStore{}
	first := mustCanonical(t, testPack(1, LifecycleActive))
	second := mustCanonical(t, testPack(2, LifecycleDraft))
	registry := newTestRegistry(t, store, first, second)

	entry, err := registry.Activate(t.Context(), Transition{
		Country: "NG", Revision: 2, ExpectedVersion: 0, Reason: "reviewed_revision", Actor: "operator",
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if entry.State != LifecycleActive || entry.Version != 1 {
		t.Fatalf("activated entry = %+v", entry)
	}
	deprecated, err := registry.Get("NG", 1)
	if err != nil || deprecated.State != LifecycleDeprecated {
		t.Fatalf("previous revision = %+v, err = %v", deprecated, err)
	}
	if active, err := registry.Active("NG"); err != nil || active.Pack.Revision != 2 {
		t.Fatalf("active entry = %+v, err = %v", active, err)
	}

	if _, err := registry.Retire(t.Context(), Transition{
		Country: "NG", Revision: 1, ExpectedVersion: 1, Reason: "superseded", Actor: "operator",
	}); err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	if _, err := registry.Deprecate(t.Context(), Transition{
		Country: "NG", Revision: 2, ExpectedVersion: 2, Reason: "withdrawn", Actor: "operator",
	}); err != nil {
		t.Fatalf("Deprecate() error = %v", err)
	}
	if _, err := registry.Activate(t.Context(), Transition{
		Country: "NG", Revision: 2, ExpectedVersion: 3, Reason: "restored", Actor: "operator",
	}); err != nil {
		t.Fatalf("re-Activate() error = %v", err)
	}
	history := registry.History("NG")
	if len(history) != 5 {
		t.Fatalf("history length = %d, want 5: %+v", len(history), history)
	}
	if history[0].Operation != OperationDeprecate || history[0].Revision != 1 ||
		history[1].Operation != OperationActivate || history[1].Revision != 2 {
		t.Fatalf("activation history = %+v", history[:2])
	}
	if history[2].Operation != OperationRetire || history[3].Operation != OperationDeprecate ||
		history[4].Operation != OperationActivate {
		t.Fatalf("remaining history = %+v", history[2:])
	}
	for _, change := range history {
		if change.Actor != "operator" || change.RecordedAt != fixedNow().Truncate(time.Microsecond) {
			t.Fatalf("history attribution = %+v", change)
		}
	}
	if len(store.applied) != 4 || len(store.applied[0]) != 2 {
		t.Fatalf("store applications = %d, first = %+v", len(store.applied), store.applied)
	}
}

func TestRegistryRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t, nil,
		mustCanonical(t, testPack(1, LifecycleActive)),
		mustCanonical(t, testPack(2, LifecycleDraft)),
	)
	for _, test := range []struct {
		name      string
		operation string
		request   Transition
	}{
		{name: "unknown country", operation: OperationActivate, request: Transition{Country: "XX", Revision: 1, ExpectedVersion: 0, Reason: "test", Actor: "operator"}},
		{name: "unknown revision", operation: OperationActivate, request: Transition{Country: "NG", Revision: 9, ExpectedVersion: 0, Reason: "test", Actor: "operator"}},
		{name: "stale version", operation: OperationActivate, request: Transition{Country: "NG", Revision: 2, ExpectedVersion: 5, Reason: "test", Actor: "operator"}},
		{name: "invalid reason", operation: OperationActivate, request: Transition{Country: "NG", Revision: 2, ExpectedVersion: 0, Reason: "Not A Token", Actor: "operator"}},
		{name: "activate active", operation: OperationActivate, request: Transition{Country: "NG", Revision: 1, ExpectedVersion: 0, Reason: "test", Actor: "operator"}},
		{name: "deprecate draft", operation: OperationDeprecate, request: Transition{Country: "NG", Revision: 2, ExpectedVersion: 0, Reason: "test", Actor: "operator"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			switch test.operation {
			case OperationActivate:
				_, err = registry.Activate(t.Context(), test.request)
			case OperationDeprecate:
				_, err = registry.Deprecate(t.Context(), test.request)
			default:
				_, err = registry.Retire(t.Context(), test.request)
			}
			if err == nil {
				t.Fatal("transition succeeded")
			}
		})
	}
}

func TestRegistryStoreFailureLeavesStateUnchanged(t *testing.T) {
	t.Parallel()

	store := &memoryStore{fail: true}
	registry := newTestRegistry(t, store,
		mustCanonical(t, testPack(1, LifecycleActive)),
		mustCanonical(t, testPack(2, LifecycleDraft)),
	)
	if _, err := registry.Activate(t.Context(), Transition{
		Country: "NG", Revision: 2, ExpectedVersion: 0, Reason: "reviewed_revision", Actor: "operator",
	}); err == nil {
		t.Fatal("Activate() succeeded despite a failing store")
	}
	if active, err := registry.Active("NG"); err != nil || active.Pack.Revision != 1 {
		t.Fatalf("active after failure = %+v, err = %v", active, err)
	}
	if registry.Version("NG") != 0 || len(registry.History("NG")) != 0 {
		t.Fatal("failed transition mutated in-memory lifecycle")
	}
}

func TestRegistryLoadRestoresPersistedLifecycle(t *testing.T) {
	t.Parallel()

	store := &memoryStore{}
	first := mustCanonical(t, testPack(1, LifecycleActive))
	second := mustCanonical(t, testPack(2, LifecycleDraft))
	source := newTestRegistry(t, store, first, second)
	if _, err := source.Activate(t.Context(), Transition{
		Country: "NG", Revision: 2, ExpectedVersion: 0, Reason: "reviewed_revision", Actor: "operator",
	}); err != nil {
		t.Fatal(err)
	}

	reopened := newTestRegistry(t, store, first, second)
	if err := reopened.Load(t.Context()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	active, err := reopened.Active("NG")
	if err != nil || active.Pack.Revision != 2 || active.State != LifecycleActive {
		t.Fatalf("restored active = %+v, err = %v", active, err)
	}
	if reopened.Version("NG") != 1 || len(reopened.History("NG")) != 2 {
		t.Fatalf("restored version = %d, history = %+v", reopened.Version("NG"), reopened.History("NG"))
	}
	if _, err := reopened.Activate(t.Context(), Transition{
		Country: "NG", Revision: 2, ExpectedVersion: 0, Reason: "stale", Actor: "operator",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version error = %v, want ErrConflict", err)
	}
}

func TestRegistryRejectsContradictoryState(t *testing.T) {
	t.Parallel()

	first := mustCanonical(t, testPack(1, LifecycleActive))
	second := mustCanonical(t, testPack(2, LifecycleActive))
	if _, err := NewRegistry([]Pack{first, second}, nil, fixedNow); err == nil {
		t.Fatal("NewRegistry() accepted two active revisions")
	}
	if _, err := NewRegistry([]Pack{first, first}, nil, fixedNow); err == nil {
		t.Fatal("NewRegistry() accepted a duplicate revision")
	}
	store := &memoryStore{states: []StoredState{{
		Country: "GH", Revision: 1, State: LifecycleActive, Digest: first.Digest, Version: 1, UpdatedAt: fixedNow(),
	}}}
	registry := newTestRegistry(t, store, first)
	if err := registry.Load(t.Context()); err == nil {
		t.Fatal("Load() accepted a state for an unregistered country")
	}
	mismatched := &memoryStore{states: []StoredState{{
		Country: "NG", Revision: 1, State: LifecycleActive, Digest: second.Digest, Version: 1, UpdatedAt: fixedNow(),
	}}}
	registry = newTestRegistry(t, mismatched, first)
	if err := registry.Load(t.Context()); err == nil {
		t.Fatal("Load() accepted a mismatched digest")
	}
}

func TestRegistryListIsOrderedAndBounded(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t, nil,
		mustCanonical(t, testPack(1, LifecycleActive)),
		mustCanonical(t, testPack(2, LifecycleDraft)),
	)
	summaries := registry.List()
	if len(summaries) != 2 || summaries[0].Revision != 2 || summaries[1].Revision != 1 {
		t.Fatalf("List() = %+v", summaries)
	}
	if summaries[0].LifecycleState != LifecycleDraft || len(summaries[0].Documents) != 3 {
		t.Fatalf("summary = %+v", summaries[0])
	}
	if !slices.IsSortedFunc(summaries, func(left, right Summary) int {
		if left.Country != right.Country {
			return -1
		}
		return int(right.Revision - left.Revision)
	}) {
		t.Fatalf("List() is not ordered: %+v", summaries)
	}
}
