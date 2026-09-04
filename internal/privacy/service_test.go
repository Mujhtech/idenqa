package privacy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestServiceRecoversPartialFailureAndCompletesAfterBackup(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	scope, _ := tenant.NewScope(tenantID)
	repository := &lifecycleRepository{}
	eraser := &targetEraser{failReference: "derived-v1"}
	service, _ := privacy.NewService(repository, eraser, ids, func() time.Time { return now })
	actor := privacy.Actor{ID: "operator-1", Permissions: []privacy.Permission{privacy.PermissionRequestDeletion, privacy.PermissionRunDeletion}}
	deletion, err := service.RequestDeletion(t.Context(), scope, actor, "ver_example", "ng-1", []privacy.Target{{Kind: "evidence", Reference: "raw-v1", Region: "ng-1"}, {Kind: "derived", Reference: "derived-v1", Region: "ng-1"}}, now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.Run(t.Context(), scope, actor, deletion.ID)
	if err == nil || failed.State != privacy.DeletionFailed || failed.Targets[0].DeletedAt.IsZero() {
		t.Fatalf("first run = %+v, %v", failed, err)
	}
	eraser.failReference = ""
	now = now.Add(35 * 24 * time.Hour)
	completed, err := service.Run(t.Context(), scope, actor, deletion.ID)
	if err != nil || completed.State != privacy.DeletionCompleted || repository.tombstone.ProofDigest == "" {
		t.Fatalf("recovery run = %+v, %v tombstone=%+v", completed, err, repository.tombstone)
	}
}

type lifecycleRepository struct {
	deletion  privacy.Deletion
	tombstone privacy.Tombstone
}

func (repository *lifecycleRepository) Create(_ context.Context, _ tenant.Scope, _ privacy.Actor, value privacy.Deletion) error {
	repository.deletion = value
	return nil
}
func (repository *lifecycleRepository) Find(context.Context, tenant.Scope, id.Deletion) (privacy.Deletion, error) {
	return repository.deletion, nil
}
func (repository *lifecycleRepository) Save(_ context.Context, _ tenant.Scope, _ privacy.Actor, value privacy.Deletion, expected int64) error {
	if repository.deletion.Version != expected {
		return privacy.ErrConflict
	}
	repository.deletion = value
	return nil
}
func (repository *lifecycleRepository) ActiveHolds(context.Context, tenant.Scope, string, time.Time) ([]privacy.Hold, error) {
	return nil, nil
}
func (repository *lifecycleRepository) Complete(_ context.Context, _ tenant.Scope, _ privacy.Actor, value privacy.Deletion, tombstone privacy.Tombstone, expected int64) error {
	if repository.deletion.Version != expected {
		return privacy.ErrConflict
	}
	repository.deletion, repository.tombstone = value, tombstone
	return nil
}

type targetEraser struct{ failReference string }

func (eraser *targetEraser) Delete(_ context.Context, target privacy.Target) error {
	if target.Reference == eraser.failReference {
		return errors.New("fixture unavailable")
	}
	return nil
}
