package policy

import (
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Routing is immutable provenance for a non-completion workflow directive.
// Request.DecisionID is the existing authorship reservation, not an authored
// terminal decision.
type Routing struct {
	request    AuthorRequest
	snapshot   Snapshot
	evaluation Evaluation
}

// NewRouting validates the reference-only provenance without weakening Decision.
func NewRouting(scope tenant.Scope, request AuthorRequest, snapshot Snapshot, evaluation Evaluation) (Routing, error) {
	if validateAuthorRequest(scope, request) != nil || request.DecidedAt.Nanosecond()%1000 != 0 || !request.Supersedes.IsZero() || snapshot.TenantID() != scope.ID() || snapshot.VerificationID() != request.VerificationID || !snapshot.EvaluatedAt().Equal(request.EvaluatedAt) || !routesWorkflow(evaluation.Selected()) || evaluation.AuthorisesCompletion() {
		return Routing{}, ErrInvalid
	}
	restored, err := RestoreSnapshotCanonical(snapshot.Canonical(), snapshot.Digest())
	if err != nil {
		return Routing{}, err
	}
	result, err := RestoreEvaluationCanonical(restored, evaluation.Canonical(), evaluation.Digest())
	if err != nil {
		return Routing{}, err
	}
	return Routing{request: request, snapshot: restored, evaluation: result}, nil
}

func routesWorkflow(directive Directive) bool {
	switch directive {
	case DirectiveRequestInput, DirectiveRouteManualReview, DirectiveFailWorkflow:
		return true
	default:
		return false
	}
}

// Request returns the immutable authorship request.
func (routing Routing) Request() AuthorRequest { return routing.request }

// Snapshot returns the canonical inputs that caused routing.
func (routing Routing) Snapshot() Snapshot { return routing.snapshot }

// Evaluation returns the non-completion workflow result.
func (routing Routing) Evaluation() Evaluation { return routing.evaluation }

// ValidateReplay checks exact request identity without reevaluating current inputs.
func (routing Routing) ValidateReplay(scope tenant.Scope, request AuthorRequest) error {
	if _, err := NewRouting(scope, request, routing.snapshot, routing.evaluation); err != nil {
		return err
	}
	if request.DecisionID != routing.request.DecisionID || !request.DecidedAt.Equal(routing.request.DecidedAt) {
		return ErrDecisionConflict
	}
	return nil
}
