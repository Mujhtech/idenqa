//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

// documentObservationSentinels are unique raw values that must never reach
// durable provider, receipt, observation, outbox, or realtime state.
func documentObservationSentinels() []string {
	return []string{"%X10000001%", "%DCSBARCODE%", "%P<UTODOE<<JOHN%", "%Doe%"}
}

// fingerprintRecordingStore captures the exact receipt fingerprint presented by
// the task commit without changing its transactional effect.
type fingerprintRecordingStore struct {
	verificationtask.CheckStore
	mu           sync.Mutex
	fingerprints []string
}

func (store *fingerprintRecordingStore) SaveCheckWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction postgres.Transaction,
	commit verification.CheckCommit,
) (bool, error) {
	if commit.Receipt != nil {
		store.mu.Lock()
		store.fingerprints = append(store.fingerprints, commit.Receipt.Fingerprint)
		store.mu.Unlock()
	}
	return store.CheckStore.SaveCheckWithin(ctx, scope, transaction, commit)
}

func (store *fingerprintRecordingStore) recorded() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.fingerprints...)
}

// TestDocumentObservationSurvivesRestartWithoutRawPersistence proves the hybrid
// document path end to end: one transient provider observation is consumed into
// bounded Core signals, the durable dispatch preserves only those signals, a
// worker replacement adopts them with the identical receipt fingerprint, and
// the raw MRZ, barcode, and field values never reach any persisted body,
// observation, outbox, or realtime payload.
func TestDocumentObservationSurvivesRestartWithoutRawPersistence(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		ctx := t.Context()
		fixture := seedProviderRecoveryFixture(t, f)

		pending := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			if resume {
				t.Fatalf("initial submission used a recovery resume: call=%d", call)
			}
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1"}, nil
		}}
		first := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, pending, nil)
		work, result := first.prepare(t, fixture)
		if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable {
			t.Fatalf("pending execution = work=%v result=%+v", work != nil, result)
		}
		assertRecoveryPending(t, f, fixture)

		lines := integrationPassport(t, "X10000001")
		observation := providerv1.DocumentObservation{
			MRZLines:       lines,
			BarcodePayload: "@\n\x1e\rANSI 636000080002PP00410272\nDCSBARCODE\nDACJOHN\nDAQX10000001\nDCGUTO\n",
			Fields: []providerv1.DocumentField{
				{Name: "document_number", Value: "X10000001"},
				{Name: "last_name", Value: "Doe"},
			},
		}
		terminal := recoveryProviderResult(fixture, f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
		terminal.Document = &observation
		runner := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			if !resume {
				t.Fatalf("status continuation submitted instead of resuming: call=%d", call)
			}
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &terminal}, nil
		}}
		guarded, err := verificationpostgres.NewGuardedCheckStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 10*time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		injected := &failingProviderCommitStore{CheckStore: guarded}
		injected.fail.Store(true)
		firstReceipts := &fingerprintRecordingStore{CheckStore: injected}
		second := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 10*time.Second)}, runner, firstReceipts)
		work, result = second.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("terminal prepare = work=%v result=%+v", work != nil, result)
		}
		result, err = runProviderRecoveryCommit(t, f, work)
		if !errors.Is(err, errProviderRecoveryCommit) || result.Outcome != platformtask.OutcomeRetry {
			t.Fatalf("injected commit = %+v err=%v", result, err)
		}

		replacement := &recoveryAdvancer{advance: func(call int32, _ bool) (providerv1.Progress, error) {
			t.Fatalf("replacement worker polled the provider instead of adopting its persisted result: call=%d", call)
			return providerv1.Progress{}, nil
		}}
		secondReceipts := &fingerprintRecordingStore{CheckStore: guarded}
		third := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 20*time.Second)}, replacement, secondReceipts)
		work, result = third.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("replacement prepare = work=%v result=%+v", work != nil, result)
		}
		if result, err = runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("replacement commit = %+v err=%v", result, err)
		}
		assertDocumentResultCounts(t, f, fixture, true)
		if replacement.callCount() != 0 {
			t.Fatalf("replacement polled the provider %d times", replacement.callCount())
		}

		// The consumed meaning is stable across worker replacement: the receipt
		// fingerprint of the failed first commit equals the committed replay.
		before := firstReceipts.recorded()
		after := secondReceipts.recorded()
		if len(before) != 1 || len(after) != 1 || before[0] != after[0] {
			t.Fatalf("fingerprints = %v / %v", before, after)
		}

		// The persisted dispatch carries only bounded derived signals.
		var body []byte
		if err := f.admin.Native().QueryRow(ctx, `SELECT result_body FROM idenqa.provider_dispatches WHERE tenant_id=$1 AND attempt_id=$2`, f.scope.ID().String(), fixture.attemptID.String()).Scan(&body); err != nil {
			t.Fatal(err)
		}
		var stored providerv1.Result
		if json.Unmarshal(body, &stored) != nil || stored.Document != nil {
			t.Fatalf("persisted dispatch result = %s", body)
		}
		assertStoredProviderSignal(t, stored, verification.SignalDocumentMRZ, providerv1.SignalOutcomeSatisfied)
		assertStoredProviderSignal(t, stored, verification.SignalDocumentExpiry, providerv1.SignalOutcomeSatisfied)
		assertStoredProviderSignal(t, stored, verification.SignalDocumentClassification, providerv1.SignalOutcomeSatisfied)
		storedFingerprint, err := verification.ProviderResultFingerprint(stored)
		if err != nil || storedFingerprint != after[0] {
			t.Fatalf("stored fingerprint = %s, receipt = %s, err = %v", storedFingerprint, after[0], err)
		}

		// Derived observations are persisted and never carry raw values.
		check, err := readDocumentCheck(t, f, fixture)
		if err != nil {
			t.Fatal(err)
		}
		if check.Outcome == verification.CheckPassed {
			t.Fatalf("single-side observation produced an identity outcome: %s", check.Outcome)
		}
		observations := check.Attempts()[0].Observations
		assertPersistedSignal(t, observations, verification.SignalDocumentMRZ, verification.SignalSatisfied)
		assertPersistedSignal(t, observations, verification.SignalDocumentClassification, verification.SignalSatisfied)

		assertNoDocumentObservationLeak(t, f, fixture, observations)

		// A full terminal replay adds nothing.
		assertRecoveryReplayAddsNothing(t, fixture, third, func() { assertDocumentResultCounts(t, f, fixture, true) })
	})
}

