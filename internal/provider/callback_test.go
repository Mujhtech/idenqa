package provider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

//nolint:gosec // test-only opaque callback reference, not a credential.
const callbackToken = "pcb_01K4AR9V8FQ2G7ZXCPNM5T6JWH"

type callbackResolverFunc func(context.Context, string) (provider.CallbackTarget, error)

func (function callbackResolverFunc) ResolveCallback(ctx context.Context, reference string) (provider.CallbackTarget, error) {
	return function(ctx, reference)
}

type callbackVerifierFunc func(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error)

func (function callbackVerifierFunc) VerifyCallback(ctx context.Context, request providerv1.Request, callback providerv1.CallbackEnvelope) (providerv1.Progress, error) {
	return function(ctx, request, callback)
}

type callbackReceiptsFunc func(context.Context, provider.CallbackTarget, providerv1.Progress) (provider.CallbackReceipt, error)

func (function callbackReceiptsFunc) SaveCallbackProgress(ctx context.Context, target provider.CallbackTarget, progress providerv1.Progress) (provider.CallbackReceipt, error) {
	return function(ctx, target, progress)
}

type callbackWakeupFunc func(context.Context, provider.CallbackTarget) error

func (function callbackWakeupFunc) Wake(ctx context.Context, target provider.CallbackTarget) error {
	return function(ctx, target)
}

