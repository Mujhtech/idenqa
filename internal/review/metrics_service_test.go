package review_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type recordingReviewMetrics struct {
	resolutions []observability.ReviewResolution
}

func (metrics *recordingReviewMetrics) RecordReviewResolution(resolution observability.ReviewResolution) {
	metrics.resolutions = append(metrics.resolutions, resolution)
}

type reviewRepositoryStub struct {
	value  review.Case
	grants []review.EvidenceGrant
}

func (*reviewRepositoryStub) CreateCase(context.Context, tenant.Scope, review.Actor, review.Case) error {
	return nil
}

func (repository *reviewRepositoryStub) FindCase(context.Context, tenant.Scope, id.ReviewCase) (review.Case, error) {
	return repository.value, nil
}

func (repository *reviewRepositoryStub) FindCaseForVerification(context.Context, tenant.Scope, id.Verification) (review.Case, error) {
	return repository.value, nil
}

func (repository *reviewRepositoryStub) SaveCase(_ context.Context, _ tenant.Scope, _ review.Actor, value review.Case, _ int64, _ *review.Finding) error {
	repository.value = value
	return nil
}

func (repository *reviewRepositoryStub) FindGrants(context.Context, tenant.Scope, string, string, []id.Grant, time.Time) ([]review.EvidenceGrant, error) {
	return repository.grants, nil
}

func (*reviewRepositoryStub) CreateAppeal(context.Context, tenant.Scope, review.Actor, review.Appeal, time.Time) error {
	return nil
}

func (*reviewRepositoryStub) FindAppeal(context.Context, tenant.Scope, id.Appeal) (review.Appeal, error) {
	return review.Appeal{}, nil
}

func (*reviewRepositoryStub) SaveAppeal(context.Context, tenant.Scope, review.Actor, review.Appeal, int64, time.Time) error {
	return nil
}

type reviewAuthorityStub struct{ principal review.Principal }

func (authority reviewAuthorityStub) ResolveReviewer(context.Context, tenant.Scope, review.Actor, string, time.Time) (review.Principal, error) {
	return authority.principal, nil
}

func TestSubmitFindingRecordsBoundedReviewResolution(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	generator, _ := id.NewSystemGenerator()
	tenantID, _ := generator.NewTenant()
	scope, _ := tenant.NewScope(tenantID)
	caseID, _ := generator.NewReviewCase()
	verificationID, _ := generator.NewVerification()
	challenged, _ := generator.NewDecision()
	grantID, _ := generator.NewGrant()
	principal := review.Principal{ID: "reviewer-1", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
	base, err := review.NewCase(caseID, verificationID, challenged, "ng-1", "document.level2", review.OversightSingle, now)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := base.Claim(principal, 1, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	grant := review.EvidenceGrant{ID: grantID, ReviewerID: principal.ID, Region: "ng-1", ExpiresAt: now.Add(time.Hour)}
	repository := &reviewRepositoryStub{value: claimed, grants: []review.EvidenceGrant{grant}}
	metrics := &recordingReviewMetrics{}
	service, err := review.NewAuthorizedService(repository, generator, func() time.Time { return now.Add(2 * time.Minute) }, reviewAuthorityStub{principal})
	if err != nil {
		t.Fatal(err)
	}
	service.WithMetrics(metrics)
	if _, err := service.SubmitFinding(t.Context(), scope, review.Actor{ID: "operator-1"}, caseID, review.ResolutionSatisfy, "document_authentic", []id.Grant{grantID}, claimed.Version); err != nil {
		t.Fatal(err)
	}
	if len(metrics.resolutions) != 1 {
		t.Fatalf("resolutions = %d, want 1", len(metrics.resolutions))
	}
	resolution := metrics.resolutions[0]
	if resolution.Outcome != observability.ReviewSatisfied || resolution.Oversight != observability.OversightSingle ||
		resolution.Duration != 2*time.Minute || resolution.Region != "ng-1" {
		t.Fatalf("resolution = %+v", resolution)
	}
}
