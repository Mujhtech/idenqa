package keycustody_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

type memoryRecoveryRepository struct {
	ceremonies map[string]keycustody.RecoveryCeremony
}

func newRecoveryRepository() *memoryRecoveryRepository {
	return &memoryRecoveryRepository{ceremonies: map[string]keycustody.RecoveryCeremony{}}
}

func (repository *memoryRecoveryRepository) Create(_ context.Context, ceremony keycustody.RecoveryCeremony) (keycustody.RecoveryCeremony, error) {
	if _, exists := repository.ceremonies[ceremony.ID]; exists {
		return keycustody.RecoveryCeremony{}, keycustody.ErrConflict
	}
	repository.ceremonies[ceremony.ID] = ceremony
	return ceremony, nil
}

func (repository *memoryRecoveryRepository) Load(_ context.Context, identifier string) (keycustody.RecoveryCeremony, error) {
	ceremony, ok := repository.ceremonies[identifier]
	if !ok {
		return keycustody.RecoveryCeremony{}, keycustody.ErrNotFound
	}
	return ceremony, nil
}

func (repository *memoryRecoveryRepository) Advance(_ context.Context, expected, next keycustody.RecoveryCeremony) (keycustody.RecoveryCeremony, error) {
	current, ok := repository.ceremonies[expected.ID]
	if !ok || current.Version != expected.Version || current.State != expected.State {
		return keycustody.RecoveryCeremony{}, keycustody.ErrConflict
	}
	repository.ceremonies[next.ID] = next
	return next, nil
}

type stubRewrapper struct{ calls int }

func (rewrapper *stubRewrapper) RewrapTenantClass(context.Context, string, id.Tenant, int) (int, error) {
	rewrapper.calls++
	return 4, nil
}

type stubEpochAuthorizer struct{ calls int }

func (authorizer *stubEpochAuthorizer) AuthorizeEpoch(context.Context, string, string, string, string, string) (int64, error) {
	authorizer.calls++
	return 7, nil
}

func recoveryService(t *testing.T, repository keycustody.RecoveryRepository, rewrapper keycustody.TenantClassRewrapper, epochs keycustody.EpochAuthorizer, now *time.Time) *keycustody.RecoveryService {
	t.Helper()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	service, err := keycustody.NewRecoveryService(repository, rewrapper, epochs, generator, func() time.Time { return *now })
	if err != nil {
		t.Fatalf("NewRecoveryService() error = %v", err)
	}
	return service
}

func TestRecoveryCeremonyRequiresTwoDistinctPrincipals(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	starter, _ := generator.NewAPIKey()
	approver, _ := generator.NewAPIKey()
	tenantID, _ := generator.NewTenant()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repository := newRecoveryRepository()
	rewrapper := &stubRewrapper{}
	service := recoveryService(t, repository, rewrapper, &stubEpochAuthorizer{}, &now)

	started, err := service.ExecuteDirect(context.Background(), starter, keycustody.RecoveryCommand{
		Operation: "start", Kind: keycustody.RecoveryKindRewrap, Class: "keycustody.hmac",
		TenantID: tenantID.String(), Reason: "integration key recovery",
	})
	if err != nil || started.Ceremony.State != keycustody.RecoveryStarted || started.Ceremony.Version != 1 {
		t.Fatalf("start = %+v, %v", started, err)
	}
	if _, err := service.ExecuteDirect(context.Background(), starter, keycustody.RecoveryCommand{
		Operation: "approve", Identifier: started.Ceremony.ID, ExpectedVersion: 1,
		Reason: "integration self approval",
	}); !errors.Is(err, keycustody.ErrRecoveryForbidden) {
		t.Fatalf("self approval error = %v, want ErrRecoveryForbidden", err)
	}
	approved, err := service.ExecuteDirect(context.Background(), approver, keycustody.RecoveryCommand{
		Operation: "approve", Identifier: started.Ceremony.ID, ExpectedVersion: 1,
		Reason: "integration distinct approval",
	})
	if err != nil || approved.Ceremony.State != keycustody.RecoveryApproved || approved.Ceremony.Version != 2 {
		t.Fatalf("approve = %+v, %v", approved, err)
	}
	completed, err := service.ExecuteDirect(context.Background(), starter, keycustody.RecoveryCommand{
		Operation: "complete", Identifier: started.Ceremony.ID, ExpectedVersion: 2,
		Reason: "integration completion",
	})
	if err != nil || completed.Ceremony.State != keycustody.RecoveryCompleted ||
		completed.Ceremony.Receipt["rewrapped"] != 4 || rewrapper.calls != 1 {
		t.Fatalf("complete = %+v, %v calls=%d", completed, err, rewrapper.calls)
	}
}

