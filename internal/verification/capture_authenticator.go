package verification

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// CaptureVerificationRepository is the narrow pre-authentication lookup for a
// signed capture-token claim set. It must constrain by tenant, token, and session.
type CaptureVerificationRepository interface {
	FindForCapture(context.Context, access.CaptureTokenClaims) (SessionCreation, error)
}

// CaptureContext is transport-neutral authority for one active capture session.
type CaptureContext struct {
	scope   tenant.Scope
	session Session
	tokenID id.CaptureToken
}

// TenantScope returns the authenticated capture tenant.
func (captureContext CaptureContext) TenantScope() tenant.Scope { return captureContext.scope }

// Session returns the authenticated active verification session.
func (captureContext CaptureContext) Session() Session { return captureContext.session }

// TokenID returns the authenticated non-secret capture credential identifier.
func (captureContext CaptureContext) TokenID() id.CaptureToken { return captureContext.tokenID }

// CaptureAuthenticator validates signed claims and their authoritative durable state.
type CaptureAuthenticator struct {
	repository CaptureVerificationRepository
	signer     *access.CaptureTokenSigner
	clock      clock.Clock
}

// NewCaptureAuthenticator constructs the capture authentication service.
func NewCaptureAuthenticator(
	repository CaptureVerificationRepository,
	signer *access.CaptureTokenSigner,
	source clock.Clock,
) (*CaptureAuthenticator, error) {
	if repository == nil || signer == nil || source == nil {
		return nil, errors.New("verification: capture authenticator dependencies are required")
	}

	return &CaptureAuthenticator{repository: repository, signer: signer, clock: source}, nil
}

// Authenticate verifies the bearer signature, database binding, revocation,
// and session lifecycle. Expected failures deliberately collapse to one error.
func (authenticator *CaptureAuthenticator) Authenticate(
	ctx context.Context,
	encoded string,
) (CaptureContext, error) {
	return authenticator.authenticate(ctx, encoded, false)
}

// CancellationAuthenticator restricts its broader session authentication to the cancellation route.
type CancellationAuthenticator struct{ Authenticator *CaptureAuthenticator }

// Authenticate validates the live credential without granting capture on a stopped session.
func (authenticator CancellationAuthenticator) Authenticate(ctx context.Context, encoded string) (CaptureContext, error) {
	return authenticator.Authenticator.authenticate(ctx, encoded, true)
}

func (authenticator *CaptureAuthenticator) authenticate(ctx context.Context, encoded string, allowStopped bool) (CaptureContext, error) {
	if authenticator == nil {
		return CaptureContext{}, errors.New("verification: capture authenticator is not initialised")
	}
	claims, err := authenticator.signer.Verify(encoded)
	if err != nil {
		return CaptureContext{}, access.ErrInvalidCaptureToken
	}
	record, err := authenticator.repository.FindForCapture(ctx, claims)
	if errors.Is(err, access.ErrInvalidCaptureToken) || errors.Is(err, ErrSessionNotFound) {
		return CaptureContext{}, access.ErrInvalidCaptureToken
	}
	if err != nil {
		return CaptureContext{}, fmt.Errorf("find capture authentication record: %w", err)
	}
	credential := record.Credential
	session := record.Session
	now := authenticator.clock.Now().UTC()
	if credential.ID().String() != claims.TokenID.String() ||
		credential.TenantID().String() != claims.TenantID.String() ||
		credential.VerificationID().String() != claims.VerificationID.String() ||
		credential.KeyVersion() != claims.KeyVersion ||
		!credential.IssuedAt().Equal(claims.IssuedAt) ||
		!credential.ExpiresAt().Equal(claims.ExpiresAt) ||
		!credential.IsUsableAt(now) ||
		session.ID().String() != claims.VerificationID.String() ||
		session.TenantID().String() != claims.TenantID.String() ||
		(!allowStopped && !session.AcceptsCaptureAt(now)) {
		return CaptureContext{}, access.ErrInvalidCaptureToken
	}
	scope, err := tenant.NewScope(claims.TenantID)
	if err != nil {
		return CaptureContext{}, access.ErrInvalidCaptureToken
	}

	return CaptureContext{scope: scope, session: session, tokenID: credential.ID()}, nil
}
