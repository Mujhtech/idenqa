package headgate

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	libheadgate "github.com/mujhtech/headgate/go"
)

type dutyStoreStub struct {
	libheadgate.Store
	claimed  bool
	claims   int
	releases int
}

func (store *dutyStoreStub) ClaimDuty(context.Context, string, string, time.Duration) (bool, error) {
	store.claims++
	return store.claimed, nil
}

func (store *dutyStoreStub) ReleaseDuty(context.Context, string, string) error {
	store.releases++
	return nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestDefaultConfigSelectsIsolatedSchemaAndQueues(t *testing.T) {
	t.Parallel()
	configuration := DefaultConfig("idenqa-dev")
	if err := configuration.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if configuration.Schema != "headgate" || len(configuration.Queues) != 4 {
		t.Fatalf("DefaultConfig() = %+v", configuration)
	}
}

func TestEnvelopeOfPreservesOwnedMeaningWithoutPayloadExpansion(t *testing.T) {
	t.Parallel()
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-dev")
	if err != nil {
		t.Fatalf("envelopeOf() error = %v", err)
	}
	if envelope.ID != intent.ID().String() || envelope.Kind != kindVerification ||
		envelope.SchemaVersion != intent.Key().Version || envelope.Queue != QueueVerification ||
		envelope.RateClass != QueueVerification ||
		envelope.PartitionKey != intent.TenantID().String() || envelope.MaxAttempts != 4 ||
		envelope.Headers["idenqa-installation-id"] != "idenqa-dev" ||
		envelope.Headers["idenqa-retry-initial-ms"] != "1000" ||
		envelope.Headers["idenqa-retry-maximum-ms"] != "60000" ||
		envelope.Headers[headerTaskName] != intent.Key().Name.String() ||
		envelope.Headers[headerIdempotencyKey] != intent.IdempotencyKey() ||
		envelope.Headers["idenqa-tenant-id"] != intent.TenantID().String() ||
		envelope.Headers[libheadgate.TraceparentHeader] != intent.Traceparent() {
		t.Fatalf("envelopeOf() = %+v", envelope)
	}
	if !bytes.Equal(envelope.Payload, intent.Payload()) {
		t.Fatalf("payload = %s, want %s", envelope.Payload, intent.Payload())
	}
	wantUnique := intent.TenantID().String() + "\x00" + intent.IdempotencyKey()
	if string(envelope.UniqueKey) != wantUnique {
		t.Fatalf("unique key = %q, want %q", envelope.UniqueKey, wantUnique)
	}
}

func TestEnvelopeOfMapsInvalidHeadgateBoundaryToOwnedError(t *testing.T) {
	t.Parallel()
	if _, err := envelopeOf(task.Intent{}, "idenqa-dev"); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("envelopeOf() error = %v, want task.ErrInvalid", err)
	}
}

func TestConfigValidatesPostgresExecutionPolicy(t *testing.T) {
	t.Parallel()
	configuration := DefaultConfig("idenqa-test")
	if err := configuration.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	configuration.RetryCap = configuration.RetryBase - time.Millisecond
	if err := configuration.Validate(); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestPostgresOptionsCarryEnforcedExecutionPolicy(t *testing.T) {
	t.Parallel()
	configuration := DefaultConfig("idenqa-test")
	configuration.CrashLimit = 7
	configuration.RetryBase = 1500 * time.Millisecond
	configuration.RetryCap = 10 * time.Minute
	options := postgresOptions(configuration)
	if options.CrashLimit != 7 || options.RetryBaseMs != 1500 ||
		options.RetryCapMs != (10*time.Minute).Milliseconds() {
		t.Fatalf("postgresOptions() = %+v", options)
	}
}

func TestAdapterEnforcesTenantAndSystemPartitions(t *testing.T) {
	t.Parallel()
	base := testIntent(t)
	adapter := &Adapter{
		installationID: "idenqa-dev",
		queues: map[string]struct{}{
			QueueVerification: {}, QueueMaintenance: {},
		},
	}
	wrongTenantPartition := rebuildWithRouting(t, base, QueueVerification, "another-tenant")
	if _, err := adapter.envelopes([]task.Intent{wrongTenantPartition}); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("tenant partition error = %v, want ErrInvalid", err)
	}
	systemMaintenance := rebuildWithRouting(t, base, QueueMaintenance, SystemPartition)
	if _, err := adapter.envelopes([]task.Intent{systemMaintenance}); err != nil {
		t.Fatalf("system maintenance error = %v", err)
	}
}

func TestRunDutyExecutesAndReleasesOnlyForTheElectedHolder(t *testing.T) {
	t.Parallel()
	store := &dutyStoreStub{claimed: true}
	adapter, err := New(store, DefaultConfig("idenqa-test"))
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	claimed, err := adapter.RunDuty(
		t.Context(), DutyVerificationReconciliation, "worker-one", 2*time.Minute,
		func(context.Context) error { runs++; return nil },
	)
	if err != nil || !claimed || runs != 1 || store.claims != 1 || store.releases != 1 {
		t.Fatalf("claimed=%v runs=%d claims=%d releases=%d err=%v", claimed, runs, store.claims, store.releases, err)
	}
	store.claimed = false
	claimed, err = adapter.RunDuty(
		t.Context(), DutyVerificationReconciliation, "worker-two", 2*time.Minute,
		func(context.Context) error { runs++; return nil },
	)
	if err != nil || claimed || runs != 1 || store.releases != 1 {
		t.Fatalf("second claimed=%v runs=%d releases=%d err=%v", claimed, runs, store.releases, err)
	}
}

func testIntent(t *testing.T) task.Intent {
	t.Helper()
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{8}, 96)))
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := generator.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	name, err := task.NewName("verification.evaluate")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := task.NewIntent(task.IntentSpec{
		ID: taskID, TenantID: tenantID, Key: task.Key{Name: name, Version: 2}, Queue: QueueVerification,
		PartitionKey: tenantID.String(), IdempotencyKey: "evaluate-one",
		Payload: struct {
			VerificationID string `json:"verification_id"`
		}{VerificationID: "ver_01K00000000000000000000000"},
		ScheduledAt: now, Deadline: now.Add(5 * time.Minute),
		Retry: task.RetryPolicy{
			MaxAttempts: 4, InitialBackoff: time.Second, MaximumBackoff: time.Minute, JitterPercent: 20,
		},
		Retention: 24 * time.Hour, CorrelationID: "req_01K00000000000000000000000",
		Traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func rebuildWithRouting(t *testing.T, original task.Intent, queue, partition string) task.Intent {
	t.Helper()
	intent, err := task.NewIntent(task.IntentSpec{
		ID: original.ID(), TenantID: original.TenantID(), Key: original.Key(), Queue: queue,
		PartitionKey: partition, IdempotencyKey: original.IdempotencyKey(), Payload: original.Payload(),
		ScheduledAt: original.ScheduledAt(), Deadline: original.Deadline(), Retry: original.Retry(),
		Retention: original.Retention(), CorrelationID: original.CorrelationID(),
		CausationID: original.CausationID(), Traceparent: original.Traceparent(), Tracestate: original.Tracestate(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}