// TestDocumentObservationTamperedDataNeverPasses proves tampered machine-readable
// data stays a bounded not-satisfied signal and never becomes a passing identity
// outcome, while the tampered line itself is not persisted anywhere.
func TestDocumentObservationTamperedDataNeverPasses(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		fixture := seedProviderRecoveryFixture(t, f)
		lines := integrationPassport(t, "X10000001")
		tampered := []byte(lines[1])
		if tampered[len(tampered)-1] == '0' {
			tampered[len(tampered)-1] = '1'
		} else {
			tampered[len(tampered)-1] = '0'
		}
		lines[1] = string(tampered)

		terminal := recoveryProviderResult(fixture, f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
		terminal.Document = &providerv1.DocumentObservation{MRZLines: lines}
		runner := &recoveryAdvancer{advance: func(int32, bool) (providerv1.Progress, error) {
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &terminal}, nil
		}}
		worker := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, runner, nil)
		work, result := worker.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("tampered prepare = work=%v result=%+v", work != nil, result)
		}
		if result, err := runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("tampered commit = %+v err=%v", result, err)
		}
		assertDocumentResultCounts(t, f, fixture, false)

		check, err := readDocumentCheck(t, f, fixture)
		if err != nil {
			t.Fatal(err)
		}
		if check.Outcome != verification.CheckNotPassed {
			t.Fatalf("tampered observation outcome = %s", check.Outcome)
		}
		observations := check.Attempts()[0].Observations
		assertPersistedSignal(t, observations, verification.SignalDocumentMRZ, verification.SignalNotSatisfied)
		assertNoDocumentObservationLeak(t, f, fixture, observations)
	})
}

