package keyrewrap_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type rotatingWrapper struct {
	version string
}

func (wrapper *rotatingWrapper) Wrap(_ context.Context, purpose kms.Purpose, plaintext []byte, aad []byte) (kms.WrappedKey, error) {
	body, err := json.Marshal([]string{wrapper.version, string(purpose), string(aad), string(plaintext)})
	if err != nil {
		return kms.WrappedKey{}, err
	}
	return kms.NewWrappedKey(kms.WrappedKeyRecord{
		Provider: "test", Reference: "ref", Version: wrapper.version,
		Algorithm: "TEST", Ciphertext: body,
	})
}

func (wrapper *rotatingWrapper) Unwrap(_ context.Context, purpose kms.Purpose, wrapped kms.WrappedKey, aad []byte) ([]byte, error) {
	record := wrapped.Record()
	var fields []string
	if err := json.Unmarshal(record.Ciphertext, &fields); err != nil || len(fields) != 4 {
		return nil, errors.New("invalid ciphertext")
	}
	if fields[1] != string(purpose) || fields[2] != string(aad) ||
		(record.Version != "v1" && record.Version != fields[0]) {
		return nil, errors.New("ciphertext identity mismatch")
	}
	return []byte(fields[3]), nil
}

type memoryRepository struct {
	states map[keyrewrap.Class]keyrewrap.State
	audit  []keyrewrap.Batch
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{states: map[keyrewrap.Class]keyrewrap.State{}}
}

func (repository *memoryRepository) Load(_ context.Context, class keyrewrap.Class) (keyrewrap.State, bool, error) {
	state, ok := repository.states[class]
	return state, ok, nil
}

func (repository *memoryRepository) States(_ context.Context) ([]keyrewrap.State, error) {
	states := make([]keyrewrap.State, 0, len(repository.states))
	for _, state := range repository.states {
		states = append(states, state)
	}
	return states, nil
}

func (repository *memoryRepository) BeginGeneration(_ context.Context, class keyrewrap.Class, epoch keyrewrap.Epoch, at time.Time) (keyrewrap.State, error) {
	previous, ok := repository.states[class]
	if !ok {
		state := keyrewrap.State{Class: class, Epoch: epoch, Generation: 1, Status: keyrewrap.StatusRunning, StartedAt: at, UpdatedAt: at}
		repository.states[class] = state
		return state, nil
	}
	if previous.Epoch.Key == epoch.Key {
		return previous, nil
	}
	previous.Epoch, previous.Generation, previous.Status = epoch, previous.Generation+1, keyrewrap.StatusRunning
	previous.Cursor, previous.Processed, previous.Rewrapped = keyrewrap.Cursor{}, 0, 0
	previous.Skipped, previous.Failed, previous.ConsecutiveFailures = 0, 0, 0
	previous.LastError, previous.NextAttemptAt, previous.CompletedAt = "", time.Time{}, time.Time{}
	previous.StartedAt, previous.UpdatedAt = at, at
	repository.states[class] = previous
	return previous, nil
}

func (repository *memoryRepository) CommitBatch(_ context.Context, expected keyrewrap.State, batch keyrewrap.Batch) error {
	current, ok := repository.states[expected.Class]
	if !ok || current.Generation != expected.Generation || current.Epoch.Key != expected.Epoch.Key ||
		current.Cursor != expected.Cursor {
		return keyrewrap.ErrConflict
	}
	current.Cursor, current.Status = batch.To, batch.Status
	current.Processed += int64(batch.Processed)
	current.Rewrapped += int64(batch.Rewrapped)
	current.Skipped += int64(batch.Skipped)
	current.Failed += int64(batch.Failed)
	if batch.Status == keyrewrap.StatusFailed {
		current.ConsecutiveFailures++
		current.NextAttemptAt = batch.OccurredAt.Add(time.Minute)
	}
	if batch.Status == keyrewrap.StatusCompleted {
		current.CompletedAt = batch.OccurredAt
	}
	repository.states[expected.Class] = current
	repository.audit = append(repository.audit, batch)
	return nil
}

type mapAdapter struct {
	class   keyrewrap.Class
	objects map[string]map[string]string
	target  *string
	calls   int
	failOn  string
}

func (adapter *mapAdapter) Class() keyrewrap.Class { return adapter.class }

func (adapter *mapAdapter) List(_ context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	objects := adapter.objects[scope.ID().String()]
	targets := make([]keyrewrap.Target, 0, limit)
	for _, object := range sortedKeys(objects) {
		if object <= after {
			continue
		}
		if len(targets) == limit {
			break
		}
		targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.class, Object: object})
	}
	return targets, nil
}

