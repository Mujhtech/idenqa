package access

import (
	stdcontext "context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

var (
	// ErrInvalidCredential deliberately covers every caller-visible
	// authentication failure without disclosing which check failed.
	ErrInvalidCredential = errors.New("access: invalid credential")
	// ErrInsufficientScope is returned only after successful authentication.
	ErrInsufficientScope = errors.New("access: insufficient scope")
)

// VerificationRecord is the result of the deliberately narrow
// pre-authentication lookup. It does not confer authority.
type VerificationRecord struct {
	key         Key
	tenantState tenant.State
}

// NewVerificationRecord validates a key and its owning tenant lifecycle as one
// lookup result. Authentication still has to verify the presented credential.
func NewVerificationRecord(key Key, tenantState tenant.State) (VerificationRecord, error) {
	if key.ID().IsZero() || key.TenantID().IsZero() {
		return VerificationRecord{}, errors.New("API key verification record is invalid")
	}
	if tenantState != tenant.StateActive && tenantState != tenant.StateDisabled {
		return VerificationRecord{}, errors.New("API key verification tenant state is invalid")
	}

	return VerificationRecord{key: key, tenantState: tenantState}, nil
}

// VerificationRepository is the only boundary allowed to use the untrusted
// tenant hint before authentication. Implementations must constrain lookup by
// both tenant and key identifiers and must not construct tenant.Scope.
type VerificationRepository interface {
	FindForVerification(stdcontext.Context, id.Tenant, id.APIKey) (VerificationRecord, error)
}

// APIKeyPrincipal identifies the successfully authenticated non-human actor.
type APIKeyPrincipal struct{ keyID id.APIKey }

// KeyID returns the non-secret credential record identifier.
func (principal APIKeyPrincipal) KeyID() id.APIKey { return principal.keyID }

// Context is transport-neutral authenticated authority for one tenant.
// Application services must still require the permission for each operation.
type Context struct {
	principal APIKeyPrincipal
	scope     tenant.Scope
	grant     Grant
}

// Principal returns the authenticated API-key actor.
func (accessContext Context) Principal() APIKeyPrincipal { return accessContext.principal }

// TenantScope returns the verified effective tenant scope.
func (accessContext Context) TenantScope() tenant.Scope { return accessContext.scope }

// Allows reports whether the authenticated immutable grant contains permission.
func (accessContext Context) Allows(permission Permission) bool {
	return !accessContext.scope.ID().IsZero() && accessContext.grant.Allows(permission)
}

// Require authorises one exact application permission.
func (accessContext Context) Require(permission Permission) error {
	if !accessContext.Allows(permission) {
		return ErrInsufficientScope
	}

	return nil
}

// Authenticator verifies tenant API keys and constructs access contexts.
type Authenticator struct {
	repository VerificationRepository
	peppers    *PepperSet
	clock      clock.Clock
}

// NewAuthenticator constructs the API-key authentication service.
func NewAuthenticator(repository VerificationRepository, peppers *PepperSet, source clock.Clock) (*Authenticator, error) {
	if repository == nil || peppers == nil || source == nil {
		return nil, errors.New("API key authenticator dependencies are required")
	}

	return &Authenticator{repository: repository, peppers: peppers, clock: source}, nil
}

// Authenticate verifies a complete display-once credential. Expected caller
// failures collapse to ErrInvalidCredential; repository and configuration
// failures retain operational detail for the process boundary to handle.
func (authenticator *Authenticator) Authenticate(ctx stdcontext.Context, encoded string) (Context, error) {
	if authenticator == nil {
		return Context{}, errors.New("API key authenticator is not initialised")
	}

	presented, err := ParsePresentedKey(encoded)
	if err != nil {
		if dummyErr := authenticator.peppers.DummyVerify(PresentedKey{}); dummyErr != nil {
			return Context{}, fmt.Errorf("perform dummy API key verification: %w", dummyErr)
		}

		return Context{}, ErrInvalidCredential
	}

	record, err := authenticator.repository.FindForVerification(ctx, presented.TenantHint(), presented.ID())
	if errors.Is(err, ErrKeyNotFound) {
		if dummyErr := authenticator.peppers.DummyVerify(presented); dummyErr != nil {
			return Context{}, fmt.Errorf("perform dummy API key verification: %w", dummyErr)
		}

		return Context{}, ErrInvalidCredential
	}
	if err != nil {
		return Context{}, fmt.Errorf("find API key for verification: %w", err)
	}

	key := record.key
	valid, err := authenticator.peppers.Verify(presented, key.PepperVersion(), key.Digest())
	if err != nil {
		return Context{}, fmt.Errorf("verify API key digest: %w", err)
	}
	if !valid || key.ID().String() != presented.ID().String() ||
		key.TenantID().String() != presented.TenantHint().String() ||
		record.tenantState != tenant.StateActive || !key.UsableAt(authenticator.clock.Now()) {
		return Context{}, ErrInvalidCredential
	}

	scope, err := tenant.NewScope(key.TenantID())
	if err != nil {
		return Context{}, fmt.Errorf("construct authenticated tenant scope: %w", err)
	}

	return Context{
		principal: APIKeyPrincipal{keyID: key.ID()},
		scope:     scope,
		grant:     key.Grant(),
	}, nil
}
