// Package privacy owns retention, legal-hold, and deletion workflow semantics.
package privacy

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

var (
	// ErrInvalid means lifecycle meaning is incomplete or contradictory.
	ErrInvalid = errors.New("privacy: invalid lifecycle data")
	// ErrHeld means deletion is suspended by a matching legal hold.
	ErrHeld = errors.New("privacy: deletion blocked by legal hold")
	// ErrConflict means a stale transition or changed replay was attempted.
	ErrConflict = errors.New("privacy: lifecycle conflict")
)

// DataClass selects one typed retention baseline.
type DataClass string

const (
	// DataClassRawEvidence is an original collected evidence object.
	DataClassRawEvidence DataClass = "raw_evidence"
	// DataClassDerivedEvidence is a result derived from collected evidence.
	DataClassDerivedEvidence DataClass = "derived_evidence"
	// DataClassWebhookPayload is a retained outbound webhook body.
	DataClassWebhookPayload DataClass = "webhook_payload"
	// DataClassWorkflowMetadata is reference-only orchestration state.
	DataClassWorkflowMetadata DataClass = "workflow_metadata"
	// DataClassAuditRecord is reference-only audit history.
	DataClassAuditRecord DataClass = "audit_record"
	// DataClassDeletionProof is a non-identifying deletion tombstone.
	DataClassDeletionProof DataClass = "deletion_proof"
	// DataClassBackup is an encrypted recovery copy.
	DataClassBackup DataClass = "backup"
)

// SelectedDefaults are the D-013 deployment defaults.
func SelectedDefaults() map[DataClass]time.Duration {
	return map[DataClass]time.Duration{
		DataClassRawEvidence: 30 * 24 * time.Hour, DataClassDerivedEvidence: 30 * 24 * time.Hour,
		DataClassWebhookPayload: 7 * 24 * time.Hour, DataClassWorkflowMetadata: 365 * 24 * time.Hour,
		DataClassAuditRecord: 7 * 365 * 24 * time.Hour, DataClassDeletionProof: 7 * 365 * 24 * time.Hour,
		DataClassBackup: 35 * 24 * time.Hour,
	}
}

// RetentionRule is one versioned authority constraint. Minimum and Maximum
// are obligations, not priorities; contradictory bounds fail closed.
type RetentionRule struct {
	Authority string
	Version   string
	Class     DataClass
	Region    string
	Minimum   time.Duration
	Maximum   time.Duration
}

// Resolution pins exact retention and region meaning for one object.
type Resolution struct {
	Class     DataClass
	Region    string
	Duration  time.Duration
	ExpiresAt time.Time
	Rules     []RetentionRule
}

// Resolve applies the selected default, a tenant request, deployment cap, and
// every applicable obligation. Tenant policy may shorten by default; an
// extension is accepted only within an explicit positive deployment cap.
func Resolve(class DataClass, region string, createdAt time.Time, tenantRequested, deploymentCap time.Duration, rules []RetentionRule) (Resolution, error) {
	defaults := SelectedDefaults()
	selected, exists := defaults[class]
	if !exists || !validRegion(region) || createdAt.IsZero() || createdAt.Location() != time.UTC {
		return Resolution{}, ErrInvalid
	}
	if tenantRequested < 0 || deploymentCap < 0 {
		return Resolution{}, ErrInvalid
	}
	if tenantRequested > 0 {
		if tenantRequested > selected && (deploymentCap == 0 || tenantRequested > deploymentCap) {
			return Resolution{}, ErrInvalid
		}
		selected = tenantRequested
	}
	minimum, maximum := time.Duration(0), time.Duration(0)
	owned := append([]RetentionRule(nil), rules...)
	for _, rule := range owned {
		if rule.Authority == "" || rule.Version == "" || rule.Class != class || rule.Region != region || rule.Minimum < 0 || rule.Maximum < 0 {
			return Resolution{}, ErrInvalid
		}
		minimum = max(minimum, rule.Minimum)
		if rule.Maximum > 0 && (maximum == 0 || rule.Maximum < maximum) {
			maximum = rule.Maximum
		}
	}
	if maximum > 0 && minimum > maximum {
		return Resolution{}, ErrInvalid
	}
	selected = max(selected, minimum)
	if maximum > 0 {
		selected = min(selected, maximum)
	}
	if selected <= 0 {
		return Resolution{}, ErrInvalid
	}
	slices.SortFunc(owned, func(left, right RetentionRule) int {
		if left.Authority < right.Authority {
			return -1
		}
		if left.Authority > right.Authority {
			return 1
		}
		if left.Version < right.Version {
			return -1
		}
		if left.Version > right.Version {
			return 1
		}
		return 0
	})
	return Resolution{Class: class, Region: region, Duration: selected, ExpiresAt: createdAt.Add(selected), Rules: owned}, nil
}

