package postgres

import (
	"testing"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestCompletionBodyMatchesReferenceOnlyEnvelope(t *testing.T) {
	t.Parallel()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	decisionID, _ := id.ParseDecision("dec_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	subject := "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	occurredAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	seed := "verification.completed:" + verificationID.String() + ":" + decisionID.String()
	data, err := delivery.EventData(map[string]any{
		"verification_id": verificationID.String(),
		"subject_id":      subject,
		"decision_id":     decisionID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := webhookv1.NewEvent(webhookv1.DeterministicEventID(seed), scope.ID().String(), "tenant_home", webhookv1.VerificationCompleted, webhookv1.SchemaVersion, occurredAt, data)
	if err != nil {
		t.Fatal(err)
	}
	body, err := event.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := webhookv1.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"` + webhookv1.DeterministicEventID(seed) + `","type":"verification.completed","schema_version":"1.0","created_at":"2026-09-06T12:00:00Z","tenant_id":"` + tenantID.String() + `","region":"tenant_home","data":{"decision_id":"` + decisionID.String() + `","subject_id":"` + subject + `","verification_id":"` + verificationID.String() + `"}}`
	if string(body) != want {
		t.Fatalf("completion envelope = %s, want %s", body, want)
	}
	if parsed.Type != webhookv1.VerificationCompleted || parsed.ID != event.ID {
		t.Fatalf("parsed completion envelope = %#v", parsed)
	}
}
