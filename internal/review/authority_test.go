package review_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestReviewerAuthorityIsolationExpiryAndRotation(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	scope, _ := tenant.NewScope(tenantID)
	key, _ := id.ParseAPIKey("key_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	assignment := review.Assignment{TenantID: tenantID.String(), APIKeyID: key.String(), OperatorID: "operator-1", Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"}, Regions: []string{"ng-1"}, NotBefore: now, ExpiresAt: now.Add(time.Hour)}
	registry, err := review.NewRegistry([]review.Assignment{assignment})
	if err != nil {
		t.Fatal(err)
	}
	assignment.Permissions[0] = review.PermissionResolve
	actor := review.Actor{ID: key.String()}
	for _, test := range []struct {
		name, region string
		scope        tenant.Scope
		actor        review.Actor
		at           time.Time
		allowed      bool
	}{
		{"valid", "ng-1", scope, actor, now, true},
		{"early", "ng-1", scope, actor, now.Add(-time.Nanosecond), false},
		{"expired", "ng-1", scope, actor, now.Add(time.Hour), false},
		{"region", "us-1", scope, actor, now, false},
		{"tenant", "ng-1", tenant.Scope{}, actor, now, false},
		{"credential", "ng-1", scope, review.Actor{ID: "other"}, now, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			principal, err := registry.ResolveReviewer(context.Background(), test.scope, test.actor, test.region, test.at)
			if test.allowed {
				if err != nil || principal.ID != "operator-1" || principal.Permissions[0] != review.PermissionClaim {
					t.Fatalf("principal=%+v error=%v", principal, err)
				}
				principal.Permissions[0] = review.PermissionResolve
			} else if !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := review.NewRegistry([]review.Assignment{assignment, assignment}); !errors.Is(err, review.ErrInvalid) {
		t.Fatalf("duplicate error=%v", err)
	}
	rotated := assignment
	rotated.APIKeyID = "key_01K4AR9V8FQ2G7ZXCPNM5T6JWM"
	registry, err = review.NewRegistry([]review.Assignment{assignment, rotated})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := registry.ResolveReviewer(context.Background(), scope, review.Actor{ID: rotated.APIKeyID}, "ng-1", now)
	if err != nil || principal.ID != assignment.OperatorID {
		t.Fatalf("rotation changed operator: %+v %v", principal, err)
	}
}

type authorityCaseRepository struct {
	review.Repository
	value review.Case
	saves int
}

func (repository *authorityCaseRepository) FindCase(context.Context, tenant.Scope, id.ReviewCase) (review.Case, error) {
	return repository.value, nil
}
func (repository *authorityCaseRepository) SaveCase(_ context.Context, _ tenant.Scope, _ review.Actor, value review.Case, _ int64, _ *review.Finding) error {
	repository.saves++
	repository.value = value
	return nil
}

func TestServiceRequiresServerAuthority(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	scope, _ := tenant.NewScope(tenantID)
	key, _ := ids.NewAPIKey()
	actor := review.Actor{ID: key.String()}
	caseID, _ := ids.NewReviewCase()
	verificationID, _ := ids.NewVerification()
	decisionID, _ := ids.NewDecision()
	value, err := review.NewCase(caseID, verificationID, decisionID, "ng-1", "document.level2", review.OversightDual, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := &authorityCaseRepository{value: value}
	service, err := review.NewService(repository, ids, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(context.Background(), scope, actor, caseID, 1); !errors.Is(err, review.ErrForbidden) || repository.saves != 0 {
		t.Fatalf("unconfigured authority error=%v saves=%d", err, repository.saves)
	}
	registry, err := review.NewRegistry([]review.Assignment{{TenantID: tenantID.String(), APIKeyID: key.String(), OperatorID: "operator-1", Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"}, Regions: []string{"ng-1"}, NotBefore: now, ExpiresAt: now.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	service, err = review.NewAuthorizedService(repository, ids, func() time.Time { return now }, registry)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(context.Background(), scope, actor, caseID, 1)
	if err != nil || claimed.AssignedReviewer != "operator-1" || repository.saves != 1 {
		t.Fatalf("claim=%+v error=%v", claimed, err)
	}
	if _, err := service.Claim(context.Background(), scope, actor, caseID, 1); !errors.Is(err, review.ErrConflict) || repository.saves != 1 {
		t.Fatalf("stale version error=%v saves=%d", err, repository.saves)
	}
}
