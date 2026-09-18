// Package postgres adapts evidence persistence ports to PostgreSQL.
package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type transactionRunner interface {
	WithinTransaction(
		context.Context,
		platformpostgres.TransactionOptions,
		func(context.Context, platformpostgres.Transaction) error,
	) error
}

// Store persists tenant-scoped evidence metadata and lifecycle audit records.
type Store struct {
	pool    transactionRunner
	catalog evidence.Catalog
	clock   clock.Clock
	wrapper platformcrypto.KeyWrapper
}

var _ evidence.IntegrityQuarantiner = (*Store)(nil)
var _ evidence.KeyRewrapPersister = (*Store)(nil)

// QuarantineIntegrity atomically persists the optimistic integrity quarantine
// and its audit record. A conflict or database failure returns an error.
func (store *Store) QuarantineIntegrity(
	ctx context.Context,
	scope tenant.Scope,
	asset evidence.Asset,
	expectedVersion int64,
) error {
	record := asset.Record()
	if record.State != evidence.StateQuarantined ||
		record.Integrity != evidence.IntegrityFailed || record.QuarantinedAt == nil {
		return evidence.ErrConflict
	}
	return store.UpdateLifecycle(ctx, scope, asset, expectedVersion)
}

// New constructs an evidence PostgreSQL adapter.
func New(pool transactionRunner, wrapper platformcrypto.KeyWrapper, catalog evidence.Catalog) (*Store, error) {
	return NewWithClock(pool, wrapper, catalog, clock.System{})
}

// NewWithClock constructs an adapter with an explicit commit-time observation clock.
func NewWithClock(pool transactionRunner, wrapper platformcrypto.KeyWrapper, catalog evidence.Catalog, source clock.Clock) (*Store, error) {
	if pool == nil || catalog.IsZero() || source == nil {
		return nil, errors.New("evidence postgres: pool and registry catalog are required")
	}

	return &Store{pool: pool, catalog: catalog, clock: source, wrapper: wrapper}, nil
}

// Create inserts protected evidence metadata and its first audit record atomically.
func (store *Store) Create(ctx context.Context, scope tenant.Scope, asset evidence.Asset) error {
	record := asset.Record()
	if scope.ID().IsZero() || record.TenantID != scope.ID() {
		return evidence.ErrNotFound
	}
	if record.Version != 1 || record.State != evidence.StateAvailable {
		return evidence.ErrConflict
	}
	if _, err := store.catalog.Resolve(record.Registry); err != nil {
		return fmt.Errorf("create evidence registry: %w", err)
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		parameters, err := createParameters(record)
		if err != nil {
			return err
		}
		if err := queries.CreateEvidenceAsset(ctx, parameters); err != nil {
			return fmt.Errorf("insert evidence asset: %w", err)
		}
		if err := queries.InsertEvidenceAssetAudit(ctx, sqlgen.InsertEvidenceAssetAuditParams{
			TenantID: record.TenantID.String(), EvidenceID: record.ID.String(),
			AggregateVersion: record.Version, Action: "create", OccurredAt: timestamp(record.CreatedAt),
		}); err != nil {
			return fmt.Errorf("insert evidence audit: %w", err)
		}

		return nil
	})
}

// Find loads one evidence asset only within its explicit tenant scope.
func (store *Store) Find(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Evidence,
) (evidence.Asset, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return evidence.Asset{}, evidence.ErrNotFound
	}

	var asset evidence.Asset
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set tenant scope: %w", err)
		}
		row, err := queries.FindEvidenceAsset(ctx, sqlgen.FindEvidenceAssetParams{
			TenantID: scope.ID().String(), ID: identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find evidence asset: %w", err)
		}
		asset, err = store.restore(row)

		return err
	})

	return asset, err
}

