package onnx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg" // Register bounded JPEG decoding.
	_ "image/png"  // Register bounded PNG decoding.
	"math"
	"reflect"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

// Configuration pins the image transform and score interpretation together.
// EvaluationOnly is mandatory until a trained model completes its acceptance review.
type Configuration struct {
	FaceMatching    bool                           `json:"face_matching,omitempty"`
	FacePreparation *FacePreparation               `json:"face_preparation,omitempty"`
	TenantID        string                         `json:"tenant_id"`
	Registration    modelv1.ConfigurationReference `json:"registration"`
	Manifest        modelv1.Manifest               `json:"manifest"`
	Width           int                            `json:"width"`
	Height          int                            `json:"height"`
	EvaluationOnly  bool                           `json:"evaluation_only"`
}

// EvidenceReader redeems purpose-bound content only within the model workload.
type EvidenceReader interface {
	ReadModelEvidence(context.Context, modelv1.Request, modelv1.EvidenceGrantReference, int64) ([]byte, error)
}

// Predictor supplies bounded native inference without leaking native tensor types.
type Predictor interface {
	Infer(context.Context, []float32) (float64, error)
}

// Adapter implements evaluation-only PAD or document/selfie matching.
type Adapter struct {
	configuration Configuration
	predictor     Predictor
	evidence      EvidenceReader
	now           func() time.Time
}

// ConfigurationDigest binds all transform and evaluation semantics.
func ConfigurationDigest(configuration Configuration) string {
	configuration.Registration.ConfigurationDigest = ""
	encoded, _ := json.Marshal(configuration)
	return digest(encoded)
}

// PreprocessingDigest identifies prepared RGB NCHW normalization to [0,1].
func PreprocessingDigest(width, height int) string {
	encoded, _ := json.Marshal([]any{"idenqa.prepared-pad-crop-rgb-nchw-unit.v1", width, height})
	return digest(encoded)
}

// OutputSchemaDigest identifies a scalar evaluation-only passive-PAD score.
func OutputSchemaDigest() string {
	return digest([]byte("idenqa.passive-pad.v1:float32[1,2]:real-spoof-logits:softmax:evaluation-only"))
}

// New constructs an exact tenant-bound evaluation-only model adapter.
func New(configuration Configuration, predictor Predictor, evidence EvidenceReader, now func() time.Time) (*Adapter, error) {
	m := configuration.Manifest
	size, grants := 128, uint16(1)
	check, signal := "idenqa.check.passive_pad", "idenqa.signal.passive_pad"
	accepted := []string{"idenqa.evidence.selfie_image"}
	output := OutputSchemaDigest()
	if configuration.FaceMatching {
		size, grants = 112, 2
		check, signal = "idenqa.check.face_match_1to1", "idenqa.signal.face_match_1to1"
		accepted = []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}
		output = MatchingOutputSchemaDigest()
		if configuration.FacePreparation == nil {
			return nil, ErrRuntime
		}
		if _, ok := predictor.(PairPredictor); !ok {
			return nil, ErrRuntime
		}
	}
	preprocessing := PreprocessingDigest(configuration.Width, configuration.Height)
	if configuration.FacePreparation != nil {
		if !configuration.FacePreparation.valid() {
			return nil, ErrRuntime
		}
		if _, ok := predictor.(ImagePredictor); !ok && !configuration.FaceMatching {
			return nil, ErrRuntime
		}
		preprocessing = FacePreprocessingDigest(*configuration.FacePreparation)
		if configuration.FaceMatching {
			preprocessing = MatchingPreprocessingDigest(*configuration.FacePreparation)
		}
	}
	if predictor == nil || evidence == nil || now == nil || !configuration.EvaluationOnly || configuration.TenantID == "" || configuration.Registration.Validate() != nil || m.Validate() != nil || m.Provenance.ModelID != configuration.Registration.ModelID || configuration.Width != size || configuration.Height != size || ConfigurationDigest(configuration) != configuration.Registration.ConfigurationDigest || m.Provenance.PreprocessingDigest != preprocessing || m.Provenance.OutputSchemaDigest != output || m.Restrictions.NetworkAllowed || m.Restrictions.MaximumGrants != grants || m.Restrictions.MaximumInputBytes > 10<<20 || m.Restrictions.MaximumDuration > 30*time.Second || len(m.Capabilities) != 1 || m.Capabilities[0].Evaluation != check || len(m.Capabilities[0].RequiredAssurances) != 0 || !reflect.DeepEqual(m.Capabilities[0].AcceptedEvidence, accepted) || !reflect.DeepEqual(m.Capabilities[0].OutputSignals, []string{signal}) {
		return nil, ErrRuntime
	}
	raw, _ := json.Marshal(configuration)
	if json.Unmarshal(raw, &configuration) != nil {
		return nil, ErrRuntime
	}
	return &Adapter{configuration, predictor, evidence, now}, nil
}

// Manifest returns the pinned public model description.
func (adapter *Adapter) Manifest(context.Context) (modelv1.Manifest, error) {
	raw, _ := json.Marshal(adapter.configuration.Manifest)
	var result modelv1.Manifest
	err := json.Unmarshal(raw, &result)
	return result, err
}

