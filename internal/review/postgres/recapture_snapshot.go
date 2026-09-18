package postgres

import (
	"context"
	"errors"
	"fmt"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func (store *EvaluationStore) snapshotWithin(ctx context.Context, scope tenant.Scope, tx pg.Transaction, value review.Case, routing policy.Routing) (policy.Snapshot, string, error) {
	var sourceVersion int64
	err := tx.QueryRow(ctx, `SELECT source_version FROM idenqa.review_recapture_evaluation_requests WHERE tenant_id=$1 AND case_id=$2 AND target_version=$3`, scope.ID().String(), value.ID.String(), value.Version).Scan(&sourceVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		snapshot, digest, found, err := arbitrationSnapshot(ctx, tx, scope, value, routing)
		if found || err != nil {
			if err != nil {
				return policy.Snapshot{}, "", fmt.Errorf("load review arbitration snapshot: %w", err)
			}
			return snapshot, digest, err
		}
		snapshot, digest, err = review.Snapshot(value, routing)
		if err != nil {
			return policy.Snapshot{}, "", fmt.Errorf("construct review snapshot: %w", err)
		}
		return snapshot, digest, nil
	}
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("find recapture evaluation source version: %w", err)
	}
	request, err := findRecaptureReevaluation(ctx, tx, scope, value.ID, sourceVersion)
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("load recapture reevaluation request: %w", err)
	}
	ack, err := findAcknowledgement(ctx, tx, scope, value.ID, sourceVersion)
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("load recapture acknowledgement: %w", err)
	}
	child, err := store.policies.FindWithin(ctx, scope, tx, request.DecisionID)
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("load recapture child decision: %w", err)
	}
	var canonical, digest string
	if err := tx.QueryRow(ctx, `SELECT s.canonical,s.snapshot_digest FROM idenqa.review_evaluations e JOIN idenqa.policy_snapshots s ON s.tenant_id=e.tenant_id AND s.snapshot_digest=e.snapshot_digest WHERE e.tenant_id=$1 AND e.case_id=$2 AND e.case_version=$3`, scope.ID().String(), value.ID.String(), sourceVersion).Scan(&canonical, &digest); err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("load recapture parent snapshot: %w", err)
	}
	previous, err := policy.RestoreSnapshotCanonical([]byte(canonical), digest)
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("restore recapture parent snapshot: %w", err)
	}
	snapshot, digest, err := review.RecaptureSnapshot(value, routing, previous, request, ack, child)
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("compose recapture snapshot: %w", err)
	}
	return snapshot, digest, nil
}
