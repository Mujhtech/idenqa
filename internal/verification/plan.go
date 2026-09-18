package verification

import (
	"context"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ErrPlanUnavailable means no explicit deployed route matches this session.
var ErrPlanUnavailable = errors.New("verification: no deployed check plan")

// PlannedCheck pins the runner and configuration before any execution is queued.
// A plan never derives assurance from a client's capability advertisement.
type PlannedCheck struct {
	Asynchronous    bool
	MaximumDuration time.Duration
	Name            string
	RunnerKind      RunnerKind
	Provenance      Provenance
}

// PlanInput binds a plan to the immutable session snapshot. It contains no evidence bytes.
type PlanInput struct {
	TenantID       id.Tenant
	VerificationID id.Verification
	ProfileDigest  string
	PolicyID       id.Policy
}

// CheckPlanner is owned by verification. Production runner selection remains
// an explicit composition decision; the synthetic implementation only tests plumbing.
type CheckPlanner interface {
	Plan(PlanInput) ([]PlannedCheck, error)
}

// CaptureTarget is an identifier-only durable discovery result.
type CaptureTarget struct {
	TenantID       id.Tenant
	VerificationID id.Verification
}

// ProcessingRepository owns the atomic check-plan, tasks, and lifecycle commit.
type ProcessingRepository interface {
	ListReadyCaptures(context.Context, time.Time, int) ([]CaptureTarget, error)
	StartProcessing(context.Context, tenant.Scope, id.Verification) (bool, error)
}

// CaptureRoute allows discovery to select a configured route before its batch limit.
type CaptureRoute interface {
	CaptureRoute() (tenantID, policyID, profileDigest string)
}
