package postgres

import (
	"context"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

// AuthorityChecker adapts processing-authority validation to PostgreSQL.
type AuthorityChecker struct {
	pool  transactionRunner
	clock clock.Clock
}

// NewAuthorityChecker constructs an authority-checker adapter.
func NewAuthorityChecker(pool transactionRunner, source clock.Clock) *AuthorityChecker {
	return &AuthorityChecker{pool: pool, clock: source}
}

// Allowed verifies that the verification session has a current active processing
// authority whose validity window contains the observation time.
func (checker *AuthorityChecker) Allowed(ctx context.Context, tenantID string, verificationID string) (bool, error) {
	tenant, verification, err := parseAuthorityIDs(tenantID, verificationID)
	if err != nil {
		return false, err
	}
	allowed := false
	err = checker.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenant.String()); err != nil {
			return err
		}
		observedAt := checker.clock.Now().UTC()
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM idenqa.processing_authorities a
			WHERE a.tenant_id=$1 AND a.verification_id=$2 AND a.state='active'
			  AND a.valid_from <= $3 AND $3 < a.expires_at
		)`, tenant.String(), verification.String(), observedAt).Scan(&allowed)
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// RegionValidator adapts jurisdiction/residency validation to PostgreSQL.
type RegionValidator struct {
	pool  transactionRunner
	clock clock.Clock
}

// NewRegionValidator constructs a region-validator adapter.
func NewRegionValidator(pool transactionRunner, source clock.Clock) *RegionValidator {
	return &RegionValidator{pool: pool, clock: source}
}

// Allowed verifies that the verification session's pinned region is one of the
// regions permitted by its current active processing authority.
func (validator *RegionValidator) Allowed(ctx context.Context, tenantID string, verificationID string) (bool, error) {
	tenant, verification, err := parseAuthorityIDs(tenantID, verificationID)
	if err != nil {
		return false, err
	}
	allowed := false
	err = validator.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenant.String()); err != nil {
			return err
		}
		observedAt := validator.clock.Now().UTC()
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM idenqa.processing_authorities a
			JOIN idenqa.verification_sessions s ON s.tenant_id=a.tenant_id AND s.id=a.verification_id
			WHERE a.tenant_id=$1 AND a.verification_id=$2 AND a.state='active'
			  AND a.valid_from <= $3 AND $3 < a.expires_at
			  AND s.region = ANY(a.regions)
		)`, tenant.String(), verification.String(), observedAt).Scan(&allowed)
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}

func parseAuthorityIDs(tenantID string, verificationID string) (id.Tenant, id.Verification, error) {
	tenant, err := id.ParseTenant(tenantID)
	if err != nil {
		return id.Tenant{}, id.Verification{}, proposal.ErrInvalid
	}
	verification, err := id.ParseVerification(verificationID)
	if err != nil {
		return id.Tenant{}, id.Verification{}, proposal.ErrInvalid
	}
	return tenant, verification, nil
}

var _ proposal.AuthorityChecker = (*AuthorityChecker)(nil)
var _ proposal.RegionValidator = (*RegionValidator)(nil)

// EvidenceChecker adapts evidence/signal reference validation to PostgreSQL.
type EvidenceChecker struct {
	pool transactionRunner
}

// NewEvidenceChecker constructs an evidence-reference checker adapter.
func NewEvidenceChecker(pool transactionRunner) *EvidenceChecker {
	return &EvidenceChecker{pool: pool}
}

// Exists verifies that a reference resolves to a session-owned evidence asset or
// immutable observation. References must use the evd_ or obs_ identifier shape.
func (checker *EvidenceChecker) Exists(ctx context.Context, tenantID string, verificationID string, reference string) (bool, error) {
	tenant, verification, err := parseAuthorityIDs(tenantID, verificationID)
	if err != nil {
		return false, err
	}
	exists := false
	err = checker.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenant.String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM idenqa.evidence_assets e WHERE e.tenant_id=$1 AND e.verification_id=$2 AND e.id=$3
			UNION ALL
			SELECT 1 FROM idenqa.verification_observations o WHERE o.tenant_id=$1 AND o.verification_id=$2 AND o.id=$3
		)`, tenant.String(), verification.String(), reference).Scan(&exists)
	})
	if err != nil {
		return false, err
	}
	return exists, nil
}

var _ proposal.EvidenceChecker = (*EvidenceChecker)(nil)