func (adapter *mapAdapter) Rewrap(_ context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	adapter.calls++
	if adapter.failOn != "" && target.Object == adapter.failOn {
		return keyrewrap.Outcome{}, errors.New("poison object")
	}
	objects := adapter.objects[target.TenantID.String()]
	version := objects[target.Object]
	if version == "" {
		version = "v1"
	}
	wanted := "v2"
	if adapter.target != nil {
		wanted = *adapter.target
	}
	if version == wanted {
		return keyrewrap.Outcome{Changed: false, Wrapping: kms.WrappedKeyRecord{Provider: "test", Reference: "ref", Version: wanted, Algorithm: "TEST", Ciphertext: []byte{1}}}, nil
	}
	objects[target.Object] = wanted
	return keyrewrap.Outcome{Changed: true, Wrapping: kms.WrappedKeyRecord{Provider: "test", Reference: "ref", Version: wanted, Algorithm: "TEST", Ciphertext: []byte{1}}}, nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for first := 0; first < len(keys); first++ {
		for second := first + 1; second < len(keys); second++ {
			if keys[second] < keys[first] {
				keys[first], keys[second] = keys[second], keys[first]
			}
		}
	}
	return keys
}

type tenantList []id.Tenant

func (tenants tenantList) ListTenants(_ context.Context, after string, limit int) ([]id.Tenant, error) {
	result := make([]id.Tenant, 0, limit)
	for _, tenantID := range tenants {
		if tenantID.String() <= after {
			continue
		}
		if len(result) == limit {
			break
		}
		result = append(result, tenantID)
	}
	return result, nil
}

