package onnx

import (
	"context"
	"encoding/json"
	"math"
	"strings"
)

// PairPredictor keeps document/selfie pixels and embeddings inside the model workload.
type PairPredictor interface {
	InferPair(context.Context, Image, Image) (Evaluation, error)
}

// MatchingPreprocessingDigest pins evaluation-only box extraction, not landmark alignment.
func MatchingPreprocessingDigest(preparation FacePreparation) string {
	raw, _ := json.Marshal([]any{preparation, "bgr-linear-640-zero-pad", "confidence-0.8-nms-0.3-top5000", "min64-margin5", "single-box-crop-rgb-linear112-nchw-minus127.5-div127.5.v1"})
	return digest(raw)
}

// MatchingOutputSchemaDigest pins 512-dimensional embeddings and cosine comparison.
func MatchingOutputSchemaDigest() string {
	return digest([]byte("idenqa.face-match.v1:float32[1,512]:finite-nonzero-l2:cosine[-1,1]:evaluation-only"))
}

// NewMatchingEngine verifies a synthetic-compatible embedding schema and pinned detector.
func NewMatchingEngine(ctx context.Context, python, modelPath, modelDigest, runtimeDigest, detectorPath string, preparation FacePreparation) (*Engine, error) {
	engine, err := newEngine(ctx, python, modelPath, modelDigest, runtimeDigest, []int{1, 3, 112, 112}, "face_match")
	if err != nil {
		return nil, err
	}
	return attachDetector(ctx, engine, detectorPath, preparation)
}

// InferPair returns only cosine similarity or a quality nonresponse; embeddings never leave native execution.
func (engine *Engine) InferPair(ctx context.Context, document, selfie Image) (Evaluation, error) {
	if engine.mode != "face_match" || len(engine.detector) == 0 {
		return Evaluation{}, ErrRuntime
	}
	payload := engine.payload("pair", nil)
	for role, picture := range map[string]Image{"document": document, "selfie": selfie} {
		if picture.Width < 1 || picture.Width > 4096 || picture.Height < 1 || picture.Height > 4096 || picture.Width*picture.Height > 4_000_000 || len(picture.RGB) != 3*picture.Width*picture.Height {
			return Evaluation{}, ErrRuntime
		}
		payload[role] = map[string]any{"rgb": picture.RGB, "image_width": picture.Width, "image_height": picture.Height}
	}
	select {
	case engine.slots <- struct{}{}:
		defer func() { <-engine.slots }()
	case <-ctx.Done():
		return Evaluation{}, ctx.Err()
	}
	value, err := invoke(ctx, engine.python, payload)
	if err != nil {
		return Evaluation{}, err
	}
	if value.RuntimeDigest != engine.runtimeDigest {
		return Evaluation{}, ErrRuntime
	}
	result := Evaluation{Score: value.Score, Reason: value.Reason}
	if !validMatchingEvaluation(result) {
		return Evaluation{}, ErrRuntime
	}
	return result, nil
}
func validMatchingEvaluation(value Evaluation) bool {
	if value.Score != nil {
		return value.Reason == "" && !math.IsNaN(*value.Score) && !math.IsInf(*value.Score, 0) && *value.Score >= -1 && *value.Score <= 1
	}
	for _, role := range []string{"document_", "selfie_"} {
		if strings.HasPrefix(value.Reason, role) {
			switch strings.TrimPrefix(value.Reason, role) {
			case "face_not_found", "multiple_faces", "face_too_small", "face_at_edge":
				return true
			}
		}
	}
	return false
}
