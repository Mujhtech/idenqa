package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func (s *FollowupStore) notifyCorrection(ctx context.Context, tx pg.Transaction, scope tenant.Scope, value review.Case, successor id.Decision, input review.FollowupInput) error {
	eventID, err := s.ids.NewEvent()
	if err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		ID            string    `json:"id"`
		Type          string    `json:"type"`
		SchemaVersion string    `json:"schema_version"`
		TenantID      string    `json:"tenant_id"`
		CreatedAt     time.Time `json:"created_at"`
		Region        string    `json:"region"`
		Data          struct {
			CaseID             string `json:"case_id"`
			VerificationID     string `json:"verification_id"`
			PreviousDecisionID string `json:"previous_decision_id"`
			DecisionID         string `json:"decision_id"`
		} `json:"data"`
	}{eventID.String(), "verification.decision.corrected", "1.0", scope.ID().String(), input.At, value.Region, struct {
		CaseID             string `json:"case_id"`
		VerificationID     string `json:"verification_id"`
		PreviousDecisionID string `json:"previous_decision_id"`
		DecisionID         string `json:"decision_id"`
	}{value.ID.String(), value.VerificationID.String(), value.ChallengedDecision.String(), successor.String()}})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.outbox_events(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES($1,$2,'review.case',$3,$4,'verification.decision.corrected',1,$5,$6,$6)`, eventID.String(), scope.ID().String(), value.ID.String(), value.Version+1, body, input.At)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND disabled_at IS NULL ORDER BY id LIMIT 1025`, scope.ID().String())
	if err != nil {
		return err
	}
	var endpoints []id.WebhookEndpoint
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			rows.Close()
			return err
		}
		endpoint, err := id.ParseWebhookEndpoint(encoded)
		if err != nil {
			rows.Close()
			return err
		}
		endpoints = append(endpoints, endpoint)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(endpoints) > 1024 {
		return review.ErrConflict
	}
	store, err := deliverypostgres.New(boundTransaction{tx})
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		identifier, err := s.ids.NewDelivery()
		if err != nil {
			return err
		}
		intent, err := delivery.NewIntent(identifier, endpoint, eventID, "verification.decision.corrected", body, 8, input.At)
		if err != nil {
			return err
		}
		if err := store.CreateDeliveryWithin(ctx, scope, tx, intent); err != nil {
			return err
		}
	}
	return nil
}
