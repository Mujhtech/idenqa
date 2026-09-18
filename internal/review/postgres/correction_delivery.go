package postgres

import (
	"context"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func (s *FollowupStore) notifyCorrection(ctx context.Context, tx pg.Transaction, scope tenant.Scope, value review.Case, successor id.Decision, input review.FollowupInput) error {
	seed := "decision.corrected:" + successor.String()
	eventID := webhookv1.DeterministicEventID(seed)
	data, err := delivery.EventData(map[string]any{
		"decision_id":          successor.String(),
		"verification_id":      value.VerificationID.String(),
		"previous_decision_id": value.ChallengedDecision.String(),
		"case_id":              value.ID.String(),
		"decision": map[string]any{
			"id":                   successor.String(),
			"type":                 "decision",
			"verification_id":      value.VerificationID.String(),
			"previous_decision_id": value.ChallengedDecision.String(),
			"case_id":              value.ID.String(),
		},
	})
	if err != nil {
		return err
	}
	event, err := webhookv1.NewEvent(eventID, scope.ID().String(), value.Region, webhookv1.DecisionCorrected, webhookv1.SchemaVersion, input.At, data)
	if err != nil {
		return err
	}
	body, err := event.Canonical()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.outbox_events(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES($1,$2,'review.case',$3,$4,'verification.decision.corrected',1,$5,$6,$6)`, eventID, scope.ID().String(), value.ID.String(), value.Version+1, body, input.At); err != nil {
		return err
	}
	if _, err := deliverypostgres.EmitEventWithin(ctx, tx, s.wrapper, event, seed); err != nil {
		return err
	}

	return nil
}
