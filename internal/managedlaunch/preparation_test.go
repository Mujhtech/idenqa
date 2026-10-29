package managedlaunch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type noticeStub struct {
	notice authority.Notice
	scope  tenant.Scope
}

func (stub *noticeStub) FindNotice(_ context.Context, scope tenant.Scope, _ id.Notice) (authority.Notice, error) {
	stub.scope = scope
	return stub.notice, nil
}

type declarationStub struct {
	input         authority.DeclarationInput
	actor         verification.SessionPrincipal
	delegate, key string
}

func (stub *declarationStub) DeclareManaged(_ context.Context, _ tenant.Scope, actor verification.SessionPrincipal, delegate, key string, input authority.DeclarationInput) (authority.Authority, error) {
	stub.input, stub.actor, stub.delegate, stub.key = input, actor, delegate, key
	return authority.Authority{}, nil
}

func TestPreparationRequiresExactConfiguredRouteAndTenantNotice(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	claims := validClaims(now, Binding{OrganisationID: "org_1", EnvironmentID: "env_sandbox", DeploymentID: "dep_1", Region: "eu-west-1"})
	tenantID, _ := id.ParseTenant(claims.CoreTenantID)
	noticeID, _ := id.ParseNotice("ntc_01HZZZZZZZZZZZZZZZZZZZZZZZ")
	actorID, _ := id.ParseAPIKey("key_01HZZZZZZZZZZZZZZZZZZZZZZZ")
	notice, err := authority.NewNotice(authority.NoticeRecord{ID: noticeID, TenantID: tenantID, Key: "tenant.notice.preview", Locale: "en", Controller: "Test tenant", Recipient: "Test tenant", Copy: authority.NoticeCopy{Title: "Test", Summary: "Synthetic test", Purpose: "Test identity flow", Consequences: "Refusing stops capture"}, EffectiveAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour), CreatedBy: actorID})
	if err != nil {
		t.Fatal(err)
	}
	route := PreparationRoute{TenantID: claims.CoreTenantID, ProfileID: claims.ProfileID, PolicyID: claims.PolicyID, NoticeID: noticeID.String(), Locale: "en", Category: "tenant.authority.customer_declared", Purpose: "idenqa.purpose.identity_verification", Jurisdiction: "tenant.jurisdiction.test", PolicyPack: "tenant.policy.test", RecipientReference: "tenant.recipient.test", RetentionReference: "tenant.retention.test"}
	notices := &noticeStub{notice: notice}
	prepare, err := NewPreparation(PreparationConfiguration{Routes: []PreparationRoute{route}}, notices, &declarationStub{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := prepare.Validate(t.Context(), claims); err != nil || notices.scope.ID() != tenantID {
		t.Fatalf("exact route: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*Claims)
	}{
		{"tenant", func(c *Claims) { c.CoreTenantID = "ten_01ARZ3NDEKTSV4RRFFQ69G5FB3" }},
		{"profile", func(c *Claims) { c.ProfileID = "prf_01ARZ3NDEKTSV4RRFFQ69G5FB3" }},
		{"policy", func(c *Claims) { c.PolicyID = "pol_01ARZ3NDEKTSV4RRFFQ69G5FB3" }},
		{"locale", func(c *Claims) { c.Locale = "fr" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := claims
			test.change(&changed)
			if err := prepare.Validate(t.Context(), changed); !errors.Is(err, ErrForbidden) {
				t.Fatalf("changed route: %v", err)
			}
		})
	}
	if _, err := NewPreparation(PreparationConfiguration{Routes: []PreparationRoute{route, route}}, notices, &declarationStub{}, time.Now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate: %v", err)
	}
	for _, material := range []string{`{"routes":[]}`, `{"routes":[],"unknown":true}`, `{"routes":[]} {}`} {
		if _, err := ParsePreparationConfiguration([]byte(material)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid configuration accepted: %s", material)
		}
	}
}