// ValidateConfiguration rejects a different configuration revision.
func (adapter *Adapter) ValidateConfiguration(_ context.Context, reference modelv1.ConfigurationReference) error {
	if reference != adapter.configuration.Registration {
		return ErrRuntime
	}
	return nil
}

// Health reports readiness of this initialized workload.
func (adapter *Adapter) Health(ctx context.Context) (modelv1.Health, error) {
	if err := ctx.Err(); err != nil {
		return modelv1.Health{}, err
	}
	return modelv1.Health{State: modelv1.HealthReady, Code: "model_ready", CheckedAt: adapter.now().UTC()}, nil
}

// Execute consumes capability-bound images without asserting biometric assurance.
func (adapter *Adapter) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	m := adapter.configuration.Manifest
	if request.Validate() != nil || request.TenantID != adapter.configuration.TenantID || request.Configuration != adapter.configuration.Registration || request.Provenance != m.Provenance || request.Restrictions != m.Restrictions || !reflect.DeepEqual(request.Capability, m.Capabilities[0]) || len(request.Evidence) != int(m.Restrictions.MaximumGrants) {
		return modelv1.Result{}, ErrRuntime
	}
	ctx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	ctx, limitCancel := context.WithTimeout(ctx, m.Restrictions.MaximumDuration)
	defer limitCancel()
	if !adapter.now().Before(request.Deadline) {
		return adapter.failure(request, modelv1.FailureDeadline, "inference_deadline"), nil
	}
	if adapter.configuration.FaceMatching {
		return adapter.executeMatch(ctx, request)
	}
	// The constructor caps this immutable value at 10 MiB.
	//nolint:gosec // MaximumInputBytes is validated before the adapter is constructed.
	raw, err := adapter.evidence.ReadModelEvidence(ctx, request, request.Evidence[0], int64(m.Restrictions.MaximumInputBytes))
	if err != nil {
		return adapter.failure(request, modelv1.FailureUnauthorized, "evidence_unavailable"), nil
	}
	defer clear(raw)
	if len(raw) == 0 || uint64(len(raw)) > m.Restrictions.MaximumInputBytes {
		return adapter.failure(request, modelv1.FailureInvalidInput, "image_invalid"), nil
	}
	var score float64
	reason := "pad_evaluation_only"
	if adapter.configuration.FacePreparation != nil {
		picture, decodeErr := DecodeImage(ctx, raw)
		if decodeErr != nil {
			return adapter.failure(request, modelv1.FailureInvalidInput, "image_invalid"), nil
		}
		defer clear(picture.RGB)
		evaluation, inferErr := adapter.predictor.(ImagePredictor).InferImage(ctx, picture)
		err = inferErr
		if evaluation.Score != nil {
			score = *evaluation.Score
		}
		if evaluation.Reason != "" {
			reason = evaluation.Reason
		}
	} else {
		tensor, decodeErr := pixels(ctx, raw, adapter.configuration.Width, adapter.configuration.Height)
		if decodeErr != nil {
			return adapter.failure(request, modelv1.FailureInvalidInput, "image_invalid"), nil
		}
		defer clear(tensor)
		score, err = adapter.predictor.Infer(ctx, tensor)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return adapter.failure(request, modelv1.FailureDeadline, "inference_deadline"), nil
		}
		if errors.Is(err, context.Canceled) {
			return adapter.failure(request, modelv1.FailureCancelled, "inference_cancelled"), nil
		}
		return adapter.failure(request, modelv1.FailureUnavailable, "inference_unavailable"), nil
	}
	if ctx.Err() != nil || !adapter.now().Before(request.Deadline) {
		return adapter.failure(request, modelv1.FailureDeadline, "inference_deadline"), nil
	}
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
		return adapter.failure(request, modelv1.FailureInternal, "model_output_invalid"), nil
	}
	// Candidate scores are not approved assurance. Until crop provenance and
	// model-specific evaluation are accepted, every native result is inconclusive.
	outcome := modelv1.SignalOutcomeInconclusive
	return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted, CompletedAt: adapter.now().UTC(), Signals: []modelv1.Signal{{Name: "idenqa.signal.passive_pad", Outcome: outcome, ReasonCodes: []string{reason}}}}, nil
}
func (adapter *Adapter) failure(request modelv1.Request, class modelv1.FailureClass, code string) modelv1.Result {
	at := adapter.now().UTC()
	if at.After(request.Deadline) {
		at = request.Deadline
	}
	return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeFailed, CompletedAt: at, Failure: &modelv1.Failure{Class: class, Code: code, Retry: modelv1.RetryReconcile}}
}
func pixels(ctx context.Context, raw []byte, width, height int) ([]float32, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != width || config.Height != height || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, ErrRuntime
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrRuntime
	}
	result := make([]float32, 3*width*height)
	bounds := decoded.Bounds()
	for y := 0; y < height; y++ {
		if ctx.Err() != nil {
			clear(result)
			return nil, ctx.Err()
		}
		for x := 0; x < width; x++ {
			r, g, b, a := decoded.At(bounds.Min.X+x*config.Width/width, bounds.Min.Y+y*config.Height/height).RGBA()
			if a != 65535 {
				clear(result)
				return nil, ErrRuntime
			}
			offset := y*width + x
			result[offset] = float32(r) / 65535
			result[width*height+offset] = float32(g) / 65535
			result[2*width*height+offset] = float32(b) / 65535
		}
	}
	return result, nil
}
