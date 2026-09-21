package privacy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Privacy-request application permissions. The tenant API key is the
// controller and is the only principal that may decide or execute requests.
const (
	// PermissionReadPrivacyRequests permits bounded privacy-request inspection.
	PermissionReadPrivacyRequests Permission = "privacy_requests:read"
	// PermissionWritePrivacyRequests permits request creation, withdrawal, and administration.
	PermissionWritePrivacyRequests Permission = "privacy_requests:write"
	// PermissionApprovePrivacyRequests permits approve, deny, execute, and lift.
	PermissionApprovePrivacyRequests Permission = "privacy_requests:approve"
)

// RequestRepository persists privacy requests, their immutable decisions, and
// their append-only events in one tenant-scoped transaction. Save uses
// optimistic expectedVersion.
type RequestRepository interface {
	CreatePrivacyRequest(context.Context, tenant.Scope, Actor, Request) error
	FindPrivacyRequest(context.Context, tenant.Scope, id.PrivacyRequest) (Request, error)
	SavePrivacyRequest(context.Context, tenant.Scope, Actor, Request, int64) error
	ListPrivacyRequests(context.Context, tenant.Scope, RequestFilter, string, int) ([]Request, error)
	DuePrivacyRequests(context.Context, tenant.Scope, time.Time, int) ([]id.PrivacyRequest, error)
	SubjectPrivacyRequests(context.Context, tenant.Scope, string, string, int) ([]Request, error)
}

// RestrictionRepository persists processing restrictions and their audits.
type RestrictionRepository interface {
	CreateRestriction(context.Context, tenant.Scope, Actor, Restriction) error
	FindRestriction(context.Context, tenant.Scope, id.PrivacyRestriction) (Restriction, error)
	SaveRestriction(context.Context, tenant.Scope, Actor, Restriction, int64) error
	ListRestrictions(context.Context, tenant.Scope, string, string, int) ([]Restriction, error)
	ActiveRestrictions(context.Context, tenant.Scope, string, time.Time) ([]Restriction, error)
}

// DisclosureRepository persists immutable transfer and disclosure records.
type DisclosureRepository interface {
	CreateDisclosure(context.Context, tenant.Scope, Actor, Disclosure) error
	ListDisclosures(context.Context, tenant.Scope, id.PrivacyRequest, string, int) ([]Disclosure, error)
}

// ProcessorRepository persists versioned processor-inventory revisions.
type ProcessorRepository interface {
	ListProcessors(context.Context, tenant.Scope, string, int) ([]Processor, error)
	FindProcessor(context.Context, tenant.Scope, id.Processor) (Processor, error)
	PutProcessor(context.Context, tenant.Scope, Actor, Processor, int64) error
}

// RequestIdentifiers supplies owned identifiers without exposing ULID.
type RequestIdentifiers interface {
	NewPrivacyRequest() (id.PrivacyRequest, error)
	NewPrivacyDecision() (id.PrivacyDecision, error)
	NewPrivacyRestriction() (id.PrivacyRestriction, error)
	NewPrivacyDisclosure() (id.PrivacyDisclosure, error)
	NewProcessor() (id.Processor, error)
}

// SubjectBundle is the byte-canonical result of one subject-scoped export.
type SubjectBundle struct {
	Digest string
	Bytes  int64
}

// SubjectBundleExporter produces a subject-scoped, digest-bearing export
// bundle through the existing tenant-export machinery.
type SubjectBundleExporter interface {
	ExportSubjectBundle(context.Context, tenant.Scope, string, bool) (SubjectBundle, error)
}

// SubjectDeletionExecutor creates the existing subject deletion workflow with
// identity and evidence targets; it does not delete anything directly.
type SubjectDeletionExecutor interface {
	RequestSubjectDeletion(context.Context, tenant.Scope, Actor, string, string) (string, error)
}

// CorrectionInstruction is the bounded correction effect instruction.
type CorrectionInstruction struct {
	SubjectID     string
	RecordID      string
	DecisionID    string
	Name          string
	Value         string
	Kind          string
	Normalization string
}

