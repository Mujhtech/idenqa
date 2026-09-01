package policy

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Reader authorises tenant decision inspection and portable export separately.
type Reader struct{ repository Repository }

// NewReader constructs the tenant decision-read application service.
func NewReader(repository Repository) (*Reader, error) {
	if repository == nil {
		return nil, errors.New("policy: decision reader repository is required")
	}

	return &Reader{repository: repository}, nil
}

// Find returns a safe reproduction report for one exact immutable decision.
func (reader *Reader) Find(
	ctx context.Context,
	authority access.Context,
	decisionID id.Decision,
) (ReproductionReport, error) {
	if err := reader.ready(); err != nil {
		return ReproductionReport{}, err
	}
	if err := authority.Require(access.PermissionDecisionsRead); err != nil {
		return ReproductionReport{}, err
	}
	if decisionID.IsZero() {
		return ReproductionReport{}, ErrDecisionNotFound
	}
	decision, err := reader.repository.Find(ctx, authority.TenantScope(), decisionID)
	if err != nil {
		return ReproductionReport{}, fmt.Errorf("read policy decision: %w", err)
	}

	return reproduceReport(decision)
}

// FindLatest returns a safe report for the current leaf of one verification's
// immutable decision lineage.
func (reader *Reader) FindLatest(
	ctx context.Context,
	authority access.Context,
	verificationID id.Verification,
) (ReproductionReport, error) {
	if err := reader.ready(); err != nil {
		return ReproductionReport{}, err
	}
	if err := authority.Require(access.PermissionDecisionsRead); err != nil {
		return ReproductionReport{}, err
	}
	if verificationID.IsZero() {
		return ReproductionReport{}, ErrDecisionNotFound
	}
	decision, err := reader.repository.FindLatest(ctx, authority.TenantScope(), verificationID)
	if err != nil {
		return ReproductionReport{}, fmt.Errorf("read latest policy decision: %w", err)
	}

	return reproduceReport(decision)
}

// Export returns exact portable bytes only under the separate export permission.
func (reader *Reader) Export(
	ctx context.Context,
	authority access.Context,
	decisionID id.Decision,
) (DecisionBundle, ReproductionReport, error) {
	if err := reader.ready(); err != nil {
		return DecisionBundle{}, ReproductionReport{}, err
	}
	if err := authority.Require(access.PermissionDecisionsExport); err != nil {
		return DecisionBundle{}, ReproductionReport{}, err
	}
	if decisionID.IsZero() {
		return DecisionBundle{}, ReproductionReport{}, ErrDecisionNotFound
	}
	decision, err := reader.repository.Find(ctx, authority.TenantScope(), decisionID)
	if err != nil {
		return DecisionBundle{}, ReproductionReport{}, fmt.Errorf("export policy decision: %w", err)
	}
	bundle, report, err := NewDecisionBundle(decision)
	if err != nil {
		return DecisionBundle{}, ReproductionReport{}, fmt.Errorf("reproduce policy decision export: %w", err)
	}

	return bundle, report, nil
}

func (reader *Reader) ready() error {
	if reader == nil || reader.repository == nil {
		return errors.New("policy: decision reader is not initialised")
	}

	return nil
}

func reproduceReport(decision Decision) (ReproductionReport, error) {
	_, report, err := NewDecisionBundle(decision)
	if err != nil {
		return ReproductionReport{}, fmt.Errorf("reproduce policy decision: %w", err)
	}

	return report, nil
}
