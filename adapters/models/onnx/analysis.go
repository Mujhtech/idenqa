package onnx

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

const (
	analysisRevision                = "idenqa.selfie-analysis.detector-heuristics.v1"
	maximumAnalysisSequenceDuration = 5 * time.Minute
)

// AnalysisPredictor produces bounded evaluation-only quality classifications.
// It never returns face coordinates, landmarks, crops, or embeddings.
type AnalysisPredictor interface {
	InferAnalysis(context.Context, Image) (AnalysisEvaluation, error)
}

// AnalysisEvaluation contains only stable private reason codes.
type AnalysisEvaluation struct {
	Codes []string
}

// AnalysisPreprocessingDigest pins detector preparation and experimental
// image/landmark heuristics. The thresholds are not production assurance.
func AnalysisPreprocessingDigest(preparation FacePreparation) string {
	raw, _ := json.Marshal([]any{preparation, analysisRevision, "brightness-40-220", "contrast-min20", "laplacian-variance-min50", "glare-ratio-max0.10", "roll-max15", "yaw-ratio-max0.22", "sequence-duration-max5m"})
	return digest(raw)
}

// AnalysisOutputSchemaDigest pins the independent signal vocabulary.
func AnalysisOutputSchemaDigest(temporal bool) string {
	raw, _ := json.Marshal([]any{analysisRevision, modelv1.SelfieAnalysisSignals(temporal), "inconclusive-only"})
	return digest(raw)
}

// NewAnalysisEngine creates a detector-only workload. The detector digest is
// also the model artefact digest; no unrelated PAD or matching graph is loaded.
func NewAnalysisEngine(ctx context.Context, python, runtimeDigest, detectorPath string, preparation FacePreparation) (*Engine, error) {
	if !preparation.valid() || !filepath.IsAbs(python) || !filepath.IsAbs(detectorPath) {
		return nil, ErrRuntime
	}
	engine := &Engine{mode: "face_analysis", python: python, modelDigest: preparation.DetectorDigest, runtimeDigest: runtimeDigest, shape: []int{1, 3, 128, 128}, slots: make(chan struct{}, 1)}
	return attachDetector(ctx, engine, detectorPath, preparation)
}

// InferAnalysis returns only bounded reason codes from detector and image
// heuristics. Face geometry and pixels remain inside the native subprocess.
func (engine *Engine) InferAnalysis(ctx context.Context, picture Image) (AnalysisEvaluation, error) {
	if engine.mode != "face_analysis" || len(engine.detector) == 0 || picture.Width < 1 || picture.Width > 4096 || picture.Height < 1 || picture.Height > 4096 || picture.Width*picture.Height > 4_000_000 || len(picture.RGB) != 3*picture.Width*picture.Height {
		return AnalysisEvaluation{}, ErrRuntime
	}
	select {
	case engine.slots <- struct{}{}:
		defer func() { <-engine.slots }()
	case <-ctx.Done():
		return AnalysisEvaluation{}, ctx.Err()
	}
	payload := engine.payload("analysis", nil)
	payload["rgb"], payload["image_width"], payload["image_height"] = picture.RGB, picture.Width, picture.Height
	value, err := invoke(ctx, engine.python, payload)
	if err != nil {
		return AnalysisEvaluation{}, err
	}
	if value.RuntimeDigest != engine.runtimeDigest || value.Score != nil || value.Reason != "" || len(value.Codes) > 16 {
		return AnalysisEvaluation{}, ErrRuntime
	}
	allowed := map[string]bool{
		"face_not_found": true, "multiple_faces": true, "face_too_small": true, "face_at_edge": true,
		"landmarks_invalid": true, "pose_out_of_range": true, "brightness_out_of_range": true,
		"contrast_too_low": true, "sharpness_too_low": true, "glare_too_high": true,
	}
	seen := map[string]bool{}
	for _, code := range value.Codes {
		if !allowed[code] || seen[code] {
			return AnalysisEvaluation{}, ErrRuntime
		}
		seen[code] = true
	}
	return AnalysisEvaluation{Codes: slices.Clone(value.Codes)}, nil
}

