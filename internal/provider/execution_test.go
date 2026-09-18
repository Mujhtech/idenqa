package provider_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type receiptStore struct {
	mu      sync.Mutex
	claimed bool
	result  *providerv1.Result
}

func (*receiptStore) Load(context.Context, tenant.Scope, verification.Check, verification.Attempt) (providerv1.Request, error) {
	return providerv1.Request{}, errors.New("unused")
}
func (s *receiptStore) Claim(context.Context, providerv1.Request) (bool, *providerv1.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed {
		return false, s.result, nil
	}
	s.claimed = true
	return true, nil, nil
}
func (s *receiptStore) Complete(_ context.Context, _ providerv1.Request, r providerv1.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = &r
	return nil
}

type executorFunc func(context.Context, providerv1.Request) (providerv1.Result, error)

func (f executorFunc) Execute(c context.Context, r providerv1.Request) (providerv1.Result, error) {
	return f(c, r)
}
func runtimeRequest(t *testing.T) providerv1.Request {
	t.Helper()
	m := dojah.Description()
	capability := m.Capabilities[2]
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	ref := providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: m.Configuration.Digest, SecretReference: "secret://provider/dojah/test", CredentialVersion: "v1"}
	r := providerv1.Request{Contract: m.Package.Contract, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ProviderID: ref.ProviderID, TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Check: capability.Check, IdempotencyKey: "idenqa-fixture-key", Adapter: m.Package, Capability: capability, Restrictions: m.Restrictions, Configuration: ref, Deadline: now.Add(time.Minute), Evidence: []providerv1.EvidenceGrantReference{{GrantID: "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH", RedemptionID: "rdm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", EvidenceID: "evd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Purpose: "idenqa.purpose.identity_verification", Variant: "document.front", ExpiresAt: now.Add(time.Minute)}}}
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
	remote := executorFunc(func(context.Context, providerv1.Request) (providerv1.Result, error) {
		calls.Add(1)
		close(entered)
		<-release
		return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: request.Deadline.Add(-time.Second)}, nil
	})
	executor, err := provider.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
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
	if !errors.Is(duplicateErr, provider.ErrDispatchPending) {
		t.Fatalf("duplicate prematurely produced a terminal result: %v", duplicateErr)
	}
	result, err := executor.Execute(t.Context(), request)
	if err != nil || result.Outcome != providerv1.ResultOutcomeCompleted || calls.Load() != 1 {
		t.Fatalf("recovery: %v %v calls=%d", result.Outcome, err, calls.Load())
	}
}
func TestDurableDispatchPersistsAmbiguousResult(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	var calls int
	store := &receiptStore{}
	remote := executorFunc(func(context.Context, providerv1.Request) (providerv1.Result, error) {
		calls++
		return providerv1.Result{}, errors.New("lost reply")
	})
	executor, err := provider.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	first, err := executor.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executor.Execute(t.Context(), request)
	if err != nil || first.Failure == nil || first.Failure.Retry != providerv1.RetryReconcile || !first.CompletedAt.Equal(second.CompletedAt) || calls != 1 {
		t.Fatalf("ambiguous result changed or resubmitted: %v calls=%d", err, calls)
	}
}
