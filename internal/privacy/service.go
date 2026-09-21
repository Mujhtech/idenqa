package privacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Permission is an application authority for consequential lifecycle work.
type Permission string

const (
	// PermissionRequestDeletion permits creation of an exact deletion workflow.
	PermissionRequestDeletion Permission = "deletions:request"
	// PermissionRunDeletion permits deletion execution and retry.
	PermissionRunDeletion Permission = "deletions:run"
	// PermissionReadDeletion permits tenant-scoped deletion and retention inspection.
	PermissionReadDeletion Permission = "deletions:read"
	// PermissionManageHold permits legal-hold creation and release.
	PermissionManageHold Permission = "legal_holds:manage"
)

// Actor contains authenticated application authority, never transport state.
type Actor struct {
	ID          string
	Permissions []Permission
}

func (actor Actor) permits(required Permission) bool {
	if actor.ID == "" {
		return false
	}
	for _, permission := range actor.Permissions {
		if permission == required {
			return true
		}
	}
	return false
}

// Repository persists each transition and its reference-only audit event in
// one tenant-scoped transaction. Save uses optimistic expectedVersion.
type Repository interface {
	Create(context.Context, tenant.Scope, Actor, Deletion) error
	Find(context.Context, tenant.Scope, id.Deletion) (Deletion, error)
	Save(context.Context, tenant.Scope, Actor, Deletion, int64) error
	ActiveHolds(context.Context, tenant.Scope, string, time.Time) ([]Hold, error)
	Complete(context.Context, tenant.Scope, Actor, Deletion, Tombstone, int64) error
}

// SchedulerRepository discovers bounded due work without weakening the core
// transition repository used by request paths.
type SchedulerRepository interface {
	Due(context.Context, tenant.Scope, time.Time, int) ([]id.Deletion, error)
}

// ReadRepository supplies bounded tenant-scoped lifecycle inspection reads.
// Deletions are ascending by identifier; a non-empty aggregate filter and
// position are exact.
type ReadRepository interface {
	ListDeletions(context.Context, tenant.Scope, string, string, int) ([]Deletion, error)
	RetainedRecords(context.Context, tenant.Scope, string) ([]RetentionRecord, error)
}

// HoldRepository owns legal-hold administration transitions.
type HoldRepository interface {
	CreateHold(context.Context, tenant.Scope, Actor, Hold, time.Time) error
	ReleaseHold(context.Context, tenant.Scope, Actor, id.LegalHold, time.Time) (Hold, error)
}

// TombstoneRepository supplies restore-time replay protection.
type TombstoneRepository interface {
	Tombstoned(context.Context, tenant.Scope, int) ([]Deletion, error)
	RecordTombstoneReplay(context.Context, tenant.Scope, Actor, Deletion, time.Time) error
}

// EvidenceTargetPlanner derives exact content targets from authoritative state.
type EvidenceTargetPlanner interface {
	EvidenceTargets(context.Context, tenant.Scope, string, string) ([]Target, error)
}

// TargetEraser removes or irreversibly crypto-shreds one exact regional copy.
type TargetEraser interface {
	Delete(context.Context, Target) error
}

// IdentifierGenerator supplies lifecycle identifiers without exposing ULID.
type IdentifierGenerator interface {
	NewDeletion() (id.Deletion, error)
}

// HoldIdentifierGenerator supplies opaque legal-hold identifiers.
type HoldIdentifierGenerator interface{ NewLegalHold() (id.LegalHold, error) }

// Tombstone is reference-only restore protection retained for seven years.
type Tombstone struct {
	DeletionID  id.Deletion
	AggregateID string
	Region      string
	TargetCount int
	ProofDigest string
	CompletedAt time.Time
	RetainUntil time.Time
}

// Service owns authorised observable deletion execution.
type Service struct {
	repository  Repository
	eraser      TargetEraser
	identifiers IdentifierGenerator
	now         func() time.Time
	metrics     Metrics
}

