package privacy_test

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type recordingRunMetrics struct {
	transitions []observability.DeletionTransition
	backlog     []observability.DeletionBacklog
	expiries    []observability.BackupExpiry
}

func (metrics *recordingRunMetrics) RecordDeletionTransition(transition observability.DeletionTransition) {
	metrics.transitions = append(metrics.transitions, transition)
}

func (metrics *recordingRunMetrics) RecordDeletionBacklog(backlog observability.DeletionBacklog) {
	metrics.backlog = append(metrics.backlog, backlog)
}

func (metrics *recordingRunMetrics) RecordBackupExpiry(expiry observability.BackupExpiry) {
	metrics.expiries = append(metrics.expiries, expiry)
}

func TestRunRecordsBoundedDeletionTransitionAndBackupAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	scope, _ := tenant.NewScope(tenantID)
	repository := &lifecycleRepository{}
	metrics := &recordingRunMetrics{}
	service, err := privacy.NewService(repository, &targetEraser{}, ids, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service.WithMetrics(metrics)
	actor := privacy.Actor{ID: "operator-1", Permissions: []privacy.Permission{privacy.PermissionRequestDeletion, privacy.PermissionRunDeletion}}
	deletion, err := service.RequestDeletion(t.Context(), scope, actor, "ver_example", "ng-1", []privacy.Target{{Kind: string(privacy.DataClassRawEvidence), Reference: "raw-v1", Region: "ng-1"}}, now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(t.Context(), scope, actor, deletion.ID)
	if err != nil || result.State != privacy.DeletionAwaitingBackup {
		t.Fatalf("run = %+v, %v", result, err)
	}
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
