package proposal

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/tenant"
)

// InMemoryEvidenceChecker is a deterministic evidence-reference checker for
// tests and local composition. It reports only whether a reference was seeded.
type InMemoryEvidenceChecker struct {
	existing map[string]bool
}

// NewInMemoryEvidenceChecker seeds an in-memory checker with known references.
func NewInMemoryEvidenceChecker(refs []string) *InMemoryEvidenceChecker {
	m := make(map[string]bool, len(refs))
	for _, ref := range refs {
		m[ref] = true
	}
	return &InMemoryEvidenceChecker{existing: m}
}

// Exists reports whether the reference was seeded.
func (checker *InMemoryEvidenceChecker) Exists(_ context.Context, _, _ string, reference string) (bool, error) {
	_, ok := checker.existing[reference]
	return ok, nil
}

// Add seeds an additional reference.
func (checker *InMemoryEvidenceChecker) Add(ref string) {
	checker.existing[ref] = true
}

var _ EvidenceChecker = (*InMemoryEvidenceChecker)(nil)

// InMemoryAuthorityChecker is a deterministic authority checker for tests and
// local composition.
type InMemoryAuthorityChecker struct {
	allowed bool
}

// NewInMemoryAuthorityChecker returns a checker that reports a fixed result.
func NewInMemoryAuthorityChecker(allowed bool) *InMemoryAuthorityChecker {
	return &InMemoryAuthorityChecker{allowed: allowed}
}

// Allowed reports the fixed result configured at construction.
func (checker *InMemoryAuthorityChecker) Allowed(_ context.Context, _, _ string) (bool, error) {
	return checker.allowed, nil
}

var _ AuthorityChecker = (*InMemoryAuthorityChecker)(nil)

// InMemoryRegionValidator is a deterministic region validator for tests and
// local composition.
type InMemoryRegionValidator struct {
	allowed bool
}

// NewInMemoryRegionValidator returns a validator that reports a fixed result.
func NewInMemoryRegionValidator(allowed bool) *InMemoryRegionValidator {
	return &InMemoryRegionValidator{allowed: allowed}
}

// Allowed reports the fixed result configured at construction.
func (validator *InMemoryRegionValidator) Allowed(_ context.Context, _, _ string) (bool, error) {
	return validator.allowed, nil
}

var _ RegionValidator = (*InMemoryRegionValidator)(nil)

// AuditEvent is one recorded proposal audit event.
type AuditEvent struct {
	Type        string
	AggregateID string
	ActorID     string
	Digest      string
}

// InMemoryAuditRecorder records audit events for tests and local composition.
type InMemoryAuditRecorder struct {
	Events []AuditEvent
}

// NewInMemoryAuditRecorder returns an empty in-memory audit recorder.
func NewInMemoryAuditRecorder() *InMemoryAuditRecorder {
	return &InMemoryAuditRecorder{}
}

// Append records one audit event.
func (recorder *InMemoryAuditRecorder) Append(_ context.Context, _ tenant.Scope, eventType string, aggregateID string, actorID string, digest string, _ time.Time) error {
	recorder.Events = append(recorder.Events, AuditEvent{Type: eventType, AggregateID: aggregateID, ActorID: actorID, Digest: digest})
	return nil
}

var _ AuditRecorder = (*InMemoryAuditRecorder)(nil)
