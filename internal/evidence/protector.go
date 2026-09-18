package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ErrCleanupRequired means exact-version ciphertext deletion failed after the
// corresponding metadata could not be committed. Operators must reconcile it.
var ErrCleanupRequired = errors.New("evidence: ciphertext cleanup required")

// ErrAcceptanceReconciliationRequired means staged ciphertext must be retained
// until an ambiguous durable acceptance outcome is authoritatively reconciled.
var ErrAcceptanceReconciliationRequired = errors.New("evidence: upload acceptance reconciliation required")

// CleanupError retains the exact non-secret object reference for reconciliation
// without placing it in ordinary error text.
type CleanupError struct{ object objectstore.Object }

// Error returns stable low-cardinality diagnostic text.
func (err *CleanupError) Error() string { return ErrCleanupRequired.Error() }

// Unwrap supports errors.Is with ErrCleanupRequired.
func (err *CleanupError) Unwrap() error { return ErrCleanupRequired }

// Object returns the exact immutable object that still requires deletion.
func (err *CleanupError) Object() objectstore.Object { return err.object }

// AcceptanceReconciliationError retains the exact non-secret staged object
// reference without exposing it in ordinary error text.
type AcceptanceReconciliationError struct {
	object objectstore.Object
	cause  error
}

// Error returns stable low-cardinality diagnostic text.
func (err *AcceptanceReconciliationError) Error() string {
	return ErrAcceptanceReconciliationRequired.Error()
}

// Unwrap preserves both the stable reconciliation classification and cause.
func (err *AcceptanceReconciliationError) Unwrap() []error {
	return []error{ErrAcceptanceReconciliationRequired, err.cause}
}

// Object returns the exact immutable object whose disposition is unresolved.
func (err *AcceptanceReconciliationError) Object() objectstore.Object { return err.object }

// ReconciliationError converts an ambiguous acceptance error into a retained
// exact-object reconciliation obligation.
func (prepared PreparedEvidence) ReconciliationError(cause error) error {
	if cause == nil {
		return errors.New("evidence: acceptance reconciliation cause is required")
	}

	return &AcceptanceReconciliationError{object: prepared.object, cause: cause}
}

// ProtectionInput is transient application input for one already-authorised
// evidence artefact. Plaintext is streamed and is never retained in the asset.
type ProtectionInput struct {
	Registry          Reference
	ID                id.Evidence
	SubjectID         id.Subject
	VerificationID    id.Verification
	RequirementKey    string
	EvidenceType      Name
	Artefact          Name
	AcquisitionMethod Name
	Assurances        []Name
	Region            string
	RetentionClass    string
	ContentRevision   uint32
	MediaType         string
	Plaintext         io.Reader
	CreatedAt         time.Time
}

// PreparedEvidence is one fully protected but not yet durably accepted asset.
// It contains no plaintext; its exact object reference remains available only
// for commit or compensation by the owning workflow.
type PreparedEvidence struct {
	asset  Asset
	object objectstore.Object
}

// Asset returns the validated protected metadata awaiting durable acceptance.
func (prepared PreparedEvidence) Asset() Asset { return prepared.asset }

// Protector streams plaintext through authenticated encryption into immutable
// object storage, then persists only the protected metadata.
type Protector struct {
	sealer  ContentSealer
	objects interface {
		ObjectWriter
		ObjectDeleter
	}
	repository     AssetCreator
	registry       Registry
	catalog        Catalog
	cleanupTimeout time.Duration
}

// NewProtector constructs the application workflow with an explicit bounded
// compensation timeout.
func NewProtector(
	sealer ContentSealer,
	objects interface {
		ObjectWriter
		ObjectDeleter
	},
	repository AssetCreator,
	registry Registry,
	cleanupTimeout time.Duration,
) (*Protector, error) {
	if sealer == nil || objects == nil || repository == nil || registry.Reference().SchemaVersion == 0 || cleanupTimeout <= 0 {
		return nil, errors.New("evidence: protector dependencies are invalid")
	}

	return &Protector{
		sealer: sealer, objects: objects, repository: repository,
		registry: registry, cleanupTimeout: cleanupTimeout,
	}, nil
}

// Protect stores ciphertext before committing its exact durable metadata. If
// metadata validation or persistence fails, it deletes only the returned object version.
func (protector *Protector) Protect(
	ctx context.Context,
	scope tenant.Scope,
	input ProtectionInput,
) (Asset, error) {
	prepared, err := protector.Prepare(ctx, scope, input)
	if err != nil {
		return Asset{}, err
	}
	asset := prepared.Asset()
	if err := protector.repository.Create(ctx, scope, asset); err != nil {
		return Asset{}, protector.compensate(ctx, prepared.object, fmt.Errorf("persist protected evidence: %w", err))
	}

	return asset, nil
}