// NewService constructs the lifecycle application service.
func NewService(repository Repository, eraser TargetEraser, identifiers IdentifierGenerator, now func() time.Time) (*Service, error) {
	if repository == nil || eraser == nil || identifiers == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository, eraser: eraser, identifiers: identifiers, now: now}, nil
}

// WithMetrics attaches the bounded privacy metric receiver.
func (service *Service) WithMetrics(metrics Metrics) *Service {
	if service != nil && metrics != nil {
		service.metrics = metrics
	}
	return service
}

// RequestDeletion creates one exact, region-pinned workflow.
func (service *Service) RequestDeletion(ctx context.Context, scope tenant.Scope, actor Actor, aggregateID, region string, targets []Target, backupExpiresAt time.Time) (Deletion, error) {
	return service.requestDeletionAt(ctx, scope, actor, aggregateID, region, targets, service.now().UTC(), backupExpiresAt)
}

// RequestEvidenceDeletion plans exact raw and derived targets and applies the
// selected 35-day backup boundary; callers cannot inject object references.
func (service *Service) RequestEvidenceDeletion(ctx context.Context, scope tenant.Scope, actor Actor, aggregateID, region string) (Deletion, error) {
	planner, ok := service.repository.(EvidenceTargetPlanner)
	if !ok {
		return Deletion{}, ErrInvalid
	}
	targets, err := planner.EvidenceTargets(ctx, scope, aggregateID, region)
	if err != nil {
		return Deletion{}, err
	}
	now := service.now().UTC()
	return service.requestDeletionAt(ctx, scope, actor, aggregateID, region, targets, now, now.Add(SelectedDefaults()[DataClassBackup]))
}

func (service *Service) requestDeletionAt(ctx context.Context, scope tenant.Scope, actor Actor, aggregateID, region string, targets []Target, requestedAt, backupExpiresAt time.Time) (Deletion, error) {
	if !actor.permits(PermissionRequestDeletion) {
		return Deletion{}, ErrConflict
	}
	identifier, err := service.identifiers.NewDeletion()
	if err != nil {
		return Deletion{}, fmt.Errorf("generate deletion identifier: %w", err)
	}
	deletion, err := NewDeletion(identifier, aggregateID, region, targets, requestedAt, backupExpiresAt)
	if err != nil {
		return Deletion{}, err
	}
	if err := service.repository.Create(ctx, scope, actor, deletion); err != nil {
		return Deletion{}, fmt.Errorf("persist deletion request: %w", err)
	}
	return deletion, nil
}

