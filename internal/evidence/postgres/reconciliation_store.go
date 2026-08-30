package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

var (
	_ evidence.ObjectReconciliationRecorder = (*Store)(nil)
	_ evidence.ObjectReconciliationQueue    = (*Store)(nil)
	_ evidence.ObjectReferenceChecker       = (*Store)(nil)
)

// IsObjectReferenced reports whether authoritative state still protects an
// exact physical object from provider-inventory deletion.
func (store *Store) IsObjectReferenced(
	ctx context.Context,
	scope tenant.Scope,
	key objectstore.Key,
	version string,
) (bool, error) {
	if scope.ID().IsZero() || key == "" || version == "" || len(version) > 200 ||
		strings.TrimSpace(version) != version {
		return false, evidence.ErrReconciliationConflict
	}
	var referenced bool
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set tenant scope: %w", err)
			}
			var err error
			referenced, err = queries.IsEvidenceObjectReferenced(
				ctx,
				sqlgen.IsEvidenceObjectReferencedParams{
					TenantID: scope.ID().String(), ObjectKey: string(key), ObjectVersion: version,
				},
			)
			if err != nil {
				return fmt.Errorf("check evidence object reference: %w", err)
			}
			return nil
		},
	)

	return referenced, err
}

// CreateObjectReconciliation durably records the exact object before any
// acceptance transaction can make its outcome ambiguous. Exact replay is safe.
func (store *Store) CreateObjectReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	upload evidence.Upload,
	prepared evidence.PreparedEvidence,
	createdAt time.Time,
) error {
	obligation, err := evidence.NewObjectReconciliation(upload, prepared, createdAt)
	if err != nil {
		return err
	}
	return store.createObjectReconciliation(ctx, scope, obligation)
}

// CreateObjectReconciliationForObject records an exact orphan returned by a
// failed staging operation before complete protected metadata was available.
func (store *Store) CreateObjectReconciliationForObject(
	ctx context.Context,
	scope tenant.Scope,
	upload evidence.Upload,
	object objectstore.Object,
	createdAt time.Time,
) error {
	obligation, err := evidence.NewObjectReconciliationForObject(upload, object, createdAt)
	if err != nil {
		return err
	}
	return store.createObjectReconciliation(ctx, scope, obligation)
}

func (store *Store) createObjectReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	obligation evidence.ObjectReconciliation,
) error {
	record := obligation.Record()
	if scope.ID().IsZero() || record.TenantID != scope.ID() ||
		record.State != evidence.ReconciliationPending || record.Version != 1 || record.Claim != 0 {
		return evidence.ErrReconciliationConflict
	}
	parameters, err := createReconciliationParameters(record)
	if err != nil {
		return err
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		inserted, err := queries.CreateEvidenceObjectReconciliation(ctx, parameters)
		if err != nil {
			return fmt.Errorf("insert evidence object reconciliation: %w", err)
		}
		row, err := queries.FindEvidenceObjectReconciliation(ctx, sqlgen.FindEvidenceObjectReconciliationParams{
			TenantID: record.TenantID.String(), UploadID: record.UploadID.String(),
			UploadAttempt: databaseInt32(record.UploadAttempt),
		})
		if err != nil {
			return fmt.Errorf("read evidence object reconciliation replay: %w", err)
		}
		persisted, err := restoreReconciliation(row)
		if err != nil {
			return err
		}
		if persisted.Record() != record {
			return evidence.ErrReconciliationConflict
		}
		if inserted == 1 {
			err = queries.InsertEvidenceObjectReconciliationAudit(ctx, sqlgen.InsertEvidenceObjectReconciliationAuditParams{
				TenantID: record.TenantID.String(), UploadID: record.UploadID.String(),
				UploadAttempt: databaseInt32(record.UploadAttempt), AggregateVersion: 1,
				Claim: 0, Action: "create", OccurredAt: timestamp(record.CreatedAt),
			})
			if err != nil {
				return fmt.Errorf("insert evidence object reconciliation audit: %w", err)
			}
		}

		return nil
	})
}