// Prepare streams plaintext into authenticated immutable ciphertext and builds
// a validated asset without making the evidence durably available.
func (protector *Protector) Prepare(
	ctx context.Context,
	scope tenant.Scope,
	input ProtectionInput,
) (PreparedEvidence, error) {
	if err := validateProtectionInput(ctx, scope, input); err != nil {
		return PreparedEvidence{}, err
	}
	registry := protector.registry
	if input.Registry.SchemaVersion != 0 && input.Registry != registry.Reference() {
		var err error
		registry, err = protector.catalog.Resolve(input.Registry)
		if err != nil {
			return PreparedEvidence{}, err
		}
	}
	record := Record{
		ID:                input.ID,
		TenantID:          scope.ID(),
		SubjectID:         input.SubjectID,
		VerificationID:    input.VerificationID,
		RequirementKey:    input.RequirementKey,
		EvidenceType:      input.EvidenceType,
		Artefact:          input.Artefact,
		AcquisitionMethod: input.AcquisitionMethod,
		Assurances:        slices.Clone(input.Assurances),
		Registry:          registry.Reference(),
		Region:            input.Region,
		RetentionClass:    input.RetentionClass,
		ContentRevision:   input.ContentRevision,
		CreatedAt:         input.CreatedAt.UTC(),
	}
	authenticatedContext, err := AuthenticatedContext(record)
	if err != nil {
		return PreparedEvidence{}, err
	}
	key, err := protectionObjectKey(record)
	if err != nil {
		return PreparedEvidence{}, err
	}

	plaintextDigest := sha256.New()
	var envelope platformcrypto.Envelope
	object, err := protector.objects.Put(ctx, key, func(destination io.Writer) error {
		envelope, err = protector.sealer.Seal(
			ctx,
			destination,
			io.TeeReader(input.Plaintext, plaintextDigest),
			authenticatedContext,
		)

		return err
	})
	if err != nil {
		if !object.IsZero() {
			return PreparedEvidence{}, protector.compensate(
				ctx, object, fmt.Errorf("protect evidence ciphertext: %w", err),
			)
		}
		return PreparedEvidence{}, fmt.Errorf("protect evidence ciphertext: %w", err)
	}
	record.Content = ContentRecord{
		Object: object.Record(), Envelope: envelope.Record(),
		PlaintextDigest: "sha256:" + hex.EncodeToString(plaintextDigest.Sum(nil)),
		MediaType:       input.MediaType,
	}
	asset, err := NewAvailable(record, registry)
	if err != nil {
		return PreparedEvidence{}, protector.compensate(ctx, object, fmt.Errorf("validate protected evidence: %w", err))
	}

	return PreparedEvidence{asset: asset, object: object}, nil
}

// Discard deletes the exact staged ciphertext with bounded cancellation-
// independent cleanup. Failure returns a reconciliation-safe CleanupError.
func (protector *Protector) Discard(ctx context.Context, prepared PreparedEvidence) error {
	if protector == nil || ctx == nil || prepared.object.IsZero() || prepared.asset.ID().IsZero() ||
		prepared.asset.Content().Object() != prepared.object {
		return errors.New("evidence: prepared evidence is invalid")
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), protector.cleanupTimeout)
	defer cancel()
	if err := protector.objects.Delete(cleanupContext, prepared.object); err != nil {
		return &CleanupError{object: prepared.object}
	}

	return nil
}

func (protector *Protector) compensate(
	ctx context.Context,
	object objectstore.Object,
	cause error,
) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), protector.cleanupTimeout)
	defer cancel()
	if err := protector.objects.Delete(cleanupContext, object); err != nil {
		return errors.Join(cause, &CleanupError{object: object})
	}

	return cause
}

func validateProtectionInput(ctx context.Context, scope tenant.Scope, input ProtectionInput) error {
	if ctx == nil {
		return errors.New("evidence: protection context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("protect evidence: %w", err)
	}
	if scope.ID().IsZero() || input.ID.IsZero() || input.SubjectID.IsZero() || input.VerificationID.IsZero() ||
		input.ContentRevision == 0 || input.Plaintext == nil || input.CreatedAt.IsZero() {
		return errors.New("evidence: protection identity or content is invalid")
	}
	mediaType, _, err := mime.ParseMediaType(input.MediaType)
	if err != nil || mediaType == "" || len(input.MediaType) > 200 || strings.ContainsAny(input.MediaType, "\r\n") {
		return errors.New("evidence: protection media type is invalid")
	}

	return nil
}

func protectionObjectKey(record Record) (objectstore.Key, error) {
	return objectstore.NewKey(path.Join(
		"tenants",
		record.TenantID.String(),
		"evidence",
		record.ID.String(),
		"content",
		strconv.FormatUint(uint64(record.ContentRevision), 10),
	))
}

// WithCatalog permits exact pinned revisions beyond the default registry.
func (protector *Protector) WithCatalog(c Catalog) *Protector {
	result := *protector
	result.catalog = c
	return &result
}
