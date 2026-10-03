package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewbrowser"
	"github.com/Mujhtech/idenqa/internal/reviewdelegation"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestCheckDelegationBindingRequiresExactOperationTargetAndVersion(t *testing.T) {
	tenantID, err := id.ParseTenant("ten_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	caseID, err := id.ParseReviewCase("rvc_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseReviewCase() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	value := review.Case{ID: caseID, Region: "eu-west-1"}
	claims := reviewdelegation.Claims{Intent: reviewdelegation.Intent{
		ActorID: "usr_1", Scope: reviewdelegation.Scope{TenantID: tenantID.String(), Region: value.Region},
		TargetKind: "review_case", TargetID: caseID.String(), Operation: "claim",
		Permission: "review:claim", ExpectedVersion: 4,
	}}
	ctx := reviewdelegation.WithClaims(context.Background(), claims)
	if err := checkDelegationBinding(ctx, scope, review.Actor{ID: "usr_1"}, value, review.PermissionClaim, 4); err != nil {
		t.Fatalf("checkDelegationBinding() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*reviewdelegation.Claims)
	}{
		{name: "actor", mutate: func(value *reviewdelegation.Claims) { value.ActorID = "usr_2" }},
		{name: "target", mutate: func(value *reviewdelegation.Claims) { value.TargetID = "rev_other" }},
		{name: "version", mutate: func(value *reviewdelegation.Claims) { value.ExpectedVersion++ }},
		{name: "operation", mutate: func(value *reviewdelegation.Claims) { value.Operation = "submit_finding" }},
		{name: "permission", mutate: func(value *reviewdelegation.Claims) { value.Permission = "review:find" }},
		{name: "region", mutate: func(value *reviewdelegation.Claims) { value.Scope.Region = "us-east-1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := claims
			test.mutate(&changed)
			err := checkDelegationBinding(reviewdelegation.WithClaims(context.Background(), changed), scope, review.Actor{ID: "usr_1"}, value, review.PermissionClaim, 4)
			if !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("checkDelegationBinding() error = %v, want %v", err, review.ErrForbidden)
			}
		})
	}
}

func TestAuthorityRejectsBrowserSessionBindingMismatch(t *testing.T) {
	t.Parallel()

	tenantID, err := id.ParseTenant("ten_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	otherTenantID, err := id.ParseTenant("ten_01M3NRK3Z6BA1MMMR66QMM1NRP")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	caseID, err := id.ParseReviewCase("rvc_01M3NRK3Z6BA1MMMR66QMM1NRN")
	if err != nil {
		t.Fatalf("ParseReviewCase() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	otherScope, err := tenant.NewScope(otherTenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	base := reviewbrowser.Session{
		Scope: scope, Actor: review.Actor{ID: "usr_reviewer"}, CaseID: caseID,
		Version: 4, Region: "eu-west-1", Origin: "https://console.example", ExpiresAt: now.Add(time.Minute),
	}
	tests := []struct {
		name   string
		scope  tenant.Scope
		actor  review.Actor
		region string
		at     time.Time
	}{
		{name: "tenant", scope: otherScope, actor: base.Actor, region: base.Region, at: now},
		{name: "actor", scope: scope, actor: review.Actor{ID: "usr_other"}, region: base.Region, at: now},
		{name: "region", scope: scope, actor: base.Actor, region: "us-east-1", at: now},
		{name: "expired", scope: scope, actor: base.Actor, region: base.Region, at: base.ExpiresAt},
	}
	authority := &Authority{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := reviewbrowser.WithSession(context.Background(), base)
			_, err := authority.ResolveReviewer(ctx, test.scope, test.actor, test.region, test.at)
			if !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("ResolveReviewer() error = %v, want %v", err, review.ErrForbidden)
			}
		})
	}
}
