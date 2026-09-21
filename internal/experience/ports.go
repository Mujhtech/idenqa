package experience

import (
	"context"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TransitionOperation is the closed lifecycle command set.
type TransitionOperation string

// Lifecycle operations. Every operation is expected-version and audited.
const (
	OperationCreate   TransitionOperation = "create"
	OperationUpdate   TransitionOperation = "update"
	OperationApprove  TransitionOperation = "approve"
	OperationPublish  TransitionOperation = "publish"
	OperationRevoke   TransitionOperation = "revoke"
	OperationRollback TransitionOperation = "rollback"
	OperationImport   TransitionOperation = "import"
)

// Valid reports whether the operation is part of the closed vocabulary.
func (operation TransitionOperation) Valid() bool {
	switch operation {
	case OperationCreate, OperationUpdate, OperationApprove, OperationPublish, OperationRevoke, OperationRollback, OperationImport:
		return true
	default:
		return false
	}
}

// Signature is one signing-boundary result. The private key never leaves the
// owned signing adapter.
type Signature struct {
	KeyID     string
	Algorithm string
	Bytes     []byte
}

// Signer is the owned Core-managed signing port. Key ids come from the
// deployment key boundary and are never caller supplied.
type Signer interface {
	Sign(ctx context.Context, canonical []byte) (Signature, error)
}

// Verifier is the owned Core-managed verification port used by import.
type Verifier interface {
	Verify(ctx context.Context, keyID string, canonical []byte, signature []byte) error
}

// AssetStore verifies vetted asset references through the owned object-store
// port. Publication fails closed when an asset cannot be verified.
type AssetStore interface {
	Verify(ctx context.Context, scope tenant.Scope, asset contract.Asset) error
}

// IDGenerator is the identifier capability consumed by the service.
type IDGenerator interface {
	NewExperience() (id.Experience, error)
	NewEvent() (id.Event, error)
}

// Clock is the deterministic time source used for pins and lifecycle times.
type Clock interface {
	Now() time.Time
}

// CreateMutation atomically creates one draft experience, its signed first
// revision, and its creation event.
type CreateMutation struct {
	Manifest contract.Manifest
	Document contract.Document
	Actor    string
	EventID  id.Event
	At       time.Time
}

// RevisionMutation saves one new immutable draft revision. ExpectedRevision
// guards the aggregate optimistic version.
type RevisionMutation struct {
	ExperienceID     id.Experience
	ExpectedRevision int64
	Manifest         contract.Manifest
	Document         contract.Document
	Actor            string
	Reason           string
	EventID          id.Event
	At               time.Time
}

// TransitionMutation applies one validated lifecycle transition. Plan is the
// domain-validated next state and may be zero for idempotent no-ops.
type TransitionMutation struct {
	ExperienceID     id.Experience
	ExpectedRevision int64
	Operation        TransitionOperation
	TargetVersion    uint32
	Digest           string
	Plan             Plan
	Applied          bool
	Actor            string
	Reason           string
	EventID          id.Event
	At               time.Time
}

// Repository is the tenant-scoped durable experience boundary. Implementations
// must enforce expected-version preconditions, immutability, and RLS.
type Repository interface {
	Create(context.Context, tenant.Scope, CreateMutation) (Experience, error)
	Find(context.Context, tenant.Scope, id.Experience) (Experience, error)
	List(context.Context, tenant.Scope, *Position, int) (Page, error)
	ApplyRevision(context.Context, tenant.Scope, RevisionMutation) (Experience, error)
	ApplyTransition(context.Context, tenant.Scope, TransitionMutation) (Experience, error)
	LoadPublished(context.Context, tenant.Scope) ([]Published, error)
	Revision(context.Context, tenant.Scope, id.Experience, uint32) (Revision, error)
	Changes(context.Context, tenant.Scope, id.Experience) ([]Change, error)
}

// PinRepository is the tenant-scoped session-pin boundary. Pins are immutable
// once written; SavePin is idempotent and returns the stored pin.
type PinRepository interface {
	SavePin(context.Context, tenant.Scope, Pin) (Pin, error)
	FindPin(context.Context, tenant.Scope, id.Verification) (Pin, error)
}

// DenyAssets is the closed verifier used when no object store is configured.
// Any document that references an asset is refused rather than trusted.
type DenyAssets struct{}

// Verify always fails closed.
func (DenyAssets) Verify(context.Context, tenant.Scope, contract.Asset) error {
	return ErrAssetUnavailable
}