// Hold suspends deletion of one exact aggregate without granting access.
type Hold struct {
	ID          id.LegalHold
	AggregateID string
	Authority   string
	Reason      string
	StartsAt    time.Time
	ReviewAt    time.Time
	ReleasedAt  time.Time
}

// ActiveAt reports whether the hold blocks deletion at a UTC instant.
func (hold Hold) ActiveAt(at time.Time) bool {
	return hold.Validate() == nil && !at.Before(hold.StartsAt) && (hold.ReleasedAt.IsZero() || at.Before(hold.ReleasedAt))
}

// Validate checks bounded, auditable hold meaning.
func (hold Hold) Validate() error {
	if hold.ID.IsZero() || !token(hold.AggregateID, 200) || !token(hold.Authority, 128) || !token(hold.Reason, 256) ||
		hold.StartsAt.IsZero() || hold.StartsAt.Location() != time.UTC || !hold.ReviewAt.After(hold.StartsAt) || hold.ReviewAt.Location() != time.UTC ||
		(!hold.ReleasedAt.IsZero() && (hold.ReleasedAt.Location() != time.UTC || hold.ReleasedAt.Before(hold.StartsAt))) {
		return ErrInvalid
	}
	return nil
}

// DeletionState is the observable workflow state.
type DeletionState string

const (
	// DeletionRequested is a durable workflow awaiting hold evaluation.
	DeletionRequested DeletionState = "requested"
	// DeletionBlockedByLegalHold is suspended without granting access.
	DeletionBlockedByLegalHold DeletionState = "blocked_by_legal_hold"
	// DeletionInProgress is deleting exact active and derived copies.
	DeletionInProgress DeletionState = "in_progress"
	// DeletionAwaitingBackup waits for the documented recovery-copy boundary.
	DeletionAwaitingBackup DeletionState = "awaiting_backup_expiry"
	// DeletionCompleted has an immutable reference-only proof.
	DeletionCompleted DeletionState = "completed"
	// DeletionFailed retains a safe failure class and may be retried.
	DeletionFailed DeletionState = "failed"
)

// Target is one exact active or derived copy that must be removed.
type Target struct {
	Kind             string
	Reference        string
	Region           string
	DeletedAt        time.Time
	Attempts         uint16
	LastFailureClass string
}

// Deletion is one replay-safe observable deletion workflow.
type Deletion struct {
	ID              id.Deletion
	AggregateID     string
	Region          string
	State           DeletionState
	Targets         []Target
	RequestedAt     time.Time
	UpdatedAt       time.Time
	BackupExpiresAt time.Time
	FailureClass    string
	Version         int64
}

// NewDeletion creates a requested deletion with an owned target set.
func NewDeletion(identifier id.Deletion, aggregateID, region string, targets []Target, requestedAt, backupExpiresAt time.Time) (Deletion, error) {
	deletion := Deletion{ID: identifier, AggregateID: aggregateID, Region: region, State: DeletionRequested, Targets: append([]Target(nil), targets...), RequestedAt: requestedAt, UpdatedAt: requestedAt, BackupExpiresAt: backupExpiresAt, Version: 1}
	if deletion.Validate() != nil {
		return Deletion{}, ErrInvalid
	}
	return deletion, nil
}

// Validate checks deletion identity, region, targets, and lifecycle times.
func (deletion Deletion) Validate() error {
	if deletion.ID.IsZero() || !token(deletion.AggregateID, 200) || !validRegion(deletion.Region) || deletion.Version < 1 ||
		deletion.RequestedAt.IsZero() || deletion.RequestedAt.Location() != time.UTC || deletion.UpdatedAt.Before(deletion.RequestedAt) || deletion.UpdatedAt.Location() != time.UTC ||
		!deletion.BackupExpiresAt.After(deletion.RequestedAt) || deletion.BackupExpiresAt.Location() != time.UTC || len(deletion.Targets) == 0 || len(deletion.Targets) > 256 {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(deletion.Targets))
	for _, target := range deletion.Targets {
		key := target.Kind + "\x00" + target.Reference
		if !token(target.Kind, 64) || !token(target.Reference, 2048) || target.Region != deletion.Region {
			return ErrInvalid
		}
		if _, exists := seen[key]; exists {
			return ErrInvalid
		}
		seen[key] = struct{}{}
	}
	return nil
}