func (adapter *Adapter) executeAnalysis(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	predictor := adapter.predictor.(AnalysisPredictor)
	remaining := adapter.configuration.Manifest.Restrictions.MaximumInputBytes
	codes := []string{}
	for _, ref := range request.Evidence {
		if ref.Variant != "selfie" {
			return modelv1.Result{}, ErrRuntime
		}
		//nolint:gosec // Constructor caps the total at 10 MiB and remaining only decreases.
		raw, err := adapter.evidence.ReadModelEvidence(ctx, request, ref, int64(remaining))
		if err != nil {
			clear(raw)
			return adapter.failure(request, modelv1.FailureUnauthorized, "evidence_unavailable"), nil
		}
		if len(raw) == 0 || uint64(len(raw)) > remaining {
			clear(raw)
			return adapter.failure(request, modelv1.FailureInvalidInput, "image_invalid"), nil
		}
		remaining -= uint64(len(raw))
		picture, err := DecodeImage(ctx, raw)
		clear(raw)
		if err != nil {
			return adapter.failure(request, modelv1.FailureInvalidInput, "image_invalid"), nil
		}
		value, inferErr := predictor.InferAnalysis(ctx, picture)
		clear(picture.RGB)
		if inferErr != nil {
			class, code := modelv1.FailureUnavailable, "analysis_unavailable"
			if errors.Is(inferErr, context.DeadlineExceeded) {
				class, code = modelv1.FailureDeadline, "analysis_deadline"
			} else if errors.Is(inferErr, context.Canceled) {
				class, code = modelv1.FailureCancelled, "analysis_cancelled"
			}
			return adapter.failure(request, class, code), nil
		}
		for _, code := range value.Codes {
			if !slices.Contains(codes, code) {
				codes = append(codes, code)
			}
		}
	}
	if ctx.Err() != nil || !adapter.now().Before(request.Deadline) {
		return adapter.failure(request, modelv1.FailureDeadline, "analysis_deadline"), nil
	}
	signals := []modelv1.Signal{
		analysisSignal(modelv1.SignalFaceCount, "face_count_evaluation_only", codes, "face_not_found", "multiple_faces"),
		analysisSignal(modelv1.SignalFacePosition, "face_position_evaluation_only", codes, "face_not_found", "multiple_faces", "face_too_small", "face_at_edge"),
		analysisSignal(modelv1.SignalHeadPose, "head_pose_evaluation_only", codes, "face_not_found", "multiple_faces", "face_too_small", "face_at_edge", "landmarks_invalid", "pose_out_of_range"),
		analysisSignal(modelv1.SignalSelfieQuality, "selfie_quality_evaluation_only", codes, "brightness_out_of_range", "contrast_too_low", "sharpness_too_low", "glare_too_high"),
	}
	if request.Capability.TemporalEvidence {
		temporalCodes := temporalAnalysisCodes(request)
		signals = append(signals, analysisSignal(modelv1.SignalTemporalIntegrity, "temporal_integrity_evaluation_only", temporalCodes, "temporal_duplicate_frame", "temporal_duration_invalid"))
	}
	return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted, CompletedAt: adapter.now().UTC(), Signals: signals}, nil
}

func analysisSignal(name, fallback string, observed []string, relevant ...string) modelv1.Signal {
	reasons := []string{}
	for _, code := range relevant {
		if slices.Contains(observed, code) {
			reasons = append(reasons, code)
		}
	}
	var quality *modelv1.SignalQuality
	if len(reasons) == 0 {
		reasons = []string{fallback}
	} else {
		quality = &modelv1.SignalQuality{Acceptable: false, Codes: slices.Clone(reasons)}
	}
	return modelv1.Signal{Name: name, Outcome: modelv1.SignalOutcomeInconclusive, ReasonCodes: reasons, Quality: quality}
}

func temporalAnalysisCodes(request modelv1.Request) []string {
	if len(request.Sequences) != 1 || len(request.Sequences[0].Frames) < 2 {
		return []string{"temporal_duration_invalid"}
	}
	frames := request.Sequences[0].Frames
	seen := map[string]bool{}
	codes := []string{}
	for _, frame := range frames {
		if seen[frame.ContentDigest] && !slices.Contains(codes, "temporal_duplicate_frame") {
			codes = append(codes, "temporal_duplicate_frame")
		}
		seen[frame.ContentDigest] = true
	}
	duration := frames[len(frames)-1].CapturedAt.Sub(frames[0].CapturedAt)
	if duration <= 0 || duration > maximumAnalysisSequenceDuration {
		codes = append(codes, "temporal_duration_invalid")
	}
	return codes
}
