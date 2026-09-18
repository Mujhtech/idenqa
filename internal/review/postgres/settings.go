package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func findPolicySettings(ctx context.Context, tx pg.Transaction, scope tenant.Scope, snapshot policy.Snapshot) (*review.PolicySettings, error) {
	var encoded []byte
	err := tx.QueryRow(ctx, `SELECT configuration FROM idenqa.review_policy_settings WHERE tenant_id=$1 AND policy_id=$2 AND policy_revision=$3 AND policy_digest=$4`, scope.ID().String(), snapshot.Policy().ID.String(), snapshot.Policy().Revision, snapshot.Policy().Digest).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var settings review.PolicySettings
	if json.Unmarshal(encoded, &settings) != nil || settings.Validate() != nil || !settings.Matches(snapshot) {
		return nil, review.ErrInvalid
	}
	return &settings, nil
}
func pinCaseSettings(ctx context.Context, tx pg.Transaction, scope tenant.Scope, value review.Case, settings review.PolicySettings, at time.Time) error {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_case_settings(tenant_id,case_id,configuration,recorded_at) VALUES($1,$2,$3,$4)`, scope.ID().String(), value.ID.String(), encoded, at); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(scope.ID().String() + "/" + value.ID.String()))
	sampled := int(uint16(digest[0])<<8|uint16(digest[1]))%100 < settings.SamplePercent
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_case_operations(tenant_id,case_id,version,priority,due_at,language,reason,assurance,risk,sampled,updated_at) VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(tenant_id,case_id) DO NOTHING`, scope.ID().String(), value.ID.String(), settings.Priority, at.Add(time.Duration(settings.SLASeconds)*time.Second), settings.Language, settings.Reason, settings.Assurance, settings.Risk, sampled, at)
	return err
}