func newService(t *testing.T, repository *memoryRepository, tenants tenantList, adapters []keyrewrap.Adapter, wrapper *rotatingWrapper) *keyrewrap.Service {
	t.Helper()
	service, err := keyrewrap.NewService(repository, tenants, adapters, wrapper, nil, func() time.Time {
		return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func TestSweepPagesResumesAndCompletes(t *testing.T) {
	t.Parallel()

	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := generator.NewTenant()
	second, _ := generator.NewTenant()
	tenants := tenantList{first, second}
	wrapper := &rotatingWrapper{version: "v2"}
	adapter := &mapAdapter{class: keyrewrap.ClassHMACKey, target: &wrapper.version, objects: map[string]map[string]string{
		first.String():  {"a": "v1", "b": "v1"},
		second.String(): {"c": "v1"},
	}}
	repository := newMemoryRepository()
	service := newService(t, repository, tenants, []keyrewrap.Adapter{adapter}, wrapper)

	result, err := service.SweepClass(context.Background(), keyrewrap.ClassHMACKey, 2)
	if err != nil {
		t.Fatalf("SweepClass() error = %v", err)
	}
	if result.Rewrapped != 2 || result.Completed {
		t.Fatalf("first batch = %+v", result)
	}
	state, ok, _ := repository.Load(context.Background(), keyrewrap.ClassHMACKey)
	if !ok || state.Cursor.Object != "b" || state.Rewrapped != 2 {
		t.Fatalf("state after first batch = %+v", state)
	}
	result, err = service.SweepClass(context.Background(), keyrewrap.ClassHMACKey, 2)
	if err != nil {
		t.Fatalf("SweepClass(second) error = %v", err)
	}
	if result.Rewrapped != 1 || !result.Completed {
		t.Fatalf("second batch = %+v", result)
	}
	// A completed generation is a no-op until the epoch changes.
	result, err = service.SweepClass(context.Background(), keyrewrap.ClassHMACKey, 2)
	if err != nil || !result.Completed || result.Processed != 0 {
		t.Fatalf("completed sweep = %+v, %v", result, err)
	}
	if adapter.calls != 3 {
		t.Fatalf("adapter calls = %d, want 3", adapter.calls)
	}
	// A new provider epoch resets the generation and re-sweeps in one pass.
	wrapper.version = "v3"
	result, err = service.SweepClass(context.Background(), keyrewrap.ClassHMACKey, 4)
	if err != nil || !result.EpochChanged || !result.Completed || result.Rewrapped != 3 {
		t.Fatalf("epoch change = %+v, %v", result, err)
	}
}

func TestSweepStopsAtPoisonObjectWithoutAdvancingCursor(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	tenantID, _ := generator.NewTenant()
	adapter := &mapAdapter{class: keyrewrap.ClassHMACKey, objects: map[string]map[string]string{
		tenantID.String(): {"a": "v1", "b": "v1"},
	}, failOn: "a"}
	repository := newMemoryRepository()
	service := newService(t, repository, tenantList{tenantID}, []keyrewrap.Adapter{adapter}, &rotatingWrapper{version: "v2"})

	result, err := service.SweepClass(context.Background(), keyrewrap.ClassHMACKey, 8)
	if err != nil {
		t.Fatalf("SweepClass() error = %v", err)
	}
	if result.Status != keyrewrap.StatusFailed || result.Failed != 1 || result.Rewrapped != 0 {
		t.Fatalf("failed batch = %+v", result)
	}
	state, _, _ := repository.Load(context.Background(), keyrewrap.ClassHMACKey)
	if state.Cursor.Object != "" {
		t.Fatalf("cursor advanced past poison object: %+v", state.Cursor)
	}
	if len(repository.audit) != 1 || repository.audit[0].ErrorClass != "rewrap_failed" {
		t.Fatalf("audit = %+v", repository.audit)
	}
	// The failed state backs off instead of hammering the poison object.
	result, err = service.SweepClass(context.Background(), keyrewrap.ClassHMACKey, 8)
	if err != nil || !result.Backoff {
		t.Fatalf("backoff = %+v, %v", result, err)
	}
}

func TestRewriteRecordVerifiesRoundTrip(t *testing.T) {
	t.Parallel()

	wrapper := &rotatingWrapper{version: "v1"}
	purpose, _ := kms.NewPurpose("idenqa.test.v1")
	previous, err := wrapper.Wrap(context.Background(), purpose, []byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	wrapper.version = "v2"
	next, changed, err := keyrewrap.RewrapRecord(context.Background(), wrapper, wrapper, purpose, previous.Record(), []byte("aad"))
	if err != nil || !changed {
		t.Fatalf("RewrapRecord() changed=%t error=%v", changed, err)
	}
	if next.Version != "v2" {
		t.Fatalf("rewrapped record = %+v", next)
	}
	_, changed, err = keyrewrap.RewrapRecord(context.Background(), wrapper, wrapper, purpose, next, []byte("aad"))
	if err != nil || changed {
		t.Fatalf("RewrapRecord(current) changed=%t error=%v", changed, err)
	}
	if _, _, err := keyrewrap.RewrapRecord(context.Background(), wrapper, wrapper, purpose, previous.Record(), []byte("other")); err == nil {
		t.Fatal("RewrapRecord(wrong context) error = nil")
	}
}

func TestObserveEpochTracksProviderIdentity(t *testing.T) {
	t.Parallel()

	wrapper := &rotatingWrapper{version: "v1"}
	first, err := keyrewrap.ObserveEpoch(context.Background(), wrapper)
	if err != nil {
		t.Fatal(err)
	}
	wrapper.version = "v2"
	second, err := keyrewrap.ObserveEpoch(context.Background(), wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key == second.Key || len(first.Key) != 64 {
		t.Fatalf("epochs = %q, %q", first.Key, second.Key)
	}
	if first.Record.Provider != "test" || first.Record.Reference != "ref" {
		t.Fatalf("epoch record = %+v", first.Record)
	}
}

func TestRewrapTenantClassIsBounded(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	tenantID, _ := generator.NewTenant()
	adapter := &mapAdapter{class: keyrewrap.ClassHMACKey, objects: map[string]map[string]string{
		tenantID.String(): {"a": "v1", "b": "v1", "c": "v1"},
	}}
	repository := newMemoryRepository()
	service := newService(t, repository, tenantList{tenantID}, []keyrewrap.Adapter{adapter}, &rotatingWrapper{version: "v2"})
	rewrapped, err := service.RewrapTenantClass(context.Background(), keyrewrap.ClassHMACKey.String(), tenantID, 2)
	if err != nil || rewrapped != 2 {
		t.Fatalf("RewrapTenantClass() = %d, %v", rewrapped, err)
	}
	rewrapped, err = service.RewrapTenantClass(context.Background(), keyrewrap.ClassHMACKey.String(), tenantID, 10)
	if err != nil || rewrapped != 1 {
		t.Fatalf("RewrapTenantClass(rest) = %d, %v", rewrapped, err)
	}
}

func TestAuthorizeEpochRequiresMatchingProviderIdentity(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	tenantID, _ := generator.NewTenant()
	adapter := &mapAdapter{class: keyrewrap.ClassHMACKey, objects: map[string]map[string]string{tenantID.String(): {}}}
	repository := newMemoryRepository()
	service := newService(t, repository, tenantList{tenantID}, []keyrewrap.Adapter{adapter}, &rotatingWrapper{version: "v2"})
	generation, err := service.AuthorizeEpoch(context.Background(), keyrewrap.ClassHMACKey.String(), "test", "ref", "v2", "TEST")
	if err != nil || generation != 1 {
		t.Fatalf("AuthorizeEpoch() = %d, %v", generation, err)
	}
	if _, err := service.AuthorizeEpoch(context.Background(), keyrewrap.ClassHMACKey.String(), "test", "ref", "v1", "TEST"); !errors.Is(err, keyrewrap.ErrConflict) {
		t.Fatalf("AuthorizeEpoch(stale) error = %v, want ErrConflict", err)
	}
}

var _ = fmt.Sprintf
