package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// AuthorRepository is the narrow durable boundary consumed by Author.
type AuthorRepository interface {
	Append(context.Context, tenant.Scope, Decision) error
	Find(context.Context, tenant.Scope, id.Decision) (Decision, error)
}

// InputLoader resolves exact authoritative inputs without exposing an
// expression language or persistence implementation to the author.
type InputLoader interface {
	LoadPolicyInput(
		context.Context,
		tenant.Scope,
		id.Verification,
		time.Time,
	) (AuthorInput, error)
}

// Evaluator is the owned expression-engine boundary. Implementations return
// only Idenqa-owned bounded results and never author a durable decision.
type Evaluator interface {
	Reference() EvaluatorReference
	Evaluate(context.Context, Snapshot) (EvaluatorOutput, error)
}

// AuthorRequest pins one replayable machine-decision attempt. The caller owns
// identifier allocation and supplies explicit time; Author never reads a clock.
type AuthorRequest struct {
	DecisionID     id.Decision
	VerificationID id.Verification
	Supersedes     id.Decision
	EvaluatedAt    time.Time
	DecidedAt      time.Time
}

// AuthorInput is the authoritative CEL-neutral input returned by InputLoader.
// Facts remain reference-only and cannot carry raw evidence bytes.
type AuthorInput struct {
	Context           *DecisionContext
	AuthorityID       id.Authority
	AcknowledgementID id.Acknowledgement
	Region            string
	Policy            Reference
	Facts             []Fact
}

// EvaluatorOutput is the complete owned result accepted from an evaluator.
type EvaluatorOutput struct {
	Results   []RequirementResult
	Assurance string
}

// Builder deterministically constructs a machine decision from authoritative
// inputs without performing persistence. Transactional task adapters use it to
// keep evaluation outside their short fenced effect transaction.
type Builder struct {
	inputs    InputLoader
	evaluator Evaluator
}

// NewBuilder constructs the replay-neutral machine-decision builder.
func NewBuilder(inputs InputLoader, evaluator Evaluator) (*Builder, error) {
	if inputs == nil || evaluator == nil {
		return nil, errors.New("policy builder: dependencies are required")
	}
	return &Builder{inputs: inputs, evaluator: evaluator}, nil
}