// ResolveObjectReconciliation records synchronous exact-object compensation.
func (store *Store) ResolveObjectReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	uploadID id.Upload,
	attempt uint32,
	object objectstore.Object,
	state evidence.ReconciliationState,
	now time.Time,
) error {
	if scope.ID().IsZero() || uploadID.IsZero() || attempt == 0 || object.IsZero() ||
		(state != evidence.ReconciliationRetained && state != evidence.ReconciliationDeleted) {
		return evidence.ErrReconciliationConflict
	}

	return store.transitionReconciliation(ctx, scope, uploadID, attempt, func(current evidence.ObjectReconciliation) (evidence.ObjectReconciliation, string, error) {
		if current.Object() != object {
			return evidence.ObjectReconciliation{}, "", evidence.ErrReconciliationConflict
		}
		if current.Record().State == state {
			return current, "", nil
		}
		transitioned, err := current.Resolve(state, now)
		return transitioned, reconciliationAction(state), err
	})
}

// ClaimObjectReconciliation fences one tenant-scoped ready obligation.
func (store *Store) ClaimObjectReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	now time.Time,
	timeout time.Duration,
) (evidence.ObjectReconciliation, error) {
	if scope.ID().IsZero() || now.IsZero() {
		return evidence.ObjectReconciliation{}, evidence.ErrNoReconciliationReady
	}
	var claimed evidence.ObjectReconciliation
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.LockNextEvidenceObjectReconciliation(ctx, sqlgen.LockNextEvidenceObjectReconciliationParams{
			TenantID: scope.ID().String(), AvailableAt: timestamp(now),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrNoReconciliationReady
		}
		if err != nil {
			return fmt.Errorf("lock next evidence object reconciliation: %w", err)
		}
		current, err := restoreReconciliation(row)
		if err != nil {
			return err
		}
		claimed, err = current.ClaimForRecovery(now, timeout)
		if err != nil {
			return err
		}
		return persistReconciliationTransition(ctx, queries, current, claimed, "claim")
	})

	return claimed, err
}

// RetryObjectReconciliation releases the exact fenced claim after a recoverable failure.
func (store *Store) RetryObjectReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	claimed evidence.ObjectReconciliation,
	now time.Time,
	delay time.Duration,
) error {
	record := claimed.Record()
	return store.transitionReconciliation(ctx, scope, record.UploadID, record.UploadAttempt, func(current evidence.ObjectReconciliation) (evidence.ObjectReconciliation, string, error) {
		if current.Record().Version != record.Version || current.Record().Claim != record.Claim {
			return evidence.ObjectReconciliation{}, "", evidence.ErrReconciliationConflict
		}
		transitioned, err := current.Retry(now, delay)
		return transitioned, "retry", err
	})
}

// CompleteObjectReconciliation records the exact fenced recovery disposition.
func (store *Store) CompleteObjectReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	claimed evidence.ObjectReconciliation,
	state evidence.ReconciliationState,
	now time.Time,
) error {
	record := claimed.Record()
	return store.transitionReconciliation(ctx, scope, record.UploadID, record.UploadAttempt, func(current evidence.ObjectReconciliation) (evidence.ObjectReconciliation, string, error) {
		if current.Record().Version != record.Version || current.Record().Claim != record.Claim {
			return evidence.ObjectReconciliation{}, "", evidence.ErrReconciliationConflict
		}
		transitioned, err := current.Resolve(state, now)
		return transitioned, reconciliationAction(state), err
	})
}

type reconciliationTransition func(evidence.ObjectReconciliation) (evidence.ObjectReconciliation, string, error)

func (store *Store) transitionReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	uploadID id.Upload,
	attempt uint32,
	transition reconciliationTransition,
) error {
	if attempt > math.MaxInt32 {
		return evidence.ErrReconciliationConflict
	}
	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.LockEvidenceObjectReconciliation(ctx, sqlgen.LockEvidenceObjectReconciliationParams{
			TenantID: scope.ID().String(), UploadID: uploadID.String(), UploadAttempt: databaseInt32(attempt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrReconciliationNotFound
		}
		if err != nil {
			return fmt.Errorf("lock evidence object reconciliation: %w", err)
		}
		current, err := restoreReconciliation(row)
		if err != nil {
			return err
		}
		next, action, err := transition(current)
		if err != nil || action == "" {
			return err
		}

		return persistReconciliationTransition(ctx, queries, current, next, action)
	})
}

