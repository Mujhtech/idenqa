package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Actor is the authenticated tenant actor attributable in the audit chain.
type Actor struct{ ID string }

// Repository owns tenant-scoped optimistic review persistence. Every mutating
// method commits its reference-only audit event in the same transaction.
type Repository interface {
	CreateCase(context.Context, tenant.Scope, Actor, Case) error
	FindCase(context.Context, tenant.Scope, id.ReviewCase) (Case, error)
	SaveCase(context.Context, tenant.Scope, Actor, Case, int64, *Finding) error
	FindGrants(context.Context, tenant.Scope, string, string, []id.Grant, time.Time) ([]EvidenceGrant, error)
	CreateAppeal(context.Context, tenant.Scope, Actor, Appeal, time.Time) error
	FindAppeal(context.Context, tenant.Scope, id.Appeal) (Appeal, error)
	SaveAppeal(context.Context, tenant.Scope, Actor, Appeal, int64, time.Time) error
}

// IdentifierGenerator supplies opaque review identifiers.
type IdentifierGenerator interface {
	NewReviewCase() (id.ReviewCase, error)
	NewFinding() (id.Finding, error)
	NewAppeal() (id.Appeal, error)
}

// Service authorises and coordinates review aggregates independently of HTTP.
type Service struct {
	repository  Repository
	identifiers IdentifierGenerator
	now         func() time.Time
}

// NewService constructs the review application service.
func NewService(repository Repository, identifiers IdentifierGenerator, now func() time.Time) (*Service, error) {
	if repository == nil || identifiers == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository, identifiers: identifiers, now: now}, nil
}

// OpenCase creates one review case linked to an immutable decision.
func (service *Service) OpenCase(ctx context.Context, scope tenant.Scope, actor Actor, verification id.Verification, challenged id.Decision, region, certificate string, oversight Oversight) (Case, error) {
	if actor.ID == "" {
		return Case{}, ErrForbidden
	}
	identifier, err := service.identifiers.NewReviewCase()
	if err != nil {
		return Case{}, fmt.Errorf("generate review case identifier: %w", err)
	}
	value, err := NewCase(identifier, verification, challenged, region, certificate, oversight, service.now().UTC())
	if err != nil {
		return Case{}, err
	}
	if err := service.repository.CreateCase(ctx, scope, actor, value); err != nil {
		return Case{}, err
	}
	return value, nil
}

// FindCase returns only non-evidence case metadata.
func (service *Service) FindCase(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.ReviewCase) (Case, error) {
	if actor.ID == "" || identifier.IsZero() {
		return Case{}, ErrForbidden
	}
	return service.repository.FindCase(ctx, scope, identifier)
}

// Claim applies certification and optimistic assignment rules.
func (service *Service) Claim(ctx context.Context, scope tenant.Scope, actor Actor, principal Principal, identifier id.ReviewCase, expectedVersion int64) (Case, error) {
	value, err := service.repository.FindCase(ctx, scope, identifier)
	if err != nil {
		return Case{}, err
	}
	next, err := value.Claim(principal, expectedVersion, service.now().UTC())
	if err != nil {
		return Case{}, err
	}
	if err := service.repository.SaveCase(ctx, scope, actor, next, expectedVersion, nil); err != nil {
		return Case{}, err
	}
	return next, nil
}

// SubmitFinding resolves grant metadata inside the tenant boundary before the
// aggregate sees it; evidence content never enters this service.
func (service *Service) SubmitFinding(ctx context.Context, scope tenant.Scope, actor Actor, principal Principal, caseID id.ReviewCase, resolution Resolution, reasonCode string, grantIDs []id.Grant, expectedVersion int64) (Case, error) {
	value, err := service.repository.FindCase(ctx, scope, caseID)
	if err != nil {
		return Case{}, err
	}
	now := service.now().UTC()
	grants, err := service.repository.FindGrants(ctx, scope, principal.ID, value.Region, grantIDs, now)
	if err != nil {
		return Case{}, err
	}
	findingID, err := service.identifiers.NewFinding()
	if err != nil {
		return Case{}, fmt.Errorf("generate finding identifier: %w", err)
	}
	finding := Finding{ID: findingID, ReviewerID: principal.ID, Resolution: resolution, ReasonCode: reasonCode, GrantIDs: append([]id.Grant(nil), grantIDs...), RecordedAt: now}
	next, err := value.SubmitFinding(principal, finding, grants, expectedVersion)
	if err != nil {
		return Case{}, err
	}
	if err := service.repository.SaveCase(ctx, scope, actor, next, expectedVersion, &finding); err != nil {
		return Case{}, err
	}
	return next, nil
}