// Run advances one workflow until a failure, legal hold, backup boundary, or completion.
func (service *Service) Run(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.Deletion) (result Deletion, err error) {
	if !actor.permits(PermissionRunDeletion) {
		return Deletion{}, ErrConflict
	}
	deletion, err := service.repository.Find(ctx, scope, identifier)
	if err != nil {
		return Deletion{}, err
	}
	from := deletion.State
	defer func() { service.observeDeletion(from, result) }()
	now := service.now().UTC()
	if deletion.State == DeletionCompleted {
		return deletion, nil
	}
	if deletion.State == DeletionAwaitingBackup {
		if now.Before(deletion.BackupExpiresAt) {
			return deletion, nil
		}
		expected := deletion.Version
		deletion, err = deletion.Complete(now)
		if err != nil {
			return Deletion{}, err
		}
		if err := service.repository.Complete(ctx, scope, actor, deletion, tombstoneFor(deletion), expected); err != nil {
			return Deletion{}, err
		}
		return deletion, nil
	}
	holds, err := service.repository.ActiveHolds(ctx, scope, deletion.AggregateID, now)
	if err != nil {
		return Deletion{}, err
	}
	expected := deletion.Version
	deletion, beginErr := deletion.Begin(now, holds)
	if err := service.repository.Save(ctx, scope, actor, deletion, expected); err != nil {
		return Deletion{}, err
	}
	if errors.Is(beginErr, ErrHeld) {
		return deletion, ErrHeld
	}
	for _, target := range deletion.Targets {
		if !target.DeletedAt.IsZero() {
			continue
		}
		expected = deletion.Version
		if err := service.eraser.Delete(ctx, target); err != nil {
			if errors.Is(err, ErrHeld) {
				paused, pauseErr := deletion.SuspendForHold(service.now().UTC())
				if pauseErr != nil {
					return deletion, pauseErr
				}
				if saveErr := service.repository.Save(ctx, scope, actor, paused, expected); saveErr != nil {
					return deletion, saveErr
				}
				return paused, ErrHeld
			}

			deletion, _ = deletion.MarkTargetFailed(target.Kind, target.Reference, classifyFailure(err), service.now().UTC())
			if saveErr := service.repository.Save(ctx, scope, actor, deletion, expected); saveErr != nil {
				return Deletion{}, errors.Join(err, saveErr)
			}
			return deletion, err
		}
		deletion, err = deletion.MarkTargetDeleted(target.Kind, target.Reference, service.now().UTC())
		if err != nil {
			return Deletion{}, err
		}
		if err := service.repository.Save(ctx, scope, actor, deletion, expected); err != nil {
			return Deletion{}, err
		}
	}
	if service.now().UTC().Before(deletion.BackupExpiresAt) {
		return deletion, nil
	}
	expected = deletion.Version
	deletion, err = deletion.Complete(service.now().UTC())
	if err != nil {
		return Deletion{}, err
	}
	tombstone := tombstoneFor(deletion)
	if err := service.repository.Complete(ctx, scope, actor, deletion, tombstone, expected); err != nil {
		return Deletion{}, err
	}
	return deletion, nil
}

// observeDeletion records the bounded state change and backup-expiry age of one
// completed Run pass. It never labels tenant, aggregate, or deletion identity.
func (service *Service) observeDeletion(from DeletionState, next Deletion) {
	if service.metrics == nil || next.ID.IsZero() {
		return
	}
	region := observability.Region(next.Region)
	kind := deletionClass(next.Targets)
	if from != next.State {
		service.metrics.RecordDeletionTransition(observability.DeletionTransition{
			From:   deletionState(from),
			To:     deletionState(next.State),
			Kind:   kind,
			Region: region,
		})
	}
	if next.State == DeletionAwaitingBackup {
		if age := next.BackupExpiresAt.Sub(service.now().UTC()); age > 0 {
			service.metrics.RecordBackupExpiry(observability.BackupExpiry{Age: age, Region: region})
		}
	}
}