// Begin evaluates holds and enters the deletion phase without bypassing region.
func (deletion Deletion) Begin(now time.Time, holds []Hold) (Deletion, error) {
	if deletion.Validate() != nil || now.IsZero() || now.Location() != time.UTC || now.Before(deletion.UpdatedAt) {
		return Deletion{}, ErrInvalid
	}
	if deletion.State != DeletionRequested && deletion.State != DeletionBlockedByLegalHold && deletion.State != DeletionFailed {
		return Deletion{}, ErrConflict
	}
	next := deletion
	next.State, next.FailureClass, next.UpdatedAt, next.Version = DeletionInProgress, "", now, deletion.Version+1
	for _, hold := range holds {
		if hold.AggregateID == deletion.AggregateID && hold.ActiveAt(now) {
			next.State = DeletionBlockedByLegalHold
			return next, ErrHeld
		}
	}
	return next, nil
}

// MarkTargetDeleted records one idempotent exact-copy outcome.
func (deletion Deletion) MarkTargetDeleted(kind, reference string, now time.Time) (Deletion, error) {
	if deletion.Validate() != nil || deletion.State != DeletionInProgress || now.Before(deletion.UpdatedAt) || now.Location() != time.UTC {
		return Deletion{}, ErrConflict
	}
	next := deletion
	next.Targets = append([]Target(nil), deletion.Targets...)
	found := false
	for index := range next.Targets {
		if next.Targets[index].Kind == kind && next.Targets[index].Reference == reference {
			found = true
			if next.Targets[index].DeletedAt.IsZero() {
				next.Targets[index].DeletedAt, next.Targets[index].Attempts = now, next.Targets[index].Attempts+1
				next.Targets[index].LastFailureClass = ""
			}
		}
	}
	if !found {
		return Deletion{}, ErrInvalid
	}
	next.UpdatedAt, next.Version = now, deletion.Version+1
	allDeleted := true
	for _, target := range next.Targets {
		allDeleted = allDeleted && !target.DeletedAt.IsZero()
	}
	if allDeleted {
		next.State = DeletionAwaitingBackup
	}
	return next, nil
}

// MarkTargetFailed retains a bounded failure class so a later Begin can retry
// only the remaining exact copies. Successfully deleted targets stay final.
func (deletion Deletion) MarkTargetFailed(kind, reference, failureClass string, now time.Time) (Deletion, error) {
	if deletion.Validate() != nil || deletion.State != DeletionInProgress || !token(failureClass, 128) ||
		now.Before(deletion.UpdatedAt) || now.Location() != time.UTC {
		return Deletion{}, ErrConflict
	}
	next := deletion
	next.Targets = append([]Target(nil), deletion.Targets...)
	found := false
	for index := range next.Targets {
		if next.Targets[index].Kind == kind && next.Targets[index].Reference == reference {
			found = true
			if next.Targets[index].DeletedAt.IsZero() {
				next.Targets[index].Attempts++
				next.Targets[index].LastFailureClass = failureClass
			}
		}
	}
	if !found {
		return Deletion{}, ErrInvalid
	}
	next.State, next.FailureClass, next.UpdatedAt, next.Version = DeletionFailed, failureClass, now, deletion.Version+1
	return next, nil
}

// Complete verifies the documented backup boundary before finalisation.
func (deletion Deletion) Complete(now time.Time) (Deletion, error) {
	if deletion.Validate() != nil || deletion.State != DeletionAwaitingBackup || now.Before(deletion.BackupExpiresAt) || now.Location() != time.UTC {
		return Deletion{}, ErrConflict
	}
	next := deletion
	next.State, next.UpdatedAt, next.Version = DeletionCompleted, now, deletion.Version+1
	return next, nil
}

func validRegion(value string) bool { return token(value, 63) }

func token(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

// DescribeResolution returns safe operator-facing retention meaning.
func DescribeResolution(value Resolution) string {
	return fmt.Sprintf("%s retained in %s for %s", value.Class, value.Region, value.Duration)
}