// CorrectionExecutor routes an approved correction to the existing identity
// successor or review-correction-intake mechanism.
type CorrectionExecutor interface {
	ExecuteCorrection(context.Context, tenant.Scope, Actor, Request, CorrectionInstruction) (string, error)
}

// EffectResult is the bounded outcome of one executed request.
type EffectResult struct {
	Kind      string
	Reference string
	Digest    string
}

// EffectExecutor executes one approved external effect. Implementations must
// be idempotent per request identifier.
type EffectExecutor interface {
	ExecuteEffect(context.Context, tenant.Scope, Actor, Request) (EffectResult, error)
}

// RequestConfig bounds expiry handling. The selected default is 30 days.
type RequestConfig struct {
	DefaultExpiry time.Duration
	MaximumExpiry time.Duration
}

// SelectedRequestConfig returns the selected 30-day default with a bounded cap.
func SelectedRequestConfig() RequestConfig {
	return RequestConfig{DefaultExpiry: 30 * 24 * time.Hour, MaximumExpiry: 365 * 24 * time.Hour}
}

// Validate checks that the selected expiry bounds are coherent.
func (config RequestConfig) Validate() error {
	if config.DefaultExpiry <= 0 || config.MaximumExpiry < config.DefaultExpiry || config.MaximumExpiry > 365*24*time.Hour {
		return ErrInvalid
	}
	return nil
}

// CreateRequestInput is one bounded privacy-request creation command.
type CreateRequestInput struct {
	Type           RequestType
	SubjectID      string
	VerificationID string
	Region         string
	Payload        []byte
	ExpiresAt      *time.Time
}

// SubjectAuthority is the closed outcome-credential authority used by the
// subject-safe surface. It can never decide, execute, or list other subjects.
type SubjectAuthority struct {
	Scope          tenant.Scope
	VerificationID id.Verification
}

// RequestService owns the data-subject privacy-request workflow.
type RequestService struct {
	requests     RequestRepository
	restrictions RestrictionRepository
	disclosures  DisclosureRepository
	processors   ProcessorRepository
	effects      EffectExecutor
	identifiers  RequestIdentifiers
	now          func() time.Time
	config       RequestConfig
	metrics      RequestMetrics
}

// NewRequestService constructs the privacy-request application boundary.
func NewRequestService(requests RequestRepository, restrictions RestrictionRepository, disclosures DisclosureRepository, processors ProcessorRepository, effects EffectExecutor, identifiers RequestIdentifiers, now func() time.Time, config RequestConfig) (*RequestService, error) {
	if requests == nil || restrictions == nil || disclosures == nil || processors == nil || identifiers == nil || now == nil || config.Validate() != nil {
		return nil, ErrInvalid
	}
	return &RequestService{
		requests: requests, restrictions: restrictions, disclosures: disclosures, processors: processors,
		effects: effects, identifiers: identifiers, now: now, config: config,
	}, nil
}

// WithMetrics attaches the bounded privacy-request metric receiver.
func (service *RequestService) WithMetrics(metrics RequestMetrics) *RequestService {
	if service != nil && metrics != nil {
		service.metrics = metrics
	}
	return service
}

// Create creates one tenant-channel request. The tenant is the controller.
func (service *RequestService) Create(ctx context.Context, scope tenant.Scope, actor Actor, input CreateRequestInput) (Request, error) {
	if !actor.permits(PermissionWritePrivacyRequests) {
		return Request{}, ErrConflict
	}
	return service.create(ctx, scope, actor, ChannelTenantAPI, input)
}

// CreateSubject creates one subject-channel request from a closed outcome
// credential. The tenant remains the controller and decides the outcome.
func (service *RequestService) CreateSubject(ctx context.Context, authority SubjectAuthority, input CreateRequestInput) (SubjectRequest, error) {
	if authority.Scope.ID().IsZero() || authority.VerificationID.IsZero() {
		return SubjectRequest{}, ErrConflict
	}
	input.VerificationID = authority.VerificationID.String()
	actor := Actor{ID: "subject:" + authority.VerificationID.String(), Permissions: []Permission{PermissionWritePrivacyRequests}}
	request, err := service.create(ctx, authority.Scope, actor, ChannelSubjectOutcome, input)
	if err != nil {
		return SubjectRequest{}, err
	}
	return request.SubjectProjection(), nil
}

