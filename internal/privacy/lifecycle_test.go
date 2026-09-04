package privacy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
)

func TestResolveRetentionPrecedenceAndRegion(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	rules := []privacy.RetentionRule{{Authority: "law.ng", Version: "1", Class: privacy.DataClassRawEvidence, Region: "ng-1", Minimum: 10 * 24 * time.Hour, Maximum: 20 * 24 * time.Hour}}
	resolved, err := privacy.Resolve(privacy.DataClassRawEvidence, "ng-1", now, 5*24*time.Hour, 0, rules)
	if err != nil || resolved.Duration != 10*24*time.Hour || !resolved.ExpiresAt.Equal(now.Add(10*24*time.Hour)) {
		t.Fatalf("Resolve() = %+v, %v", resolved, err)
	}
	if _, err := privacy.Resolve(privacy.DataClassRawEvidence, "ng-1", now, 40*24*time.Hour, 0, nil); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("unbounded extension error = %v", err)
	}
	if _, err := privacy.Resolve(privacy.DataClassRawEvidence, "ng-1", now, 40*24*time.Hour, 45*24*time.Hour, nil); err != nil {
		t.Fatalf("bounded extension: %v", err)
	}
	conflict := []privacy.RetentionRule{{Authority: "minimum", Version: "1", Class: privacy.DataClassRawEvidence, Region: "ng-1", Minimum: 20 * 24 * time.Hour}, {Authority: "maximum", Version: "1", Class: privacy.DataClassRawEvidence, Region: "ng-1", Maximum: 10 * 24 * time.Hour}}
	if _, err := privacy.Resolve(privacy.DataClassRawEvidence, "ng-1", now, 0, 0, conflict); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("contradictory rules error = %v", err)
	}
}

func TestDeletionHoldRetryTombstoneAndBackupBoundary(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	deletionID, _ := generator.NewDeletion()
	holdID, _ := generator.NewLegalHold()
	deletion, err := privacy.NewDeletion(deletionID, "ver_example", "ng-1", []privacy.Target{{Kind: "evidence", Reference: "object-v1", Region: "ng-1"}, {Kind: "derived", Reference: "ocr-v1", Region: "ng-1"}}, now, now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	hold := privacy.Hold{ID: holdID, AggregateID: "ver_example", Authority: "court.ng", Reason: "case-42", StartsAt: now, ReviewAt: now.Add(24 * time.Hour)}
	blocked, err := deletion.Begin(now, []privacy.Hold{hold})
	if !errors.Is(err, privacy.ErrHeld) || blocked.State != privacy.DeletionBlockedByLegalHold {
		t.Fatalf("Begin(held) = %+v, %v", blocked, err)
	}
	hold.ReleasedAt = now.Add(time.Hour)
	started, err := blocked.Begin(now.Add(2*time.Hour), []privacy.Hold{hold})
	if err != nil || started.State != privacy.DeletionInProgress {
		t.Fatalf("Begin(released) = %+v, %v", started, err)
	}
	failed, err := started.MarkTargetFailed("derived", "ocr-v1", "store_unavailable", now.Add(3*time.Hour))
	if err != nil || failed.State != privacy.DeletionFailed || failed.Targets[1].Attempts != 1 {
		t.Fatalf("partial failure = %+v, %v", failed, err)
	}
	started, err = failed.Begin(now.Add(4*time.Hour), nil)
	if err != nil || started.State != privacy.DeletionInProgress {
		t.Fatalf("retry begin = %+v, %v", started, err)
	}
	first, _ := started.MarkTargetDeleted("evidence", "object-v1", now.Add(5*time.Hour))
	replayed, err := first.MarkTargetDeleted("evidence", "object-v1", now.Add(6*time.Hour))
	if err != nil || replayed.Targets[0].Attempts != 1 {
		t.Fatalf("replay = %+v, %v", replayed.Targets[0], err)
	}
	awaiting, _ := replayed.MarkTargetDeleted("derived", "ocr-v1", now.Add(7*time.Hour))
	if awaiting.Targets[1].Attempts != 2 || awaiting.Targets[1].LastFailureClass != "" {
		t.Fatalf("derived cleanup retry = %+v", awaiting.Targets[1])
	}
	if _, err := awaiting.Complete(now.Add(34 * 24 * time.Hour)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("early completion error = %v", err)
	}
	completed, err := awaiting.Complete(now.Add(35 * 24 * time.Hour))
	if err != nil || completed.State != privacy.DeletionCompleted {
		t.Fatalf("Complete() = %+v, %v", completed, err)
	}
}
