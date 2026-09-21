package privacy

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

func validDeletionID(t *testing.T) id.Deletion {
	t.Helper()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	value, err := generator.NewDeletion()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type recordingPrivacyMetrics struct {
	transitions []observability.DeletionTransition
	backlog     []observability.DeletionBacklog
	expiries    []observability.BackupExpiry
}

func (metrics *recordingPrivacyMetrics) RecordDeletionTransition(transition observability.DeletionTransition) {
	metrics.transitions = append(metrics.transitions, transition)
}

func (metrics *recordingPrivacyMetrics) RecordDeletionBacklog(backlog observability.DeletionBacklog) {
	metrics.backlog = append(metrics.backlog, backlog)
}

func (metrics *recordingPrivacyMetrics) RecordBackupExpiry(expiry observability.BackupExpiry) {
	metrics.expiries = append(metrics.expiries, expiry)
}

func TestDeletionClassCollapsesMixedAndUnknownTargets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		targets []Target
		want    observability.DataClass
	}{
		{name: "raw", targets: []Target{{Kind: string(DataClassRawEvidence)}}, want: observability.DataRawEvidence},
		{name: "derived", targets: []Target{{Kind: string(DataClassDerivedEvidence)}}, want: observability.DataDerivedEvidence},
		{name: "mixed", targets: []Target{{Kind: string(DataClassRawEvidence)}, {Kind: string(DataClassDerivedEvidence)}}, want: observability.DataMixed},
		{name: "unknown", targets: []Target{{Kind: "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"}}, want: observability.DataOther},
		{name: "empty", targets: nil, want: observability.DataOther},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := deletionClass(test.targets); got != test.want {
				t.Fatalf("deletionClass() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestObserveBacklogGroupsOnlyBoundedStates(t *testing.T) {
	t.Parallel()
	metrics := &recordingPrivacyMetrics{}
	service := &Service{metrics: metrics}
	service.observeBacklog([]Deletion{
		{ID: validDeletionID(t), State: DeletionAwaitingBackup},
		{ID: validDeletionID(t), State: DeletionAwaitingBackup},
		{ID: validDeletionID(t), State: DeletionFailed},
		{},
	})
	if len(metrics.backlog) != 2 {
		t.Fatalf("backlog samples = %d, want 2", len(metrics.backlog))
	}
	counts := map[observability.DeletionState]int64{}
	for _, sample := range metrics.backlog {
		counts[sample.State] = sample.Count
	}
	if counts[observability.DeletionAwaiting] != 2 || counts[observability.DeletionFailed] != 1 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestObserveDeletionRecordsBoundedTransitionAndBackupAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	metrics := &recordingPrivacyMetrics{}
	service := &Service{metrics: metrics, now: func() time.Time { return now }}
	service.observeDeletion(DeletionRequested, Deletion{
		ID: validDeletionID(t), State: DeletionAwaitingBackup, Region: "ng-1",
		BackupExpiresAt: now.Add(35 * 24 * time.Hour),
		Targets:         []Target{{Kind: string(DataClassRawEvidence)}},
	})
	if len(metrics.transitions) != 1 {
		t.Fatalf("transitions = %d, want 1", len(metrics.transitions))
	}
	transition := metrics.transitions[0]
	if transition.From != observability.DeletionRequested || transition.To != observability.DeletionAwaiting ||
		transition.Kind != observability.DataRawEvidence || transition.Region != "ng-1" {
		t.Fatalf("transition = %+v", transition)
	}
	if len(metrics.expiries) != 1 || metrics.expiries[0].Age != 35*24*time.Hour || metrics.expiries[0].Region != "ng-1" {
		t.Fatalf("expiries = %+v", metrics.expiries)
	}
}