func (service *RequestService) create(ctx context.Context, scope tenant.Scope, actor Actor, channel Channel, input CreateRequestInput) (Request, error) {
	if scope.ID().IsZero() || !input.Type.Valid() || !validRegion(input.Region) {
		return Request{}, ErrInvalid
	}
	if channel == ChannelSubjectOutcome && input.Type == RequestCorrection && input.VerificationID == "" {
		return Request{}, ErrInvalid
	}
	expiresAt, err := service.expiry(input.ExpiresAt)
	if err != nil {
		return Request{}, err
	}
	payload, err := canonicalPayload(input.Type, input.Payload)
	if err != nil {
		return Request{}, err
	}
	identifier, err := service.identifiers.NewPrivacyRequest()
	if err != nil {
		return Request{}, fmt.Errorf("generate privacy request id: %w", err)
	}
	request, err := NewPrivacyRequest(identifier, input.Type, channel, input.SubjectID, input.VerificationID, input.Region, payload, service.now().UTC(), expiresAt)
	if err != nil {
		return Request{}, err
	}
	if (request.Type == RequestRestriction || request.Type == RequestErasure) && request.SubjectID == "" {
		return Request{}, ErrInvalid
	}
	if err := service.requests.CreatePrivacyRequest(ctx, scope, actor, request); err != nil {
		return Request{}, fmt.Errorf("persist privacy request: %w", err)
	}
	return request, nil
}

func (service *RequestService) expiry(requested *time.Time) (time.Time, error) {
	now := service.now().UTC()
	selected := now.Add(service.config.DefaultExpiry)
	if requested != nil {
		candidate := requested.UTC()
		if !candidate.After(now) || candidate.After(now.Add(service.config.MaximumExpiry)) {
			return time.Time{}, ErrInvalid
		}
		selected = candidate
	}
	return selected, nil
}

// BeginReview records the requested -> in_review transition.
func (service *RequestService) BeginReview(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.PrivacyRequest) (Request, error) {
	if !actor.permits(PermissionWritePrivacyRequests) || identifier.IsZero() {
		return Request{}, ErrConflict
	}
	request, err := service.requests.FindPrivacyRequest(ctx, scope, identifier)
	if err != nil {
		return Request{}, err
	}
	next, err := request.BeginReview(targetReferenceDigest(actor.ID), service.now().UTC())
	if err != nil {
		return Request{}, err
	}
	if next.Version == request.Version {
		return next, nil
	}
	if err := service.requests.SavePrivacyRequest(ctx, scope, actor, next, request.Version); err != nil {
		return Request{}, err
	}
	service.observeTransition(request, next)
	return next, nil
}

// Decide records one approve, partially-approve, or deny outcome. Replay of
// the same decision is idempotent regardless of the expected version.
func (service *RequestService) Decide(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.PrivacyRequest, outcome DecisionOutcome, reason ReasonCode, expectedVersion int64) (Request, error) {
	if !actor.permits(PermissionApprovePrivacyRequests) || identifier.IsZero() || !outcome.Valid() || expectedVersion < 1 {
		return Request{}, ErrConflict
	}
	request, err := service.requests.FindPrivacyRequest(ctx, scope, identifier)
	if err != nil {
		return Request{}, err
	}
	if request.State.Decided() && len(request.Decisions) > 0 {
		existing := request.Decisions[len(request.Decisions)-1]
		if existing.Outcome == outcome && existing.ReasonCode == reason {
			return request, nil
		}
		return Request{}, ErrConflict
	}
	if request.State != RequestStateRequested && request.State != RequestStateInReview {
		return Request{}, ErrConflict
	}
	if request.State == RequestStateInReview && request.Version != expectedVersion {
		return Request{}, ErrConflict
	}
	if request.State == RequestStateRequested {
		if expectedVersion != request.Version && expectedVersion != request.Version+1 {
			return Request{}, ErrConflict
		}
		reviewed, err := request.BeginReview(targetReferenceDigest(actor.ID), service.now().UTC())
		if err != nil {
			return Request{}, err
		}
		if err := service.requests.SavePrivacyRequest(ctx, scope, actor, reviewed, request.Version); err != nil {
			return Request{}, err
		}
		service.observeTransition(request, reviewed)
		request = reviewed
	}
	decisionID, err := service.identifiers.NewPrivacyDecision()
	if err != nil {
		return Request{}, fmt.Errorf("generate privacy decision id: %w", err)
	}
	next, err := request.Decide(decisionID, outcome, reason, actor.ID, service.now().UTC())
	if err != nil {
		return Request{}, err
	}
	if next.Version != request.Version {
		if err := service.requests.SavePrivacyRequest(ctx, scope, actor, next, request.Version); err != nil {
			return Request{}, err
		}
	}
	service.observeTransition(request, next)
	return next, nil
}