// UpdateLifecycle persists a domain-authorised quarantine transition using
// optimistic concurrency and appends its audit record in the same transaction.
func (store *Store) UpdateLifecycle(
	ctx context.Context,
	scope tenant.Scope,
	asset evidence.Asset,
	expectedVersion int64,
) error {
	record := asset.Record()
	if scope.ID().IsZero() || record.TenantID != scope.ID() {
		return evidence.ErrNotFound
	}
	if expectedVersion < 1 || record.Version != expectedVersion+1 ||
		record.State != evidence.StateQuarantined || record.QuarantinedAt == nil {
		return evidence.ErrConflict
	}
	action := "quarantine"
	if record.Integrity == evidence.IntegrityFailed {
		action = "integrity_failed"
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.TransitionEvidenceAsset(ctx, sqlgen.TransitionEvidenceAssetParams{
			TenantID: record.TenantID.String(), ID: record.ID.String(), Version: expectedVersion,
			Integrity: string(record.Integrity), State: string(record.State), Version_2: record.Version,
			UpdatedAt: timestamp(record.UpdatedAt), QuarantineReason: &record.QuarantineReason,
			QuarantinedAt: nullableTimestamp(record.QuarantinedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("transition evidence asset: %w", err)
		}
		persisted, err := store.restore(row)
		if err != nil {
			return err
		}
		if persisted.Record().Version != record.Version {
			return errors.New("evidence postgres: transition returned an unexpected version")
		}
		if err := queries.InsertEvidenceAssetAudit(ctx, sqlgen.InsertEvidenceAssetAuditParams{
			TenantID: record.TenantID.String(), EvidenceID: record.ID.String(),
			AggregateVersion: record.Version, Action: action, Reason: &record.QuarantineReason,
			OccurredAt: timestamp(record.UpdatedAt),
		}); err != nil {
			return fmt.Errorf("insert evidence audit: %w", err)
		}

		return nil
	})
}

// PersistKeyRewrap atomically replaces the exact prior wrapped key and appends
// both aggregate and attributed key-identity audit records.
func (store *Store) PersistKeyRewrap(
	ctx context.Context,
	scope tenant.Scope,
	change evidence.KeyRewrap,
	attribution evidence.CommandAttribution,
) error {
	record := change.Asset().Record()
	previous := change.PreviousKey().Record()
	target := record.Content.Envelope.WrappedKey
	if scope.ID().IsZero() || record.TenantID != scope.ID() || record.ID.IsZero() {
		return evidence.ErrNotFound
	}
	if record.Version < 2 || !attribution.Valid() || sameWrappedKeyIdentity(previous, target) {
		return evidence.ErrConflict
	}
	expectedVersion := record.Version - 1

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		locked, err := queries.LockEvidenceAssetForKeyRewrap(ctx, sqlgen.LockEvidenceAssetForKeyRewrapParams{
			TenantID: record.TenantID.String(), ID: record.ID.String(), Version: expectedVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("lock evidence asset for key rewrap: %w", err)
		}
		lockedKey := kms.WrappedKeyRecord{
			Provider: locked.KeyProvider, Reference: locked.KeyReference, Version: locked.KeyVersion,
			Algorithm: locked.KeyAlgorithm, Ciphertext: locked.WrappedKey,
		}
		if !sameWrappedKey(previous, lockedKey) {
			return evidence.ErrVersionConflict
		}

		row, err := queries.RewrapEvidenceAssetKey(ctx, sqlgen.RewrapEvidenceAssetKeyParams{
			TenantID: record.TenantID.String(), ID: record.ID.String(), Version: expectedVersion,
			KeyProvider: target.Provider, KeyReference: target.Reference, KeyVersion: target.Version,
			KeyAlgorithm: target.Algorithm, WrappedKey: append([]byte(nil), target.Ciphertext...),
			Version_2: record.Version, UpdatedAt: timestamp(record.UpdatedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("rewrap evidence asset key: %w", err)
		}
		persisted, err := store.restore(row)
		if err != nil {
			return err
		}
		persistedKey := persisted.Record().Content.Envelope.WrappedKey
		if persisted.Version() != record.Version || !sameWrappedKey(target, persistedKey) {
			return errors.New("evidence postgres: key rewrap returned unexpected state")
		}

		reason := "evidence.key.rewrap"
		if err := queries.InsertEvidenceAssetAudit(ctx, sqlgen.InsertEvidenceAssetAuditParams{
			TenantID: record.TenantID.String(), EvidenceID: record.ID.String(),
			AggregateVersion: record.Version, Action: "rewrap", Reason: &reason,
			OccurredAt: timestamp(record.UpdatedAt),
		}); err != nil {
			return fmt.Errorf("insert evidence rewrap aggregate audit: %w", err)
		}
		if err := queries.InsertEvidenceKeyRewrapAudit(ctx, sqlgen.InsertEvidenceKeyRewrapAuditParams{
			TenantID: record.TenantID.String(), EvidenceID: record.ID.String(),
			AggregateVersion:    record.Version,
			PreviousKeyProvider: previous.Provider, PreviousKeyReference: previous.Reference,
			PreviousKeyVersion: previous.Version, PreviousKeyAlgorithm: previous.Algorithm,
			NewKeyProvider: target.Provider, NewKeyReference: target.Reference,
			NewKeyVersion: target.Version, NewKeyAlgorithm: target.Algorithm,
			PrincipalType: attribution.Principal.Type, PrincipalID: attribution.Principal.ID,
			TenantActorType: attribution.TenantActor.Type, TenantActorID: attribution.TenantActor.ID,
			Reason: attribution.Reason, OccurredAt: timestamp(record.UpdatedAt),
		}); err != nil {
			return fmt.Errorf("insert evidence key rewrap audit: %w", err)
		}

		return nil
	})
}

func sameWrappedKeyIdentity(first, second kms.WrappedKeyRecord) bool {
	return first.Provider == second.Provider && first.Reference == second.Reference &&
		first.Version == second.Version && first.Algorithm == second.Algorithm
}

func sameWrappedKey(first, second kms.WrappedKeyRecord) bool {
	return sameWrappedKeyIdentity(first, second) && bytes.Equal(first.Ciphertext, second.Ciphertext)
}

func (store *Store) write(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.writeTx(ctx, scope, func(ctx context.Context, _ platformpostgres.Transaction, queries *sqlgen.Queries) error {
		return work(ctx, queries)
	})
}

func (store *Store) writeTx(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, platformpostgres.Transaction, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set tenant scope: %w", err)
		}

		return work(ctx, tx, queries)
	})
}

