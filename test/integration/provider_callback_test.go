//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
)

type callbackIntegrationVerifier struct{ progress providerv1.Progress }

func (verifier callbackIntegrationVerifier) VerifyCallback(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error) {
	return verifier.progress, nil
}

func TestProviderCallbackReceiptRoundTrip(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		ctx := t.Context()
		sessionID := f.creation.Session.ID()
		callback, err := f.ids.NewProviderCallback()
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
		deadline := started.Add(10 * time.Minute)
		checkID := seedCallbackCheck(t, f, sessionID.String(), started)
		request := callbackIntegrationRequest(t, f, sessionID.String(), callback.String(), deadline)
		attemptID := seedCallbackRequest(t, f, sessionID, checkID, request, started, deadline)
		request.AttemptID = attemptID

		requests, err := providerpostgres.NewRequestStore(f.runtime, clock.System{})
		if err != nil {
			t.Fatal(err)
		}
		target, err := requests.ResolveCallback(ctx, callback.String())
		if err != nil {
			t.Fatal(err)
		}
		if target.Request.AttemptID != attemptID || target.CheckID.String() != checkID || target.Scope.ID().String() != f.scope.ID().String() {
			t.Fatalf("target = %+v", target)
		}
		if _, err := requests.ResolveCallback(ctx, "pcb_01K4AR9V8FQ2G7ZXCPNM5T6JWH"); !errors.Is(err, provider.ErrCallbackUnavailable) {
			t.Fatalf("unknown callback reference = %v", err)
		}
		result := providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
			Signals: []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: started.Add(time.Minute)}
		progress := providerv1.Progress{ProviderJobID: "job-123", ReplayID: "job-123", Result: &result}
		if err := progress.ValidateForRequest(target.Request); err != nil {
			t.Fatalf("progress binding: %v", err)
		}
		service, err := provider.NewCallbackService(requests, callbackIntegrationVerifier{progress: progress}, requests, nil, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		envelope := providerv1.CallbackEnvelope{Method: "POST", Headers: []providerv1.CallbackHeader{{Name: "Content-Type", Value: "application/json"}}, Body: []byte(`{"status":"clear"}`)}
		accepted, err := service.Handle(ctx, callback.String(), envelope)
		if err != nil || accepted.Status != provider.CallbackAccepted {
			t.Fatalf("accepted = %+v err=%v", accepted, err)
		}
		duplicate, err := service.Handle(ctx, callback.String(), envelope)
		if err != nil || duplicate.Status != provider.CallbackDuplicate {
			t.Fatalf("duplicate = %+v err=%v", duplicate, err)
		}
		claimed, saved, err := requests.Claim(ctx, request)
		if err != nil || !claimed || saved == nil || saved.AttemptID != request.AttemptID {
			t.Fatalf("claim = %v %+v err=%v", claimed, saved, err)
		}
		var stored []byte
		if err := f.admin.Native().QueryRow(ctx, `SELECT result_body FROM idenqa.provider_dispatches WHERE tenant_id=$1 AND attempt_id=$2`, request.TenantID, request.AttemptID).Scan(&stored); err != nil || len(stored) == 0 {
			t.Fatalf("receipt was not promoted into the dispatch: %v", err)
		}
		conflicting := progress
		conflicting.Result = &providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
			Signals: []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: providerv1.SignalOutcomeNotSatisfied}}, CompletedAt: started.Add(time.Minute)}
		if _, err := requests.SaveCallbackProgress(ctx, target, conflicting); !errors.Is(err, provider.ErrCallbackConflict) {
			t.Fatalf("conflicting reproduction = %v", err)
		}
	})
}