func TestRecoveryCeremonyAbortLeavesStateUnchanged(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	starter, _ := generator.NewAPIKey()
	approver, _ := generator.NewAPIKey()
	tenantID, _ := generator.NewTenant()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repository := newRecoveryRepository()
	rewrapper := &stubRewrapper{}
	service := recoveryService(t, repository, rewrapper, &stubEpochAuthorizer{}, &now)

	started, err := service.ExecuteDirect(context.Background(), starter, keycustody.RecoveryCommand{
		Operation: "start", Kind: keycustody.RecoveryKindRewrap, Class: "keycustody.hmac",
		TenantID: tenantID.String(), Reason: "integration key recovery",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteDirect(context.Background(), approver, keycustody.RecoveryCommand{
		Operation: "abort", Identifier: started.Ceremony.ID, ExpectedVersion: 1,
		Reason: "integration abort",
	}); err != nil {
		t.Fatalf("abort error = %v", err)
	}
	if rewrapper.calls != 0 {
		t.Fatalf("aborted ceremony executed %d rewraps", rewrapper.calls)
	}
	// A completed ceremony cannot be aborted, and version conflicts fail closed.
	if _, err := service.ExecuteDirect(context.Background(), approver, keycustody.RecoveryCommand{
		Operation: "abort", Identifier: started.Ceremony.ID, ExpectedVersion: 1,
		Reason: "integration stale abort",
	}); !errors.Is(err, keycustody.ErrConflict) {
		t.Fatalf("stale abort error = %v, want ErrConflict", err)
	}
}

func TestRecoveryCeremonyWindowsAndEpochMigration(t *testing.T) {
	t.Parallel()

	generator, _ := id.NewSystemGenerator()
	starter, _ := generator.NewAPIKey()
	approver, _ := generator.NewAPIKey()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	repository := newRecoveryRepository()
	epochs := &stubEpochAuthorizer{}
	service := recoveryService(t, repository, nil, epochs, &now)

	started, err := service.ExecuteDirect(context.Background(), starter, keycustody.RecoveryCommand{
		Operation: "start", Kind: keycustody.RecoveryKindMigrateEpoch, Class: "evidence.content",
		Target: keycustody.DestructionTarget{Provider: "test", Reference: "ref", Version: "v2", Algorithm: "TEST"},
		Reason: "integration epoch migration",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Approval after the bounded window is refused.
	now = now.Add(keycustody.RecoveryApprovalWindow + time.Minute)
	if _, err := service.ExecuteDirect(context.Background(), approver, keycustody.RecoveryCommand{
		Operation: "approve", Identifier: started.Ceremony.ID, ExpectedVersion: 1,
		Reason: "integration late approval",
	}); !errors.Is(err, keycustody.ErrRecoveryExpired) {
		t.Fatalf("late approval error = %v, want ErrRecoveryExpired", err)
	}
	// Re-start inside the window, approve, then let the use window lapse.
	now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	started, err = service.ExecuteDirect(context.Background(), starter, keycustody.RecoveryCommand{
		Operation: "start", Kind: keycustody.RecoveryKindMigrateEpoch, Class: "evidence.content",
		Target: keycustody.DestructionTarget{Provider: "test", Reference: "ref", Version: "v2", Algorithm: "TEST"},
		Reason: "integration epoch migration",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteDirect(context.Background(), approver, keycustody.RecoveryCommand{
		Operation: "approve", Identifier: started.Ceremony.ID, ExpectedVersion: 1,
		Reason: "integration approval",
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(keycustody.RecoveryUseWindow + time.Minute)
	if _, err := service.ExecuteDirect(context.Background(), approver, keycustody.RecoveryCommand{
		Operation: "complete", Identifier: started.Ceremony.ID, ExpectedVersion: 2,
		Reason: "integration late completion",
	}); !errors.Is(err, keycustody.ErrRecoveryExpired) {
		t.Fatalf("late completion error = %v, want ErrRecoveryExpired", err)
	}
	if epochs.calls != 0 {
		t.Fatalf("expired ceremony executed %d epoch migrations", epochs.calls)
	}
}
