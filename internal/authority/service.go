package authority

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

const (
	operationCreateNotice = "notices.create"
	operationDeclare      = "authorities.declare"
	operationRespond      = "authorities.respond"
)

// NoticeMutation is one atomic immutable-notice creation command.
type NoticeMutation struct {
	Notice      Notice
	EventID     id.Event
	Idempotency idempotency.Request
}

// DeclarationMutation is one atomic subject, authority, session-binding,
// audit, outbox, and idempotency command.
type DeclarationMutation struct {
	Authority   Authority
	EventID     id.Event
	Idempotency idempotency.Request
}

// TransitionMutation is one optimistic authority lifecycle command.
type TransitionMutation struct {
	Authority       Authority
	ExpectedVersion int64
	Actor           id.APIKey
	EventID         id.Event
	Action          State
	Idempotency     idempotency.Request
}

// ResponseMutation is one atomic append-only response, outbox, and idempotency command.
type ResponseMutation struct {
	Response    Response
	EventID     id.Event
	Idempotency idempotency.Request
}

// Snapshot is the capture-visible authority, notice, and latest response state.
type Snapshot struct {
	Authority Authority
	Notice    Notice
	Response  *Response
}

// NoticeRepository is the immutable notice boundary consumed by Service.
type NoticeRepository interface {
	CreateNotice(context.Context, tenant.Scope, NoticeMutation) (Notice, error)
	FindNotice(context.Context, tenant.Scope, id.Notice) (Notice, error)
}

// ProcessingRepository is the declaration and lifecycle boundary consumed by Service.
type ProcessingRepository interface {
	Declare(context.Context, tenant.Scope, DeclarationMutation) (Authority, error)
	FindByVerification(context.Context, tenant.Scope, id.Verification) (Authority, error)
	ReplayAuthority(context.Context, tenant.Scope, idempotency.Request) (Authority, bool, error)
	Transition(context.Context, tenant.Scope, TransitionMutation) (Authority, error)
}

// ResponseRepository is the subject-interaction boundary consumed by Service.
type ResponseRepository interface {
	AppendResponse(context.Context, tenant.Scope, ResponseMutation) (Response, error)
	CaptureSnapshot(context.Context, tenant.Scope, id.Verification) (Snapshot, error)
}

// SessionFinder retrieves immutable verification requirements.
type SessionFinder interface {
	FindSession(context.Context, tenant.Scope, id.Verification) (verification.Session, error)
}

// IDGenerator is the identifier capability consumed by Service.
type IDGenerator interface {
	NewNotice() (id.Notice, error)
	NewAuthority() (id.Authority, error)
	NewSubject() (id.Subject, error)
	NewAcknowledgement() (id.Acknowledgement, error)
	NewEvent() (id.Event, error)
}

// NoticeInput is tenant-declared immutable notice content.
type NoticeInput struct {
	Key         string
	Locale      string
	Controller  string
	Recipient   string
	Copy        NoticeCopy
	EffectiveAt time.Time
}

// DeclarationInput binds declared authority to one verification.
type DeclarationInput struct {
	VerificationID       id.Verification
	NoticeID             id.Notice
	Category             string
	Purpose              string
	Jurisdiction         string
	PolicyPack           string
	IsConsentRequired    bool
	RecipientReference   string
	RecipientDisplayName string
	Regions              []string
	RetentionReference   string
	ValidFrom            time.Time
	ExpiresAt            time.Time
}

// ResponseInput is one capture-principal subject interaction.
type ResponseInput struct {
	Action                    ResponseAction
	Locale                    string
	RenderedExperienceVersion string
}

// Service coordinates authority use cases without transport or storage types.
type Service struct {
	notices     NoticeRepository
	authorities ProcessingRepository
	responses   ResponseRepository
	sessions    SessionFinder
	identifiers IDGenerator
	clock       clock.Clock
	retention   time.Duration
}

