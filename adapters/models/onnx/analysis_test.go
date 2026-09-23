package onnx

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

type analysisPredictor struct{ values []AnalysisEvaluation }

func (*analysisPredictor) Infer(context.Context, []float32) (float64, error) { return 0, ErrRuntime }
func (p *analysisPredictor) InferAnalysis(context.Context, Image) (AnalysisEvaluation, error) {
	value := p.values[0]
	p.values = p.values[1:]
	return value, nil
}

func TestSelfieAnalysisRemainsInconclusive(t *testing.T) {
	t.Parallel()
	configuration, request, raw, now := adapterFixture(t)
	configuration.SelfieAnalysis = true
	preparation := FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: configuration.Manifest.Provenance.ModelDigest}
	configuration.FacePreparation = &preparation
	configuration.Manifest.Provenance.PreprocessingDigest = AnalysisPreprocessingDigest(preparation)
	configuration.Manifest.Provenance.OutputSchemaDigest = AnalysisOutputSchemaDigest(true)
	configuration.Manifest.Capabilities = []modelv1.Capability{{
		Evaluation: modelv1.EvaluationSelfieAnalysis, AcceptedEvidence: []string{"idenqa.evidence.selfie_image"},
		OutputSignals: modelv1.SelfieAnalysisSignals(true), TemporalEvidence: true,
	}}
	configuration.Manifest.Restrictions.MaximumGrants = 2
	configuration.Registration.ConfigurationRef = "configuration://model/selfie-analysis"
	configuration.Registration.ConfigurationDigest = ConfigurationDigest(configuration)
	request.Configuration = configuration.Registration
	request.Evaluation = modelv1.EvaluationSelfieAnalysis
	request.Provenance = configuration.Manifest.Provenance
	request.Capability = configuration.Manifest.Capabilities[0]
	request.Restrictions = configuration.Manifest.Restrictions
	request.Evidence = append(request.Evidence, modelv1.EvidenceGrantReference{
		GrantID: "grt_01ARZ3NDEKTSV4RRFFQ69G5FAW", RedemptionID: "rdm_01ARZ3NDEKTSV4RRFFQ69G5FAW",
		EvidenceID: "evd_01ARZ3NDEKTSV4RRFFQ69G5FAW", Purpose: request.Evidence[0].Purpose,
		Variant: "selfie", ExpiresAt: request.Evidence[0].ExpiresAt,
	})
	digest := "sha256:" + strings.Repeat("b", 64)
	request.Sequences = []modelv1.EvidenceSequence{{SequenceDigest: "sha256:" + strings.Repeat("c", 64), Frames: []modelv1.EvidenceSequenceFrame{
		{GrantID: request.Evidence[0].GrantID, ChallengeID: "idenqa.challenge.turn_left", Index: 0, CapturedAt: now.Add(time.Second), ContentDigest: digest},
		{GrantID: request.Evidence[1].GrantID, ChallengeID: "idenqa.challenge.turn_right", Index: 1, CapturedAt: now.Add(2 * time.Second), PreviousDigest: digest, ContentDigest: digest},
	}}}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	predictor := &analysisPredictor{values: []AnalysisEvaluation{{Codes: []string{"multiple_faces"}}, {Codes: []string{"glare_too_high"}}}}
	adapter, err := New(configuration, predictor, &testReader{raw: raw}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.ValidateForRequest(request) != nil || len(result.Signals) != 5 {
		t.Fatalf("analysis failed: %+v %v", result, err)
	}
	for _, signal := range result.Signals {
		if signal.Outcome != modelv1.SignalOutcomeInconclusive {
			t.Fatalf("assurance escaped: %+v", signal)
		}
	}
	if result.Signals[0].Quality == nil || !slices.Contains(result.Signals[0].ReasonCodes, "multiple_faces") ||
		result.Signals[3].Quality == nil || !slices.Contains(result.Signals[3].ReasonCodes, "glare_too_high") ||
		result.Signals[4].Quality == nil || !slices.Contains(result.Signals[4].ReasonCodes, "temporal_duplicate_frame") {
		t.Fatalf("quality classifications missing: %+v", result.Signals)
	}
}
