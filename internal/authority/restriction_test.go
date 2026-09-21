package authority_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type blockingRestrictionGate struct {
	blocked bool
	calls   int
}

func (gate *blockingRestrictionGate) Blocked(_ context.Context, _ tenant.Scope, _ string) (bool, error) {
	gate.calls++
	return gate.blocked, nil
}

func TestAuthorizeEvidenceHonoursSubjectRestrictionGate(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, true)
	session := authoritySession(t, fixture)
	repository := &authorityServiceStub{snapshot: authority.Snapshot{
		Authority: fixture.authority, Notice: fixture.notice, Response: fixture.response,
	}, session: session}
	service, err := authority.NewService(
		repository, repository, repository, repository, repository,
		authorityClock{now: fixture.now}, time.Hour,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	scope, err := tenant.NewScope(fixture.request.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	evidenceID, err := id.ParseEvidence("evd_01ARZ3NDEKTSV4RRFFQ69G5FB3")
	if err != nil {
		t.Fatalf("ParseEvidence() error = %v", err)
	}
	request := evidence.ReadAuthorization{
		TenantID: fixture.request.TenantID, SubjectID: fixture.request.SubjectID,
		VerificationID: fixture.request.VerificationID, EvidenceID: evidenceID,
		RequirementKey: "selfie", Purpose: evidence.Name(fixture.request.Purpose),
		EvidenceType:       evidence.Name(fixture.request.EvidenceType),
		RecipientReference: fixture.request.RecipientReference, Region: fixture.request.Region,
	}
	gate := &blockingRestrictionGate{blocked: true}
	service.WithRestrictionGate(gate)
	if _, err := service.AuthorizeEvidence(context.Background(), scope, request); !errors.Is(err, authority.ErrProcessingNotPermitted) {
		t.Fatalf("blocked AuthorizeEvidence() error = %v, want ErrProcessingNotPermitted", err)
	}
	if gate.calls != 1 {
		t.Fatalf("gate calls = %d", gate.calls)
	}
	gate.blocked = false
	if _, err := service.AuthorizeEvidence(context.Background(), scope, request); err != nil {
		t.Fatalf("lifted AuthorizeEvidence() error = %v", err)
	}
}