// AuthorizeEvidence re-evaluates current authority and the latest subject
// response for one exact evidence operation. It does not issue or redeem grants.
func (service *Service) AuthorizeEvidence(
	ctx context.Context,
	scope tenant.Scope,
	request evidence.ReadAuthorization,
) (evidence.AuthorizationDecision, error) {
	if ctx == nil || scope.ID().IsZero() || request.TenantID != scope.ID() ||
		request.SubjectID.IsZero() || request.VerificationID.IsZero() || request.EvidenceID.IsZero() {
		return evidence.AuthorizationDecision{}, ErrProcessingNotPermitted
	}
	if err := ctx.Err(); err != nil {
		return evidence.AuthorizationDecision{}, fmt.Errorf("authorize evidence: %w", err)
	}
	session, err := service.sessions.FindSession(ctx, scope, request.VerificationID)
	if err != nil || session.TenantID() != scope.ID() {
		return evidence.AuthorizationDecision{}, ErrProcessingNotPermitted
	}
	requirementMatched := false
	for _, requirement := range session.Requirements().Requirements {
		if requirement.Key == request.RequirementKey &&
			requirement.Purpose == request.Purpose &&
			requirement.EvidenceType == request.EvidenceType {
			requirementMatched = true
			break
		}
	}
	if !requirementMatched {
		return evidence.AuthorizationDecision{}, ErrProcessingNotPermitted
	}
	snapshot, err := service.responses.CaptureSnapshot(ctx, scope, request.VerificationID)
	if err != nil {
		return evidence.AuthorizationDecision{}, fmt.Errorf("load evidence authority snapshot: %w", err)
	}
	if err := Evaluate(snapshot.Authority, snapshot.Notice, snapshot.Response, GrantRequest{
		TenantID: request.TenantID, VerificationID: request.VerificationID,
		SubjectID: request.SubjectID, Purpose: string(request.Purpose),
		EvidenceType: string(request.EvidenceType), RecipientReference: request.RecipientReference,
		Region: request.Region,
	}, service.clock.Now()); err != nil {
		return evidence.AuthorizationDecision{}, err
	}
	if snapshot.Response == nil {
		return evidence.AuthorizationDecision{}, ErrSubjectResponseRequired
	}

	return evidence.AuthorizationDecision{
		AuthorityID: snapshot.Authority.ID(), ResponseID: snapshot.Response.Record().ID,
		PolicyReference: snapshot.Authority.Record().PolicyPack,
	}, nil
}

// NewService constructs the processing-authority application service.
func NewService(
	notices NoticeRepository,
	authorities ProcessingRepository,
	responses ResponseRepository,
	sessions SessionFinder,
	identifiers IDGenerator,
	source clock.Clock,
	idempotencyRetention time.Duration,
) (*Service, error) {
	if notices == nil || authorities == nil || responses == nil || sessions == nil ||
		identifiers == nil || source == nil || idempotencyRetention <= 0 {
		return nil, errors.New("authority: service dependencies and idempotency retention are required")
	}
	return &Service{
		notices: notices, authorities: authorities, responses: responses,
		sessions: sessions, identifiers: identifiers, clock: source,
		retention: idempotencyRetention,
	}, nil
}

// CreateNotice creates one immutable notice version.
func (service *Service) CreateNotice(
	ctx context.Context,
	accessContext access.Context,
	idempotencyKey string,
	input NoticeInput,
) (Notice, error) {
	if err := accessContext.Require(access.PermissionNoticesWrite); err != nil {
		return Notice{}, err
	}
	now := service.clock.Now().UTC().Truncate(time.Second)
	noticeID, err := service.identifiers.NewNotice()
	if err != nil {
		return Notice{}, fmt.Errorf("generate notice id: %w", err)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Notice{}, fmt.Errorf("generate notice event id: %w", err)
	}
	notice, err := NewNotice(NoticeRecord{
		ID: noticeID, TenantID: accessContext.TenantScope().ID(),
		Key: input.Key, Locale: input.Locale, Controller: input.Controller,
		Recipient: input.Recipient, Copy: input.Copy, EffectiveAt: input.EffectiveAt,
		CreatedAt: now, CreatedBy: accessContext.Principal().KeyID(),
	})
	if err != nil {
		return Notice{}, err
	}
	request, err := service.idempotencyRequest(
		accessContext, operationCreateNotice, idempotencyKey, input, now,
	)
	if err != nil {
		return Notice{}, err
	}
	return service.notices.CreateNotice(ctx, accessContext.TenantScope(), NoticeMutation{
		Notice: notice, EventID: eventID, Idempotency: request,
	})
}

// FindNotice returns one tenant-owned immutable notice version.
func (service *Service) FindNotice(
	ctx context.Context,
	accessContext access.Context,
	identifier id.Notice,
) (Notice, error) {
	if err := accessContext.Require(access.PermissionNoticesRead); err != nil {
		return Notice{}, err
	}
	return service.notices.FindNotice(ctx, accessContext.TenantScope(), identifier)
}