func persistReconciliationTransition(
	ctx context.Context,
	queries *sqlgen.Queries,
	current evidence.ObjectReconciliation,
	next evidence.ObjectReconciliation,
	action string,
) error {
	record := next.Record()
	row, err := queries.TransitionEvidenceObjectReconciliation(ctx, sqlgen.TransitionEvidenceObjectReconciliationParams{
		TenantID: record.TenantID.String(), UploadID: record.UploadID.String(),
		UploadAttempt: databaseInt32(record.UploadAttempt), Version: current.Record().Version,
		State: string(record.State), Version_2: record.Version, Claim: databaseInt32(record.Claim),
		AvailableAt: timestamp(record.AvailableAt), LeaseExpiresAt: nullableTimestamp(record.LeaseExpiresAt),
		UpdatedAt: timestamp(record.UpdatedAt), ResolvedAt: nullableTimestamp(record.ResolvedAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence.ErrReconciliationConflict
	}
	if err != nil {
		return fmt.Errorf("transition evidence object reconciliation: %w", err)
	}
	if _, err := restoreReconciliation(row); err != nil {
		return err
	}
	if err := queries.InsertEvidenceObjectReconciliationAudit(ctx, sqlgen.InsertEvidenceObjectReconciliationAuditParams{
		TenantID: record.TenantID.String(), UploadID: record.UploadID.String(),
		UploadAttempt: databaseInt32(record.UploadAttempt), AggregateVersion: record.Version,
		Claim: databaseInt32(record.Claim), Action: action, OccurredAt: timestamp(record.UpdatedAt),
	}); err != nil {
		return fmt.Errorf("insert evidence object reconciliation audit: %w", err)
	}

	return nil
}

func createReconciliationParameters(record evidence.ReconciliationRecord) (sqlgen.CreateEvidenceObjectReconciliationParams, error) {
	if record.UploadAttempt > math.MaxInt32 || record.Claim > math.MaxInt32 {
		return sqlgen.CreateEvidenceObjectReconciliationParams{}, evidence.ErrReconciliationConflict
	}
	return sqlgen.CreateEvidenceObjectReconciliationParams{
		TenantID: record.TenantID.String(), UploadID: record.UploadID.String(),
		EvidenceID: record.EvidenceID.String(), UploadAttempt: databaseInt32(record.UploadAttempt),
		ObjectKey: record.Object.Key, ObjectVersion: record.Object.Version,
		ObjectSize: record.Object.Size, ObjectChecksum: record.Object.Checksum,
		State: string(record.State), Version: record.Version, Claim: databaseInt32(record.Claim),
		AvailableAt: timestamp(record.AvailableAt), LeaseExpiresAt: nullableTimestamp(record.LeaseExpiresAt),
		CreatedAt: timestamp(record.CreatedAt), UpdatedAt: timestamp(record.UpdatedAt),
		ResolvedAt: nullableTimestamp(record.ResolvedAt),
	}, nil
}

func restoreReconciliation(row sqlgen.IdenqaEvidenceObjectReconciliation) (evidence.ObjectReconciliation, error) {
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return evidence.ObjectReconciliation{}, fmt.Errorf("restore reconciliation tenant: %w", err)
	}
	uploadID, err := id.ParseUpload(row.UploadID)
	if err != nil {
		return evidence.ObjectReconciliation{}, fmt.Errorf("restore reconciliation upload: %w", err)
	}
	evidenceID, err := id.ParseEvidence(row.EvidenceID)
	if err != nil {
		return evidence.ObjectReconciliation{}, fmt.Errorf("restore reconciliation evidence: %w", err)
	}
	if row.UploadAttempt <= 0 || row.Claim < 0 {
		return evidence.ObjectReconciliation{}, evidence.ErrReconciliationConflict
	}
	return evidence.RestoreObjectReconciliation(evidence.ReconciliationRecord{
		TenantID: tenantID, UploadID: uploadID, EvidenceID: evidenceID,
		UploadAttempt: uint32(row.UploadAttempt), Object: objectstore.ObjectRecord{
			Key: row.ObjectKey, Version: row.ObjectVersion, Size: row.ObjectSize, Checksum: row.ObjectChecksum,
		},
		State: evidence.ReconciliationState(row.State), Version: row.Version, Claim: uint32(row.Claim),
		AvailableAt: row.AvailableAt.Time, LeaseExpiresAt: timestampPointer(row.LeaseExpiresAt),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		ResolvedAt: timestampPointer(row.ResolvedAt),
	})
}

func reconciliationAction(state evidence.ReconciliationState) string {
	if state == evidence.ReconciliationRetained {
		return "retain"
	}
	return "delete"
}

func databaseInt32(value uint32) int32 {
	return int32(value) //nolint:gosec // domain and store boundaries reject values above MaxInt32.
}
