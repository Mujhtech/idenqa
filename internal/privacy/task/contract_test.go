package task_test

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	privacytask "github.com/Mujhtech/idenqa/internal/privacy/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestDeleteIntentContainsOnlyOpaqueIdentifier(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	tenantID, _ := identifiers.NewTenant()
	deletionID, _ := identifiers.NewDeletion()
	scope, _ := tenant.NewScope(tenantID)
	intent, err := privacytask.NewDeleteIntent(identifiers, scope, privacytask.DeletePayload{DeletionID: deletionID}, privacytask.IntentMetadata{ScheduledAt: now, WorkflowVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := privacytask.DecodeDelete(intent.Payload())
	if err != nil || payload.DeletionID != deletionID {
		t.Fatalf("DecodeDelete() = %+v, %v", payload, err)
	}
	if got := string(intent.Payload()); got != `{"deletion_id":"`+deletionID.String()+`"}` {
		t.Fatalf("payload = %s", got)
	}
}
