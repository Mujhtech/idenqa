//go:build integration

package integration_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
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

type smileCallbackSecrets struct{}

func (smileCallbackSecrets) ResolveSmileID(context.Context, string, string) (smileid.Config, error) {
	return smileid.Config{
		BaseURL: "https://testapi.smileidentity.com", PartnerID: "085", APIKey: "callback-test-key",
		CallbackURL: "https://core.example/callback", Mode: "sandbox", Region: "africa", PollInterval: time.Millisecond,
	}, nil
}

type smileCallbackInputs struct{}

func (smileCallbackInputs) ResolveProviderInput(context.Context, string) (string, error) {
	return "NG", nil
}

type smileCallbackEvidence struct{}

func (smileCallbackEvidence) ReadProviderEvidence(context.Context, providerv1.EvidenceGrantReference, int64) ([]byte, error) {
	return []byte("unused"), nil
}

type unusedSmileClient struct{}

func (unusedSmileClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("smile callback verification must not call the provider")
}

// TestSmileCallbackDocumentObservationReachesCoreWithoutRawPersistence proves
// the adapter-mapped v3 webhook id_fields observation traverses the real
// callback ingress, is consumed into bounded Core document signals, and leaves
// no raw extracted value in any receipt, dispatch, observation, outbox, or
// realtime state.
func TestSmileCallbackDocumentObservationReachesCoreWithoutRawPersistence(t *testing.T) {
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
		description := smileid.Description()
		providerID, err := f.ids.NewProvider()
		if err != nil {
			t.Fatal(err)
		}
		request := providerv1.Request{
			Contract: providerv1.CurrentVersion, ProviderID: providerID.String(),
			TenantID: f.scope.ID().String(), VerificationID: sessionID.String(),
			Check: "idenqa.check.document_biometric", IdempotencyKey: "smile-callback-document-observation",
			CallbackReference: callback.String(), Adapter: description.Package,
			Capability: description.Capabilities[0], Restrictions: description.Restrictions,
			Configuration: providerv1.ConfigurationReference{ProviderID: providerID.String(), SchemaDigest: description.Configuration.Digest, SecretReference: "secret://provider/smileid", CredentialVersion: "v1"},
			Inputs: []providerv1.InputReference{
				{Name: "idenqa.input.country", Reference: "secret://input/country"},
				{Name: "idenqa.input.id_type", Reference: "secret://input/id-type"},
			},
			Deadline: deadline,
		}
		attemptID := seedCallbackRequest(t, f, sessionID, checkID, request, started, deadline)
		request.AttemptID = attemptID
		adapter, err := smileid.New(smileCallbackSecrets{}, smileCallbackInputs{}, smileCallbackEvidence{}, unusedSmileClient{}, nil, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		requests, err := providerpostgres.NewRequestStore(f.runtime, clock.System{})
		if err != nil {
			t.Fatal(err)
		}
		service, err := provider.NewCallbackService(requests, adapter, requests, nil, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := service.Handle(ctx, callback.String(), smileDocumentCallbackEnvelope(t, request))
		if err != nil || accepted.Status != provider.CallbackAccepted {
			t.Fatalf("accepted = %+v err=%v", accepted, err)
		}
		claimed, saved, err := requests.Claim(ctx, request)
		if err != nil || !claimed || saved == nil {
			t.Fatalf("claim = %v %+v err=%v", claimed, saved, err)
		}
		if saved.Document != nil {
			t.Fatalf("document observation survived consumption: %+v", saved.Document)
		}
		assertStoredProviderSignal(t, *saved, verification.SignalDocumentExpiry, providerv1.SignalOutcomeSatisfied)
		assertStoredProviderSignal(t, *saved, verification.SignalDocumentClassification, providerv1.SignalOutcomeSatisfied)
		assertNoProviderRawLeak(t, f.admin, "SENTINELDOCUMENTNUMBER")
		assertNoProviderRawLeak(t, f.admin, "SENTINELLASTNAME")
	})
}

// smileDocumentCallbackEnvelope builds the documented v3 Document Verification
// webhook shape, including id_fields and the Response-Signature headers.
func smileDocumentCallbackEnvelope(t *testing.T, request providerv1.Request) providerv1.CallbackEnvelope {
	t.Helper()
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(map[string]any{
		"status": "clear", "message": "Verification successful", "reason": nil,
		"product": "document_verification", "created_at": timestamp, "completed_at": timestamp,
		"partner_params": map[string]any{"job_type": 6},
		"id_fields": map[string]any{
			"first_name": "SENTINELFIRSTNAME", "other_names": "Fatou", "last_name": "SENTINELLASTNAME",
			"full_name": "SENTINELFIRSTNAME SENTINELLASTNAME", "id_number": "SENTINELDOCUMENTNUMBER",
			"country": "NG", "id_type": "PASSPORT", "date_of_birth": "1992-08-22",
			"gender": "Female", "expiration_date": "2099-01-15", "nationality": nil,
			"issuance_date": "2021-01-15", "secondary_id_number": "SECONDARY",
			"address": map[string]any{"address_line_1": "123 Main Street", "country": "NG"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("callback-test-key"))
	_, _ = mac.Write([]byte(timestamp + "085sid_request"))
	return providerv1.CallbackEnvelope{
		Method: http.MethodPost,
		Headers: []providerv1.CallbackHeader{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "Response-Timestamp", Value: timestamp},
			{Name: "Response-Signature", Value: base64.StdEncoding.EncodeToString(mac.Sum(nil))},
			{Name: "Job-ID", Value: "job-123"},
			{Name: "User-ID", Value: request.VerificationID},
		},
		Body: body,
	}
}