func createParameters(record evidence.Record) (sqlgen.CreateEvidenceAssetParams, error) {
	if record.Registry.SchemaVersion > math.MaxInt32 || record.Registry.Revision > math.MaxInt32 ||
		record.ContentRevision > math.MaxInt32 ||
		record.Content.Envelope.FormatVersion > math.MaxInt32 ||
		record.Content.Envelope.ContextSchemaVersion > math.MaxInt32 {
		return sqlgen.CreateEvidenceAssetParams{}, errors.New("evidence postgres: version exceeds database range")
	}
	content := record.Content
	object := content.Object
	envelope := content.Envelope
	wrappedKey := envelope.WrappedKey
	assurances := make([]string, len(record.Assurances))
	for index, assurance := range record.Assurances {
		assurances[index] = string(assurance)
	}

	return sqlgen.CreateEvidenceAssetParams{
		ID: record.ID.String(), TenantID: record.TenantID.String(), SubjectID: record.SubjectID.String(),
		VerificationID: record.VerificationID.String(), RequirementKey: record.RequirementKey,
		EvidenceType: string(record.EvidenceType), Artefact: string(record.Artefact),
		AcquisitionMethod: string(record.AcquisitionMethod), Assurances: assurances,
		RegistrySchemaVersion: int32(record.Registry.SchemaVersion), RegistryRevision: int32(record.Registry.Revision),
		RegistryDigest: record.Registry.Digest, Region: record.Region, RetentionClass: record.RetentionClass,
		ContentRevision: int32(record.ContentRevision), ObjectKey: object.Key, ObjectVersion: object.Version,
		CiphertextSize: object.Size, CiphertextChecksum: object.Checksum,
		EnvelopeFormatVersion: int32(envelope.FormatVersion), //nolint:gosec // bounded by MaxInt32 above
		ContentAlgorithm:      envelope.ContentAlgorithm,
		KeyPurpose:            envelope.Purpose, KeyProvider: wrappedKey.Provider, KeyReference: wrappedKey.Reference,
		KeyVersion: wrappedKey.Version, KeyAlgorithm: wrappedKey.Algorithm,
		WrappedKey:           append([]byte(nil), wrappedKey.Ciphertext...),
		ContextSchemaVersion: int32(envelope.ContextSchemaVersion), //nolint:gosec // bounded by MaxInt32 above
		ContextDigest:        envelope.ContextDigest,
		PlaintextDigest:      content.PlaintextDigest, MediaType: content.MediaType,
		Integrity: string(record.Integrity), State: string(record.State), Version: record.Version,
		CreatedAt: timestamp(record.CreatedAt), UpdatedAt: timestamp(record.UpdatedAt),
		QuarantineReason: nullableString(record.QuarantineReason), QuarantinedAt: nullableTimestamp(record.QuarantinedAt),
	}, nil
}

