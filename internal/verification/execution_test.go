package verification

import (
	"context"
	"errors"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
)

const testULID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

type observationIDs struct{ next byte }

func (generator *observationIDs) NewObservation() (id.Observation, error) {
	last := '0' + generator.next
	generator.next++
	return id.ParseObservation("obs_01ARZ3NDEKTSV4RRFFQ69G5FA" + string(last))
}

type eventIDs struct{}

func (eventIDs) NewEvent() (id.Event, error) { return id.ParseEvent("evt_" + testULID) }

func TestProviderScenariosPreserveMeaning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		scenario synthetic.Scenario
		state    CheckState
		outcome  CheckOutcome
	}{
		{"success", synthetic.Success, CheckCompleted, CheckPassed},
		{"rejection is evidence", synthetic.Rejected, CheckCompleted, CheckNotPassed},
		{"inconclusive", synthetic.Inconclusive, CheckCompleted, CheckInconclusive},
		{"unavailable is operational", synthetic.Unavailable, CheckFailed, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check, attempt, now := runningCheck(t, RunnerProvider, 7)
			request := providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String()}
			result, err := (synthetic.Provider{Scenario: test.scenario, Now: func() time.Time { return now.Add(time.Second) }}).Execute(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyProviderResult(&check, result, &observationIDs{}, 7); err != nil {
				t.Fatal(err)
			}
			if check.State != test.state || check.Outcome != test.outcome {
				t.Fatalf("got state=%s outcome=%s", check.State, check.Outcome)
			}
			if len(check.Attempts()) != 1 {
				t.Fatal("attempt history changed")
			}
		})
	}
}

func TestDuplicateConflictRetryStaleAndCancellation(t *testing.T) {
	t.Parallel()
	check, first, now := runningCheck(t, RunnerProvider, 9)
	request := providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: first.ID.String()}
	result, err := (synthetic.Provider{Scenario: synthetic.Success, Now: func() time.Time { return now.Add(time.Second) }}).Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ids := &observationIDs{}
	if disposition, err := ApplyProviderResult(&check, result, ids, 9); err != nil || disposition != "applied" {
		t.Fatalf("first: %s %v", disposition, err)
	}
	if disposition, err := ApplyProviderResult(&check, result, ids, 9); err != nil || disposition != "duplicate" {
		t.Fatalf("duplicate: %s %v", disposition, err)
	}
	changed := result
	changed.Signals[0].Outcome = providerv1.SignalOutcomeNotSatisfied
	if disposition, err := ApplyProviderResult(&check, changed, ids, 9); !errors.Is(err, ErrAttemptConflict) || disposition != "conflict" {
		t.Fatalf("conflict: %s %v", disposition, err)
	}

	retryCheck, retryFirst, retryNow := runningCheck(t, RunnerProvider, 9)
	if _, err := retryCheck.FailAttempt(retryFirst.ID, retryFirst.Fence,
		Failure{Class: "unavailable", Code: "synthetic_unavailable", Retry: RetryBackoff}, retryNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	second := attemptFor(t, RunnerProvider, 2, 10, retryNow.Add(2*time.Second))
	if err := retryCheck.BeginAttempt(second); err != nil {
		t.Fatal(err)
	}
	lateResult := providerv1.Result{Contract: providerv1.CurrentVersion, AttemptID: retryFirst.ID.String(),
		Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeSatisfied}},
		CompletedAt: retryNow.Add(3 * time.Second)}
	if _, err := ApplyProviderResult(&retryCheck, lateResult, &observationIDs{}, retryFirst.Fence); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("stale: %v", err)
	}
	if err := retryCheck.Cancel(retryNow.Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if retryCheck.State != CheckCancelled || len(retryCheck.Attempts()) != 2 || retryCheck.Attempts()[0].State != AttemptFailed {
		t.Fatal("immutable retry/cancellation history not preserved")
	}
}

func TestDeadlineIsOperationalTimeout(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 5)
	if _, err := check.FailAttempt(attempt.ID, 5,
		Failure{Class: "deadline_exceeded", Code: "runner_deadline", Retry: RetryBackoff}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if check.State != CheckTimedOut || check.Outcome != "" || check.Attempts()[0].State != AttemptTimedOut {
		t.Fatal("deadline became evidence or lost timeout state")
	}
}

func TestModelNormalisationAndMalformedBinding(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerModel, 3)
	request := modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: attempt.ID.String()}
	adapter := synthetic.Model{Scenario: synthetic.Inconclusive, Now: func() time.Time { return now.Add(time.Second) }}
	result, err := adapter.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyModelResult(&check, result, &observationIDs{}, 3); err != nil {
		t.Fatal(err)
	}
	if check.Outcome != CheckInconclusive || check.Attempts()[0].Observations[0].RunnerKind != RunnerModel {
		t.Fatal("model provenance lost")
	}

	other, otherAttempt, otherNow := runningCheck(t, RunnerModel, 4)
	malformed, err := (synthetic.Model{Scenario: synthetic.Malformed, Now: func() time.Time { return otherNow.Add(time.Second) }}).
		Execute(context.Background(), modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: otherAttempt.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyModelResult(&other, malformed, &observationIDs{}, 4); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("malformed result accepted: %v", err)
	}
}

func TestSyntheticTimeoutHonoursCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (synthetic.Provider{Scenario: synthetic.Timeout, Now: time.Now}).Execute(ctx, providerv1.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestExecutionServicePersistsDiagnosticsAndSafeProgress(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 11)
	repository := NewMemoryCheckRepository()
	if err := repository.Insert(check); err != nil {
		t.Fatal(err)
	}
	progress := &progressRecorder{}
	reconciliation := &reconciliationRecorder{}
	service, err := NewExecutionService(repository, eventIDs{}, progress, reconciliation)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(check.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, disposition, err := service.Apply(context.Background(), scope, check.ID, func(value *Check) (string, error) {
		return value.CompleteAttempt(attempt.ID, 99, nil, now.Add(time.Second))
	})
	if !errors.Is(err, ErrStaleAttempt) || disposition != "stale" || reconciliation.calls != 1 {
		t.Fatalf("stale reconciliation: %s %v", disposition, err)
	}
	if loaded.Version != check.Version+1 || progress.calls != 0 || attempt.ID.IsZero() {
		t.Fatal("diagnostic persistence or safe publication is wrong")
	}
}

func TestExecutionServiceResultInboxDeduplicatesAtomicEffect(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 12)
	repository := NewMemoryCheckRepository()
	if err := repository.Insert(check); err != nil {
		t.Fatal(err)
	}
	progress := &progressRecorder{}
	service, err := NewExecutionService(repository, eventIDs{}, progress, &reconciliationRecorder{})
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := tenant.NewScope(check.TenantID)
	receipt, err := NewResultReceipt(attempt.ID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	mutation := func(value *Check) (string, error) {
		return value.FailAttempt(attempt.ID, attempt.Fence,
			Failure{Class: "unavailable", Code: "synthetic_unavailable", Retry: RetryBackoff}, now.Add(time.Second))
	}
	first, disposition, err := service.ApplyResult(context.Background(), scope, check.ID, receipt, mutation)
	if err != nil || disposition != "applied" || first.State != CheckFailed {
		t.Fatalf("first = %s %s %v", first.State, disposition, err)
	}
	replayedReceipt := receipt
	replayedReceipt.ReceivedAt = now.Add(2 * time.Second)
	replayed, disposition, err := service.ApplyResult(context.Background(), scope, check.ID, replayedReceipt, mutation)
	if err != nil || disposition != "duplicate" || replayed.Version != first.Version || progress.calls != 1 {
		t.Fatalf("replay = version %d disposition %s progress %d error %v", replayed.Version, disposition, progress.calls, err)
	}
}

func TestRestoreAttemptRejectsTamperedTerminalContent(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 13)
	result := providerv1.Result{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String(),
		Outcome:     providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeSatisfied}},
		CompletedAt: now.Add(time.Second)}
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence); err != nil {
		t.Fatal(err)
	}
	stored := check.Attempts()[0]
	stored.Observations[0].Signal.Outcome = SignalNotSatisfied
	if _, err := RestoreAttempt(stored, stored.ResultDigest()); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("tampered attempt restore = %v", err)
	}
}

func TestCompletedAttemptCanonicalisesAbsentReasonCodes(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 14)
	result := providerv1.Result{
		Contract:    providerv1.CurrentVersion,
		AttemptID:   attempt.ID.String(),
		Outcome:     providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeSatisfied}},
		CompletedAt: now.Add(time.Second),
	}
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence); err != nil {
		t.Fatal(err)
	}
	stored := check.Attempts()[0]
	if stored.Observations[0].Signal.ReasonCodes == nil {
		t.Fatal("absent reason codes were not canonicalised")
	}
	if _, err := RestoreAttempt(stored, stored.ResultDigest()); err != nil {
		t.Fatalf("restore canonical attempt: %v", err)
	}
}

type progressRecorder struct{ calls int }

func (recorder *progressRecorder) PublishCheckProgress(context.Context, tenant.Scope, CheckProgress) error {
	recorder.calls++
	return nil
}

type reconciliationRecorder struct{ calls int }

func (recorder *reconciliationRecorder) RequestReconciliation(context.Context, tenant.Scope, id.Check, id.Attempt, string) error {
	recorder.calls++
	return nil
}

func runningCheck(t *testing.T, kind RunnerKind, fence uint64) (Check, Attempt, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	checkID, _ := id.ParseCheck("chk_" + testULID)
	tenantID, _ := id.ParseTenant("ten_" + testULID)
	verificationID, _ := id.ParseVerification("ver_" + testULID)
	check, err := NewCheck(checkID, tenantID, verificationID, "document.authenticity", now)
	if err != nil {
		t.Fatal(err)
	}
	attempt := attemptFor(t, kind, 1, fence, now)
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	return check, attempt, now
}

func attemptFor(t *testing.T, kind RunnerKind, number uint32, fence uint64, now time.Time) Attempt {
	t.Helper()
	encoded := "atm_" + testULID
	if number == 2 {
		encoded = "atm_01ARZ3NDEKTSV4RRFFQ69G5FAW"
	}
	attemptID, err := id.ParseAttempt(encoded)
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return Attempt{ID: attemptID, Number: number, Fence: fence, RunnerKind: kind, State: AttemptRunning,
		Provenance: Provenance{RunnerID: "synthetic.runner", RunnerVersion: "1.0.0", PackageDigest: digest,
			ContractMajor: 1, RequestDigest: digest, Configuration: digest}, StartedAt: now, Deadline: now.Add(time.Minute)}
}