// Declare binds one processing-authority declaration to a verification.
func (service *Service) Declare(
	ctx context.Context,
	accessContext access.Context,
	idempotencyKey string,
	input DeclarationInput,
) (Authority, error) {
	if err := accessContext.Require(access.PermissionAuthoritiesWrite); err != nil {
		return Authority{}, err
	}
	session, err := service.sessions.FindSession(ctx, accessContext.TenantScope(), input.VerificationID)
	if err != nil {
		return Authority{}, err
	}
	now := service.clock.Now().UTC().Truncate(time.Second)
	if !session.AcceptsCaptureAt(now) || input.ExpiresAt.After(session.ExpiresAt()) {
		return Authority{}, ErrConflict
	}
	notice, err := service.notices.FindNotice(ctx, accessContext.TenantScope(), input.NoticeID)
	if err != nil {
		return Authority{}, err
	}
	if input.ValidFrom.Before(notice.EffectiveAt()) ||
		input.RecipientDisplayName != notice.Recipient() {
		return Authority{}, ErrConflict
	}
	authorityID, err := service.identifiers.NewAuthority()
	if err != nil {
		return Authority{}, fmt.Errorf("generate authority id: %w", err)
	}
	subjectID, err := service.identifiers.NewSubject()
	if err != nil {
		return Authority{}, fmt.Errorf("generate subject id: %w", err)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Authority{}, fmt.Errorf("generate authority event id: %w", err)
	}
	purposes, evidenceTypes := requirementScope(session.Requirements())
	declaration, err := New(Record{
		ID: authorityID, TenantID: accessContext.TenantScope().ID(),
		SubjectID: subjectID, VerificationID: session.ID(), NoticeID: notice.ID(),
		Category: input.Category, Purpose: input.Purpose,
		Jurisdiction: input.Jurisdiction, PolicyPack: input.PolicyPack,
		IsConsentRequired:   input.IsConsentRequired,
		RequirementPurposes: purposes, EvidenceTypes: evidenceTypes,
		RecipientReference:   input.RecipientReference,
		RecipientDisplayName: input.RecipientDisplayName,
		Regions:              input.Regions, RetentionReference: input.RetentionReference,
		ValidFrom: input.ValidFrom, ExpiresAt: input.ExpiresAt,
		CreatedAt: now, CreatedBy: accessContext.Principal().KeyID(),
	})
	if err != nil {
		return Authority{}, err
	}
	request, err := service.idempotencyRequest(
		accessContext, operationDeclare, idempotencyKey, input, now,
	)
	if err != nil {
		return Authority{}, err
	}
	return service.authorities.Declare(ctx, accessContext.TenantScope(), DeclarationMutation{
		Authority: declaration, EventID: eventID, Idempotency: request,
	})
}

// FindByVerification returns the declaration bound to a verification.
func (service *Service) FindByVerification(
	ctx context.Context,
	accessContext access.Context,
	verificationID id.Verification,
) (Authority, error) {
	if err := accessContext.Require(access.PermissionAuthoritiesRead); err != nil {
		return Authority{}, err
	}
	return service.authorities.FindByVerification(
		ctx, accessContext.TenantScope(), verificationID,
	)
}

