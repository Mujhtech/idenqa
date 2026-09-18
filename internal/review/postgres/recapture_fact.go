package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const recaptureRequestedFact = "review.recapture.requested"

// RecaptureFactProjector lets a pinned policy distinguish a fresh review-linked
// child from the parent journey. The link is context, not proof of identity or
// assurance; only the child's own completed checks can support its outcome.
type RecaptureFactProjector struct{}

// ProjectWithin derives one reference-only fact from immutable recapture
// lineage inside the authoritative policy snapshot transaction.
func (RecaptureFactProjector) ProjectWithin(
	ctx context.Context,
	tx pg.Transaction,
	scope tenant.Scope,
	projection policypostgres.Projection,
) ([]policy.Fact, error) {
	if tx == nil || scope.ID().IsZero() || projection.VerificationID.IsZero() || projection.EvaluatedAt.IsZero() {
		return nil, fmt.Errorf("%w: recapture fact projection", policy.ErrInvalid)
	}
	var row struct {
		CaseID         string
		CaseVersion    int64
		ParentID       string
		SnapshotDigest string
		CreatedAt      time.Time
	}
	err := tx.QueryRow(ctx, `
SELECT case_id,case_version,parent_verification_id,policy_snapshot_digest,created_at
FROM idenqa.review_recaptures
WHERE tenant_id=$1 AND child_verification_id=$2 AND created_at<=$3
`, scope.ID().String(), projection.VerificationID.String(), projection.EvaluatedAt).Scan(
		&row.CaseID,
		&row.CaseVersion,
		&row.ParentID,
		&row.SnapshotDigest,
		&row.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read recapture fact lineage: %w", err)
	}
	if row.CaseID == "" || row.CaseVersion < 1 || row.ParentID == "" || len(row.SnapshotDigest) != 64 || row.CreatedAt.IsZero() || row.CreatedAt.After(projection.EvaluatedAt) {
		return nil, fmt.Errorf("%w: recapture fact lineage", policy.ErrInvalid)
	}
	canonical, err := json.Marshal(struct {
		CaseID         string    `json:"case_id"`
		CaseVersion    int64     `json:"case_version"`
		ParentID       string    `json:"parent_verification_id"`
		ChildID        string    `json:"child_verification_id"`
		SnapshotDigest string    `json:"policy_snapshot_digest"`
		CreatedAt      time.Time `json:"created_at"`
	}{
		CaseID:         row.CaseID,
		CaseVersion:    row.CaseVersion,
		ParentID:       row.ParentID,
		ChildID:        projection.VerificationID.String(),
		SnapshotDigest: row.SnapshotDigest,
		CreatedAt:      row.CreatedAt.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode recapture fact lineage: %w", err)
	}
	digest := sha256.Sum256(canonical)
	key, err := policy.NewFactKey(recaptureRequestedFact)
	if err != nil {
		return nil, fmt.Errorf("construct recapture fact key: %w", err)
	}
	return []policy.Fact{{
		Key:   key,
		State: policy.RequirementSatisfied,
		Source: policy.FactSource{
			Kind: policy.FactSourceReviewFinding,
			ReviewFinding: &policy.ReviewFindingSource{
				Reference: "review:" + hex.EncodeToString(digest[:]),
			},
		},
		ObservedAt:  row.CreatedAt.UTC(),
		ReasonCodes: []string{"review.recapture_requested"},
	}}, nil
}
