package authority_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestServiceDeclareValidatesPurposeAgainstPinnedRequirements(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		profilePurpose string
		purpose        string
		wantConflict   bool
	}{
		{name: "matching identity purpose", profilePurpose: "idenqa.purpose.identity_verification", purpose: "idenqa.purpose.identity_verification"},
		{name: "matching fraud purpose", profilePurpose: "idenqa.purpose.fraud_prevention", purpose: "idenqa.purpose.fraud_prevention"},
		{name: "absent purpose", profilePurpose: "idenqa.purpose.identity_verification", purpose: "idenqa.purpose.fraud_prevention", wantConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newFixture(t, true)
			fixture.request.Purpose = test.profilePurpose
			repository := &authorityServiceStub{snapshot: authority.Snapshot{Notice: fixture.notice}, session: authoritySession(t, fixture)}
			identifiers, err := id.NewGenerator(authorityClock{now: fixture.now}, bytes.NewReader(bytes.Repeat([]byte{1}, 512)))
			if err != nil {
				t.Fatalf("NewGenerator() error = %v", err)
			}
			service, err := authority.NewService(repository, repository, repository, repository, identifiers, authorityClock{now: fixture.now}, time.Hour)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			record := fixture.authority.Record()
			declaration, err := service.Declare(t.Context(), authorityAccess(t, fixture), "declare-purpose-test", authority.DeclarationInput{
				VerificationID: record.VerificationID, NoticeID: record.NoticeID,
				Category: record.Category, Purpose: test.purpose, Jurisdiction: record.Jurisdiction,
				PolicyPack: record.PolicyPack, IsConsentRequired: record.IsConsentRequired,
				RecipientReference: record.RecipientReference, RecipientDisplayName: record.RecipientDisplayName,
				Regions: record.Regions, RetentionReference: record.RetentionReference,
				ValidFrom: record.ValidFrom, ExpiresAt: record.ExpiresAt,
			})
			if test.wantConflict {
				if !errors.Is(err, authority.ErrConflict) {
					t.Fatalf("Declare() error = %v, want ErrConflict", err)
				}
				if repository.declared != nil {
					t.Fatal("an incompatible purpose was persisted")
				}
				return
			}
			if err != nil {
				t.Fatalf("Declare() error = %v", err)
			}
			if declaration.Record().Purpose != test.purpose || repository.declared == nil {
				t.Fatal("matching authority was not persisted with its declared purpose")
			}
		})
	}
}

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
	declared *authority.Authority
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
	return stub.snapshot.Notice, nil
}

func (stub *authorityServiceStub) Declare(
	_ context.Context,
	_ tenant.Scope,
	mutation authority.DeclarationMutation,
) (authority.Authority, error) {
	stub.declared = &mutation.Authority
	return mutation.Authority, nil
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
	if fixture.request.Purpose == string(evidence.PurposeFraudPrevention) {
		registry, err = evidence.FraudRegistry()
	}
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

type authorityAccessRepository struct{ record access.VerificationRecord }

func (repository authorityAccessRepository) FindForVerification(context.Context, id.Tenant, id.APIKey) (access.VerificationRecord, error) {
	return repository.record, nil
}

func authorityAccess(t *testing.T, fixture fixture) access.Context {
	t.Helper()
	generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := generator.Generate(fixture.request.TenantID, fixture.authority.Record().CreatedBy)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{2}, 32)})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(access.Pattern("authorities:write"))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: presented.ID(), TenantID: presented.TenantHint(), Label: "authority test",
		Digest: digest, PepperVersion: version, Grant: grant, Version: 1,
		CreatedAt: fixture.now.Add(-time.Hour), UpdatedAt: fixture.now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatalf("NewVerificationRecord() error = %v", err)
	}
	authenticator, err := access.NewAuthenticator(authorityAccessRepository{record: record}, peppers, authorityClock{now: fixture.now})
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	accessContext, err := authenticator.Authenticate(t.Context(), presented.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	return accessContext
}