// Build constructs one terminal machine decision without reading or writing a
// decision repository. The caller remains responsible for exact replay and
// atomic persistence semantics.
func (builder *Builder) Build(
	ctx context.Context,
	scope tenant.Scope,
	request AuthorRequest,
) (Decision, error) {
	snapshot, evaluation, err := builder.Evaluate(ctx, scope, request)
	if err != nil {
		return Decision{}, err
	}
	if !evaluation.AuthorisesCompletion() {
		return Decision{}, fmt.Errorf("%w: non-terminal evaluator output", ErrInvalid)
	}

	decision, err := NewDecision(DecisionInput{
		ID:         request.DecisionID,
		Snapshot:   snapshot,
		Evaluation: evaluation,
		Actor:      ActorMachine,
		Supersedes: request.Supersedes,
		DecidedAt:  request.DecidedAt,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("construct policy decision: %w", err)
	}
	return decision, nil
}

// Evaluate constructs canonical authoritative inputs and deterministic results,
// including nonterminal directives, without authoring a decision.
func (builder *Builder) Evaluate(ctx context.Context, scope tenant.Scope, request AuthorRequest) (Snapshot, Evaluation, error) {
	if builder == nil || builder.inputs == nil || builder.evaluator == nil {
		return Snapshot{}, Evaluation{}, fmt.Errorf("%w: policy builder", ErrInvalid)
	}
	if err := validateAuthorRequest(scope, request); err != nil {
		return Snapshot{}, Evaluation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, Evaluation{}, err
	}

	input, err := builder.inputs.LoadPolicyInput(ctx, scope, request.VerificationID, request.EvaluatedAt)
	if err != nil {
		return Snapshot{}, Evaluation{}, fmt.Errorf("load policy author input: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, Evaluation{}, err
	}

	snapshot, err := NewSnapshot(SnapshotInput{
		TenantID:          scope.ID(),
		VerificationID:    request.VerificationID,
		AuthorityID:       input.AuthorityID,
		AcknowledgementID: input.AcknowledgementID,
		Region:            input.Region,
		Policy:            input.Policy,
		Evaluator:         builder.evaluator.Reference(),
		EvaluatedAt:       request.EvaluatedAt,
		Facts:             input.Facts, Context: input.Context,
	})
	if err != nil {
		return Snapshot{}, Evaluation{}, fmt.Errorf("construct policy author snapshot: %w", err)
	}

	output, err := builder.evaluator.Evaluate(ctx, snapshot)
	if err != nil {
		return Snapshot{}, Evaluation{}, fmt.Errorf("evaluate policy author snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, Evaluation{}, err
	}
	evaluation, err := Resolve(snapshot, output.Results, output.Assurance)
	if err != nil {
		return Snapshot{}, Evaluation{}, fmt.Errorf("resolve policy author output: %w", err)
	}
	return snapshot, evaluation, nil
}

// Author validates, evaluates, terminally authorises, and durably appends one
// replayable machine-authored decision.
type Author struct {
	repository AuthorRepository
	builder    *Builder
}

// NewAuthor constructs the CEL-neutral machine-decision application service.
func NewAuthor(repository AuthorRepository, inputs InputLoader, evaluator Evaluator) (*Author, error) {
	if repository == nil {
		return nil, errors.New("policy author: dependencies are required")
	}
	builder, err := NewBuilder(inputs, evaluator)
	if err != nil {
		return nil, errors.New("policy author: dependencies are required")
	}
	return &Author{repository: repository, builder: builder}, nil
}

// Author creates one terminal decision or returns its exact durable replay.
func (author *Author) Author(
	ctx context.Context,
	scope tenant.Scope,
	request AuthorRequest,
) (Decision, error) {
	if err := validateAuthorRequest(scope, request); err != nil {
		return Decision{}, err
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}

	existing, err := author.repository.Find(ctx, scope, request.DecisionID)
	if err == nil {
		return exactAuthorReplay(existing, scope, request)
	}
	if !errors.Is(err, ErrDecisionNotFound) {
		return Decision{}, fmt.Errorf("find policy decision replay: %w", err)
	}

	decision, err := author.builder.Build(ctx, scope, request)
	if err != nil {
		return Decision{}, err
	}
	if err := author.repository.Append(ctx, scope, decision); err != nil {
		if !errors.Is(err, ErrDecisionConflict) {
			return Decision{}, fmt.Errorf("append policy decision: %w", err)
		}
		return author.recoverConcurrentReplay(ctx, scope, request, decision)
	}

	stored, err := author.repository.Find(ctx, scope, request.DecisionID)
	if err != nil {
		return Decision{}, fmt.Errorf("read authored policy decision: %w", err)
	}
	if stored.Digest() != decision.Digest() {
		return Decision{}, ErrDecisionConflict
	}
	return exactAuthorReplay(stored, scope, request)
}

func (author *Author) recoverConcurrentReplay(
	ctx context.Context,
	scope tenant.Scope,
	request AuthorRequest,
	candidate Decision,
) (Decision, error) {
	stored, err := author.repository.Find(ctx, scope, request.DecisionID)
	if err != nil {
		if errors.Is(err, ErrDecisionNotFound) {
			return Decision{}, ErrDecisionConflict
		}
		return Decision{}, fmt.Errorf("find concurrent policy decision replay: %w", err)
	}
	if stored.Digest() != candidate.Digest() {
		return Decision{}, ErrDecisionConflict
	}
	return exactAuthorReplay(stored, scope, request)
}

func validateAuthorRequest(scope tenant.Scope, request AuthorRequest) error {
	if scope.ID().IsZero() || request.DecisionID.IsZero() || request.VerificationID.IsZero() ||
		!validUTC(request.EvaluatedAt) || !validUTC(request.DecidedAt) ||
		request.DecidedAt.Before(request.EvaluatedAt) ||
		(!request.Supersedes.IsZero() && request.Supersedes.String() == request.DecisionID.String()) {
		return fmt.Errorf("%w: policy author request", ErrInvalid)
	}
	return nil
}

func exactAuthorReplay(decision Decision, scope tenant.Scope, request AuthorRequest) (Decision, error) {
	snapshot := decision.Snapshot()
	if decision.ID().String() != request.DecisionID.String() ||
		snapshot.TenantID().String() != scope.ID().String() ||
		snapshot.VerificationID().String() != request.VerificationID.String() ||
		decision.Actor() != ActorMachine ||
		decision.Supersedes().String() != request.Supersedes.String() ||
		!snapshot.EvaluatedAt().Equal(request.EvaluatedAt) ||
		!decision.DecidedAt().Equal(request.DecidedAt) {
		return Decision{}, ErrDecisionConflict
	}
	if _, err := Reproduce(decision); err != nil {
		return Decision{}, fmt.Errorf("reproduce authored policy decision: %w", err)
	}
	return decision, nil
}

// ValidateAuthorReplay verifies that a durable machine decision is the exact
// replay addressed by request and still reproduces from its stored lineage.
func ValidateAuthorReplay(decision Decision, scope tenant.Scope, request AuthorRequest) error {
	_, err := exactAuthorReplay(decision, scope, request)
	return err
}
