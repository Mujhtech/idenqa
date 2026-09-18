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

// OutcomeCredentialRepository is the narrow pre-authentication lookup for a
// signed outcome-token claim set. It must constrain by tenant, token, and session.
type OutcomeCredentialRepository interface {
	FindForOutcome(context.Context, access.OutcomeTokenClaims) (access.OutcomeCredential, error)
}

// OutcomeContext is transport-neutral, read-only authority for one safe outcome projection.
type OutcomeContext struct {
	scope          tenant.Scope
	verificationID id.Verification
	tokenID        id.OutcomeToken
}

// TenantScope returns the authenticated outcome tenant.
func (outcomeContext OutcomeContext) TenantScope() tenant.Scope { return outcomeContext.scope }

// VerificationID returns the verification bound by the signed credential.
func (outcomeContext OutcomeContext) VerificationID() id.Verification {
	return outcomeContext.verificationID
}

// TokenID returns the authenticated non-secret outcome credential identifier.
func (outcomeContext OutcomeContext) TokenID() id.OutcomeToken { return outcomeContext.tokenID }

// OutcomeAuthenticator validates signed outcome claims and their authoritative durable state.
type OutcomeAuthenticator struct {
	repository OutcomeCredentialRepository
	signer     *access.OutcomeTokenSigner
	clock      clock.Clock
}

// NewOutcomeAuthenticator constructs the read-only outcome authentication service.
func NewOutcomeAuthenticator(
	repository OutcomeCredentialRepository,
	signer *access.OutcomeTokenSigner,
	source clock.Clock,
) (*OutcomeAuthenticator, error) {
	if repository == nil || signer == nil || source == nil {
		return nil, errors.New("verification: outcome authenticator dependencies are required")
	}

	return &OutcomeAuthenticator{repository: repository, signer: signer, clock: source}, nil
}

// Authenticate verifies the signature, database binding, revocation, and token lifetime.
// It deliberately does not load or grant any capture-session authority.
func (authenticator *OutcomeAuthenticator) Authenticate(
	ctx context.Context,
	encoded string,
) (OutcomeContext, error) {
	if authenticator == nil {
		return OutcomeContext{}, errors.New("verification: outcome authenticator is not initialised")
	}
	claims, err := authenticator.signer.Verify(encoded)
	if err != nil {
		return OutcomeContext{}, access.ErrInvalidOutcomeToken
	}
	credential, err := authenticator.repository.FindForOutcome(ctx, claims)
	if errors.Is(err, access.ErrInvalidOutcomeToken) || errors.Is(err, ErrSessionNotFound) {
		return OutcomeContext{}, access.ErrInvalidOutcomeToken
	}
	if err != nil {
		return OutcomeContext{}, fmt.Errorf("find outcome authentication record: %w", err)
	}
	if credential.ID().String() != claims.TokenID.String() ||
		credential.TenantID().String() != claims.TenantID.String() ||
		credential.VerificationID().String() != claims.VerificationID.String() ||
		credential.KeyVersion() != claims.KeyVersion ||
		!credential.IssuedAt().Equal(claims.IssuedAt) ||
		!credential.ExpiresAt().Equal(claims.ExpiresAt) ||
		!credential.IsUsableAt(authenticator.clock.Now().UTC()) {
		return OutcomeContext{}, access.ErrInvalidOutcomeToken
	}
	scope, err := tenant.NewScope(claims.TenantID)
	if err != nil {
		return OutcomeContext{}, access.ErrInvalidOutcomeToken
	}

	return OutcomeContext{
		scope: scope, verificationID: claims.VerificationID, tokenID: credential.ID(),
	}, nil
}
