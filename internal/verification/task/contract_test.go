package task

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const taskTestULID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

type fixedTaskIDs struct{ value id.Task }

func (generator fixedTaskIDs) NewTask() (id.Task, error) { return generator.value, nil }

func TestNewExecuteIntentPinsApprovedContract(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	checkID, _ := id.ParseCheck("chk_" + taskTestULID)
	attemptID, _ := id.ParseAttempt("atm_" + taskTestULID)
	scope, _ := tenant.NewScope(tenantID)

	intent, err := NewExecuteIntent(fixedTaskIDs{taskID}, scope, ExecutePayload{
		CheckID: checkID, AttemptID: attemptID,
	}, IntentMetadata{ScheduledAt: now, Deadline: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("NewExecuteIntent() error = %v", err)
	}
	if intent.Key() != ExecuteKey || intent.Queue() != "verification" ||
		intent.PartitionKey() != tenantID.String() ||
		intent.IdempotencyKey() != "verification.execute:"+attemptID.String() ||
		intent.Deadline() != now.Add(MaximumExecuteDuration) ||
		intent.Retention() != SuccessfulMetadataRetention || intent.Retry() != ExecuteRetry {
		t.Fatalf("intent does not match approved contract: %+v", intent)
	}
	var payload map[string]string
	if err := json.Unmarshal(intent.Payload(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["check_id"] != checkID.String() || payload["attempt_id"] != attemptID.String() {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestNewReconcileIntentPinsApprovedContract(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	checkID, _ := id.ParseCheck("chk_" + taskTestULID)
	attemptID, _ := id.ParseAttempt("atm_" + taskTestULID)
	scope, _ := tenant.NewScope(tenantID)
	intent, err := NewReconcileIntent(fixedTaskIDs{taskID}, scope, ReconcilePayload{
		CheckID: checkID, AttemptID: attemptID,
	}, IntentMetadata{ScheduledAt: now, Deadline: now.Add(48 * time.Hour)})
	if err != nil {
		t.Fatalf("NewReconcileIntent() error = %v", err)
	}
	if intent.Key() != ReconcileKey || intent.Queue() != "maintenance" ||
		intent.Deadline() != now.Add(MaximumReconcileDuration) || intent.Retry() != ReconcileRetry {
		t.Fatalf("intent does not match approved contract: %+v", intent)
	}
}

func TestDecodePayloadsRejectUnknownAndTrailingContent(t *testing.T) {
	t.Parallel()
	valid := []byte(`{"check_id":"chk_01ARZ3NDEKTSV4RRFFQ69G5FAV","attempt_id":"atm_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`)
	if _, err := DecodeExecute(valid); err != nil {
		t.Fatalf("DecodeExecute(valid) error = %v", err)
	}
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unknown", payload: `{"check_id":"chk_01ARZ3NDEKTSV4RRFFQ69G5FAV","attempt_id":"atm_01ARZ3NDEKTSV4RRFFQ69G5FAV","evidence":"raw"}`},
		{name: "trailing", payload: string(valid) + `{}`},
		{name: "scalar", payload: `"raw"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeExecute([]byte(test.payload)); !errors.Is(err, platformtask.ErrInvalid) {
				t.Fatalf("DecodeExecute() error = %v", err)
			}
		})
	}
}

func FuzzDecodeExecute(f *testing.F) {
	f.Add([]byte(`{"check_id":"chk_01ARZ3NDEKTSV4RRFFQ69G5FAV","attempt_id":"atm_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`))
	f.Add([]byte(`{"evidence":"secret"}`))
	f.Fuzz(func(_ *testing.T, payload []byte) {
		_, _ = DecodeExecute(payload)
	})
}
