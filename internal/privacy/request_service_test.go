package privacy_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type requestFakeRepository struct {
	mu           sync.Mutex
	requests     map[string]privacy.Request
	restrictions map[string]privacy.Restriction
	disclosures  map[string]privacy.Disclosure
	processors   map[string]privacy.Processor
	events       int
}

func newRequestFakeRepository() *requestFakeRepository {
	return &requestFakeRepository{
		requests:     map[string]privacy.Request{},
		restrictions: map[string]privacy.Restriction{},
		disclosures:  map[string]privacy.Disclosure{},
		processors:   map[string]privacy.Processor{},
	}
}

func (repository *requestFakeRepository) CreatePrivacyRequest(_ context.Context, _ tenant.Scope, _ privacy.Actor, request privacy.Request) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.requests[request.ID.String()]; exists {
		return privacy.ErrConflict
	}
	repository.requests[request.ID.String()] = request
	repository.events += len(request.Events)
	return nil
}

func (repository *requestFakeRepository) FindPrivacyRequest(_ context.Context, _ tenant.Scope, identifier id.PrivacyRequest) (privacy.Request, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, exists := repository.requests[identifier.String()]
	if !exists {
		return privacy.Request{}, privacy.ErrInvalid
	}
	return request, nil
}

func (repository *requestFakeRepository) SavePrivacyRequest(_ context.Context, _ tenant.Scope, _ privacy.Actor, request privacy.Request, expectedVersion int64) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.requests[request.ID.String()]
	if !exists || current.Version != expectedVersion {
		return privacy.ErrConflict
	}
	repository.events += len(request.Events) - len(current.Events)
	repository.requests[request.ID.String()] = request
	return nil
}

