package verification

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// OperationSelectDocument binds retries to this capture-only command.
const OperationSelectDocument = "capture.document_selection"

// DocumentSelectionInput activates one immutable profile branch.
type DocumentSelectionInput struct {
	RequirementKey  string `json:"requirement_key"`
	DocumentType    string `json:"document_type"`
	ExpectedVersion int64  `json:"expected_version"`
}

// DocumentSelectionMutation carries authenticated scope and replay identity.
type DocumentSelectionMutation struct {
	Input          DocumentSelectionInput
	VerificationID id.Verification
	CaptureTokenID id.CaptureToken
	EventID        id.Event
	Retry          idempotency.Request
}

// DocumentSelectionWriter owns the atomic selection transaction.
type DocumentSelectionWriter interface {
	SelectDocument(context.Context, tenant.Scope, DocumentSelectionMutation) (Session, error)
}

// DocumentSelectionIdentifiers supplies an outbox identity.
type DocumentSelectionIdentifiers interface{ NewEvent() (id.Event, error) }

// DocumentSelectionService authorises the command before entering persistence.
type DocumentSelectionService struct {
	writer      DocumentSelectionWriter
	identifiers DocumentSelectionIdentifiers
	clock       clock.Clock
	retention   time.Duration
}

// NewDocumentSelectionService constructs capture-scoped document selection.
func NewDocumentSelectionService(writer DocumentSelectionWriter, identifiers DocumentSelectionIdentifiers, source clock.Clock, retention time.Duration) (*DocumentSelectionService, error) {
	if writer == nil || identifiers == nil || source == nil || retention <= 0 {
		return nil, errors.New("document selection dependencies are required")
	}
	return &DocumentSelectionService{writer: writer, identifiers: identifiers, clock: source, retention: retention}, nil
}

// Select applies an expected-version command using the authenticated capture principal.
func (service *DocumentSelectionService) Select(ctx context.Context, capture CaptureContext, key string, input DocumentSelectionInput) (Session, error) {
	session := capture.Session()
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	if capture.TokenID().IsZero() || capture.TenantScope().ID().IsZero() || session.TenantID() != capture.TenantScope().ID() || !session.AcceptsCaptureAt(now) || input.ExpectedVersion < 1 || !validRequirementKey(input.RequirementKey) || !validRequirementKey(input.DocumentType) {
		return Session{}, ErrSessionConflict
	}
	// Version validation belongs after replay lookup; a lost successful response
	// must remain recoverable with the original expected version.
	encoded, err := json.Marshal(input)
	if err != nil {
		return Session{}, err
	}
	retry, err := idempotency.NewRequest(session.TenantID(), capture.TokenID(), OperationSelectDocument, key, encoded, now, service.retention)
	if err != nil {
		return Session{}, err
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Session{}, err
	}
	return service.writer.SelectDocument(ctx, capture.TenantScope(), DocumentSelectionMutation{Input: input, VerificationID: session.ID(), CaptureTokenID: capture.TokenID(), EventID: eventID, Retry: retry})
}
