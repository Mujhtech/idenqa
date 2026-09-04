package task_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacytask "github.com/Mujhtech/idenqa/internal/privacy/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestHandlerClassifiesReplaySafeDeletion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	identifiers, _ := id.NewSystemGenerator()
	tenantID, _ := identifiers.NewTenant()
	deletionID, _ := identifiers.NewDeletion()
	scope, _ := tenant.NewScope(tenantID)
	intent, _ := privacytask.NewDeleteIntent(identifiers, scope, privacytask.DeletePayload{DeletionID: deletionID}, privacytask.IntentMetadata{ScheduledAt: now, WorkflowVersion: 1})
	tests := []struct {
		name    string
		err     error
		outcome platformtask.Outcome
	}{
		{name: "complete", outcome: platformtask.OutcomeComplete},
		{name: "held is complete until rediscovery", err: privacy.ErrHeld, outcome: platformtask.OutcomeComplete},
		{name: "conflict retries", err: privacy.ErrConflict, outcome: platformtask.OutcomeRetry},
		{name: "invalid quarantines", err: privacy.ErrInvalid, outcome: platformtask.OutcomeQuarantine},
		{name: "dependency retries", err: errors.New("object store unavailable"), outcome: platformtask.OutcomeRetry},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := privacytask.NewHandler(&runnerStub{err: test.err})
			result := handler.Handle(t.Context(), platformtask.Delivery{Intent: intent})
			if result.Outcome != test.outcome || result.Validate() != nil {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

type runnerStub struct{ err error }

func (stub *runnerStub) Run(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.Deletion, error) {
	return privacy.Deletion{}, stub.err
}