func TestProviderCallbackExpiredReferenceFailsClosed(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		ctx := t.Context()
		sessionID := f.creation.Session.ID()
		callback, err := f.ids.NewProviderCallback()
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now().UTC().Truncate(time.Microsecond).Add(-2 * time.Hour)
		deadline := started.Add(time.Hour)
		checkID := seedCallbackCheck(t, f, sessionID.String(), started)
		request := callbackIntegrationRequest(t, f, sessionID.String(), callback.String(), deadline)
		_ = seedCallbackRequest(t, f, sessionID, checkID, request, started, deadline)
		requests, err := providerpostgres.NewRequestStore(f.runtime, clock.System{})
		if err != nil {
			t.Fatal(err)
		}
		service, err := provider.NewCallbackService(requests, callbackIntegrationVerifier{}, requests, nil, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		envelope := providerv1.CallbackEnvelope{Method: "POST", Headers: []providerv1.CallbackHeader{{Name: "Content-Type", Value: "application/json"}}, Body: []byte(`{"status":"clear"}`)}
		if _, err := service.Handle(ctx, callback.String(), envelope); !errors.Is(err, provider.ErrCallbackUnavailable) {
			t.Fatalf("expired callback = %v", err)
		}
	})
}

func callbackIntegrationRequest(t *testing.T, f captureAcceptanceFixture, verificationID, reference string, deadline time.Time) providerv1.Request {
	t.Helper()
	digest := "sha256:" + strings.Repeat("4", 64)
	providerID, err := f.ids.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	request := providerv1.Request{
		Contract: providerv1.CurrentVersion, AttemptID: "", ProviderID: providerID.String(),
		TenantID: f.scope.ID().String(), VerificationID: verificationID,
		Check: "idenqa.check.document_biometric", IdempotencyKey: "callback-integration-attempt",
		CallbackReference: reference,
		Adapter:           providerv1.PackageProvenance{AdapterID: "smileid", AdapterVersion: "0.1.1", PackageDigest: "sha256:" + strings.Repeat("5", 64), Contract: providerv1.CurrentVersion},
		Capability: providerv1.Capability{Check: "idenqa.check.document_biometric", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"},
			AcceptedInputs: []string{"idenqa.input.country"}, ProcessingRegions: []string{"africa"}},
		Restrictions:  providerv1.Restrictions{MaximumGrants: 2, MaximumResultSize: providerv1.MaxResultBytes, MaximumDuration: 10 * time.Minute},
		Configuration: providerv1.ConfigurationReference{ProviderID: providerID.String(), SchemaDigest: digest, SecretReference: "secret://provider/smileid", CredentialVersion: "v1"},
		Inputs:        []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}},
		Deadline:      deadline,
	}
	return request
}

func seedCallbackCheck(t *testing.T, f captureAcceptanceFixture, verificationID string, started time.Time) string {
	t.Helper()
	checkID, err := f.ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.verification_checks(id,tenant_id,verification_id,name,state,version,created_at,updated_at) VALUES($1,$2,$3,$4,'running',1,$5,$5)`,
		checkID.String(), f.scope.ID().String(), verificationID, "idenqa.check.document_biometric", started); err != nil {
		t.Fatal(err)
	}
	return checkID.String()
}

func seedCallbackRequest(t *testing.T, f captureAcceptanceFixture, sessionID id.Verification, checkID string, request providerv1.Request, started, deadline time.Time) string {
	t.Helper()
	attemptID, err := f.ids.NewAttempt()
	if err != nil {
		t.Fatal(err)
	}
	request.AttemptID = attemptID.String()
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	tokenDigest, err := provider.CallbackTokenDigest(request.CallbackReference)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.verification_attempts(id,tenant_id,verification_id,check_id,attempt_number,fence,runner_kind,runner_id,runner_version,package_digest,contract_major,contract_minor,request_digest,configuration_digest,state,started_at,deadline) VALUES($1,$2,$3,$4,1,1,'provider','smileid','0.1.1',$5,1,1,$6,$7,'running',$8,$9)`,
		attemptID.String(), f.scope.ID().String(), sessionID.String(), checkID, strings.Repeat("5", 64), digest, strings.Repeat("6", 64), started, deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.provider_requests(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body,callback_token_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		request.TenantID, attemptID.String(), request.VerificationID, checkID, digest, body, tokenDigest); err != nil {
		t.Fatal(err)
	}
	return attemptID.String()
}
