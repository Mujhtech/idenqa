package model_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type receiptStore struct {
	mu      sync.Mutex
	claimed bool
	result  *modelv1.Result
}

func (*receiptStore) Load(context.Context, tenant.Scope, verification.Check, verification.Attempt) (modelv1.Request, error) {
	return modelv1.Request{}, errors.New("unused")
}
func (s *receiptStore) Claim(context.Context, modelv1.Request) (bool, *modelv1.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed {
		return false, s.result, nil
	}
	s.claimed = true
	return true, nil, nil
}
func (s *receiptStore) Complete(_ context.Context, _ modelv1.Request, r modelv1.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = &r
	return nil
}

type executorFunc func(context.Context, modelv1.Request) (modelv1.Result, error)

func (f executorFunc) Execute(c context.Context, r modelv1.Request) (modelv1.Result, error) {
	return f(c, r)
}
func runtimeRequest(t *testing.T) modelv1.Request {
	t.Helper()
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	hash := "sha256:" + strings.Repeat("a", 64)
	ref := modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://model/test", ConfigurationDigest: hash}
	capability := modelv1.Capability{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}
	r := modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ModelID: ref.ModelID, TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Evaluation: capability.Evaluation, IdempotencyKey: "idenqa-model-fixture-key", Provenance: modelv1.Provenance{ModelID: ref.ModelID, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: hash, OutputSchemaDigest: hash, Contract: modelv1.CurrentVersion}, Capability: capability, Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 1024, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}, Configuration: ref, Deadline: now.Add(30 * time.Second), Evidence: []modelv1.EvidenceGrantReference{{GrantID: "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH", RedemptionID: "rdm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", EvidenceID: "evd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Purpose: "idenqa.purpose.identity_verification", Variant: "selfie", ExpiresAt: now.Add(time.Minute)}}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestDurableDispatchConcurrentRetryCannotReplaceSuccess(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &receiptStore{}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	remote := executorFunc(func(context.Context, modelv1.Request) (modelv1.Result, error) {
		calls.Add(1)
		close(entered)
		<-release
		return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted, Signals: []modelv1.Signal{{Name: "idenqa.signal.passive_pad", Outcome: modelv1.SignalOutcomeInconclusive}}, CompletedAt: request.Deadline.Add(-time.Second)}, nil
	})
	executor, err := model.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := executor.Execute(t.Context(), request); done <- err }()
	<-entered
	_, duplicateErr := executor.Execute(t.Context(), request)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(duplicateErr, model.ErrDispatchPending) {
		t.Fatalf("duplicate prematurely produced a terminal result: %v", duplicateErr)
	}
	result, err := executor.Execute(t.Context(), request)
	if err != nil || result.Outcome != modelv1.ResultOutcomeCompleted || calls.Load() != 1 {
		t.Fatalf("recovery: %v %v calls=%d", result.Outcome, err, calls.Load())
	}
}
func TestDurableDispatchPersistsAmbiguousResult(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	var calls int
	store := &receiptStore{}
	remote := executorFunc(func(context.Context, modelv1.Request) (modelv1.Result, error) {
		calls++
		return modelv1.Result{}, errors.New("lost reply")
	})
	executor, err := model.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	first, err := executor.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executor.Execute(t.Context(), request)
	if err != nil || first.Failure == nil || first.Failure.Retry != modelv1.RetryReconcile || !first.CompletedAt.Equal(second.CompletedAt) || calls != 1 {
		t.Fatalf("ambiguous result changed or resubmitted: %v calls=%d", err, calls)
	}
}
