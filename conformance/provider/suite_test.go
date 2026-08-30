package provider_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	providerconformance "github.com/Mujhtech/idenqa/conformance/provider"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

const providerULID = "01K3P4NQF00000000000000000"

type adapter struct {
	manifest providerv1.Manifest
	result   providerv1.Result
}

func (candidate adapter) Manifest(context.Context) (providerv1.Manifest, error) {
	return candidate.manifest, nil
}

func (adapter) ValidateConfiguration(context.Context, providerv1.ConfigurationReference) error {
	return nil
}

func (candidate adapter) Execute(context.Context, providerv1.Request) (providerv1.Result, error) {
	return candidate.result, nil
}

func (adapter) Health(context.Context) (providerv1.Health, error) {
	return providerv1.Health{
		State: providerv1.HealthReady, Code: "ready", CheckedAt: time.Date(2026, 8, 30, 12, 0, 1, 0, time.UTC),
	}, nil
}

func TestCheckAcceptsConformingAdapter(t *testing.T) {
	t.Parallel()

	fixture, manifest, result := providerFixture()
	if err := providerconformance.Check(t.Context(), adapter{manifest: manifest, result: result}, fixture); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestCheckRejectsDeliberatelyNonconformingAdapter(t *testing.T) {
	t.Parallel()

	fixture, manifest, result := providerFixture()
	result.AttemptID = "atm_01K3P4NQF00000000000000001"
	err := providerconformance.Check(t.Context(), adapter{manifest: manifest, result: result}, fixture)
	if err == nil || !strings.Contains(err.Error(), "does not bind the exact request") {
		t.Fatalf("Check() error = %v, want request-binding rejection", err)
	}
}

func TestCheckRejectsAdapterError(t *testing.T) {
	t.Parallel()

	fixture, manifest, result := providerFixture()
	broken := failingAdapter{adapter: adapter{manifest: manifest, result: result}}
	if err := providerconformance.Check(t.Context(), broken, fixture); !errors.Is(err, errAdapter) {
		t.Fatalf("Check() error = %v, want %v", err, errAdapter)
	}
}

var errAdapter = errors.New("adapter unavailable")

type failingAdapter struct{ adapter }

func (failingAdapter) Health(context.Context) (providerv1.Health, error) {
	return providerv1.Health{}, errAdapter
}

func providerFixture() (providerconformance.Fixture, providerv1.Manifest, providerv1.Result) {
	digest := "sha256:" + strings.Repeat("a", 64)
	provenance := providerv1.PackageProvenance{
		AdapterID: "com.example.provider.synthetic", AdapterVersion: "1.2.3", PackageDigest: digest,
		Contract: providerv1.CurrentVersion,
	}
	capability := providerv1.Capability{
		Check: "idenqa.check.document", AcceptedEvidence: []string{"idenqa.evidence.document_image"},
		AcceptedAssurances: []string{"idenqa.assurance.capture_freshness"}, ProcessingRegions: []string{"lagos-1"},
		SupportsIdempotency: true, SupportsCancellation: true,
	}
	restrictions := providerv1.Restrictions{
		NetworkRequired: true, MaximumGrants: 2, MaximumResultSize: 16 * 1024, MaximumDuration: time.Minute,
	}
	//nolint:gosec // This public fixture contains only a non-secret reference URI.
	configuration := providerv1.ConfigurationReference{
		ProviderID: "pvd_" + providerULID, SchemaDigest: digest,
		SecretReference: strings.Join([]string{"secret", "providers/synthetic"}, "://"), CredentialVersion: "credential-v1",
	}
	request := providerv1.Request{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_" + providerULID,
		ProviderID: configuration.ProviderID, TenantID: "ten_" + providerULID,
		VerificationID: "ver_" + providerULID, Check: capability.Check,
		IdempotencyKey: "provider-attempt-0001", Adapter: provenance, Capability: capability,
		Restrictions: restrictions, Configuration: configuration,
		Evidence: []providerv1.EvidenceGrantReference{{
			GrantID: "grt_" + providerULID, RedemptionID: "rdm_" + providerULID,
			EvidenceID: "evd_" + providerULID, Purpose: "idenqa.purpose.identity_verification",
			Variant: "evidence.variant.original", ExpiresAt: time.Date(2026, 8, 30, 12, 5, 0, 0, time.UTC),
		}},
		Deadline: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC),
		Trace:    providerv1.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
	}
	manifest := providerv1.Manifest{
		Package: provenance, Configuration: providerv1.ConfigurationSchema{ID: "com.example.provider.synthetic.config.v1", Digest: digest},
		Capabilities: []providerv1.Capability{capability}, Restrictions: restrictions,
	}
	result := providerv1.Result{
		Contract: providerv1.CurrentVersion, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: "idenqa.signal.document.valid", Outcome: providerv1.SignalOutcomeSatisfied, ReasonCodes: []string{"valid"}}},
		CompletedAt: time.Date(2026, 8, 30, 12, 0, 1, 0, time.UTC),
	}
	return providerconformance.Fixture{Configuration: configuration, Request: request}, manifest, result
}
