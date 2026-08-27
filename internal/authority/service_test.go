package authority_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestServiceAuthorizeEvidenceReturnsPinnedLiveDecision(t *testing.T) {
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
		RequirementKey: "selfie",
		Purpose:        evidence.Name(fixture.request.Purpose), EvidenceType: evidence.Name(fixture.request.EvidenceType),
		RecipientReference: fixture.request.RecipientReference, Region: fixture.request.Region,
	}
	decision, err := service.AuthorizeEvidence(context.Background(), scope, request)
	if err != nil {
		t.Fatalf("AuthorizeEvidence() error = %v", err)
	}
	if decision.AuthorityID != fixture.authority.ID() ||
		decision.ResponseID != fixture.response.Record().ID ||
		decision.PolicyReference != fixture.authority.Record().PolicyPack {
		t.Fatalf("decision = %+v", decision)
	}
	mismatchedRequirement := request
	mismatchedRequirement.RequirementKey = "different_requirement"
	if _, err := service.AuthorizeEvidence(context.Background(), scope, mismatchedRequirement); !errors.Is(err, authority.ErrProcessingNotPermitted) {
		t.Fatalf("AuthorizeEvidence(mismatched requirement) error = %v, want ErrProcessingNotPermitted", err)
	}

	withdrawn := fixture.authority
	if err := withdrawn.Withdraw(fixture.now.Add(time.Minute)); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	repository.snapshot.Authority = withdrawn
	if _, err := service.AuthorizeEvidence(context.Background(), scope, request); !errors.Is(err, authority.ErrProcessingNotPermitted) {
		t.Fatalf("AuthorizeEvidence(withdrawn) error = %v, want ErrProcessingNotPermitted", err)
	}
}

type authorityClock struct{ now time.Time }

func (source authorityClock) Now() time.Time { return source.now }

type authorityServiceStub struct {
	snapshot authority.Snapshot
	session  verification.Session
}

func (stub *authorityServiceStub) CreateNotice(
	context.Context,
	tenant.Scope,
	authority.NoticeMutation,
) (authority.Notice, error) {
	return authority.Notice{}, nil
}

func (stub *authorityServiceStub) FindNotice(
	context.Context,
	tenant.Scope,
	id.Notice,
) (authority.Notice, error) {
	return authority.Notice{}, nil
}

func (stub *authorityServiceStub) Declare(
	context.Context,
	tenant.Scope,
	authority.DeclarationMutation,
) (authority.Authority, error) {
	return authority.Authority{}, nil
}

func (stub *authorityServiceStub) FindByVerification(
	context.Context,
	tenant.Scope,
	id.Verification,
) (authority.Authority, error) {
	return authority.Authority{}, nil
}

func (stub *authorityServiceStub) ReplayAuthority(
	context.Context,
	tenant.Scope,
	idempotency.Request,
) (authority.Authority, bool, error) {
	return authority.Authority{}, false, nil
}

func (stub *authorityServiceStub) Transition(
	context.Context,
	tenant.Scope,
	authority.TransitionMutation,
) (authority.Authority, error) {
	return authority.Authority{}, nil
}

func (stub *authorityServiceStub) AppendResponse(
	context.Context,
	tenant.Scope,
	authority.ResponseMutation,
) (authority.Response, error) {
	return authority.Response{}, nil
}

func (stub *authorityServiceStub) CaptureSnapshot(
	context.Context,
	tenant.Scope,
	id.Verification,
) (authority.Snapshot, error) {
	return stub.snapshot, nil
}

func (stub *authorityServiceStub) FindSession(
	context.Context,
	tenant.Scope,
	id.Verification,
) (verification.Session, error) {
	return stub.session, nil
}

func (stub *authorityServiceStub) NewNotice() (id.Notice, error) { return id.Notice{}, nil }

func (stub *authorityServiceStub) NewAuthority() (id.Authority, error) { return id.Authority{}, nil }

func (stub *authorityServiceStub) NewSubject() (id.Subject, error) { return id.Subject{}, nil }

func (stub *authorityServiceStub) NewAcknowledgement() (id.Acknowledgement, error) {
	return id.Acknowledgement{}, nil
}

func (stub *authorityServiceStub) NewEvent() (id.Event, error) { return id.Event{}, nil }

func authoritySession(t *testing.T, fixture fixture) verification.Session {
	t.Helper()
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	profile, err := verification.NewProfile(registry, []verification.Requirement{{
		Key: "selfie", Purpose: evidence.Name(fixture.request.Purpose),
		EvidenceType: evidence.Name(fixture.request.EvidenceType),
		Artefacts:    []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition: verification.Acquisition{
			Strategy: verification.StrategyAnyOf,
			Methods:  []evidence.Name{evidence.MethodFileUpload},
		},
	}})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	digest, err := verification.Digest(profile, registry)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	profileID, err := id.ParseProfile("prf_01ARZ3NDEKTSV4RRFFQ69G5FB5")
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	session, err := verification.RestoreSession(
		fixture.request.VerificationID, fixture.request.TenantID,
		verification.SessionStateCollecting, 1, profileID, 1, digest, profile,
		"local",
		policyID,
		fixture.now.Add(-time.Hour), fixture.now, fixture.now.Add(time.Hour), registry,
	)
	if err != nil {
		t.Fatalf("RestoreSession() error = %v", err)
	}
	return session
}