func assertStoredProviderSignal(t *testing.T, result providerv1.Result, name string, outcome providerv1.SignalOutcome) {
	t.Helper()
	for _, signal := range result.Signals {
		if signal.Name != name {
			continue
		}
		if signal.Outcome != outcome {
			t.Fatalf("%s outcome = %s", name, signal.Outcome)
		}
		return
	}
	t.Fatalf("missing persisted provider signal %s", name)
}

// assertNoDocumentObservationLeak proves raw document values never reached any
// durable provider body, receipt, observation, outbox event, or realtime event.
func assertNoDocumentObservationLeak(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture, observations []verification.Observation) {
	t.Helper()
	sentinels := documentObservationSentinels()
	var leaked int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM idenqa.provider_dispatches d WHERE d.tenant_id=$1 AND d.attempt_id=$2 AND d.result_body::text LIKE ANY($3)) +
		(SELECT count(*) FROM idenqa.provider_requests r WHERE r.tenant_id=$1 AND r.attempt_id=$2 AND r.request_body::text LIKE ANY($3)) +
		(SELECT count(*) FROM idenqa.provider_callback_receipts c WHERE c.tenant_id=$1 AND c.attempt_id=$2 AND c.progress_body::text LIKE ANY($3)) +
		(SELECT count(*) FROM idenqa.verification_observations o WHERE o.tenant_id=$1 AND (o.signal_name::text LIKE ANY($3) OR o.reason_codes::text LIKE ANY($3))) +
		(SELECT count(*) FROM idenqa.outbox_events e WHERE e.tenant_id=$1 AND e.payload::text LIKE ANY($3)) +
		(SELECT count(*) FROM idenqa.realtime_events v WHERE v.tenant_id=$1 AND v.payload::text LIKE ANY($3))`,
		f.scope.ID().String(), fixture.attemptID.String(), sentinels).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("durable state leaked raw document values: %d rows", leaked)
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"X10000001", "DCSBARCODE", "P<UTODOE<<JOHN", "Doe"} {
		if strings.Contains(string(encoded), sentinel) {
			t.Fatalf("observations retained raw value %q", sentinel)
		}
	}
}

// assertDocumentResultCounts proves one authoritative document result produced
// exactly one inbox claim, one dispatch result, and a completed check with the
// session back in processing. When requiresExternalWait is set, the
// awaiting_external -> processing edge is also required.
func assertDocumentResultCounts(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture, requiresExternalWait bool) {
	t.Helper()
	counts := readProviderRecoveryCounts(t, f, fixture)
	if counts.inbox != 1 || counts.dispatchResults != 1 || counts.observations < 2 ||
		counts.checkState != string(verification.CheckCompleted) || counts.sessionState != string(verification.SessionStateProcessing) {
		t.Fatalf("document result state = %+v", counts)
	}
	if requiresExternalWait && (counts.awaitingExternal != 1 || counts.resumedProcessing != 1) {
		t.Fatalf("document external wait edges = %+v", counts)
	}
	if !requiresExternalWait && (counts.awaitingExternal != 0 || counts.resumedProcessing != 0) {
		t.Fatalf("document direct result created external wait edges = %+v", counts)
	}
}

// readDocumentCheck reads the exact check through a fresh tenant-scoped store.
func readDocumentCheck(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) (verification.Check, error) {
	t.Helper()
	store, err := verificationpostgres.NewGuardedCheckStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now.Add(5 * time.Minute)})
	if err != nil {
		return verification.Check{}, err
	}
	return store.FindCheck(t.Context(), f.scope, fixture.checkID)
}
