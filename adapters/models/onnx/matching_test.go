package onnx

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

type matchingPredictor struct {
	value Evaluation
	calls int
}

func (p *matchingPredictor) Infer(context.Context, []float32) (float64, error) { return 0, ErrRuntime }
func (p *matchingPredictor) InferPair(context.Context, Image, Image) (Evaluation, error) {
	p.calls++
	return p.value, nil
}
func matchingFixture(t *testing.T) (Configuration, modelv1.Request, []byte, time.Time) {
	t.Helper()
	c, r, raw, now := adapterFixture(t)
	c.FaceMatching = true
	c.Width = 112
	c.Height = 112
	c.FacePreparation = &FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: c.Manifest.Provenance.ModelDigest, Alignment: "arcface-five-point-v1"}
	c.Manifest.Provenance.PreprocessingDigest = MatchingPreprocessingDigest(*c.FacePreparation)
	c.Manifest.Provenance.OutputSchemaDigest = MatchingOutputSchemaDigest()
	c.Manifest.Restrictions.MaximumGrants = 2
	c.Manifest.Capabilities = []modelv1.Capability{{Evaluation: "idenqa.check.face_match_1to1", AcceptedEvidence: []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.face_match_1to1"}}}
	c.Registration.ConfigurationDigest = ConfigurationDigest(c)
	r.Provenance = c.Manifest.Provenance
	r.Configuration = c.Registration
	r.Restrictions = c.Manifest.Restrictions
	r.Capability = c.Manifest.Capabilities[0]
	r.Evaluation = r.Capability.Evaluation
	doc := r.Evidence[0]
	doc.Variant = "document.front"
	doc.GrantID = "grt_01ARZ3NDEKTSV4RRFFQ69G5FAW"
	doc.EvidenceID = "evd_01ARZ3NDEKTSV4RRFFQ69G5FAW"
	doc.RedemptionID = "rdm_01ARZ3NDEKTSV4RRFFQ69G5FAW"
	r.Evidence = append(r.Evidence, doc)
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return c, r, raw, now
}
func TestMatchingAdapterNeverAssertsIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   Evaluation
		failure bool
	}{
		{"identical", Evaluation{Score: matchingFloat(1)}, false},
		{"opposite", Evaluation{Score: matchingFloat(-1)}, false},
		{"quality", Evaluation{Reason: "document_multiple_faces"}, false},
		{"nonfinite", Evaluation{Score: matchingFloat(math.NaN())}, true},
		{"empty", Evaluation{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r, raw, now := matchingFixture(t)
			p := &matchingPredictor{value: tc.value}
			a, err := New(c, p, &testReader{raw: raw}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Execute(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if tc.failure {
				if result.Failure == nil {
					t.Fatal("invalid output accepted")
				}
				return
			}
			if result.ValidateForRequest(r) != nil || len(result.Signals) != 1 || result.Signals[0].Outcome != modelv1.SignalOutcomeInconclusive {
				t.Fatal("matching established identity", result)
			}
		})
	}
}
func TestMatchingAdapterRejectsInvalidPairBeforeReading(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*modelv1.Request)
	}{
		{"missing document", func(r *modelv1.Request) { r.Evidence = r.Evidence[:1] }},
		{"duplicate role", func(r *modelv1.Request) { r.Evidence[1].Variant = "selfie" }},
		{"same asset", func(r *modelv1.Request) { r.Evidence[1].EvidenceID = r.Evidence[0].EvidenceID }},
		{"wrong role", func(r *modelv1.Request) { r.Evidence[1].Variant = "document.back" }},
		{"tenant", func(r *modelv1.Request) { r.TenantID = "ten_01ARZ3NDEKTSV4RRFFQ69G5FAW" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r, raw, now := matchingFixture(t)
			p := &matchingPredictor{}
			reader := &testReader{raw: raw}
			a, err := New(c, p, reader, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&r)
			if _, err := a.Execute(t.Context(), r); err == nil || p.calls != 0 || reader.calls != 0 {
				t.Fatal("invalid pair executed")
			}
		})
	}
}
func matchingFloat(v float64) *float64 { return &v }
func TestNativeMatchingCosineAndSchema(t *testing.T) {
	python := os.Getenv("ONNX_TEST_PYTHON")
	if python == "" {
		t.Skip("native ONNX environment required")
	}
	runtime, err := RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	model, err := os.ReadFile("testdata/embedding_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	detector, err := os.ReadFile("testdata/detector_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	modelPath, _ := filepath.Abs("testdata/embedding_fixture.onnx")
	detectorPath, _ := filepath.Abs("testdata/detector_fixture.onnx")
	prep := FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: digest(detector), Alignment: "arcface-five-point-v1"}
	engine, err := NewMatchingEngine(t.Context(), python, modelPath, digest(model), runtime, detectorPath, prep)
	if err != nil {
		t.Fatal(err)
	}
	a := Image{Width: 128, Height: 128, RGB: bytes.Repeat([]byte{128, 64, 32}, 128*128)}
	b := Image{Width: 128, Height: 128, RGB: bytes.Repeat([]byte{255, 255, 255}, 128*128)}
	same, err := engine.InferPair(t.Context(), a, a)
	if err != nil || same.Score == nil || math.Abs(*same.Score-1) > 1e-6 {
		t.Fatal("identical cosine", same, err)
	}
	different, err := engine.InferPair(t.Context(), a, b)
	if err != nil || different.Score == nil || *different.Score >= 0.999 {
		t.Fatal("fixture did not distinguish inputs", different, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := engine.InferPair(ctx, a, a); err == nil {
		t.Fatal("cancelled pair executed")
	}
	if _, err := NewEngine(t.Context(), python, modelPath, digest(model), runtime, []int{1, 3, 128, 128}); err == nil {
		t.Fatal("embedding accepted as PAD")
	}
}

func TestMatchingAdapterEnforcesTotalInputBound(t *testing.T) {
	t.Parallel()
	c, r, raw, now := matchingFixture(t)
	c.Manifest.Restrictions.MaximumInputBytes = uint64(len(raw))*2 - 1
	c.Registration.ConfigurationDigest = ConfigurationDigest(c)
	r.Restrictions = c.Manifest.Restrictions
	r.Configuration = c.Registration
	p := &matchingPredictor{value: Evaluation{Score: matchingFloat(1)}}
	a, err := New(c, p, &testReader{raw: raw}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Execute(t.Context(), r)
	if err != nil || result.Failure == nil || result.Failure.Class != modelv1.FailureInvalidInput || p.calls != 0 {
		t.Fatal("combined input limit not enforced", result, err)
	}
}
