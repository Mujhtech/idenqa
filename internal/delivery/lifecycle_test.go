package delivery_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type identifiers struct {
	endpoint   id.WebhookEndpoint
	deliveries []id.Delivery
	tasks      []id.Task
}

func (value *identifiers) NewWebhookEndpoint() (id.WebhookEndpoint, error) {
	return value.endpoint, nil
}
func (value *identifiers) NewDelivery() (id.Delivery, error) {
	result := value.deliveries[0]
	value.deliveries = value.deliveries[1:]
	return result, nil
}
func (value *identifiers) NewTask() (id.Task, error) {
	result := value.tasks[0]
	if len(value.tasks) > 1 {
		value.tasks = value.tasks[1:]
	}
	return result, nil
}

type protector struct{}

func (protector) Wrap(_ context.Context, _ kms.Purpose, plaintext, _ []byte) (kms.WrappedKey, error) {
	return kms.NewWrappedKey(kms.WrappedKeyRecord{Provider: "test", Reference: "keyring", Version: "v1", Algorithm: "TEST", Ciphertext: append([]byte(nil), plaintext...)})
}
func (protector) Unwrap(_ context.Context, _ kms.Purpose, wrapped kms.WrappedKey, _ []byte) ([]byte, error) {
	return wrapped.Record().Ciphertext, nil
}

type memoryRepository struct {
	mutex      sync.Mutex
	endpoints  map[string]delivery.Endpoint
	deliveries map[string]delivery.Intent
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{endpoints: map[string]delivery.Endpoint{}, deliveries: map[string]delivery.Intent{}}
}
func scoped(scope tenant.Scope, identifier string) string {
	return scope.ID().String() + "\x00" + identifier
}
func (repo *memoryRepository) CreateEndpoint(_ context.Context, scope tenant.Scope, endpoint delivery.Endpoint) error {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	key := scoped(scope, endpoint.ID.String())
	if _, ok := repo.endpoints[key]; ok {
		return delivery.ErrConflict
	}
	repo.endpoints[key] = endpoint
	return nil
}
func (repo *memoryRepository) UpdateEndpoint(_ context.Context, scope tenant.Scope, endpoint delivery.Endpoint, expected int64) error {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	key := scoped(scope, endpoint.ID.String())
	current, ok := repo.endpoints[key]
	if !ok {
		return delivery.ErrNotFound
	}
	if current.Version != expected {
		return delivery.ErrConflict
	}
	repo.endpoints[key] = endpoint
	return nil
}
func (repo *memoryRepository) FindEndpoint(_ context.Context, scope tenant.Scope, endpointID id.WebhookEndpoint) (delivery.Endpoint, error) {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	value, ok := repo.endpoints[scoped(scope, endpointID.String())]
	if !ok {
		return delivery.Endpoint{}, delivery.ErrNotFound
	}
	return value, nil
}
func (repo *memoryRepository) CreateDelivery(_ context.Context, scope tenant.Scope, intent delivery.Intent) error {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	for key, current := range repo.deliveries {
		if key[:len(scope.ID().String())] == scope.ID().String() && current.EndpointID.String() == intent.EndpointID.String() && current.EventID.String() == intent.EventID.String() && current.ReplayOf.IsZero() && intent.ReplayOf.IsZero() {
			return delivery.ErrConflict
		}
	}
	repo.deliveries[scoped(scope, intent.ID.String())] = intent
	return nil
}
func (repo *memoryRepository) FindDelivery(_ context.Context, scope tenant.Scope, deliveryID id.Delivery) (delivery.Intent, error) {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	value, ok := repo.deliveries[scoped(scope, deliveryID.String())]
	if !ok {
		return delivery.Intent{}, delivery.ErrNotFound
	}
	value.Body = append([]byte(nil), value.Body...)
	return value, nil
}
func (repo *memoryRepository) RecordAttemptWithin(_ context.Context, scope tenant.Scope, _ platformpostgres.Transaction, deliveryID id.Delivery, attempt delivery.Attempt, succeeded, retry bool, next time.Time) error {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	key := scoped(scope, deliveryID.String())
	value, ok := repo.deliveries[key]
	if !ok {
		return delivery.ErrNotFound
	}
	if value.State != delivery.StatePending || value.AttemptCount+1 != attempt.Number {
		return delivery.ErrConflict
	}
	value.AttemptCount = attempt.Number
	value.UpdatedAt = attempt.CompletedAt
	value.NextAttemptAt = next
	if succeeded {
		value.State = delivery.StateDelivered
		value.DeliveredAt = attempt.CompletedAt
	} else if !retry || attempt.Number >= value.MaxAttempts {
		value.State = delivery.StateExhausted
	}
	repo.deliveries[key] = value
	return nil
}

