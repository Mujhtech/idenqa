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

type recordingVerifier struct {
	err   error
	calls []review.CertificateAssertion
}

func (verifier *recordingVerifier) Verify(_ context.Context, _ tenant.Scope, assertion review.CertificateAssertion) error {
	verifier.calls = append(verifier.calls, assertion)
	return verifier.err
}

func TestReviewerAuthorityExternalCertificationAssertions(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	scope, _ := tenant.NewScope(tenantID)
	key, _ := id.ParseAPIKey("key_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	assertion := review.CertificateAssertion{Certificate: "document.level2", Region: "ng-1", Token: "v1.payload.signature"}
	base := review.Assignment{
		TenantID: tenantID.String(), APIKeyID: key.String(), OperatorID: "operator-1",
		Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"},
		CertificationAssertions: []review.CertificateAssertion{assertion}, Regions: []string{"ng-1"},
		NotBefore: now, ExpiresAt: now.Add(time.Hour),
	}
	actor := review.Actor{ID: key.String()}

	// A configured verifier requires the exact configured assertion and passes
	// the operator identity, certificate and region binding to verification.
	verifier := &recordingVerifier{}
	registry, err := review.NewRegistry([]review.Assignment{base})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := registry.WithCertificationVerifier(verifier).ResolveReviewer(context.Background(), scope, actor, "ng-1", now)
	if err != nil || principal.ID != "operator-1" || len(verifier.calls) != 1 {
		t.Fatalf("verified principal=%+v calls=%+v error=%v", principal, verifier.calls, err)
	}
	if verifier.calls[0].ReviewerID != "operator-1" || verifier.calls[0].Certificate != "document.level2" || verifier.calls[0].Region != "ng-1" || verifier.calls[0].Token != assertion.Token {
		t.Fatalf("verifier binding = %+v", verifier.calls[0])
	}
	verifier.err = review.ErrForbidden
	if _, err := registry.WithCertificationVerifier(verifier).ResolveReviewer(context.Background(), scope, actor, "ng-1", now); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("denied assertion error=%v", err)
	}

	// Without a configured verifier the tenant-attested behaviour is unchanged.
	attested, err := review.NewRegistry([]review.Assignment{base})
	if err != nil {
		t.Fatal(err)
	}
	principal, err = attested.ResolveReviewer(context.Background(), scope, actor, "ng-1", now)
	if err != nil || principal.ID != "operator-1" {
		t.Fatalf("tenant-attested principal=%+v error=%v", principal, err)
	}

	// A configured verifier fails closed when the assignment carries no assertion.
	unasserted := base
	unasserted.CertificationAssertions = nil
	registry, err = review.NewRegistry([]review.Assignment{unasserted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.WithCertificationVerifier(&recordingVerifier{}).ResolveReviewer(context.Background(), scope, actor, "ng-1", now); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("missing assertion error=%v", err)
	}
}

func TestReviewRegistryRejectsInvalidCertificationAssertions(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	base := review.Assignment{ // #nosec G101 -- public test fixtures, not credentials.
		TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", APIKeyID: "key_01K4AR9V8FQ2G7ZXCPNM5T6JWH", OperatorID: "operator-1",
		Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"}, Regions: []string{"ng-1"},
		NotBefore: now, ExpiresAt: now.Add(time.Hour),
	}
	for _, test := range []struct {
		name       string
		assertions []review.CertificateAssertion
	}{
		{name: "unknown_certificate", assertions: []review.CertificateAssertion{{Certificate: "document.level3", Region: "ng-1", Token: "v1.token"}}},
		{name: "unknown_region", assertions: []review.CertificateAssertion{{Certificate: "document.level2", Region: "us-1", Token: "v1.token"}}},
		{name: "empty_token", assertions: []review.CertificateAssertion{{Certificate: "document.level2", Region: "ng-1"}}},
		{name: "whitespace_token", assertions: []review.CertificateAssertion{{Certificate: "document.level2", Region: "ng-1", Token: "v1 token"}}},
		{name: "mismatched_reviewer", assertions: []review.CertificateAssertion{{ReviewerID: "operator-2", Certificate: "document.level2", Region: "ng-1", Token: "v1.token"}}},
		{name: "duplicate_binding", assertions: []review.CertificateAssertion{
			{Certificate: "document.level2", Region: "ng-1", Token: "v1.token"},
			{Certificate: "document.level2", Region: "ng-1", Token: "v1.other"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assignment := base
			assignment.CertificationAssertions = test.assertions
			if _, err := review.NewRegistry([]review.Assignment{assignment}); !errors.Is(err, review.ErrInvalid) {
				t.Fatalf("NewRegistry() error = %v, want ErrInvalid", err)
			}
		})
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
