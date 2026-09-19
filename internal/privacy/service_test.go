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

func TestServiceReadCapabilitiesInspectSafely(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	scope, _ := tenant.NewScope(tenantID)
	firstID, _ := ids.NewDeletion()
	secondID, _ := ids.NewDeletion()
	holdID, _ := ids.NewLegalHold()
	targets := []privacy.Target{
		{Kind: "raw_evidence", Reference: "raw-v1", Region: "ng-1"},
		{Kind: "derived_evidence", Reference: "derived-v1", Region: "ng-1"},
	}
	first, err := privacy.NewDeletion(firstID, "ver_example", "ng-1", targets, now, now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	first, err = first.Begin(now, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err = first.MarkTargetDeleted("raw_evidence", "raw-v1", now)
	if err != nil {
		t.Fatal(err)
	}
	first, err = first.MarkTargetFailed("derived_evidence", "derived-v1", "target_unavailable", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := privacy.NewDeletion(secondID, "ver_other", "ng-1", targets, now, now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	repository := &readRepository{
		deletions: []privacy.Deletion{first, second},
		holds: []privacy.Hold{{
			ID: holdID, AggregateID: "ver_example", Authority: "court-order", Reason: "pending proceedings",
			StartsAt: now, ReviewAt: now.Add(24 * time.Hour),
		}},
		records: []privacy.RetentionRecord{
			{ID: "evd_raw", Class: privacy.DataClassRawEvidence, Region: "ng-1", CreatedAt: now, Requested: 10 * 24 * time.Hour},
			{ID: "evd_derived", Class: privacy.DataClassDerivedEvidence, Region: "ng-1", CreatedAt: now},
		},
	}
	service, _ := privacy.NewService(repository, &targetEraser{}, ids, func() time.Time { return now })
	reader := privacy.Actor{ID: "operator-1", Permissions: []privacy.Permission{privacy.PermissionReadDeletion}}

	status, err := service.DeletionStatus(t.Context(), scope, reader, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if found, err := service.FindDeletion(t.Context(), scope, reader, firstID); err != nil || found.ID != firstID {
		t.Fatalf("find = %+v, %v", found, err)
	}
	if status.Deletion.State != privacy.DeletionFailed || len(status.Targets) != 2 || len(status.Holds) != 1 {
		t.Fatalf("status = %+v", status)
	}
	if status.Targets[0].Reference == "raw-v1" || len(status.Targets[0].Reference) != 24 {
		t.Fatalf("target reference was not digested: %+v", status.Targets[0])
	}
	if status.Targets[0].State != privacy.TargetDeleted || status.Targets[1].State != privacy.TargetFailed ||
		status.Targets[1].FailureClass != "target_unavailable" {
		t.Fatalf("target states = %+v", status.Targets)
	}

	page, err := service.ListDeletions(t.Context(), scope, reader, "", "", 1)
	if err != nil || !page.HasMore || len(page.Deletions) != 1 {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	next, err := service.ListDeletions(t.Context(), scope, reader, "", page.Deletions[0].ID.String(), 1)
	if err != nil || next.HasMore || len(next.Deletions) != 1 || next.Deletions[0].ID != secondID {
		t.Fatalf("next page = %+v, %v", next, err)
	}
	filtered, err := service.ListDeletions(t.Context(), scope, reader, "ver_other", "", 25)
	if err != nil || len(filtered.Deletions) != 1 || filtered.Deletions[0].ID != secondID {
		t.Fatalf("filtered page = %+v, %v", filtered, err)
	}

	resolution, err := service.ResolveRetention(t.Context(), scope, reader, "ver_example")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.AggregateID != "ver_example" || len(resolution.Holds) != 1 || len(resolution.Records) != 2 {
		t.Fatalf("resolution = %+v", resolution)
	}
	for index, record := range repository.records {
		expected, resolveErr := privacy.Resolve(record.Class, record.Region, record.CreatedAt, record.Requested, 0, nil)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		actual := resolution.Records[index]
		if actual.ID != record.ID || actual.Class != expected.Class || actual.Duration != expected.Duration || !actual.ExpiresAt.Equal(expected.ExpiresAt) {
			t.Fatalf("record %d = %+v, want %+v", index, actual, expected)
		}
	}

	unprivileged := privacy.Actor{ID: "operator-2", Permissions: []privacy.Permission{privacy.PermissionRunDeletion}}
	if _, err := service.DeletionStatus(t.Context(), scope, unprivileged, firstID); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("unprivileged status error = %v", err)
	}
	if _, err := service.ListDeletions(t.Context(), scope, unprivileged, "", "", 25); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("unprivileged list error = %v", err)
	}
	if _, err := service.ResolveRetention(t.Context(), scope, unprivileged, "ver_example"); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("unprivileged resolution error = %v", err)
	}
	if _, err := service.ListDeletions(t.Context(), scope, reader, "ver_example", "", 101); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("oversized page error = %v", err)
	}
	if _, err := service.ResolveRetention(t.Context(), scope, reader, " "); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("invalid aggregate error = %v", err)
	}
}

type readRepository struct {
	lifecycleRepository
	deletions []privacy.Deletion
	records   []privacy.RetentionRecord
	holds     []privacy.Hold
}

func (repository *readRepository) Find(_ context.Context, _ tenant.Scope, identifier id.Deletion) (privacy.Deletion, error) {
	for _, deletion := range repository.deletions {
		if deletion.ID == identifier {
			return deletion, nil
		}
	}
	return privacy.Deletion{}, privacy.ErrInvalid
}

func (repository *readRepository) ListDeletions(_ context.Context, _ tenant.Scope, aggregateID, position string, limit int) ([]privacy.Deletion, error) {
	result := make([]privacy.Deletion, 0, limit)
	for _, deletion := range repository.deletions {
		if aggregateID != "" && deletion.AggregateID != aggregateID {
			continue
		}
		if position != "" && deletion.ID.String() <= position {
			continue
		}
		if len(result) == limit {
			break
		}
		result = append(result, deletion)
	}
	return result, nil
}

func (repository *readRepository) RetainedRecords(context.Context, tenant.Scope, string) ([]privacy.RetentionRecord, error) {
	return repository.records, nil
}

func (repository *readRepository) ActiveHolds(context.Context, tenant.Scope, string, time.Time) ([]privacy.Hold, error) {
	return repository.holds, nil
}
