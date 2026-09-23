package onnx

import (
	"context"
	"errors"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

func (adapter *Adapter) executeMatch(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	// Role order is not trusted. Require two distinct, purpose-compatible assets.
	refs := map[string]modelv1.EvidenceGrantReference{}
	for _, ref := range request.Evidence {
		if ref.Variant != "document.front" && ref.Variant != "selfie" {
			return modelv1.Result{}, ErrRuntime
		}
		if _, exists := refs[ref.Variant]; exists {
			return modelv1.Result{}, ErrRuntime
		}
		refs[ref.Variant] = ref
	}
	document, selfie := refs["document.front"], refs["selfie"]
	if document.EvidenceID == selfie.EvidenceID || document.GrantID == selfie.GrantID || document.Purpose != selfie.Purpose {
		return modelv1.Result{}, ErrRuntime
	}
	pictures := make([]Image, 0, 2)
	defer func() {
		for _, picture := range pictures {
			clear(picture.RGB)
		}
	}()
	remaining := adapter.configuration.Manifest.Restrictions.MaximumInputBytes
	for _, ref := range []modelv1.EvidenceGrantReference{document, selfie} {
		//nolint:gosec // Constructor caps total input at 10 MiB; remaining only decreases.
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
		pictures = append(pictures, picture)
	}
	value, err := adapter.predictor.(PairPredictor).InferPair(ctx, pictures[0], pictures[1])
	if err != nil {
		class, code := modelv1.FailureUnavailable, "inference_unavailable"
		if errors.Is(err, context.DeadlineExceeded) {
			class, code = modelv1.FailureDeadline, "inference_deadline"
		}
		if errors.Is(err, context.Canceled) {
			class, code = modelv1.FailureCancelled, "inference_cancelled"
		}
		return adapter.failure(request, class, code), nil
	}
	if ctx.Err() != nil || !adapter.now().Before(request.Deadline) {
		return adapter.failure(request, modelv1.FailureDeadline, "inference_deadline"), nil
	}
	if !validMatchingEvaluation(value) {
		return adapter.failure(request, modelv1.FailureInternal, "model_output_invalid"), nil
	}
	reason := value.Reason
	var quality *modelv1.SignalQuality
	if reason == "" {
		reason = "face_match_evaluation_only"
	} else {
		quality = &modelv1.SignalQuality{Acceptable: false, Codes: []string{reason}}
	}
	return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted, CompletedAt: adapter.now().UTC(), Signals: []modelv1.Signal{{Name: modelv1.SignalFaceMatch, Outcome: modelv1.SignalOutcomeInconclusive, ReasonCodes: []string{reason}, Quality: quality}}}, nil
}
