package evidence

import (
	"context"
	"io"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// AssetCreator durably inserts one fully protected evidence record.
type AssetCreator interface {
	Create(context.Context, tenant.Scope, Asset) error
}

// AssetFinder loads one tenant-scoped protected evidence asset.
type AssetFinder interface {
	Find(context.Context, tenant.Scope, id.Evidence) (Asset, error)
}

// AssetLifecycleUpdater persists one optimistic quarantine transition.
type AssetLifecycleUpdater interface {
	UpdateLifecycle(context.Context, tenant.Scope, Asset, int64) error
}

// UploadCreator atomically creates one resolved intent and its idempotency result.
type UploadCreator interface {
	CreateUpload(context.Context, tenant.Scope, UploadCreateMutation) (Upload, error)
}

// UploadFinder loads one upload intent within an explicit tenant scope.
type UploadFinder interface {
	FindUpload(context.Context, tenant.Scope, id.Upload) (Upload, error)
}

// UploadAttemptClaimer atomically locks and fences one complete-body attempt.
type UploadAttemptClaimer interface {
	ClaimUploadAttempt(
		context.Context,
		tenant.Scope,
		id.CaptureToken,
		id.Upload,
		int64,
		time.Time,
	) (Upload, error)
}

// UploadAttemptReleaser atomically releases the exact current attempt for retry.
type UploadAttemptReleaser interface {
	FailUploadAttempt(
		context.Context,
		tenant.Scope,
		id.CaptureToken,
		id.Upload,
		int64,
		uint32,
		time.Time,
	) (Upload, error)
}

// UploadAttemptRejecter atomically terminally rejects one exact current attempt.
type UploadAttemptRejecter interface {
	RejectUploadAttempt(
		context.Context,
		tenant.Scope,
		id.CaptureToken,
		id.Upload,
		int64,
		uint32,
		string,
		time.Time,
	) (Upload, error)
}

// UploadAccepter atomically persists protected evidence, accepts its exact
// fenced upload, appends both audits, and records the evidence-ready event.
type UploadAccepter interface {
	AcceptUpload(context.Context, tenant.Scope, UploadAcceptance) (Upload, error)
}

// KeyRewrapPersister atomically replaces only wrapped-key metadata using
// optimistic concurrency and appends the exact attributed audit record.
type KeyRewrapPersister interface {
	PersistKeyRewrap(context.Context, tenant.Scope, KeyRewrap, CommandAttribution) error
}

// GrantCreator durably records a scoped processing grant and creation audit.
type GrantCreator interface {
	CreateGrant(context.Context, tenant.Scope, Grant, CommandAttribution) error
}

// GrantClaimer atomically consumes one permitted grant use for an authenticated
// runner. Repeating a redemption ID must return its existing pending or terminal
// state without consuming another use.
type GrantClaimer interface {
	ClaimGrant(context.Context, tenant.Scope, id.Grant, id.Redemption, Runner, time.Time) (Redemption, error)
}

// GrantOutcomeRecorder appends the terminal outcome for one claimed grant use.
// Repeating the same terminal outcome is idempotent; a different terminal
// outcome for the same redemption must fail as a conflict.
type GrantOutcomeRecorder interface {
	RecordGrantOutcome(context.Context, tenant.Scope, Redemption, GrantOutcome, time.Time) error
}

// GrantRevoker atomically and irreversibly revokes a processing grant.
type GrantRevoker interface {
	RevokeGrant(context.Context, tenant.Scope, id.Grant, CommandAttribution, time.Time) (Grant, error)
}

// ReadAuthorizer evaluates current processing authority for one exact asset operation.
type ReadAuthorizer interface {
	AuthorizeEvidence(context.Context, tenant.Scope, ReadAuthorization) (AuthorizationDecision, error)
}

// GrantIDGenerator creates opaque processing-grant identifiers.
type GrantIDGenerator interface {
	NewGrant() (id.Grant, error)
}

// IntegrityQuarantiner must atomically quarantine the asset or durably record a
// fail-closed blocking incident and reconciliation intent before returning nil.
type IntegrityQuarantiner interface {
	QuarantineIntegrity(context.Context, tenant.Scope, Asset, int64) error
}

// PlaintextReceiver is a trusted internal adapter for an authenticated endpoint.
// It stages plaintext, commits once per redemption ID after produce succeeds,
// then invokes afterCommit. Repeating an already committed redemption must skip
// produce and invoke afterCommit so an incomplete outcome audit can resume.
type PlaintextReceiver interface {
	Binding() ReceiverBinding
	Receive(context.Context, id.Redemption, string, func(io.Writer) error, func() error) error
}

// ContentSealer encrypts a plaintext stream and writes authenticated ciphertext.
type ContentSealer interface {
	Seal(context.Context, io.Writer, io.Reader, platformcrypto.Context) (platformcrypto.Envelope, error)
}

// ContentOpener authenticates and decrypts a ciphertext stream.
type ContentOpener interface {
	Open(context.Context, io.Writer, io.Reader, platformcrypto.Envelope, platformcrypto.Context) error
}

// ObjectWriter stores a ciphertext stream at an internally generated key.
type ObjectWriter interface {
	Put(context.Context, objectstore.Key, func(io.Writer) error) (objectstore.Object, error)
}

// ObjectReader opens the exact immutable ciphertext object version.
type ObjectReader interface {
	Open(context.Context, objectstore.Object) (io.ReadCloser, error)
}

// ObjectDeleter removes the exact ciphertext object version idempotently.
type ObjectDeleter interface {
	Delete(context.Context, objectstore.Object) error
}

// ObjectInventoryLister discovers exact physical objects beneath one owned prefix.
type ObjectInventoryLister interface {
	ListInventory(
		context.Context, objectstore.Key, string, int,
	) (objectstore.InventoryPage, error)
}

// InventoryObjectDeleter deletes a provider-discovered physical object after
// authoritative orphan classification.
type InventoryObjectDeleter interface {
	DeleteInventory(context.Context, objectstore.InventoryObject) error
}

// ObjectInventory composes the two provider inventory capabilities consumed by discovery.
type ObjectInventory interface {
	ObjectInventoryLister
	InventoryObjectDeleter
}

// ObjectReferenceChecker reports whether authoritative durable state protects
// an exact physical object from orphan deletion.
type ObjectReferenceChecker interface {
	IsObjectReferenced(context.Context, tenant.Scope, objectstore.Key, string) (bool, error)
}