// Correct records immutable decision supersession lineage.
func (service *Service) Correct(ctx context.Context, scope tenant.Scope, actor Actor, principal Principal, caseID id.ReviewCase, superseding id.Decision, expectedVersion int64) (Case, error) {
	value, err := service.repository.FindCase(ctx, scope, caseID)
	if err != nil {
		return Case{}, err
	}
	next, err := value.AuthorCorrection(principal, superseding, expectedVersion, service.now().UTC())
	if err != nil {
		return Case{}, err
	}
	if err := service.repository.SaveCase(ctx, scope, actor, next, expectedVersion, nil); err != nil {
		return Case{}, err
	}
	return next, nil
}

// RequestAppeal creates an independent-review workflow from a resolved case.
func (service *Service) RequestAppeal(ctx context.Context, scope tenant.Scope, actor Actor, caseID id.ReviewCase, deadline time.Time) (Appeal, error) {
	value, err := service.repository.FindCase(ctx, scope, caseID)
	if err != nil {
		return Appeal{}, err
	}
	if value.State != CaseResolved || actor.ID == "" {
		return Appeal{}, ErrConflict
	}
	identifier, err := service.identifiers.NewAppeal()
	if err != nil {
		return Appeal{}, err
	}
	now := service.now().UTC()
	if !deadline.After(now) || deadline.Location() != time.UTC {
		return Appeal{}, ErrInvalid
	}
	reviewers := make([]string, 0, len(value.Findings))
	for _, finding := range value.Findings {
		reviewers = append(reviewers, finding.ReviewerID)
	}
	appeal := Appeal{ID: identifier, CaseID: value.ID, ChallengedDecision: value.ChallengedDecision, OriginalReviewers: reviewers, State: AppealRequested, Deadline: deadline, Version: 1}
	if err := service.repository.CreateAppeal(ctx, scope, actor, appeal, now); err != nil {
		return Appeal{}, err
	}
	return appeal, nil
}

// AssignAppeal assigns an independent reviewer optimistically.
func (service *Service) AssignAppeal(ctx context.Context, scope tenant.Scope, actor Actor, principal Principal, identifier id.Appeal, expectedVersion int64) (Appeal, error) {
	appeal, err := service.repository.FindAppeal(ctx, scope, identifier)
	if err != nil {
		return Appeal{}, err
	}
	now := service.now().UTC()
	next, err := appeal.Assign(principal, expectedVersion, now)
	if err != nil {
		return Appeal{}, err
	}
	if err := service.repository.SaveAppeal(ctx, scope, actor, next, expectedVersion, now); err != nil {
		return Appeal{}, err
	}
	return next, nil
}

// ResolveAppeal records a bounded independent outcome and optional successor.
func (service *Service) ResolveAppeal(ctx context.Context, scope tenant.Scope, actor Actor, principal Principal, identifier id.Appeal, outcome AppealOutcome, reason string, superseding id.Decision, expectedVersion int64) (Appeal, error) {
	appeal, err := service.repository.FindAppeal(ctx, scope, identifier)
	if err != nil {
		return Appeal{}, err
	}
	now := service.now().UTC()
	next, err := appeal.Resolve(principal, outcome, reason, superseding, expectedVersion, now)
	if err != nil {
		return Appeal{}, err
	}
	if err := service.repository.SaveAppeal(ctx, scope, actor, next, expectedVersion, now); err != nil {
		return Appeal{}, err
	}
	return next, nil
}

// IsExpectedFailure reports safe non-disclosing caller failures.
func IsExpectedFailure(err error) bool {
	return errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) || errors.Is(err, ErrForbidden)
}
