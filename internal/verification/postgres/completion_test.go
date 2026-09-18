package postgres

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestCompletionBodyMatchesReferenceOnlyEnvelope(t *testing.T) {
	t.Parallel()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	eventID, _ := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	decisionID, _ := id.ParseDecision("dec_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	subject := "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	occurredAt := time.Date(2026, time.September, 6, 12, 0, 0, 123456000, time.UTC)
	body, err := completionBody(scope, verification.LifecycleReceipt{EventID: eventID, VerificationID: verificationID, DecisionID: decisionID, OccurredAt: occurredAt}, subject, "tenant_home")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": eventID.String(), "type": "verification.completed", "schema_version": "1.0", "created_at": occurredAt.Format(time.RFC3339Nano), "tenant_id": tenantID.String(), "region": "tenant_home", "data": map[string]any{"verification_id": verificationID.String(), "subject_id": subject, "decision_id": decisionID.String()}}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("completion envelope = %#v", decoded)
	}
}
