package privacy

import (
	"context"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Dispatcher executes approved external effects by delegating to the owning
// deletion, export, and correction mechanisms. It never duplicates them.
type Dispatcher struct {
	exports     SubjectBundleExporter
	deletions   SubjectDeletionExecutor
	corrections CorrectionExecutor
}

// NewDispatcher constructs the effect dispatcher. Correction may be nil when
// correction effects are gated in a deployment.
func NewDispatcher(exports SubjectBundleExporter, deletions SubjectDeletionExecutor, corrections CorrectionExecutor) (*Dispatcher, error) {
	if exports == nil || deletions == nil {
		return nil, ErrInvalid
	}
	return &Dispatcher{exports: exports, deletions: deletions, corrections: corrections}, nil
}

// ExecuteEffect routes one approved request to its owning mechanism.
func (dispatcher *Dispatcher) ExecuteEffect(ctx context.Context, scope tenant.Scope, actor Actor, request Request) (EffectResult, error) {
	if dispatcher == nil || scope.ID().IsZero() || request.ID.IsZero() {
		return EffectResult{}, ErrInvalid
	}
	payload, err := ValidatePayload(request.Type, request.Payload)
	if err != nil {
		return EffectResult{}, err
	}
	switch request.Type {
	case RequestAccess, RequestPortability:
		if request.SubjectID == "" {
			return EffectResult{}, ErrInvalid
		}
		structured := request.Type == RequestPortability
		bundle, err := dispatcher.exports.ExportSubjectBundle(ctx, scope, request.SubjectID, structured)
		if err != nil {
			return EffectResult{}, fmt.Errorf("export subject bundle: %w", err)
		}
		if bundle.Digest == "" || bundle.Bytes <= 0 {
			return EffectResult{}, ErrInvalid
		}
		return EffectResult{Kind: "subject_export", Reference: "subject:" + request.SubjectID, Digest: bundle.Digest}, nil
	case RequestErasure:
		if request.SubjectID == "" {
			return EffectResult{}, ErrInvalid
		}
		reference, err := dispatcher.deletions.RequestSubjectDeletion(ctx, scope, actor, request.SubjectID, request.Region)
		if err != nil {
			return EffectResult{}, fmt.Errorf("request subject deletion: %w", err)
		}
		if reference == "" {
			return EffectResult{}, ErrInvalid
		}
		return EffectResult{Kind: "deletion_request", Reference: reference, Digest: targetReferenceDigest(reference)}, nil
	case RequestCorrection:
		if dispatcher.corrections == nil {
			return EffectResult{}, ErrUnavailable
		}
		instruction := CorrectionInstruction{
			SubjectID: request.SubjectID, RecordID: payload.RecordID, DecisionID: payload.DecisionID,
			Name: payload.Name, Value: payload.Value, Kind: payload.Kind, Normalization: payload.Normalization,
		}
		reference, err := dispatcher.corrections.ExecuteCorrection(ctx, scope, actor, request, instruction)
		if err != nil {
			return EffectResult{}, fmt.Errorf("execute correction: %w", err)
		}
		if reference == "" {
			return EffectResult{}, ErrInvalid
		}
		return EffectResult{Kind: "correction", Reference: reference, Digest: targetReferenceDigest(reference)}, nil
	default:
		return EffectResult{}, ErrInvalid
	}
}

var _ EffectExecutor = (*Dispatcher)(nil)