// Withdraw ends an undecided request at tenant request.
func (service *RequestService) Withdraw(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.PrivacyRequest, expectedVersion int64) (Request, error) {
	if !actor.permits(PermissionWritePrivacyRequests) || identifier.IsZero() || expectedVersion < 1 {
		return Request{}, ErrConflict
	}
	request, err := service.requests.FindPrivacyRequest(ctx, scope, identifier)
	if err != nil {
		return Request{}, err
	}
	if request.State == RequestStateWithdrawn {
		return request, nil
	}
	if request.Version != expectedVersion {
		return Request{}, ErrConflict
	}
	next, err := request.Withdraw(targetReferenceDigest(actor.ID), service.now().UTC())
	if err != nil {
		return Request{}, err
	}
	if next.Version != request.Version {
		if err := service.requests.SavePrivacyRequest(ctx, scope, actor, next, request.Version); err != nil {
			return Request{}, err
		}
	}
	service.observeTransition(request, next)
	return next, nil
}

// Execute runs the approved effect through the existing owning mechanism.
// Completed requests are returned unchanged, so replay adds nothing.
func (service *RequestService) Execute(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.PrivacyRequest, expectedVersion int64) (Request, error) {
	if !actor.permits(PermissionApprovePrivacyRequests) || identifier.IsZero() || expectedVersion < 1 {
		return Request{}, ErrConflict
	}
	request, err := service.requests.FindPrivacyRequest(ctx, scope, identifier)
	if err != nil {
		return Request{}, err
	}
	if request.State == RequestStateCompleted {
		return request, nil
	}
	if request.Version != expectedVersion {
		return Request{}, ErrConflict
	}
	next, err := request.BeginExecution(targetReferenceDigest(actor.ID), service.now().UTC())
	if err != nil {
		return Request{}, err
	}
	if next.Version != request.Version {
		if err := service.requests.SavePrivacyRequest(ctx, scope, actor, next, request.Version); err != nil {
			return Request{}, err
		}
		service.observeTransition(request, next)
	}
	result, effectErr := service.executeEffect(ctx, scope, actor, next)
	if effectErr != nil {
		failed, failErr := next.Fail(classifyRequestFailure(effectErr), service.now().UTC())
		if failErr != nil {
			return Request{}, errors.Join(effectErr, failErr)
		}
		if saveErr := service.requests.SavePrivacyRequest(ctx, scope, actor, failed, next.Version); saveErr != nil {
			return Request{}, errors.Join(effectErr, saveErr)
		}
		service.observeTransition(next, failed)
		return failed, effectErr
	}
	completed, err := next.Complete(result.Kind, result.Reference, result.Digest, service.now().UTC())
	if err != nil {
		return Request{}, err
	}
	if err := service.requests.SavePrivacyRequest(ctx, scope, actor, completed, next.Version); err != nil {
		return Request{}, err
	}
	service.observeTransition(next, completed)
	return completed, nil
}

