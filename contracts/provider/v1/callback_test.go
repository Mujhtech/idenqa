package provider_test

import (
	"strings"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

func contractRequest() providerv1.Request {
	digest := "sha256:" + strings.Repeat("0", 64)
	capability := providerv1.Capability{
		Check: "document", AcceptedEvidence: []string{"document"}, AcceptedAssurances: []string{"uploaded"},
		ProcessingRegions: []string{"eu"}, SupportsIdempotency: true,
	}
	reference := providerv1.ConfigurationReference{
		ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: digest,
		SecretReference: "secret://provider/test", CredentialVersion: "1.0.0",
	}
	return providerv1.Request{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		ProviderID: reference.ProviderID, TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Check: capability.Check,
		IdempotencyKey: "provider-attempt-idempotency",
		Adapter: providerv1.PackageProvenance{
			AdapterID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterVersion: "1.0.0",
			PackageDigest: digest, Contract: providerv1.CurrentVersion,
		},
		Capability: capability,
		Restrictions: providerv1.Restrictions{
			MaximumGrants: 1, MaximumResultSize: providerv1.MaxResultBytes, MaximumDuration: time.Minute,
		},
		Configuration: reference,
		Evidence: []providerv1.EvidenceGrantReference{{
			GrantID: "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH", RedemptionID: "rdm_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
			EvidenceID: "evd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Purpose: "document_check",
			Variant: "front", ExpiresAt: time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC),
		}},
		Deadline: time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC),
	}
}

func TestCallbackReferenceIsAdditiveAndBounded(t *testing.T) {
	t.Parallel()
	request := contractRequest()
	if err := request.Validate(); err != nil {
		t.Fatalf("absent callback reference rejected: %v", err)
	}
	request.CallbackReference = "pcb_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	if err := request.Validate(); err != nil {
		t.Fatalf("valid callback reference rejected: %v", err)
	}
	for _, invalid := range []string{"01K4AR9V8FQ2G7ZXCPNM5T6JWH", "pcb_not-a-ulid", "pcb_01k4ar9v8fq2g7zxcpnm5t6jwh"} {
		request.CallbackReference = invalid
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid callback reference %q accepted", invalid)
		}
	}
}

func TestCallbackEnvelopeBounds(t *testing.T) {
	t.Parallel()
	valid := providerv1.CallbackEnvelope{
		Method:  "POST",
		Headers: []providerv1.CallbackHeader{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"status":"clear"}`),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	oversized := valid
	oversized.Body = make([]byte, providerv1.MaxCallbackBodyBytes+1)
	if err := oversized.Validate(); err == nil {
		t.Fatal("oversized callback body accepted")
	}
	duplicate := valid
	duplicate.Headers = []providerv1.CallbackHeader{
		{Name: "Response-Signature", Value: "a"},
		{Name: "response-signature", Value: "b"},
	}
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate callback headers accepted")
	}
	tooMany := valid
	tooMany.Headers = make([]providerv1.CallbackHeader, providerv1.MaxCallbackHeaders+1)
	for index := range tooMany.Headers {
		tooMany.Headers[index] = providerv1.CallbackHeader{Name: "X-Header", Value: "value"}
	}
	if err := tooMany.Validate(); err == nil {
		t.Fatal("excessive callback headers accepted")
	}
	control := valid
	control.Headers = []providerv1.CallbackHeader{{Name: "X-Header", Value: "bad\nvalue"}}
	if err := control.Validate(); err == nil {
		t.Fatal("control characters in a callback header accepted")
	}
	missingBody := valid
	missingBody.Body = nil
	if err := missingBody.Validate(); err == nil {
		t.Fatal("empty callback body accepted")
	}
}

func TestCallbackRejectionClassification(t *testing.T) {
	t.Parallel()
	signature, ok := providerv1.AsCallbackRejection(providerv1.Reject(providerv1.CallbackRejectionSignature, "response-signature"))
	if !ok || !signature.Permission() {
		t.Fatal("signature rejection must be permission class")
	}
	malformed, ok := providerv1.AsCallbackRejection(providerv1.Reject(providerv1.CallbackRejectionMalformed, "body"))
	if !ok || malformed.Permission() {
		t.Fatal("malformed rejection must not be permission class")
	}
	if _, ok := providerv1.AsCallbackRejection(providerv1.Reject(providerv1.CallbackRejectionStale, "response-timestamp")); !ok {
		t.Fatal("typed rejection not recovered")
	}
}
