package onnx

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

type predictFunc func(context.Context, []float32) (float64, error)

func (f predictFunc) Infer(c context.Context, v []float32) (float64, error) { return f(c, v) }

type testReader struct {
	raw   []byte
	calls int
}

func (r *testReader) ReadModelEvidence(context.Context, modelv1.Request, modelv1.EvidenceGrantReference, int64) ([]byte, error) {
	r.calls++
	return bytes.Clone(r.raw), nil
}
func adapterFixture(t *testing.T) (Configuration, modelv1.Request, []byte, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	hash := "sha256:" + strings.Repeat("a", 64)
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	configuration := Configuration{TenantID: "ten_" + id, Width: 128, Height: 128, EvaluationOnly: true, Registration: modelv1.ConfigurationReference{ModelID: "mdl_" + id, ConfigurationRef: "configuration://model/pad"}, Manifest: modelv1.Manifest{Provenance: modelv1.Provenance{ModelID: "mdl_" + id, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: PreprocessingDigest(128, 128), OutputSchemaDigest: OutputSchemaDigest(), Contract: modelv1.CurrentVersion}, Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}}, Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 10 << 20, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}}}
	configuration.Registration.ConfigurationDigest = ConfigurationDigest(configuration)
	request := modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: "atm_" + id, ModelID: configuration.Registration.ModelID, TenantID: configuration.TenantID, VerificationID: "ver_" + id, Evaluation: configuration.Manifest.Capabilities[0].Evaluation, IdempotencyKey: "onnx-pad-fixture-idempotency", Provenance: configuration.Manifest.Provenance, Capability: configuration.Manifest.Capabilities[0], Restrictions: configuration.Manifest.Restrictions, Configuration: configuration.Registration, Deadline: now.Add(25 * time.Second), Evidence: []modelv1.EvidenceGrantReference{{GrantID: "grt_" + id, RedemptionID: "rdm_" + id, EvidenceID: "evd_" + id, Purpose: "idenqa.purpose.identity_verification", Variant: "selfie", ExpiresAt: now.Add(time.Minute)}}}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			picture.Set(x, y, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, picture); err != nil {
		t.Fatal(err)
	}
	return configuration, request, output.Bytes(), now
}
func TestAdapterCandidateNeverEstablishesLiveness(t *testing.T) {
	t.Parallel()
	configuration, request, raw, now := adapterFixture(t)
	for _, score := range []float64{0, 0.5, 1} {
		t.Run(fmt.Sprint(score), func(t *testing.T) {
			reader := &testReader{raw: raw}
			adapter, err := New(configuration, predictFunc(func(_ context.Context, values []float32) (float64, error) {
				if len(values) != 3*128*128 || math.Abs(float64(values[0])-128.0/255) > 1e-6 {
					t.Fatal("RGB tensor conversion")
				}
				return score, nil
			}), reader, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(t.Context(), request)
			if err != nil || result.ValidateForRequest(request) != nil || result.Outcome != modelv1.ResultOutcomeCompleted || result.Signals[0].Outcome != modelv1.SignalOutcomeInconclusive || result.Signals[0].Name != "idenqa.signal.passive_pad" {
				t.Fatalf("evaluation claim escaped: %+v %v", result, err)
			}
		})
	}
}
func TestAdapterRejectsMismatchedScopeBeforeEvidence(t *testing.T) {
	t.Parallel()
	configuration, request, raw, now := adapterFixture(t)
	reader := &testReader{raw: raw}
	adapter, err := New(configuration, predictFunc(func(context.Context, []float32) (float64, error) { t.Fatal("unexpected inference"); return 0, nil }), reader, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request.TenantID = "ten_01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if _, err := adapter.Execute(t.Context(), request); err == nil || reader.calls != 0 {
		t.Fatal("cross-tenant read allowed")
	}
	configuration.EvaluationOnly = false
	configuration.Registration.ConfigurationDigest = ConfigurationDigest(configuration)
	if _, err := New(configuration, adapter.predictor, reader, func() time.Time { return now }); err == nil {
		t.Fatal("unapproved production activation")
	}
}

func TestAdapterRejectsInvalidEvidenceAndNativeResults(t *testing.T) {
	for _, tc := range []struct {
		name           string
		invalidImage   bool
		score          float64
		inferenceError error
		want           modelv1.FailureClass
	}{
		{name: "bad image", invalidImage: true, want: modelv1.FailureInvalidInput},
		{name: "nonfinite output", score: math.NaN(), want: modelv1.FailureInternal},
		{name: "out of range", score: 2, want: modelv1.FailureInternal},
		{name: "deadline", inferenceError: context.DeadlineExceeded, want: modelv1.FailureDeadline},
		{name: "cancelled", inferenceError: context.Canceled, want: modelv1.FailureCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration, request, raw, now := adapterFixture(t)
			if tc.invalidImage {
				raw = []byte("not an image")
			}
			adapter, err := New(configuration, predictFunc(func(context.Context, []float32) (float64, error) {
				if tc.invalidImage {
					t.Fatal("invalid image reached native execution")
				}
				return tc.score, tc.inferenceError
			}), &testReader{raw: raw}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(t.Context(), request)
			if err != nil || result.ValidateForRequest(request) != nil || result.Failure == nil || result.Failure.Class != tc.want {
				t.Fatalf("unsafe failure: %+v %v", result, err)
			}
		})
	}
}