func (service *RequestService) executeEffect(ctx context.Context, scope tenant.Scope, actor Actor, request Request) (EffectResult, error) {
	switch request.Type {
	case RequestRestriction:
		payload, err := ValidatePayload(request.Type, request.Payload)
		if err != nil {
			return EffectResult{}, err
		}
		identifier, err := service.identifiers.NewPrivacyRestriction()
		if err != nil {
			return EffectResult{}, err
		}
		reason := RestrictionAccuracyDispute
		if request.Channel == ChannelSubjectOutcome {
			reason = RestrictionRequestedBySubject
		}
		restriction, err := NewRestriction(identifier, request.ID, request.SubjectID, payload.Purpose, reason, request.Region, service.now().UTC())
		if err != nil {
			return EffectResult{}, err
		}
		if err := service.restrictions.CreateRestriction(ctx, scope, actor, restriction); err != nil {
			return EffectResult{}, err
		}
		return EffectResult{Kind: "restriction", Reference: restriction.ID.String(), Digest: targetReferenceDigest(restriction.ID.String())}, nil
	case RequestObjection:
		payload, err := ValidatePayload(request.Type, request.Payload)
		if err != nil {
			return EffectResult{}, err
		}
		identifier, err := service.identifiers.NewPrivacyRestriction()
		if err != nil {
			return EffectResult{}, err
		}
		reason := RestrictionAccuracyDispute
		if request.Channel == ChannelSubjectOutcome {
			reason = RestrictionRequestedBySubject
		}
		objection, err := NewObjection(identifier, request.ID, request.SubjectID, payload.Purpose, reason, request.Region, service.now().UTC())
		if err != nil {
			return EffectResult{}, err
		}
		if err := service.restrictions.CreateRestriction(ctx, scope, actor, objection); err != nil {
			return EffectResult{}, err
		}
		return EffectResult{Kind: "objection", Reference: objection.ID.String(), Digest: targetReferenceDigest(objection.ID.String())}, nil
	default:
		if service.effects == nil {
			return EffectResult{}, ErrUnavailable
		}
		return service.effects.ExecuteEffect(ctx, scope, actor, request)
	}
}