func callbackEnvelope(body string) providerv1.CallbackEnvelope {
	return providerv1.CallbackEnvelope{
		Method:  "POST",
		Headers: []providerv1.CallbackHeader{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(body),
	}
}

func callbackRequest(t *testing.T) providerv1.Request {
	t.Helper()
	request := runtimeRequest(t)
	request.CallbackReference = callbackToken
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request
}

func callbackTarget(t *testing.T, request providerv1.Request) provider.CallbackTarget {
	t.Helper()
	tenantID, err := id.ParseTenant(request.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := id.ParseAttempt(request.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	checkID, err := id.ParseCheck("chk_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	if err != nil {
		t.Fatal(err)
	}
	return provider.CallbackTarget{Scope: scope, Request: request, CheckID: checkID, AttemptID: attemptID, Deadline: request.Deadline}
}

func TestRequestDigestIncludesCallbackReferenceOnlyWhenPresent(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	without, err := provider.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	request.CallbackReference = callbackToken
	with, err := provider.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	if without == with {
		t.Fatal("callback reference was excluded from the request digest")
	}
	request.CallbackReference = "pcb_not-a-ulid"
	if _, err := provider.RequestDigest(request); err == nil {
		t.Fatal("malformed callback reference was accepted")
	}
	digest, err := provider.CallbackTokenDigest(callbackToken)
	if err != nil || len(digest) != 64 {
		t.Fatalf("callback token digest: %q %v", digest, err)
	}
	if _, err := provider.CallbackTokenDigest("grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH"); !errors.Is(err, provider.ErrCallbackUnavailable) {
		t.Fatalf("unexpected token digest error: %v", err)
	}
}

func TestCallbackServiceDeduplicatesTerminalDeliveryAndWakesOnce(t *testing.T) {
	t.Parallel()
	request := callbackRequest(t)
	target := callbackTarget(t, request)
	result := providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
		Signals: []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: request.Deadline.Add(-time.Minute)}
	progress := providerv1.Progress{ProviderJobID: "job-123", ReplayID: "job-123", Result: &result}
	wakes := 0
	service, err := provider.NewCallbackService(
		callbackResolverFunc(func(context.Context, string) (provider.CallbackTarget, error) { return target, nil }),
		callbackVerifierFunc(func(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error) {
			return progress, nil
		}),
		callbackReceiptsFunc(func(_ context.Context, _ provider.CallbackTarget, received providerv1.Progress) (provider.CallbackReceipt, error) {
			if received.ReplayID != progress.ReplayID {
				t.Fatalf("unexpected replay identity: %+v", received)
			}
			duplicate := wakes > 0
			wakes++
			return provider.CallbackReceipt{Duplicate: duplicate, Terminal: true}, nil
		}),
		callbackWakeupFunc(func(context.Context, provider.CallbackTarget) error { return nil }),
		func() time.Time { return request.Deadline.Add(-time.Minute) },
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Handle(t.Context(), callbackToken, callbackEnvelope(`{"status":"clear"}`))
	if err != nil || first.Status != provider.CallbackAccepted {
		t.Fatalf("first delivery: %+v %v", first, err)
	}
	second, err := service.Handle(t.Context(), callbackToken, callbackEnvelope(`{"status":"clear"}`))
	if err != nil || second.Status != provider.CallbackDuplicate {
		t.Fatalf("duplicate delivery: %+v %v", second, err)
	}
}

func TestCallbackServiceFailsClosedForUnknownExpiredAndRejectedCallbacks(t *testing.T) {
	t.Parallel()
	request := callbackRequest(t)
	target := callbackTarget(t, request)
	build := func(resolver provider.CallbackResolver, verifier providerv1.CallbackVerifier, now time.Time) *provider.CallbackService {
		t.Helper()
		service, err := provider.NewCallbackService(resolver, verifier,
			callbackReceiptsFunc(func(context.Context, provider.CallbackTarget, providerv1.Progress) (provider.CallbackReceipt, error) {
				return provider.CallbackReceipt{Terminal: true}, nil
			}), nil, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	accepting := callbackVerifierFunc(func(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error) {
		return providerv1.Progress{ReplayID: "job-1"}, nil
	})
	tests := []struct {
		name    string
		service *provider.CallbackService
		token   string
		want    error
	}{
		{name: "malformed token", service: build(callbackResolverFunc(func(context.Context, string) (provider.CallbackTarget, error) {
			return target, nil
		}), accepting, request.Deadline.Add(-time.Minute)), token: "not-a-token", want: provider.ErrCallbackUnavailable},
		{name: "unknown reference", service: build(callbackResolverFunc(func(context.Context, string) (provider.CallbackTarget, error) {
			return provider.CallbackTarget{}, provider.ErrCallbackUnavailable
		}), accepting, request.Deadline.Add(-time.Minute)), token: callbackToken, want: provider.ErrCallbackUnavailable},
		{name: "expired reference", service: build(callbackResolverFunc(func(context.Context, string) (provider.CallbackTarget, error) {
			return target, nil
		}), accepting, request.Deadline.Add(time.Second)), token: callbackToken, want: provider.ErrCallbackUnavailable},
		{name: "adapter rejection", service: build(callbackResolverFunc(func(context.Context, string) (provider.CallbackTarget, error) {
			return target, nil
		}), callbackVerifierFunc(func(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error) {
			return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionSignature, "response-signature")
		}), request.Deadline.Add(-time.Minute)), token: callbackToken, want: provider.ErrCallbackRejected},
		{name: "malformed envelope", service: build(callbackResolverFunc(func(context.Context, string) (provider.CallbackTarget, error) {
			return target, nil
		}), accepting, request.Deadline.Add(-time.Minute)), token: callbackToken, want: provider.ErrCallbackInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.name == "malformed envelope" {
				if _, err := test.service.Handle(t.Context(), test.token, providerv1.CallbackEnvelope{Method: "POST"}); !errors.Is(err, test.want) {
					t.Fatalf("error = %v, want %v", err, test.want)
				}
				return
			}
			if _, err := test.service.Handle(t.Context(), test.token, callbackEnvelope(`{"status":"clear"}`)); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestProgressReplayIdentityIsBounded(t *testing.T) {
	t.Parallel()
	request := callbackRequest(t)
	valid := providerv1.Progress{ProviderJobID: "job-123", ReplayID: "job-123", Result: &providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: request.Deadline.Add(-time.Minute)}}
	if err := valid.ValidateForRequest(request); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.ReplayID = "job 123"
	if err := invalid.ValidateForRequest(request); err == nil {
		t.Fatal("unbounded replay identity was accepted")
	}
}