func (store *Store) restore(row sqlgen.IdenqaEvidenceAsset) (evidence.Asset, error) {
	if row.RegistrySchemaVersion <= 0 || row.RegistryRevision <= 0 || row.ContentRevision <= 0 ||
		row.EnvelopeFormatVersion <= 0 || row.ContextSchemaVersion <= 0 {
		return evidence.Asset{}, errors.New("evidence postgres: invalid durable version")
	}
	evidenceID, err := id.ParseEvidence(row.ID)
	if err != nil {
		return evidence.Asset{}, fmt.Errorf("restore evidence id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return evidence.Asset{}, fmt.Errorf("restore evidence tenant: %w", err)
	}
	subjectID, err := id.ParseSubject(row.SubjectID)
	if err != nil {
		return evidence.Asset{}, fmt.Errorf("restore evidence subject: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return evidence.Asset{}, fmt.Errorf("restore evidence verification: %w", err)
	}
	reference := evidence.Reference{
		SchemaVersion: uint32(row.RegistrySchemaVersion), Revision: uint32(row.RegistryRevision),
		Digest: row.RegistryDigest,
	}
	registry, err := store.catalog.Resolve(reference)
	if err != nil {
		return evidence.Asset{}, fmt.Errorf("restore evidence registry: %w", err)
	}
	assurances := make([]evidence.Name, len(row.Assurances))
	for index, assurance := range row.Assurances {
		assurances[index] = evidence.Name(assurance)
	}
	quarantinedAt := timestampPointer(row.QuarantinedAt)

	return evidence.RestoreAsset(evidence.Record{
		ID: evidenceID, TenantID: tenantID, SubjectID: subjectID, VerificationID: verificationID,
		RequirementKey: row.RequirementKey, EvidenceType: evidence.Name(row.EvidenceType),
		Artefact: evidence.Name(row.Artefact), AcquisitionMethod: evidence.Name(row.AcquisitionMethod),
		Assurances: assurances, Registry: reference, Region: row.Region, RetentionClass: row.RetentionClass,
		ContentRevision: uint32(row.ContentRevision),
		Content: evidence.ContentRecord{
			Object: objectstore.ObjectRecord{Key: row.ObjectKey, Version: row.ObjectVersion, Size: row.CiphertextSize, Checksum: row.CiphertextChecksum},
			Envelope: platformcrypto.EnvelopeRecord{
				FormatVersion: uint32(row.EnvelopeFormatVersion), ContentAlgorithm: row.ContentAlgorithm,
				Purpose: row.KeyPurpose,
				WrappedKey: kms.WrappedKeyRecord{Provider: row.KeyProvider, Reference: row.KeyReference,
					Version: row.KeyVersion, Algorithm: row.KeyAlgorithm, Ciphertext: append([]byte(nil), row.WrappedKey...)},
				ContextSchemaVersion: uint32(row.ContextSchemaVersion), ContextDigest: row.ContextDigest,
			},
			PlaintextDigest: row.PlaintextDigest, MediaType: row.MediaType,
		},
		Integrity: evidence.Integrity(row.Integrity), State: evidence.State(row.State), Version: row.Version,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		QuarantineReason: valueOrEmpty(row.QuarantineReason), QuarantinedAt: quarantinedAt,
	}, registry)
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func nullableTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}

	return timestamp(*value)
}

func timestampPointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()

	return &result
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