// ExpireDue expires a bounded batch of undecided requests at their boundary.
func (service *RequestService) ExpireDue(ctx context.Context, scope tenant.Scope, actor Actor, limit int) ([]Request, error) {
	if !actor.permits(PermissionWritePrivacyRequests) || limit < 1 || limit > 1000 {
		return nil, ErrConflict
	}
	identifiers, err := service.requests.DuePrivacyRequests(ctx, scope, service.now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	results := make([]Request, 0, len(identifiers))
	for _, identifier := range identifiers {
		request, err := service.requests.FindPrivacyRequest(ctx, scope, identifier)
		if err != nil {
			continue
		}
		next, err := request.Expire(service.now().UTC())
		if err != nil {
			continue
		}
		if next.Version != request.Version {
			if err := service.requests.SavePrivacyRequest(ctx, scope, actor, next, request.Version); err != nil {
				continue
			}
			service.observeTransition(request, next)
		}
		results = append(results, next)
	}
	return results, nil
}

// Find returns one tenant-scoped request.
func (service *RequestService) Find(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.PrivacyRequest) (Request, error) {
	if !actor.permits(PermissionReadPrivacyRequests) || identifier.IsZero() {
		return Request{}, ErrConflict
	}
	return service.requests.FindPrivacyRequest(ctx, scope, identifier)
}

// List returns one bounded tenant-scoped page.
func (service *RequestService) List(ctx context.Context, scope tenant.Scope, actor Actor, filter RequestFilter, position string, limit int) (RequestPage, error) {
	if !actor.permits(PermissionReadPrivacyRequests) || limit < 1 || limit > 100 ||
		(filter.SubjectID != "" && !token(filter.SubjectID, 200)) || (position != "" && !token(position, 64)) {
		return RequestPage{}, ErrConflict
	}
	if filter.State != "" && !filter.State.Valid() {
		return RequestPage{}, ErrInvalid
	}
	if filter.Type != "" && !filter.Type.Valid() {
		return RequestPage{}, ErrInvalid
	}
	requests, err := service.requests.ListPrivacyRequests(ctx, scope, filter, position, limit+1)
	if err != nil {
		return RequestPage{}, err
	}
	page := RequestPage{HasMore: len(requests) > limit}
	if page.HasMore {
		requests = requests[:limit]
	}
	if requests == nil {
		requests = []Request{}
	}
	page.Requests = requests
	return page, nil
}

// SubjectList returns the closed subject-safe projection of requests created
// through one outcome credential's verification.
func (service *RequestService) SubjectList(ctx context.Context, authority SubjectAuthority, position string, limit int) (SubjectRequestPage, error) {
	if authority.Scope.ID().IsZero() || authority.VerificationID.IsZero() || limit < 1 || limit > 100 ||
		(position != "" && !token(position, 64)) {
		return SubjectRequestPage{}, ErrConflict
	}
	requests, err := service.requests.SubjectPrivacyRequests(ctx, authority.Scope, authority.VerificationID.String(), position, limit+1)
	if err != nil {
		return SubjectRequestPage{}, err
	}
	page := SubjectRequestPage{HasMore: len(requests) > limit}
	if page.HasMore {
		requests = requests[:limit]
	}
	projections := make([]SubjectRequest, 0, len(requests))
	for _, request := range requests {
		projections = append(projections, request.SubjectProjection())
	}
	if len(requests) > 0 {
		page.NextPosition = requests[len(requests)-1].ID.String()
	}
	page.Requests = projections
	return page, nil
}

// Restrictions lists bounded restrictions, optionally for one exact subject.
func (service *RequestService) Restrictions(ctx context.Context, scope tenant.Scope, actor Actor, subjectID, position string, limit int) ([]Restriction, error) {
	if !actor.permits(PermissionReadPrivacyRequests) || limit < 1 || limit > 100 ||
		(subjectID != "" && !token(subjectID, 200)) || (position != "" && !token(position, 64)) {
		return nil, ErrConflict
	}
	return service.restrictions.ListRestrictions(ctx, scope, subjectID, position, limit)
}

// LiftRestriction ends an active restriction and audits the reason.
func (service *RequestService) LiftRestriction(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.PrivacyRestriction, reason RestrictionReason, expectedVersion int64) (Restriction, error) {
	if !actor.permits(PermissionApprovePrivacyRequests) || identifier.IsZero() || expectedVersion < 1 {
		return Restriction{}, ErrConflict
	}
	restriction, err := service.restrictions.FindRestriction(ctx, scope, identifier)
	if err != nil {
		return Restriction{}, err
	}
	if restriction.State == RestrictionLifted {
		if restriction.LiftReasonCode == reason {
			return restriction, nil
		}
		return Restriction{}, ErrConflict
	}
	if restriction.Version != expectedVersion {
		return Restriction{}, ErrConflict
	}
	next, err := restriction.Lift(reason, service.now().UTC())
	if err != nil {
		return Restriction{}, err
	}
	if next.Version != restriction.Version {
		if err := service.restrictions.SaveRestriction(ctx, scope, actor, next, restriction.Version); err != nil {
			return Restriction{}, err
		}
	}
	return next, nil
}

// Blocked reports whether a subject-scoped restriction blocks new processing.
func (service *RequestService) Blocked(ctx context.Context, scope tenant.Scope, subjectID string) (bool, error) {
	if scope.ID().IsZero() || !token(subjectID, 200) {
		return false, ErrInvalid
	}
	active, err := service.restrictions.ActiveRestrictions(ctx, scope, subjectID, service.now().UTC())
	if err != nil {
		return false, err
	}
	for _, restriction := range active {
		if restriction.Scope == RestrictionScopeSubject && restriction.ActiveAt(service.now().UTC()) {
			return true, nil
		}
	}
	return false, nil
}

// CreateDisclosure records one immutable disclosure.
func (service *RequestService) CreateDisclosure(ctx context.Context, scope tenant.Scope, actor Actor, requestID id.PrivacyRequest, recipient, purpose string, class DisclosureClass, legalBasis, region, reference string) (Disclosure, error) {
	if !actor.permits(PermissionWritePrivacyRequests) || requestID.IsZero() {
		return Disclosure{}, ErrConflict
	}
	identifier, err := service.identifiers.NewPrivacyDisclosure()
	if err != nil {
		return Disclosure{}, fmt.Errorf("generate disclosure id: %w", err)
	}
	disclosure, err := NewDisclosure(identifier, requestID, recipient, purpose, class, legalBasis, region, reference, service.now().UTC())
	if err != nil {
		return Disclosure{}, err
	}
	if err := service.disclosures.CreateDisclosure(ctx, scope, actor, disclosure); err != nil {
		return Disclosure{}, err
	}
	return disclosure, nil
}

// ListDisclosures returns a bounded page for one request or the whole tenant.
func (service *RequestService) ListDisclosures(ctx context.Context, scope tenant.Scope, actor Actor, requestID id.PrivacyRequest, position string, limit int) ([]Disclosure, error) {
	if !actor.permits(PermissionReadPrivacyRequests) || limit < 1 || limit > 100 || (position != "" && !token(position, 64)) {
		return nil, ErrConflict
	}
	return service.disclosures.ListDisclosures(ctx, scope, requestID, position, limit)
}

// PutProcessor creates or updates one versioned processor-inventory entry.
// Version zero creates; a positive version updates with expected-version.
func (service *RequestService) PutProcessor(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.Processor, expectedVersion int64, name string, role ProcessorRole, purpose string, dataClasses []DataClass, regions []string, transferMechanism string) (Processor, error) {
	if !actor.permits(PermissionWritePrivacyRequests) || expectedVersion < 0 {
		return Processor{}, ErrConflict
	}
	now := service.now().UTC()
	if expectedVersion == 0 {
		if identifier.IsZero() {
			generated, err := service.identifiers.NewProcessor()
			if err != nil {
				return Processor{}, fmt.Errorf("generate processor id: %w", err)
			}
			identifier = generated
		}
		processor, err := NewProcessor(identifier, name, role, purpose, dataClasses, regions, transferMechanism, now)
		if err != nil {
			return Processor{}, err
		}
		if err := service.processors.PutProcessor(ctx, scope, actor, processor, 0); err != nil {
			return Processor{}, err
		}
		return processor, nil
	}
	if identifier.IsZero() {
		return Processor{}, ErrConflict
	}
	current, err := service.processors.FindProcessor(ctx, scope, identifier)
	if err != nil {
		return Processor{}, err
	}
	next, err := current.Update(expectedVersion, name, role, purpose, dataClasses, regions, transferMechanism, now)
	if err != nil {
		return Processor{}, err
	}
	if err := service.processors.PutProcessor(ctx, scope, actor, next, expectedVersion); err != nil {
		return Processor{}, err
	}
	return next, nil
}

// ListProcessors returns a bounded inventory page.
func (service *RequestService) ListProcessors(ctx context.Context, scope tenant.Scope, actor Actor, position string, limit int) ([]Processor, error) {
	if !actor.permits(PermissionReadPrivacyRequests) || limit < 1 || limit > 100 || (position != "" && !token(position, 64)) {
		return nil, ErrConflict
	}
	return service.processors.ListProcessors(ctx, scope, position, limit)
}

// FindProcessor returns one inventory entry.
func (service *RequestService) FindProcessor(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.Processor) (Processor, error) {
	if !actor.permits(PermissionReadPrivacyRequests) || identifier.IsZero() {
		return Processor{}, ErrConflict
	}
	return service.processors.FindProcessor(ctx, scope, identifier)
}

func (service *RequestService) observeTransition(from, to Request) {
	if service.metrics == nil || from.State == to.State || to.ID.IsZero() {
		return
	}
	service.metrics.RecordPrivacyRequestTransition(observability.PrivacyRequestTransition{
		Type: requestType(to.Type), From: requestState(from.State), To: requestState(to.State),
		Region: observability.Region(to.Region),
	})
	if to.State.Terminal() || to.State == RequestStateInReview || to.State == RequestStateExecuting {
		age := to.UpdatedAt.Sub(to.RequestedAt)
		if age < 0 {
			age = 0
		}
		service.metrics.RecordPrivacyRequestAge(observability.PrivacyRequestAge{
			Type: requestType(to.Type), State: requestState(to.State),
			Overdue: service.now().UTC().After(to.ExpiresAt), Age: age,
		})
	}
}

func classifyRequestFailure(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	if errors.Is(err, ErrUnavailable) {
		return "effect_unavailable"
	}
	return "effect_failed"
}