func (repository *requestFakeRepository) ListPrivacyRequests(_ context.Context, _ tenant.Scope, filter privacy.RequestFilter, _ string, limit int) ([]privacy.Request, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []privacy.Request{}
	for _, request := range repository.requests {
		if filter.State != "" && request.State != filter.State {
			continue
		}
		if filter.Type != "" && request.Type != filter.Type {
			continue
		}
		if filter.SubjectID != "" && request.SubjectID != filter.SubjectID {
			continue
		}
		result = append(result, request)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) DuePrivacyRequests(_ context.Context, _ tenant.Scope, at time.Time, limit int) ([]id.PrivacyRequest, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []id.PrivacyRequest{}
	for _, request := range repository.requests {
		if (request.State == privacy.RequestStateRequested || request.State == privacy.RequestStateInReview) && !at.Before(request.ExpiresAt) {
			result = append(result, request.ID)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) SubjectPrivacyRequests(_ context.Context, _ tenant.Scope, verificationID, _ string, limit int) ([]privacy.Request, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []privacy.Request{}
	for _, request := range repository.requests {
		if request.Channel == privacy.ChannelSubjectOutcome && request.VerificationID == verificationID {
			result = append(result, request)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) CreateRestriction(_ context.Context, _ tenant.Scope, _ privacy.Actor, restriction privacy.Restriction) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.restrictions[restriction.ID.String()] = restriction
	return nil
}

func (repository *requestFakeRepository) FindRestriction(_ context.Context, _ tenant.Scope, identifier id.PrivacyRestriction) (privacy.Restriction, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	restriction, exists := repository.restrictions[identifier.String()]
	if !exists {
		return privacy.Restriction{}, privacy.ErrInvalid
	}
	return restriction, nil
}

func (repository *requestFakeRepository) SaveRestriction(_ context.Context, _ tenant.Scope, _ privacy.Actor, restriction privacy.Restriction, expectedVersion int64) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.restrictions[restriction.ID.String()]
	if !exists || current.Version != expectedVersion {
		return privacy.ErrConflict
	}
	repository.restrictions[restriction.ID.String()] = restriction
	return nil
}

func (repository *requestFakeRepository) ListRestrictions(_ context.Context, _ tenant.Scope, subjectID, _ string, limit int) ([]privacy.Restriction, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []privacy.Restriction{}
	for _, restriction := range repository.restrictions {
		if subjectID != "" && restriction.SubjectID != subjectID {
			continue
		}
		result = append(result, restriction)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) ActiveRestrictions(_ context.Context, _ tenant.Scope, subjectID string, at time.Time) ([]privacy.Restriction, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []privacy.Restriction{}
	for _, restriction := range repository.restrictions {
		if restriction.SubjectID == subjectID && restriction.ActiveAt(at) {
			result = append(result, restriction)
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) CreateDisclosure(_ context.Context, _ tenant.Scope, _ privacy.Actor, disclosure privacy.Disclosure) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.disclosures[disclosure.ID.String()] = disclosure
	return nil
}

func (repository *requestFakeRepository) ListDisclosures(_ context.Context, _ tenant.Scope, requestID id.PrivacyRequest, _ string, limit int) ([]privacy.Disclosure, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []privacy.Disclosure{}
	for _, disclosure := range repository.disclosures {
		if !requestID.IsZero() && disclosure.RequestID != requestID {
			continue
		}
		result = append(result, disclosure)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) ListProcessors(_ context.Context, _ tenant.Scope, _ string, limit int) ([]privacy.Processor, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := []privacy.Processor{}
	for _, processor := range repository.processors {
		result = append(result, processor)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (repository *requestFakeRepository) FindProcessor(_ context.Context, _ tenant.Scope, identifier id.Processor) (privacy.Processor, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	processor, exists := repository.processors[identifier.String()]
	if !exists {
		return privacy.Processor{}, privacy.ErrInvalid
	}
	return processor, nil
}

func (repository *requestFakeRepository) PutProcessor(_ context.Context, _ tenant.Scope, _ privacy.Actor, processor privacy.Processor, expectedVersion int64) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.processors[processor.ID.String()]
	if expectedVersion == 0 {
		if exists {
			return privacy.ErrConflict
		}
	} else if !exists || current.Version != expectedVersion {
		return privacy.ErrConflict
	}
	repository.processors[processor.ID.String()] = processor
	return nil
}

type requestFakeExecutor struct {
	calls int
	err   error
}

func (executor *requestFakeExecutor) ExecuteEffect(_ context.Context, _ tenant.Scope, _ privacy.Actor, request privacy.Request) (privacy.EffectResult, error) {
	executor.calls++
	if executor.err != nil {
		return privacy.EffectResult{}, executor.err
	}
	return privacy.EffectResult{Kind: "subject_export", Reference: "bundle:" + request.ID.String(), Digest: "sha256:fixture"}, nil
}

type requestTestClock struct{ now time.Time }

func (clock *requestTestClock) Now() time.Time { return clock.now }

func requestServiceFixture(t *testing.T) (*privacy.RequestService, *requestFakeExecutor, *requestFakeRepository, *id.Generator, tenant.Scope, *requestTestClock) {
	t.Helper()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	repository := newRequestFakeRepository()
	executor := &requestFakeExecutor{}
	clock := &requestTestClock{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
	service, err := privacy.NewRequestService(repository, repository, repository, repository, executor, generator, clock.Now, privacy.SelectedRequestConfig())
	if err != nil {
		t.Fatal(err)
	}
	scopeTenant, _ := generator.NewTenant()
	scope, _ := tenant.NewScope(scopeTenant)
	return service, executor, repository, generator, scope, clock
}

func TestRequestServiceLifecycleAndIdempotency(t *testing.T) {
	service, executor, repository, _, scope, clock := requestServiceFixture(t)
	now := clock.now
	writer := privacy.Actor{ID: "key_write", Permissions: []privacy.Permission{privacy.PermissionWritePrivacyRequests, privacy.PermissionReadPrivacyRequests}}
	approver := privacy.Actor{ID: "key_approve", Permissions: []privacy.Permission{privacy.PermissionApprovePrivacyRequests, privacy.PermissionReadPrivacyRequests}}
	subjectID := "sub_01M11HEQG00000000000000000"

	request, err := service.Create(context.Background(), scope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID, Region: "ng-1", Payload: []byte(`{}`)})
	if err != nil || request.State != privacy.RequestStateRequested {
		t.Fatalf("Create() = %+v, %v", request, err)
	}
	if !request.ExpiresAt.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Fatalf("default expiry = %v", request.ExpiresAt)
	}
	if _, err := service.Create(context.Background(), scope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID, Region: "ng-1", Payload: []byte(`{"purpose":"x"}`)}); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("payload error = %v", err)
	}
	if _, err := service.Execute(context.Background(), scope, writer, request.ID, request.Version); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("unapproved execute error = %v", err)
	}

	approved, err := service.Decide(context.Background(), scope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, request.Version+1)
	if err != nil || approved.State != privacy.RequestStateApproved {
		t.Fatalf("Decide() = %+v, %v", approved, err)
	}
	replay, err := service.Decide(context.Background(), scope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, approved.Version)
	if err != nil || replay.Version != approved.Version {
		t.Fatalf("Decide replay = %+v, %v", replay, err)
	}
	completed, err := service.Execute(context.Background(), scope, approver, request.ID, approved.Version)
	if err != nil || completed.State != privacy.RequestStateCompleted || completed.EffectDigest != "sha256:fixture" {
		t.Fatalf("Execute() = %+v, %v", completed, err)
	}
	executor.calls = 0
	again, err := service.Execute(context.Background(), scope, approver, request.ID, completed.Version)
	if err != nil || again.Version != completed.Version || executor.calls != 0 {
		t.Fatalf("Execute replay = %+v, calls=%d, %v", again, executor.calls, err)
	}
	if len(repository.requests) != 1 {
		t.Fatalf("requests = %d", len(repository.requests))
	}
}

func TestRequestServiceExpiryRestrictionAndGate(t *testing.T) {
	service, _, _, generator, scope, clock := requestServiceFixture(t)
	now := clock.now
	writer := privacy.Actor{ID: "key_write", Permissions: []privacy.Permission{privacy.PermissionWritePrivacyRequests, privacy.PermissionReadPrivacyRequests}}
	approver := privacy.Actor{ID: "key_approve", Permissions: []privacy.Permission{privacy.PermissionApprovePrivacyRequests}}
	subjectID := "sub_01M11HEQG00000000000000000"

	expiring, err := service.Create(context.Background(), scope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID, Region: "ng-1", Payload: []byte(`{}`), ExpiresAt: timePointer(now.Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Hour)
	expired, err := service.ExpireDue(context.Background(), scope, writer, 10)
	if err != nil || len(expired) != 1 || expired[0].ID != expiring.ID || expired[0].State != privacy.RequestStateExpired {
		t.Fatalf("ExpireDue() = %+v, %v", expired, err)
	}

	restrictionRequest, err := service.Create(context.Background(), scope, writer, privacy.CreateRequestInput{Type: privacy.RequestRestriction, SubjectID: subjectID, Region: "ng-1", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := service.Decide(context.Background(), scope, approver, restrictionRequest.ID, privacy.OutcomeApproved, privacy.ReasonRestrictionApproved, restrictionRequest.Version+1)
	if err != nil {
		t.Fatal(err)
	}
	executed, err := service.Execute(context.Background(), scope, approver, decided.ID, decided.Version)
	if err != nil || executed.EffectKind != "restriction" {
		t.Fatalf("restriction execute = %+v, %v", executed, err)
	}
	blocked, err := service.Blocked(context.Background(), scope, subjectID)
	if err != nil || !blocked {
		t.Fatalf("Blocked() = %v, %v", blocked, err)
	}
	restrictions, err := service.Restrictions(context.Background(), scope, writer, subjectID, "", 10)
	if err != nil || len(restrictions) != 1 {
		t.Fatalf("Restrictions() = %+v, %v", restrictions, err)
	}
	lifted, err := service.LiftRestriction(context.Background(), scope, approver, restrictions[0].ID, privacy.RestrictionLiftedByTenant, restrictions[0].Version)
	if err != nil || lifted.State != privacy.RestrictionLifted {
		t.Fatalf("LiftRestriction() = %+v, %v", lifted, err)
	}
	replay, err := service.LiftRestriction(context.Background(), scope, approver, restrictions[0].ID, privacy.RestrictionLiftedByTenant, restrictions[0].Version)
	if err != nil || replay.Version != lifted.Version {
		t.Fatalf("lift replay = %+v, %v", replay, err)
	}
	blocked, err = service.Blocked(context.Background(), scope, subjectID)
	if err != nil || blocked {
		t.Fatalf("Blocked after lift = %v, %v", blocked, err)
	}
	_ = generator
}

func TestRequestServiceSubjectChannelCannotDecideOrExecute(t *testing.T) {
	service, _, _, generator, scope, _ := requestServiceFixture(t)
	verificationID, _ := generator.NewVerification()
	authority := privacy.SubjectAuthority{Scope: scope, VerificationID: verificationID}

	projection, err := service.CreateSubject(context.Background(), authority, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: "sub_01M11HEQG00000000000000000", Region: "ng-1", Payload: []byte(`{}`)})
	if err != nil || projection.Status != privacy.SubjectStatusReceived {
		t.Fatalf("CreateSubject() = %+v, %v", projection, err)
	}
	page, err := service.SubjectList(context.Background(), authority, "", 10)
	if err != nil || len(page.Requests) != 1 || page.Requests[0].Type != privacy.RequestAccess {
		t.Fatalf("SubjectList() = %+v, %v", page, err)
	}
	foreignVerification, _ := generator.NewVerification()
	foreign, err := service.SubjectList(context.Background(), privacy.SubjectAuthority{Scope: scope, VerificationID: foreignVerification}, "", 10)
	if err != nil || len(foreign.Requests) != 0 {
		t.Fatalf("foreign subject list = %+v, %v", foreign, err)
	}
}

func TestRequestServiceDisclosureAndProcessorInventory(t *testing.T) {
	service, _, _, generator, scope, _ := requestServiceFixture(t)
	writer := privacy.Actor{ID: "key_write", Permissions: []privacy.Permission{privacy.PermissionWritePrivacyRequests, privacy.PermissionReadPrivacyRequests}}

	request, err := service.Create(context.Background(), scope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: "sub_01M11HEQG00000000000000000", Region: "ng-1", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := service.CreateDisclosure(context.Background(), scope, writer, request.ID, "legal.counsel", "access.request", privacy.DisclosureSubjectExport, "controller.contract", "ng-1", "sha256:digest")
	if err != nil || disclosure.Version != 1 {
		t.Fatalf("CreateDisclosure() = %+v, %v", disclosure, err)
	}
	list, err := service.ListDisclosures(context.Background(), scope, writer, request.ID, "", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDisclosures() = %+v, %v", list, err)
	}

	processorID, _ := generator.NewProcessor()
	processor, err := service.PutProcessor(context.Background(), scope, writer, processorID, 0, "Example KYC", privacy.ProcessorRoleProcessor, "identity.verification", []privacy.DataClass{privacy.DataClassRawEvidence}, []string{"ng-1"}, "standard.contractual_clauses")
	if err != nil || processor.Version != 1 {
		t.Fatalf("PutProcessor create = %+v, %v", processor, err)
	}
	updated, err := service.PutProcessor(context.Background(), scope, writer, processorID, 1, "Example KYC Ltd", privacy.ProcessorRoleProcessor, "identity.verification", []privacy.DataClass{privacy.DataClassDerivedEvidence}, []string{"ng-1"}, "standard.contractual_clauses")
	if err != nil || updated.Version != 2 {
		t.Fatalf("PutProcessor update = %+v, %v", updated, err)
	}
	if _, err := service.PutProcessor(context.Background(), scope, writer, processorID, 1, "stale", privacy.ProcessorRoleProcessor, "identity.verification", []privacy.DataClass{privacy.DataClassRawEvidence}, []string{"ng-1"}, "standard.contractual_clauses"); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("stale processor error = %v", err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
