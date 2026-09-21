package experience

import (
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func lifecycleExperience() Experience {
	return Experience{
		ID: mustExperienceID("exp_01J00000000000000000000000"), State: StateDraft, Revision: 3,
		LatestVersion: 2, ApprovedVersion: 1, PublishedVersion: 1,
	}
}

func TestPlanTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		current   Experience
		operation TransitionOperation
		target    uint32
		want      Plan
		applied   bool
		wantErr   error
	}{
		{
			name: "update from draft creates next revision", current: Experience{ID: lifecycleExperience().ID, State: StateDraft, Revision: 1, LatestVersion: 1},
			operation: OperationUpdate, target: 2,
			want:    Plan{State: StateDraft, LatestVersion: 2, TargetVersion: 2, TargetRevision: RevisionDraft},
			applied: true,
		},
		{
			name: "update from published keeps live version", current: lifecycleExperience(),
			operation: OperationUpdate, target: 3,
			want:    Plan{State: StateDraft, LatestVersion: 3, PublishedVersion: 1, TargetVersion: 3, TargetRevision: RevisionDraft},
			applied: true,
		},
		{
			name: "update from revoked is refused", current: Experience{ID: lifecycleExperience().ID, State: StateRevoked, Revision: 2, LatestVersion: 1},
			operation: OperationUpdate, target: 2, wantErr: ErrRevoked,
		},
		{
			name: "approve latest", current: Experience{ID: lifecycleExperience().ID, State: StateDraft, Revision: 2, LatestVersion: 2},
			operation: OperationApprove,
			want:      Plan{State: StateApproved, LatestVersion: 2, ApprovedVersion: 2, TargetVersion: 2, TargetRevision: RevisionApproved},
			applied:   true,
		},
		{
			name: "approve replays idempotently", current: Experience{ID: lifecycleExperience().ID, State: StateApproved, Revision: 2, LatestVersion: 2, ApprovedVersion: 2},
			operation: OperationApprove, applied: false,
		},
		{
			name: "publish requires approval", current: Experience{ID: lifecycleExperience().ID, State: StateDraft, Revision: 2, LatestVersion: 2},
			operation: OperationPublish, wantErr: ErrConflict,
		},
		{
			name: "publish approved latest", current: Experience{ID: lifecycleExperience().ID, State: StateApproved, Revision: 3, LatestVersion: 2, ApprovedVersion: 2, PublishedVersion: 1},
			operation: OperationPublish,
			want: Plan{State: StatePublished, LatestVersion: 2, ApprovedVersion: 2, PublishedVersion: 2,
				SupersededVersion: 1, SupersededRevision: RevisionSuperseded, TargetVersion: 2, TargetRevision: RevisionPublished},
			applied: true,
		},
		{
			name: "publish replays idempotently", current: Experience{ID: lifecycleExperience().ID, State: StatePublished, Revision: 3, LatestVersion: 2, ApprovedVersion: 2, PublishedVersion: 2},
			operation: OperationPublish, applied: false,
		},
		{
			name: "revoke live revision", current: Experience{ID: lifecycleExperience().ID, State: StatePublished, Revision: 3, LatestVersion: 2, ApprovedVersion: 2, PublishedVersion: 2},
			operation: OperationRevoke,
			want: Plan{State: StateRevoked, LatestVersion: 2, ApprovedVersion: 2, PublishedVersion: 2,
				SupersededVersion: 2, SupersededRevision: RevisionRevoked, TargetVersion: 2, TargetRevision: RevisionRevoked},
			applied: true,
		},
		{
			name: "revoke without a live revision is refused", current: Experience{ID: lifecycleExperience().ID, State: StateDraft, Revision: 1, LatestVersion: 1},
			operation: OperationRevoke, wantErr: ErrConflict,
		},
		{
			name: "revoke replays idempotently", current: Experience{ID: lifecycleExperience().ID, State: StateRevoked, Revision: 4, LatestVersion: 2, ApprovedVersion: 2, PublishedVersion: 2},
			operation: OperationRevoke, applied: false,
		},
		{
			name: "rollback republishes a prior version", current: Experience{ID: lifecycleExperience().ID, State: StateRevoked, Revision: 4, LatestVersion: 3, ApprovedVersion: 3, PublishedVersion: 2},
			operation: OperationRollback, target: 2,
			want:    Plan{State: StatePublished, LatestVersion: 3, ApprovedVersion: 3, PublishedVersion: 2, TargetVersion: 2, TargetRevision: RevisionPublished},
			applied: true,
		},
		{
			name: "rollback to the live version replays idempotently", current: Experience{ID: lifecycleExperience().ID, State: StatePublished, Revision: 4, LatestVersion: 3, ApprovedVersion: 3, PublishedVersion: 2},
			operation: OperationRollback, target: 2, applied: false,
		},
		{
			name: "rollback from draft is refused", current: Experience{ID: lifecycleExperience().ID, State: StateDraft, Revision: 1, LatestVersion: 1},
			operation: OperationRollback, target: 1, wantErr: ErrConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, applied, err := PlanTransition(test.current, test.operation, test.target)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("PlanTransition() error = %v, want %v", err, test.wantErr)
			}
			if err != nil {
				return
			}
			if applied != test.applied {
				t.Fatalf("PlanTransition() applied = %v, want %v", applied, test.applied)
			}
			if test.applied && plan != test.want {
				t.Fatalf("PlanTransition() plan = %+v, want %+v", plan, test.want)
			}
		})
	}
}

func TestPlanTransitionRejectsUnknownOperationAndZeroAggregate(t *testing.T) {
	t.Parallel()
	if _, _, err := PlanTransition(Experience{}, OperationApprove, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero aggregate error = %v, want ErrInvalid", err)
	}
	if _, _, err := PlanTransition(lifecycleExperience(), TransitionOperation("merge"), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown operation error = %v, want ErrInvalid", err)
	}
	if _, err := id.ParseExperience("exp_not-a-ulid-value"); err == nil {
		t.Fatal("ParseExperience accepted an invalid identifier")
	}
}
