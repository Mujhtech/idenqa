package model_test

import (
	"context"
	"strings"
	"testing"
	"time"

	modelconformance "github.com/Mujhtech/idenqa/conformance/model"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

const modelULID = "01K3P4NQF00000000000000000"

type adapter struct {
	manifest modelv1.Manifest
	result   modelv1.Result
}

func (candidate adapter) Manifest(context.Context) (modelv1.Manifest, error) {
	return candidate.manifest, nil
}
func (adapter) ValidateConfiguration(context.Context, modelv1.ConfigurationReference) error {
	return nil
}
func (candidate adapter) Execute(context.Context, modelv1.Request) (modelv1.Result, error) {
	return candidate.result, nil
}
func (adapter) Health(context.Context) (modelv1.Health, error) {
	return modelv1.Health{State: modelv1.HealthReady, Code: "ready", CheckedAt: time.Date(2026, 8, 30, 12, 0, 1, 0, time.UTC)}, nil
}

func TestCheckAcceptsConformingAdapter(t *testing.T) {
	t.Parallel()

	fixture, manifest, result := modelFixture()
	if err := modelconformance.Check(t.Context(), adapter{manifest: manifest, result: result}, fixture); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestCheckRejectsDeliberatelyNonconformingAdapter(t *testing.T) {
	t.Parallel()

	fixture, manifest, result := modelFixture()
	manifest.Restrictions.MaximumInputBytes++
	if err := modelconformance.Check(t.Context(), adapter{manifest: manifest, result: result}, fixture); err == nil ||
		!strings.Contains(err.Error(), "does not pin advertised manifest") {
		t.Fatalf("Check() error = %v, want manifest-pinning rejection", err)
	}
}

func modelFixture() (modelconformance.Fixture, modelv1.Manifest, modelv1.Result) {
	digest := "sha256:" + strings.Repeat("b", 64)
	provenance := modelv1.Provenance{
		ModelID: "com.example.model.document", ModelVersion: "2.1.0", ModelDigest: digest,
		RuntimeDigest: digest, PreprocessingDigest: digest, OutputSchemaDigest: digest,
		Contract: modelv1.CurrentVersion,
	}
	capability := modelv1.Capability{
		Evaluation: "idenqa.evaluation.document_quality", AcceptedEvidence: []string{"idenqa.evidence.document_image"},
		RequiredAssurances: []string{"idenqa.assurance.capture_freshness"},
		OutputSignals:      []string{"idenqa.signal.document.quality"},
	}
	restrictions := modelv1.Restrictions{
		MaximumGrants: 1, MaximumInputBytes: 16 * 1024 * 1024,
		MaximumResultSize: 16 * 1024, MaximumDuration: time.Minute,
	}
	configuration := modelv1.ConfigurationReference{
		ModelID: "mdl_" + modelULID, ConfigurationDigest: digest,
		ConfigurationRef: "configuration://models/document/v1",
	}
	request := modelv1.Request{
		Contract: modelv1.CurrentVersion, AttemptID: "atm_" + modelULID,
		ModelID: configuration.ModelID, TenantID: "ten_" + modelULID,
		VerificationID: "ver_" + modelULID, Evaluation: capability.Evaluation,
		IdempotencyKey: "model-attempt-000001", Provenance: provenance, Capability: capability,
		Restrictions: restrictions, Configuration: configuration,
		Evidence: []modelv1.EvidenceGrantReference{{
			GrantID: "grt_" + modelULID, RedemptionID: "rdm_" + modelULID,
			EvidenceID: "evd_" + modelULID, Purpose: "idenqa.purpose.identity_verification",
			Variant: "evidence.variant.original", ExpiresAt: time.Date(2026, 8, 30, 12, 5, 0, 0, time.UTC),
		}},
		Deadline: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC),
		Trace:    modelv1.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
	}
	manifest := modelv1.Manifest{Provenance: provenance, Capabilities: []modelv1.Capability{capability}, Restrictions: restrictions}
	result := modelv1.Result{
		Contract: modelv1.CurrentVersion, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted,
		Signals:     []modelv1.Signal{{Name: "idenqa.signal.document.quality", Outcome: modelv1.SignalOutcomeSatisfied, ReasonCodes: []string{"quality_ok"}}},
		CompletedAt: time.Date(2026, 8, 30, 12, 0, 1, 0, time.UTC),
	}
	return modelconformance.Fixture{Configuration: configuration, Request: request}, manifest, result
}
