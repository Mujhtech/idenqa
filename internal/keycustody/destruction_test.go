package keycustody_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

type stubScanner struct {
	counts map[keycustody.ReferenceClass]int64
	err    error
}

func (scanner stubScanner) Scan(context.Context, keycustody.DestructionTarget) (map[keycustody.ReferenceClass]int64, error) {
	if scanner.err != nil {
		return nil, scanner.err
	}
	return scanner.counts, nil
}

type memoryDestructionRepository struct {
	verifications map[string]keycustody.VerificationReceipt
	schedules     []keycustody.DestructionSchedule
}

func newDestructionRepository() *memoryDestructionRepository {
	return &memoryDestructionRepository{verifications: map[string]keycustody.VerificationReceipt{}}
}

func (repository *memoryDestructionRepository) RecordVerification(_ context.Context, receipt keycustody.VerificationReceipt) error {
	repository.verifications[receipt.ID] = receipt
	return nil
}

func (repository *memoryDestructionRepository) Verification(_ context.Context, identifier string) (keycustody.VerificationReceipt, error) {
	receipt, ok := repository.verifications[identifier]
	if !ok {
		return keycustody.VerificationReceipt{}, keycustody.ErrNotFound
	}
	return receipt, nil
}

func (repository *memoryDestructionRepository) RecordSchedule(_ context.Context, schedule keycustody.DestructionSchedule) error {
	repository.schedules = append(repository.schedules, schedule)
	return nil
}

type stubScheduler struct {
	calls int
}

func (scheduler *stubScheduler) ScheduleDestruction(_ context.Context, reference, version string, _ time.Duration) (time.Time, error) {
	scheduler.calls++
	if reference != "ref" || version != "v1" {
		return time.Time{}, errors.New("unexpected target")
	}
	return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), nil
}

func destructionTarget() keycustody.DestructionTarget {
	return keycustody.DestructionTarget{Provider: "test", Reference: "ref", Version: "v1", Algorithm: "TEST"}
}

func newDestructionService(t *testing.T, scanner keycustody.ReferenceScanner, repository *memoryDestructionRepository, scheduler keycustody.DestructionScheduler, now time.Time) *keycustody.DestructionService {
	t.Helper()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	service, err := keycustody.NewDestructionService(scanner, repository, scheduler, generator, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewDestructionService() error = %v", err)
	}
	return service
}

func TestDestructionVerificationFailsClosedOnReferences(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	actor, _ := generator.NewAPIKey()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repository := newDestructionRepository()
	service := newDestructionService(t, stubScanner{counts: map[keycustody.ReferenceClass]int64{
		keycustody.ReferenceEvidenceAsset: 2,
		keycustody.ReferenceHMACKey:       1,
	}}, repository, nil, now)

	receipt, err := service.Verify(context.Background(), actor, destructionTarget(), "integration destruction check")
	if !errors.Is(err, keycustody.ErrDestructionBlocked) {
		t.Fatalf("Verify() error = %v, want ErrDestructionBlocked", err)
	}
	if receipt.State != "blocked" || receipt.Total != 3 || receipt.Counts[keycustody.ReferenceWebhookEvent] != 0 {
		t.Fatalf("blocked receipt = %+v", receipt)
	}
	if _, err := service.Authorize(context.Background(), receipt.ID); !errors.Is(err, keycustody.ErrDestructionUnverified) {
		t.Fatalf("Authorize(blocked) error = %v, want ErrDestructionUnverified", err)
	}
	if len(repository.verifications) != 1 {
		t.Fatalf("recorded receipts = %d, want 1", len(repository.verifications))
	}
}

func TestDestructionScheduleRequiresFreshVerifiedReceipt(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	actor, _ := generator.NewAPIKey()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repository := newDestructionRepository()
	scheduler := &stubScheduler{}
	service := newDestructionService(t, stubScanner{counts: map[keycustody.ReferenceClass]int64{}}, repository, scheduler, now)

	receipt, err := service.Verify(context.Background(), actor, destructionTarget(), "integration destruction check")
	if err != nil || !receipt.Verified() {
		t.Fatalf("Verify() = %+v, %v", receipt, err)
	}
	schedule, err := service.Schedule(context.Background(), actor, receipt.ID, 7*24*time.Hour, false, "integration destruction schedule")
	if err != nil || schedule.Mode != "scheduled" || schedule.ProviderDeletionAt == nil || scheduler.calls != 1 {
		t.Fatalf("Schedule() = %+v, %v calls=%d", schedule, err, scheduler.calls)
	}

	// A receipt older than the approval window can no longer authorise deletion.
	stale := newDestructionService(t, stubScanner{counts: map[keycustody.ReferenceClass]int64{}}, repository,
		scheduler, now.Add(2*keycustody.DestructionApprovalWindow))
	if _, err := stale.Schedule(context.Background(), actor, receipt.ID, 7*24*time.Hour, false, "integration destruction schedule"); !errors.Is(err, keycustody.ErrDestructionUnverified) {
		t.Fatalf("Schedule(stale) error = %v, want ErrDestructionUnverified", err)
	}
	if scheduler.calls != 1 {
		t.Fatalf("scheduler calls = %d, want 1", scheduler.calls)
	}
}

func TestDestructionScheduleRecordOnlyNeverCallsProvider(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	actor, _ := generator.NewAPIKey()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repository := newDestructionRepository()
	scheduler := &stubScheduler{}
	service := newDestructionService(t, stubScanner{counts: map[keycustody.ReferenceClass]int64{}}, repository, scheduler, now)
	receipt, err := service.Verify(context.Background(), actor, destructionTarget(), "integration destruction check")
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := service.Schedule(context.Background(), actor, receipt.ID, 7*24*time.Hour, true, "integration destruction record")
	if err != nil || schedule.Mode != "recorded" || schedule.ProviderDeletionAt != nil || scheduler.calls != 0 {
		t.Fatalf("Schedule(record only) = %+v, %v calls=%d", schedule, err, scheduler.calls)
	}
}

func TestDestructionTargetRejectsMalformedIdentity(t *testing.T) {
	t.Parallel()

	tests := map[string]keycustody.DestructionTarget{
		"empty provider": {Reference: "ref", Version: "v1", Algorithm: "TEST"},
		"empty version":  {Provider: "test", Reference: "ref", Algorithm: "TEST"},
		"control char":   {Provider: "te\x01st", Reference: "ref", Version: "v1", Algorithm: "TEST"},
	}
	for name, target := range tests {
		if target.Valid() {
			t.Fatalf("%s target unexpectedly valid: %+v", name, target)
		}
	}
}
