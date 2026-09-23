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

// List returns a bounded newest-first page from one verification's immutable
// decision lineage. The before decision is an opaque tenant-scoped cursor.
func (reader *Reader) List(
	ctx context.Context,
	authority access.Context,
	verificationID id.Verification,
	before id.Decision,
	limit int,
) ([]ReproductionReport, error) {
	if err := reader.ready(); err != nil {
		return nil, err
	}
	if err := authority.Require(access.PermissionDecisionsRead); err != nil {
		return nil, err
	}
	if verificationID.IsZero() || limit < 1 || limit > 100 {
		return nil, ErrDecisionNotFound
	}
	history, ok := reader.repository.(HistoryRepository)
	if !ok {
		return nil, errors.New("policy: decision history is unavailable")
	}
	decisions, err := history.List(ctx, authority.TenantScope(), verificationID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list policy decisions: %w", err)
	}
	reports := make([]ReproductionReport, 0, len(decisions))
	for _, decision := range decisions {
		report, reportErr := reproduceReport(decision)
		if reportErr != nil {
			return nil, reportErr
		}
		reports = append(reports, report)
	}
	return reports, nil
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