// RunDue advances a bounded batch of expired or retryable workflows. Each
// workflow remains independently transactional so one failure cannot hide the
// remaining identifiers from a later scheduler pass.
func (service *Service) RunDue(ctx context.Context, scope tenant.Scope, actor Actor, limit int) ([]Deletion, error) {
	if !actor.permits(PermissionRunDeletion) || limit < 1 || limit > 1000 {
		return nil, ErrConflict
	}
	repository, ok := service.repository.(SchedulerRepository)
	if !ok {
		return nil, ErrInvalid
	}
	identifiers, err := repository.Due(ctx, scope, service.now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	results := make([]Deletion, 0, len(identifiers))
	var failures error
	for _, identifier := range identifiers {
		deletion, runErr := service.Run(ctx, scope, actor, identifier)
		if runErr != nil && !errors.Is(runErr, ErrHeld) {
			failures = errors.Join(failures, fmt.Errorf("run deletion %s: %w", identifier.String(), runErr))
		}
		if !deletion.ID.IsZero() {
			results = append(results, deletion)
		}
	}
	service.observeBacklog(results)
	return results, failures
}

// observeBacklog records the bounded due-work sample grouped by resulting
// state. It is a lower bound when the caller's limit truncates the queue.
func (service *Service) observeBacklog(results []Deletion) {
	if service.metrics == nil || len(results) == 0 {
		return
	}
	counts := make(map[DeletionState]int64, len(results))
	for _, value := range results {
		if value.ID.IsZero() {
			continue
		}
		counts[value.State]++
	}
	for state, count := range counts {
		service.metrics.RecordDeletionBacklog(observability.DeletionBacklog{
			State: deletionState(state),
			Count: count,
		})
	}
}

// FindDeletion loads one tenant-scoped deletion workflow.
func (service *Service) FindDeletion(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.Deletion) (Deletion, error) {
	if !actor.permits(PermissionReadDeletion) || identifier.IsZero() {
		return Deletion{}, ErrConflict
	}
	return service.repository.Find(ctx, scope, identifier)
}

// DeletionStatus projects one deletion with exact target states and active holds.
func (service *Service) DeletionStatus(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.Deletion) (DeletionStatus, error) {
	if !actor.permits(PermissionReadDeletion) || identifier.IsZero() {
		return DeletionStatus{}, ErrConflict
	}
	deletion, err := service.repository.Find(ctx, scope, identifier)
	if err != nil {
		return DeletionStatus{}, err
	}
	holds, err := service.repository.ActiveHolds(ctx, scope, deletion.AggregateID, service.now().UTC())
	if err != nil {
		return DeletionStatus{}, err
	}
	return deletion.Status(holds), nil
}

// ListDeletions returns one bounded ascending page, optionally filtered by
// exact aggregate. A non-empty position is the previous page's last identifier.
func (service *Service) ListDeletions(ctx context.Context, scope tenant.Scope, actor Actor, aggregateID, position string, limit int) (DeletionPage, error) {
	if !actor.permits(PermissionReadDeletion) || limit < 1 || limit > 100 ||
		(aggregateID != "" && !token(aggregateID, 200)) || (position != "" && !token(position, 64)) {
		return DeletionPage{}, ErrConflict
	}
	repository, ok := service.repository.(ReadRepository)
	if !ok {
		return DeletionPage{}, ErrInvalid
	}
	deletions, err := repository.ListDeletions(ctx, scope, aggregateID, position, limit+1)
	if err != nil {
		return DeletionPage{}, err
	}
	page := DeletionPage{HasMore: len(deletions) > limit}
	if page.HasMore {
		deletions = deletions[:limit]
	}
	if deletions == nil {
		deletions = []Deletion{}
	}
	return DeletionPage{Deletions: deletions, HasMore: page.HasMore}, nil
}

// ResolveRetention recomputes typed retention meaning read-only for each
// retained evidence object of one aggregate and includes active holds. It
// never trusts denormalised deadlines over privacy.Resolve.
func (service *Service) ResolveRetention(ctx context.Context, scope tenant.Scope, actor Actor, aggregateID string) (RetentionResolution, error) {
	if !actor.permits(PermissionReadDeletion) || !token(aggregateID, 200) {
		return RetentionResolution{}, ErrConflict
	}
	repository, ok := service.repository.(ReadRepository)
	if !ok {
		return RetentionResolution{}, ErrInvalid
	}
	records, err := repository.RetainedRecords(ctx, scope, aggregateID)
	if err != nil {
		return RetentionResolution{}, err
	}
	holds, err := service.repository.ActiveHolds(ctx, scope, aggregateID, service.now().UTC())
	if err != nil {
		return RetentionResolution{}, err
	}
	resolution := RetentionResolution{AggregateID: aggregateID, Records: make([]RetentionDeadline, 0, len(records)), Holds: append([]Hold(nil), holds...)}
	for _, record := range records {
		resolved, resolveErr := Resolve(record.Class, record.Region, record.CreatedAt.UTC(), record.Requested, 0, nil)
		if resolveErr != nil {
			// A pinned binding that cannot be reproduced is a state conflict,
			// never a missing resource or a silently different deadline.
			return RetentionResolution{}, fmt.Errorf("%w: retention resolution for %s", ErrConflict, record.ID)
		}
		resolution.Records = append(resolution.Records, RetentionDeadline{ID: record.ID, Class: resolved.Class, Region: resolved.Region, Duration: resolved.Duration, ExpiresAt: resolved.ExpiresAt})
	}
	return resolution, nil
}

// CreateHold creates an auditable hold without granting evidence access.
func (service *Service) CreateHold(ctx context.Context, scope tenant.Scope, actor Actor, aggregateID, authority, reason string, startsAt, reviewAt time.Time) (Hold, error) {
	identifiers, ok := service.identifiers.(HoldIdentifierGenerator)
	if !actor.permits(PermissionManageHold) || !ok {
		return Hold{}, ErrConflict
	}
	identifier, err := identifiers.NewLegalHold()
	if err != nil {
		return Hold{}, fmt.Errorf("generate legal hold identifier: %w", err)
	}
	hold := Hold{ID: identifier, AggregateID: aggregateID, Authority: authority, Reason: reason, StartsAt: startsAt.UTC(), ReviewAt: reviewAt.UTC()}
	if err := hold.Validate(); err != nil {
		return Hold{}, err
	}
	repository, ok := service.repository.(HoldRepository)
	if !ok {
		return Hold{}, ErrInvalid
	}
	if err := repository.CreateHold(ctx, scope, actor, hold, service.now().UTC()); err != nil {
		return Hold{}, fmt.Errorf("persist legal hold: %w", err)
	}
	return hold, nil
}

// ReleaseHold ends one exact hold; it does not delete or reveal held data.
func (service *Service) ReleaseHold(ctx context.Context, scope tenant.Scope, actor Actor, identifier id.LegalHold) (Hold, error) {
	if !actor.permits(PermissionManageHold) || identifier.IsZero() {
		return Hold{}, ErrConflict
	}
	repository, ok := service.repository.(HoldRepository)
	if !ok {
		return Hold{}, ErrInvalid
	}
	return repository.ReleaseHold(ctx, scope, actor, identifier, service.now().UTC())
}

// ReplayTombstones reapplies every exact deletion target after a restore. The
// eraser must be idempotent; evidence access remains disabled until this pass
// succeeds and its audit event commits.
func (service *Service) ReplayTombstones(ctx context.Context, scope tenant.Scope, actor Actor, limit int) (int, error) {
	if !actor.permits(PermissionRunDeletion) || limit < 1 || limit > 1000 {
		return 0, ErrConflict
	}
	repository, ok := service.repository.(TombstoneRepository)
	if !ok {
		return 0, ErrInvalid
	}
	deletions, err := repository.Tombstoned(ctx, scope, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, deletion := range deletions {
		for _, target := range deletion.Targets {
			if err := service.eraser.Delete(ctx, target); err != nil {

				return completed, fmt.Errorf("replay deletion target: %w", err)
			}
		}
		if err := repository.RecordTombstoneReplay(ctx, scope, actor, deletion, service.now().UTC()); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

func tombstoneFor(deletion Deletion) Tombstone {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "idenqa.deletion-proof.v1\n%s\n%s\n%s\n%d\n", deletion.ID.String(), deletion.AggregateID, deletion.Region, len(deletion.Targets))
	for _, target := range deletion.Targets {
		_, _ = fmt.Fprintf(hash, "%s\n%s\n%s\n", target.Kind, target.Reference, target.DeletedAt.Format(time.RFC3339Nano))
	}
	return Tombstone{DeletionID: deletion.ID, AggregateID: deletion.AggregateID, Region: deletion.Region, TargetCount: len(deletion.Targets), ProofDigest: hex.EncodeToString(hash.Sum(nil)), CompletedAt: deletion.UpdatedAt, RetainUntil: deletion.UpdatedAt.Add(7 * 365 * 24 * time.Hour)}
}

func classifyFailure(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "target_unavailable"
}
