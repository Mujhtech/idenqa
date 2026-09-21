package experience

import (
	"fmt"
)

// Plan is the domain-validated next durable aggregate state for one lifecycle
// operation. It is produced only by PlanTransition so the PostgreSQL adapter
// and unit tests share exactly one rule set.
type Plan struct {
	State              State
	LatestVersion      uint32
	ApprovedVersion    uint32
	PublishedVersion   uint32
	SupersededVersion  uint32
	SupersededRevision RevisionState
	TargetRevision     RevisionState
	TargetVersion      uint32
}

// PlanTransition validates one expected-version lifecycle command against the
// current aggregate. applied is false for an idempotent replay that must not
// append a duplicate event.
func PlanTransition(current Experience, operation TransitionOperation, targetVersion uint32) (Plan, bool, error) {
	if current.IsZero() || !current.State.Valid() {
		return Plan{}, false, ErrInvalid
	}
	switch operation {
	case OperationUpdate:
		return planUpdate(current, targetVersion)
	case OperationApprove:
		return planApprove(current)
	case OperationPublish:
		return planPublish(current)
	case OperationRevoke:
		return planRevoke(current)
	case OperationRollback:
		return planRollback(current, targetVersion)
	default:
		return Plan{}, false, fmt.Errorf("%w: operation", ErrInvalid)
	}
}

func planUpdate(current Experience, version uint32) (Plan, bool, error) {
	if current.State == StateRevoked {
		return Plan{}, false, ErrRevoked
	}
	if version != current.LatestVersion+1 {
		return Plan{}, false, fmt.Errorf("%w: next revision version", ErrInvalid)
	}
	return Plan{
		State:            StateDraft,
		LatestVersion:    version,
		ApprovedVersion:  0,
		PublishedVersion: current.PublishedVersion,
		TargetVersion:    version,
		TargetRevision:   RevisionDraft,
	}, true, nil
}

func planApprove(current Experience) (Plan, bool, error) {
	if current.State == StateRevoked {
		return Plan{}, false, ErrRevoked
	}
	if current.LatestVersion == 0 {
		return Plan{}, false, ErrInvalid
	}
	if current.ApprovedVersion == current.LatestVersion {
		return Plan{State: StateApproved, LatestVersion: current.LatestVersion, ApprovedVersion: current.ApprovedVersion, PublishedVersion: current.PublishedVersion}, false, nil
	}
	return Plan{
		State:            StateApproved,
		LatestVersion:    current.LatestVersion,
		ApprovedVersion:  current.LatestVersion,
		PublishedVersion: current.PublishedVersion,
		TargetVersion:    current.LatestVersion,
		TargetRevision:   RevisionApproved,
	}, true, nil
}

func planPublish(current Experience) (Plan, bool, error) {
	if current.State == StateRevoked {
		return Plan{}, false, ErrRevoked
	}
	if current.ApprovedVersion == 0 || current.ApprovedVersion != current.LatestVersion {
		return Plan{}, false, fmt.Errorf("%w: publication requires approval of the latest revision", ErrConflict)
	}
	if current.PublishedVersion == current.LatestVersion {
		return Plan{State: StatePublished, LatestVersion: current.LatestVersion, ApprovedVersion: current.ApprovedVersion, PublishedVersion: current.PublishedVersion}, false, nil
	}
	plan := Plan{
		State:            StatePublished,
		LatestVersion:    current.LatestVersion,
		ApprovedVersion:  current.ApprovedVersion,
		PublishedVersion: current.LatestVersion,
		TargetVersion:    current.LatestVersion,
		TargetRevision:   RevisionPublished,
	}
	if current.PublishedVersion != 0 {
		plan.SupersededVersion = current.PublishedVersion
		plan.SupersededRevision = RevisionSuperseded
	}
	return plan, true, nil
}

func planRevoke(current Experience) (Plan, bool, error) {
	if current.State == StateRevoked {
		return Plan{State: StateRevoked, LatestVersion: current.LatestVersion, ApprovedVersion: current.ApprovedVersion, PublishedVersion: current.PublishedVersion}, false, nil
	}
	if current.PublishedVersion == 0 {
		return Plan{}, false, fmt.Errorf("%w: nothing is published to revoke", ErrConflict)
	}
	return Plan{
		State:              StateRevoked,
		LatestVersion:      current.LatestVersion,
		ApprovedVersion:    current.ApprovedVersion,
		PublishedVersion:   current.PublishedVersion,
		SupersededVersion:  current.PublishedVersion,
		SupersededRevision: RevisionRevoked,
		TargetVersion:      current.PublishedVersion,
		TargetRevision:     RevisionRevoked,
	}, true, nil
}

func planRollback(current Experience, targetVersion uint32) (Plan, bool, error) {
	if current.State != StatePublished && current.State != StateRevoked {
		return Plan{}, false, fmt.Errorf("%w: rollback requires a published or revoked aggregate", ErrConflict)
	}
	if targetVersion == 0 {
		return Plan{}, false, fmt.Errorf("%w: rollback target", ErrInvalid)
	}
	if current.State == StatePublished && current.PublishedVersion == targetVersion {
		return Plan{State: StatePublished, LatestVersion: current.LatestVersion, ApprovedVersion: current.ApprovedVersion, PublishedVersion: current.PublishedVersion, TargetVersion: targetVersion, TargetRevision: RevisionPublished}, false, nil
	}
	plan := Plan{
		State:            StatePublished,
		LatestVersion:    current.LatestVersion,
		ApprovedVersion:  current.ApprovedVersion,
		PublishedVersion: targetVersion,
		TargetVersion:    targetVersion,
		TargetRevision:   RevisionPublished,
	}
	if current.PublishedVersion != 0 && current.PublishedVersion != targetVersion {
		plan.SupersededVersion = current.PublishedVersion
		plan.SupersededRevision = RevisionSuperseded
	}
	return plan, true, nil
}
