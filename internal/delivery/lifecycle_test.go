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
	value.tasks = value.tasks[1:]
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
		if key[:len(scope.ID().String())] == scope.ID().String() && current.EndpointID.String() == intent.EndpointID.String() && current.EventID.String() == intent.EventID.String() {
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
func (repo *memoryRepository) RecordAttempt(_ context.Context, scope tenant.Scope, deliveryID id.Delivery, attempt delivery.Attempt, succeeded, retry bool, next time.Time) error {
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
	handler, err := deliverytask.NewHandler(repository, protector{}, transport, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	workIntent, err := deliverytask.NewIntent(ids, scope, intent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	first := handler.Handle(context.Background(), platformtask.Delivery{Intent: workIntent, Attempt: 1, Fence: 1})
	if first.Outcome != platformtask.OutcomeRetry {
		t.Fatalf("first outcome=%v", first.Outcome)
	}
	second := handler.Handle(context.Background(), platformtask.Delivery{Intent: workIntent, Attempt: 2, Fence: 2})
	if second.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("second outcome=%v", second.Outcome)
	}
	if replay := handler.Handle(context.Background(), platformtask.Delivery{Intent: workIntent, Attempt: 3, Fence: 3}); replay.Outcome != platformtask.OutcomeComplete || transport.calls != 2 {
		t.Fatalf("duplicate invoked sender: %#v calls=%d", replay, transport.calls)
	}
	now = now.Add(time.Minute)
	replayed, err := manager.Replay(context.Background(), scope, intent.ID, mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAW"))
	if err != nil || replayed.ReplayOf.String() != intent.ID.String() {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
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
	handler, _ := deliverytask.NewHandler(repository, protector{}, &sender{failUntil: 3}, func() time.Time { return now })
	taskID := mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	work, _ := platformtask.NewIntent(platformtask.IntentSpec{ID: taskID, TenantID: scope.ID(), Key: deliverytask.DeliverKey, Queue: "delivery", PartitionKey: scope.ID().String(), IdempotencyKey: "delivery", Payload: struct {
		DeliveryID string `json:"delivery_id"`
	}{deliveryID.String()}, ScheduledAt: now, Deadline: now.Add(time.Hour), Retry: deliverytask.DeliverRetry, Retention: time.Hour})
	if got := handler.Handle(context.Background(), platformtask.Delivery{Intent: work, Attempt: 1}); got.Outcome != platformtask.OutcomeRetry {
		t.Fatalf("attempt1=%v", got.Outcome)
	}
	if got := handler.Handle(context.Background(), platformtask.Delivery{Intent: work, Attempt: 2}); got.Outcome != platformtask.OutcomeComplete {
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
