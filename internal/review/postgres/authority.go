package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/verification"

	"github.com/Mujhtech/idenqa/internal/platform/id"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// Authority resolves durable assignments on every operation. A revoked row never falls back to a file.
type Authority struct {
	pool     transactionRunner
	fallback review.Authority
}

// NewAuthority constructs durable authority with an optional bootstrap fallback.
func NewAuthority(pool transactionRunner, fallback review.Authority) (*Authority, error) {
	if pool == nil {
		return nil, review.ErrInvalid
	}
	return &Authority{pool, fallback}, nil
}

// ResolveReviewer resolves current tenant, region and credential authority.
func (a *Authority) ResolveReviewer(ctx context.Context, scope tenant.Scope, actor review.Actor, region string, at time.Time) (review.Principal, error) {
	var p review.Principal
	err := a.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		var err error
		p, err = a.ResolveWithin(ctx, tx, scope, actor, region, at)
		return err
	})
	return p, err
}

// ResolveWithin locks current authority until the consequential transaction commits.
func (a *Authority) ResolveWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, actor review.Actor, region string, at time.Time) (review.Principal, error) {
	if err := setScope(ctx, tx, scope); err != nil {
		return review.Principal{}, err
	}
	var encoded []byte
	var revoked bool
	err := tx.QueryRow(ctx, `SELECT assignment,revoked FROM idenqa.review_operator_assignments WHERE tenant_id=$1 AND api_key_id=$2 FOR SHARE`, scope.ID().String(), actor.ID).Scan(&encoded, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		if a.fallback == nil {
			return review.Principal{}, review.ErrForbidden
		}
		return a.fallback.ResolveReviewer(ctx, scope, actor, region, at)
	}
	if err != nil {
		return review.Principal{}, err
	}
	if revoked {
		return review.Principal{}, review.ErrForbidden
	}
	var assignment review.Assignment
	if json.Unmarshal(encoded, &assignment) != nil {
		return review.Principal{}, review.ErrForbidden
	}
	registry, err := review.NewRegistry([]review.Assignment{assignment})
	if err != nil {
		return review.Principal{}, err
	}
	return registry.ResolveReviewer(ctx, scope, actor, region, at)
}
func resolveWithin(ctx context.Context, tx pg.Transaction, authority review.Authority, scope tenant.Scope, actor review.Actor, region string, at time.Time) (review.Principal, error) {
	if authority == nil {
		return review.Principal{}, review.ErrForbidden
	}
	if transactional, ok := authority.(interface {
		ResolveWithin(context.Context, pg.Transaction, tenant.Scope, review.Actor, string, time.Time) (review.Principal, error)
	}); ok {
		return transactional.ResolveWithin(ctx, tx, scope, actor, region, at)
	}
	return authority.ResolveReviewer(ctx, scope, actor, region, at)
}

// WithAuthority supplies transactional reviewer checks for production mutations.
func (s *Store) WithAuthority(authority review.Authority) *Store {
	result := *s
	result.authority = authority
	return &result
}

// WithReviewerAuthority supplies transactional reviewer authorization.
func (s *RecaptureStore) WithReviewerAuthority(authority review.Authority) *RecaptureStore {
	result := *s
	result.Store = s.WithAuthority(authority)
	return &result
}
func (s *Store) checkAuthority(ctx context.Context, tx pg.Transaction, scope tenant.Scope, actor review.Actor, value review.Case, permission review.Permission) error {
	if s.authority == nil {
		return nil
	}
	principal, err := resolveWithin(ctx, tx, s.authority, scope, actor, value.Region, s.clock.Now().UTC())
	if err != nil {
		return err
	}
	if !slices.Contains(principal.Permissions, permission) || !slices.Contains(principal.Certifications, value.RequiredCertificate) {
		return review.ErrForbidden
	}
	return nil
}
func validateSuccessor(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verification id.Verification, challenged, successor id.Decision) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_decisions old JOIN idenqa.verification_decisions next ON next.tenant_id=old.tenant_id AND next.verification_id=old.verification_id AND next.supersedes_id=old.id WHERE old.tenant_id=$1 AND old.verification_id=$2 AND old.id=$3 AND next.id=$4 AND EXISTS(SELECT 1 FROM idenqa.review_correction_evaluations e WHERE e.tenant_id=next.tenant_id AND e.decision_id=next.id))`, scope.ID().String(), verification.String(), challenged.String(), successor.String()).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return review.ErrForbidden
	}
	return nil
}

func validateCaseProcessing(ctx context.Context, tx pg.Transaction, scope tenant.Scope, value review.Case, at time.Time, source clock.Clock) error {
	if !value.ChallengedDecision.IsZero() {
		return authoritypostgres.ValidateReconsiderationWithin(ctx, tx, scope, value.VerificationID, value.ChallengedDecision, at, source)
	}
	return authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, value.VerificationID, at, source, verification.SessionStateManualReview)
}

func independentOfCase(ctx context.Context, tx pg.Transaction, scope tenant.Scope, value review.Case, reviewer string) error {
	for _, finding := range value.Findings {
		if finding.ReviewerID == reviewer {
			return review.ErrForbidden
		}
	}
	var involved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.review_arbitrations WHERE tenant_id=$1 AND case_id=$2 AND reviewer_id=$3)`, scope.ID().String(), value.ID.String(), reviewer).Scan(&involved); err != nil {
		return err
	}
	if involved {
		return review.ErrForbidden
	}
	return nil
}