type sender struct {
	mutex     sync.Mutex
	calls     int
	failUntil int
}

func (value *sender) Send(_ context.Context, _ string, _ delivery.Signature, _ []byte) (delivery.SafeDiagnostic, bool, bool, error) {
	value.mutex.Lock()
	defer value.mutex.Unlock()
	value.calls++
	if value.calls <= value.failUntil {
		diagnostic, _ := delivery.NewSafeDiagnostic(503, "retryable_status", 0)
		return diagnostic, false, true, errors.New("unavailable")
	}
	diagnostic, _ := delivery.NewSafeDiagnostic(204, "delivered", 0)
	return diagnostic, true, false, nil
}

func TestWebhookLifecycleRotationReplayRetryAndTenantIsolation(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	scope := mustScope(t, "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	other := mustScope(t, "ten_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	ids := &identifiers{endpoint: mustEndpoint(t, "whk_01ARZ3NDEKTSV4RRFFQ69G5FAV"), deliveries: []id.Delivery{mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV"), mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAW")}, tasks: []id.Task{mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV")}}
	repository := newMemoryRepository()
	manager, err := delivery.NewManager(repository, ids, protector{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	endpoint, firstSecret, err := manager.CreateEndpoint(context.Background(), scope, "https://hooks.example.com/idenqa")
	if err != nil || len(firstSecret) != 32 {
		t.Fatalf("create endpoint = %v, secret=%d", err, len(firstSecret))
	}
	if endpoint.SchemaVersion != delivery.DefaultSchemaVersion {
		t.Fatalf("schema version = %q", endpoint.SchemaVersion)
	}
	if _, _, err := manager.CreateEndpointSubscribedVersioned(context.Background(), scope, "https://hooks.example.com/unsupported", delivery.DefaultEventTypes, "2.0"); !errors.Is(err, delivery.ErrInvalid) {
		t.Fatalf("unsupported schema version = %v", err)
	}
	now = now.Add(time.Minute)
	rotated, secondSecret, err := manager.Rotate(context.Background(), scope, endpoint.ID, 10*time.Minute)
	if err != nil || rotated.Active.Version != 2 || rotated.Previous == nil || string(firstSecret) == string(secondSecret) {
		t.Fatalf("rotate = %#v, %v", rotated, err)
	}
	if _, err := repository.FindEndpoint(context.Background(), other, endpoint.ID); !errors.Is(err, delivery.ErrNotFound) {
		t.Fatalf("cross tenant find = %v", err)
	}
	event := mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	intent, err := manager.CreateDelivery(context.Background(), scope, endpoint.ID, event, "verification.decision.v1", []byte(`{"decision":"verified"}`))
	if err != nil {
		t.Fatal(err)
	}
	transport := &sender{failUntil: 1}
	queue := &memoryTaskQueue{}
	handler, err := deliverytask.NewHandler(repository, protector{}, transport, ids, queue, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	workIntent, err := deliverytask.NewIntent(ids, scope, intent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	first := runDeliveryEffect(t, handler, workIntent)
	if first.Outcome != platformtask.OutcomeComplete || len(queue.intents) != 1 {
		t.Fatalf("first outcome=%v", first.Outcome)
	}
	now = queue.intents[0].ScheduledAt()
	second := runDeliveryEffect(t, handler, queue.intents[0])
	if second.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("second outcome=%v", second.Outcome)
	}
	if replay := runDeliveryEffect(t, handler, workIntent); replay.Outcome != platformtask.OutcomeComplete || transport.calls != 2 {
		t.Fatalf("duplicate invoked sender: %#v calls=%d", replay, transport.calls)
	}
	now = now.Add(time.Minute)
	replayed, err := manager.Replay(context.Background(), scope, intent.ID, intent.EventID)
	if err != nil || replayed.ReplayOf.String() != intent.ID.String() || replayed.EventID != intent.EventID || string(replayed.Body) != string(intent.Body) {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	now = now.Add(time.Hour) // Disabling after the overlap must still validate.
	if err := manager.Disable(context.Background(), scope, endpoint.ID, "operator_requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateDelivery(context.Background(), scope, endpoint.ID, mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAX"), "verification.decision.v1", []byte(`{}`)); !errors.Is(err, delivery.ErrDisabled) {
		t.Fatalf("disabled create=%v", err)
	}
}

func TestWebhookRetryExhaustion(t *testing.T) {
	// The state transition itself is independent of Headgate's scheduling.
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	scope := mustScope(t, "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	repository := newMemoryRepository()
	endpointID := mustEndpoint(t, "whk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	wrapped, _ := protector{}.Wrap(context.Background(), mustPurpose(t), make([]byte, 32), nil)
	repository.endpoints[scoped(scope, endpointID.String())] = delivery.Endpoint{ID: endpointID, URL: "https://hooks.example.com", Active: delivery.Secret{Version: 1, Wrapped: wrapped, CreatedAt: now}, Version: 1, CreatedAt: now, UpdatedAt: now}
	deliveryID := mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	intent, _ := delivery.NewIntent(deliveryID, endpointID, mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV"), "verification.decision.v1", []byte(`{}`), 2, now)
	repository.deliveries[scoped(scope, deliveryID.String())] = intent
	queue := &memoryTaskQueue{}
	ids := &identifiers{tasks: []id.Task{mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV")}}
	handler, _ := deliverytask.NewHandler(repository, protector{}, &sender{failUntil: 3}, ids, queue, func() time.Time { return now })
	work, _ := deliverytask.NewIntent(ids, scope, deliveryID, now)
	if got := runDeliveryEffect(t, handler, work); got.Outcome != platformtask.OutcomeComplete || len(queue.intents) != 1 {
		t.Fatalf("attempt1=%v", got.Outcome)
	}
	now = queue.intents[0].ScheduledAt()
	if got := runDeliveryEffect(t, handler, queue.intents[0]); got.Outcome != platformtask.OutcomeComplete || len(queue.intents) != 1 {
		t.Fatalf("attempt2=%v", got.Outcome)
	}

	stored, _ := repository.FindDelivery(context.Background(), scope, deliveryID)
	if stored.State != delivery.StateExhausted {
		t.Fatalf("state=%s", stored.State)
	}
}

func mustScope(t *testing.T, value string) tenant.Scope {
	t.Helper()
	parsed, err := id.ParseTenant(value)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
func mustEndpoint(t *testing.T, value string) id.WebhookEndpoint {
	t.Helper()
	parsed, err := id.ParseWebhookEndpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustDelivery(t *testing.T, value string) id.Delivery {
	t.Helper()
	parsed, err := id.ParseDelivery(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustEvent(t *testing.T, value string) id.Event {
	t.Helper()
	parsed, err := id.ParseEvent(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustTask(t *testing.T, value string) id.Task {
	t.Helper()
	parsed, err := id.ParseTask(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustPurpose(t *testing.T) kms.Purpose {
	t.Helper()
	purpose, err := kms.NewPurpose("delivery.webhook-secret")
	if err != nil {
		t.Fatal(err)
	}
	return purpose
}

// These in-memory lifecycle tests exercise the prepared transaction body.
// PostgreSQL integration tests verify real rollback and fencing behavior.
func runDeliveryEffect(t *testing.T, handler *deliverytask.Handler, intent platformtask.Intent) platformtask.Result {
	t.Helper()
	effect, result := handler.Prepare(context.Background(), platformtask.Delivery{Intent: intent, Attempt: 1, Fence: 1})
	if result.Outcome != platformtask.OutcomeComplete {
		return result
	}
	if effect == nil {
		t.Fatal("missing transactional effect")
	}
	return effect(context.Background(), nil)
}

type memoryTaskQueue struct{ intents []platformtask.Intent }

func (queue *memoryTaskQueue) EnqueueTx(_ context.Context, _ platformpostgres.Transaction, intents ...platformtask.Intent) error {
	queue.intents = append(queue.intents, intents...)
	return nil
}
func (repo *memoryRepository) FinishWithin(_ context.Context, scope tenant.Scope, _ platformpostgres.Transaction, deliveryID id.Delivery, expected int32, state delivery.State, at time.Time) error {
	repo.mutex.Lock()
	defer repo.mutex.Unlock()
	key := scoped(scope, deliveryID.String())
	value, ok := repo.deliveries[key]
	if !ok {
		return delivery.ErrNotFound
	}
	if value.State != delivery.StatePending || value.AttemptCount != expected {
		return delivery.ErrConflict
	}
	value.State, value.UpdatedAt = state, at
	repo.deliveries[key] = value
	return nil
}

func TestWebhookStopsBeforeSendingForDisabledOrExpiredDelivery(t *testing.T) {
	for _, test := range []struct {
		name     string
		disabled bool
		age      time.Duration
		expected delivery.State
	}{
		{name: "disabled endpoint", disabled: true, expected: delivery.StateCancelled},
		{name: "expired delivery window", age: 25 * time.Hour, expected: delivery.StateExhausted},
	} {
		t.Run(test.name, func(t *testing.T) {
			created := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			now := created.Add(test.age)
			scope := mustScope(t, "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
			repository := newMemoryRepository()
			endpointID := mustEndpoint(t, "whk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
			wrapped, err := protector{}.Wrap(t.Context(), mustPurpose(t), make([]byte, 32), nil)
			if err != nil {
				t.Fatal(err)
			}
			endpoint := delivery.Endpoint{ID: endpointID, URL: "https://hooks.example.com", Active: delivery.Secret{Version: 1, Wrapped: wrapped, CreatedAt: created}, Version: 1, CreatedAt: created, UpdatedAt: created}
			if test.disabled {
				endpoint.DisabledAt, endpoint.DisabledReason = now, "operator_requested"
			}
			repository.endpoints[scoped(scope, endpointID.String())] = endpoint
			deliveryID := mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV")
			intent, err := delivery.NewIntent(deliveryID, endpointID, mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV"), "verification.completed.v1", []byte(`{}`), 8, created)
			if err != nil {
				t.Fatal(err)
			}
			repository.deliveries[scoped(scope, deliveryID.String())] = intent
			ids := &identifiers{tasks: []id.Task{mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV")}}
			transport, queue := &sender{}, &memoryTaskQueue{}
			handler, err := deliverytask.NewHandler(repository, protector{}, transport, ids, queue, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			work, err := deliverytask.NewIntent(ids, scope, deliveryID, now)
			if err != nil {
				t.Fatal(err)
			}
			if result := handler.Handle(t.Context(), platformtask.Delivery{Intent: work, Attempt: 1}); result.Outcome != platformtask.OutcomeQuarantine {
				t.Fatal("unfenced delivery ran")
			}
			if result := runDeliveryEffect(t, handler, work); result.Outcome != platformtask.OutcomeComplete {
				t.Fatalf("effect: %#v", result)
			}
			stored, err := repository.FindDelivery(t.Context(), scope, deliveryID)
			if err != nil || stored.State != test.expected || stored.AttemptCount != 0 || transport.calls != 0 || len(queue.intents) != 0 {
				t.Fatalf("stopped delivery: %#v calls=%d err=%v", stored, transport.calls, err)
			}
		})
	}
}