// Transition irreversibly restricts, withdraws, or supersedes an authority.
func (service *Service) Transition(
	ctx context.Context,
	accessContext access.Context,
	verificationID id.Verification,
	idempotencyKey string,
	expectedVersion int64,
	state State,
) (Authority, error) {
	if err := accessContext.Require(access.PermissionAuthoritiesWrite); err != nil {
		return Authority{}, err
	}
	if expectedVersion < 1 {
		return Authority{}, ErrConflict
	}
	now := service.clock.Now().UTC().Truncate(time.Second)
	request, err := service.idempotencyRequest(
		accessContext, "authorities."+string(state), idempotencyKey,
		struct {
			VerificationID  string `json:"verification_id"`
			ExpectedVersion int64  `json:"expected_version"`
			State           string `json:"state"`
		}{verificationID.String(), expectedVersion, string(state)}, now,
	)
	if err != nil {
		return Authority{}, err
	}
	if replay, exists, replayErr := service.authorities.ReplayAuthority(
		ctx, accessContext.TenantScope(), request,
	); replayErr != nil {
		return Authority{}, replayErr
	} else if exists {
		return replay, nil
	}
	current, err := service.authorities.FindByVerification(
		ctx, accessContext.TenantScope(), verificationID,
	)
	if err != nil {
		return Authority{}, err
	}
	if current.Record().Version != expectedVersion {
		return Authority{}, ErrVersionConflict
	}
	switch state {
	case StateRestricted:
		err = current.Restrict(now)
	case StateWithdrawn:
		err = current.Withdraw(now)
	case StateSuperseded:
		err = current.Supersede(now)
	default:
		err = ErrConflict
	}
	if err != nil {
		return Authority{}, err
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Authority{}, fmt.Errorf("generate authority lifecycle event id: %w", err)
	}
	return service.authorities.Transition(ctx, accessContext.TenantScope(), TransitionMutation{
		Authority: current, ExpectedVersion: expectedVersion,
		Actor: accessContext.Principal().KeyID(), EventID: eventID, Action: state,
		Idempotency: request,
	})
}

// CaptureSnapshot returns the current subject-facing authority state.
func (service *Service) CaptureSnapshot(
	ctx context.Context,
	captureContext verification.CaptureContext,
) (Snapshot, error) {
	snapshot, err := service.responses.CaptureSnapshot(ctx, captureContext.TenantScope(), captureContext.Session().ID())
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Response != nil && snapshot.Response.Record().CaptureTokenID != captureContext.TokenID() {
		snapshot.Response = nil
	}
	return snapshot, nil
}

// Respond appends one capture-principal subject interaction.
func (service *Service) Respond(
	ctx context.Context,
	captureContext verification.CaptureContext,
	idempotencyKey string,
	input ResponseInput,
) (Response, error) {
	snapshot, err := service.CaptureSnapshot(ctx, captureContext)
	if err != nil {
		return Response{}, err
	}
	if input.Locale != snapshot.Notice.Locale() {
		return Response{}, ErrProcessingNotPermitted
	}
	now := service.clock.Now().UTC().Truncate(time.Second)
	responseID, err := service.identifiers.NewAcknowledgement()
	if err != nil {
		return Response{}, fmt.Errorf("generate subject response id: %w", err)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Response{}, fmt.Errorf("generate subject response event id: %w", err)
	}
	record := snapshot.Authority.Record()
	response, err := NewResponse(ResponseRecord{
		ID: responseID, TenantID: record.TenantID, AuthorityID: record.ID,
		NoticeID: record.NoticeID, SubjectID: record.SubjectID,
		VerificationID: record.VerificationID, CaptureTokenID: captureContext.TokenID(),
		Action: input.Action, Locale: input.Locale,
		RenderedExperienceVersion: input.RenderedExperienceVersion, RecordedAt: now,
	})
	if err != nil {
		return Response{}, err
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return Response{}, fmt.Errorf("serialise subject response command: %w", err)
	}
	request, err := idempotency.NewRequest(
		record.TenantID, captureContext.TokenID(), operationRespond,
		idempotencyKey, canonical, now, service.retention,
	)
	if err != nil {
		return Response{}, err
	}
	return service.responses.AppendResponse(ctx, captureContext.TenantScope(), ResponseMutation{
		Response: response, EventID: eventID, Idempotency: request,
	})
}

func (service *Service) idempotencyRequest(
	accessContext access.Context,
	operation string,
	key string,
	input any,
	now time.Time,
) (idempotency.Request, error) {
	canonical, err := json.Marshal(input)
	if err != nil {
		return idempotency.Request{}, fmt.Errorf("serialise authority command: %w", err)
	}
	return idempotency.NewRequest(
		accessContext.TenantScope().ID(), accessContext.Principal().KeyID(),
		operation, key, canonical, now, service.retention,
	)
}

func requirementScope(profile verification.Profile) ([]string, []string) {
	purposes := make([]string, 0, len(profile.Requirements))
	evidenceTypes := make([]string, 0, len(profile.Requirements))
	for _, requirement := range profile.Requirements {
		purposes = append(purposes, string(requirement.Purpose))
		evidenceTypes = append(evidenceTypes, string(requirement.EvidenceType))
	}
	slices.Sort(purposes)
	slices.Sort(evidenceTypes)
	return slices.Compact(purposes), slices.Compact(evidenceTypes)
}
