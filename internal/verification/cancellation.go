package verification

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// CancellationResult is the original safe cancellation receipt retained for replay.
type CancellationResult struct {
	EventID        string       `json:"event_id"`
	VerificationID string       `json:"verification_id"`
	State          SessionState `json:"state"`
	Version        int64        `json:"version"`
	OccurredAt     time.Time    `json:"occurred_at"`
}

// CancellationMutation binds a command to its authenticated actor and exact version.
type CancellationMutation struct {
	VerificationID  id.Verification
	ExpectedVersion int64
	Retry           idempotency.Request
}

// CancellationRepository commits the transition and replay receipt together.
type CancellationRepository interface {
	Cancel(context.Context, tenant.Scope, CancellationMutation) (CancellationResult, error)
}

// CancellationService owns tenant and subject cancellation authorisation.
type CancellationService struct {
	repository CancellationRepository
	clock      clock.Clock
	retention  time.Duration
}

// NewCancellationService constructs the cancellation application boundary.
func NewCancellationService(repository CancellationRepository, source clock.Clock, retention time.Duration) (*CancellationService, error) {
	if repository == nil || source == nil || retention <= 0 {
		return nil, errors.New("verification: cancellation dependencies are required")
	}
	return &CancellationService{repository: repository, clock: source, retention: retention}, nil
}

// CancelTenant requires the dedicated immutable API-key permission snapshot.
func (service *CancellationService) CancelTenant(ctx context.Context, actor access.Context, verificationID id.Verification, version int64, key string) (CancellationResult, error) {
	if err := actor.Require(access.PermissionVerificationSessionsCancel); err != nil {
		return CancellationResult{}, err
	}
	return service.cancel(ctx, actor.TenantScope(), actor.Principal().KeyID(), verificationID, version, key)
}

// CancelSubject addresses only the session authenticated by the capture credential.
// Cancellation does not require consent to further processing.
func (service *CancellationService) CancelSubject(ctx context.Context, actor CaptureContext, version int64, key string) (CancellationResult, error) {
	if actor.TokenID().IsZero() || actor.Session().ID().IsZero() || actor.Session().TenantID() != actor.TenantScope().ID() {
		return CancellationResult{}, access.ErrInvalidCaptureToken
	}
	return service.cancel(ctx, actor.TenantScope(), actor.TokenID(), actor.Session().ID(), version, key)
}

func (service *CancellationService) cancel(ctx context.Context, scope tenant.Scope, principal idempotency.PrincipalValue, verificationID id.Verification, version int64, key string) (CancellationResult, error) {
	if verificationID.IsZero() || version < 1 || version == math.MaxInt64 {
		return CancellationResult{}, ErrSessionConflict
	}
	canonical, err := json.Marshal(struct {
		VerificationID  string `json:"verification_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}{verificationID.String(), version})
	if err != nil {
		return CancellationResult{}, err
	}
	retry, err := idempotency.NewRequest(scope.ID(), principal, "verification.cancel", key, canonical, service.clock.Now().UTC().Truncate(time.Microsecond), service.retention)
	if err != nil {
		return CancellationResult{}, err
	}
	return service.repository.Cancel(ctx, scope, CancellationMutation{VerificationID: verificationID, ExpectedVersion: version, Retry: retry})
}
