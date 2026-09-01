package policy_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type authorRepository struct {
	mu          sync.Mutex
	decisions   map[string]policy.Decision
	findErr     error
	appendErr   error
	findCalls   atomic.Int32
	appendCalls atomic.Int32
}

func newAuthorRepository() *authorRepository {
	return &authorRepository{decisions: make(map[string]policy.Decision)}
}

func (repository *authorRepository) Append(
	ctx context.Context,
	scope tenant.Scope,
	decision policy.Decision,
) error {
	repository.appendCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	if repository.appendErr != nil {
		return repository.appendErr
	}
	if scope.ID().String() != decision.Snapshot().TenantID().String() {
		return policy.ErrDecisionConflict
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := scope.ID().String() + ":" + decision.ID().String()
	stored, exists := repository.decisions[key]
	if exists && stored.Digest() != decision.Digest() {
		return policy.ErrDecisionConflict
	}
	repository.decisions[key] = decision
	return nil
}

func (repository *authorRepository) Find(
	ctx context.Context,
	scope tenant.Scope,
	decisionID id.Decision,
) (policy.Decision, error) {
	repository.findCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return policy.Decision{}, err
	}
	if repository.findErr != nil {
		return policy.Decision{}, repository.findErr
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	decision, exists := repository.decisions[scope.ID().String()+":"+decisionID.String()]
	if !exists {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	return decision, nil
}

type authorInputLoader struct {
	mu     sync.Mutex
	input  policy.AuthorInput
	err    error
	after  func()
	calls  atomic.Int32
	scope  id.Tenant
	verify id.Verification
	at     time.Time
}

func (loader *authorInputLoader) LoadPolicyInput(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
	at time.Time,
) (policy.AuthorInput, error) {
	loader.calls.Add(1)
	loader.mu.Lock()
	loader.scope, loader.verify, loader.at = scope.ID(), verificationID, at
	loader.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return policy.AuthorInput{}, err
	}
	if loader.after != nil {
		loader.after()
	}
	return loader.input, loader.err
}

func (loader *authorInputLoader) lastRequest() (id.Tenant, id.Verification, time.Time) {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.scope, loader.verify, loader.at
}

type authorEvaluator struct {
	mu        sync.Mutex
	reference policy.EvaluatorReference
	output    policy.EvaluatorOutput
	err       error
	after     func()
	calls     atomic.Int32
	snapshot  policy.Snapshot
}

func (evaluator *authorEvaluator) Reference() policy.EvaluatorReference { return evaluator.reference }

func (evaluator *authorEvaluator) Evaluate(
	ctx context.Context,
	snapshot policy.Snapshot,
) (policy.EvaluatorOutput, error) {
	evaluator.calls.Add(1)
	evaluator.mu.Lock()
	evaluator.snapshot = snapshot
	evaluator.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorOutput{}, err
	}
	if evaluator.after != nil {
		evaluator.after()
	}
	return evaluator.output, evaluator.err
}

func (evaluator *authorEvaluator) lastSnapshot() policy.Snapshot {
	evaluator.mu.Lock()
	defer evaluator.mu.Unlock()
	return evaluator.snapshot
}

type authorTestFixture struct {
	scope     tenant.Scope
	request   policy.AuthorRequest
	input     policy.AuthorInput
	reference policy.EvaluatorReference
	fact      policy.FactKey
}

func TestNewBuilderRequiresDependencies(t *testing.T) {
	t.Parallel()

	fixture := newAuthorTestFixture(t)
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	if _, err := policy.NewBuilder(nil, evaluator); err == nil {
		t.Fatal("NewBuilder(nil, evaluator) error = nil")
	}
	if _, err := policy.NewBuilder(loader, nil); err == nil {
		t.Fatal("NewBuilder(loader, nil) error = nil")
	}
}

func TestBuilderConstructsWithoutDecisionPersistence(t *testing.T) {
	t.Parallel()

	fixture := newAuthorTestFixture(t)
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	builder, err := policy.NewBuilder(loader, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := builder.Build(t.Context(), fixture.scope, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.ID().String() != fixture.request.DecisionID.String() ||
		decision.Actor() != policy.ActorMachine || !decision.Evaluation().AuthorisesCompletion() ||
		loader.calls.Load() != 1 || evaluator.calls.Load() != 1 {
		t.Fatalf("Build() decision = %+v", decision)
	}
}

func TestNewAuthorRequiresDependencies(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	repository := newAuthorRepository()
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)

	for _, test := range []struct {
		name       string
		repository policy.AuthorRepository
		loader     policy.InputLoader
		evaluator  policy.Evaluator
	}{
		{name: "repository", loader: loader, evaluator: evaluator},
		{name: "input loader", repository: repository, evaluator: evaluator},
		{name: "evaluator", repository: repository, loader: loader},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.NewAuthor(test.repository, test.loader, test.evaluator); err == nil {
				t.Fatal("NewAuthor() error = nil")
			}
		})
	}
}

func TestAuthorAuthorsClosedTerminalOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		candidate policy.Directive
		state     policy.RequirementState
		outcome   policy.Outcome
	}{
		{name: "verified", candidate: policy.DirectiveCompleteVerified,
			state: policy.RequirementSatisfied, outcome: policy.OutcomeVerified},
		{name: "not verified", candidate: policy.DirectiveCompleteNotVerified,
			state: policy.RequirementNotSatisfied, outcome: policy.OutcomeNotVerified},
		{name: "inconclusive", candidate: policy.DirectiveCompleteInconclusive,
			state: policy.RequirementInconclusive, outcome: policy.OutcomeInconclusive},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthorTestFixture(t)
			repository := newAuthorRepository()
			loader := &authorInputLoader{input: fixture.input}
			evaluator := fixture.evaluator(test.candidate, test.state)
			author, err := policy.NewAuthor(repository, loader, evaluator)
			if err != nil {
				t.Fatal(err)
			}

			decision, err := author.Author(t.Context(), fixture.scope, fixture.request)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Actor() != policy.ActorMachine || decision.Evaluation().Outcome() != test.outcome ||
				decision.ID().String() != fixture.request.DecisionID.String() ||
				decision.Snapshot().Evaluator() != fixture.reference {
				t.Fatalf("Author() decision = %+v", decision)
			}
			if _, err := policy.Reproduce(decision); err != nil {
				t.Fatalf("Reproduce() error = %v", err)
			}
			loadedTenant, loadedVerification, loadedAt := loader.lastRequest()
			if loadedTenant.String() != fixture.scope.ID().String() ||
				loadedVerification.String() != fixture.request.VerificationID.String() ||
				!loadedAt.Equal(fixture.request.EvaluatedAt) ||
				evaluator.lastSnapshot().Digest() != decision.Snapshot().Digest() {
				t.Fatal("author dependencies did not receive exact request meaning")
			}
		})
	}
}

func TestAuthorExactReplayBypassesInputAndEvaluation(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	repository := newAuthorRepository()
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	author, _ := policy.NewAuthor(repository, loader, evaluator)

	first, err := author.Author(t.Context(), fixture.scope, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	loader.err = errors.New("must not be called")
	evaluator.err = errors.New("must not be called")
	second, err := author.Author(t.Context(), fixture.scope, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() || loader.calls.Load() != 1 || evaluator.calls.Load() != 1 ||
		repository.appendCalls.Load() != 1 {
		t.Fatalf("replay digests=%q/%q loader=%d evaluator=%d append=%d", first.Digest(), second.Digest(),
			loader.calls.Load(), evaluator.calls.Load(), repository.appendCalls.Load())
	}
}

func TestAuthorRejectsChangedReplayMeaning(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	repository := newAuthorRepository()
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	author, _ := policy.NewAuthor(repository, loader, evaluator)
	if _, err := author.Author(t.Context(), fixture.scope, fixture.request); err != nil {
		t.Fatal(err)
	}
	alternateVerification, _ := id.ParseVerification("ver_01K3P4NQF00000000000000001")
	alternateDecision, _ := id.ParseDecision("dec_01K3P4NQF00000000000000001")
	tests := []struct {
		name   string
		change func(*policy.AuthorRequest)
	}{
		{name: "verification", change: func(request *policy.AuthorRequest) {
			request.VerificationID = alternateVerification
		}},
		{name: "evaluation time", change: func(request *policy.AuthorRequest) {
			request.EvaluatedAt = request.EvaluatedAt.Add(time.Second)
		}},
		{name: "decision time", change: func(request *policy.AuthorRequest) {
			request.DecidedAt = request.DecidedAt.Add(time.Second)
		}},
		{name: "lineage", change: func(request *policy.AuthorRequest) {
			request.Supersedes = alternateDecision
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := fixture.request
			test.change(&changed)
			if _, err := author.Author(t.Context(), fixture.scope, changed); !errors.Is(err, policy.ErrDecisionConflict) {
				t.Fatalf("Author() error = %v", err)
			}
		})
	}
}

func TestAuthorRejectsRepositoryTenantEscape(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	alternateTenant, _ := id.ParseTenant("ten_01K3P4NQF00000000000000001")
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{
		TenantID: alternateTenant, VerificationID: fixture.request.VerificationID,
		AuthorityID: fixture.input.AuthorityID, AcknowledgementID: fixture.input.AcknowledgementID,
		Region: fixture.input.Region, Policy: fixture.input.Policy, Evaluator: fixture.reference,
		EvaluatedAt: fixture.request.EvaluatedAt, Facts: fixture.input.Facts,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := fixture.output(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	evaluation, err := policy.Resolve(snapshot, output.Results, output.Assurance)
	if err != nil {
		t.Fatal(err)
	}
	wrongTenant, err := policy.NewDecision(policy.DecisionInput{
		ID: fixture.request.DecisionID, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorMachine, DecidedAt: fixture.request.DecidedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := newAuthorRepository()
	repository.decisions[fixture.scope.ID().String()+":"+fixture.request.DecisionID.String()] = wrongTenant
	author, _ := policy.NewAuthor(repository, &authorInputLoader{input: fixture.input},
		fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied))
	if _, err := author.Author(t.Context(), fixture.scope, fixture.request); !errors.Is(err, policy.ErrDecisionConflict) {
		t.Fatalf("Author() error = %v", err)
	}
}

func TestAuthorRejectsInvalidRequestsBeforeDependencies(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	repository := newAuthorRepository()
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	author, _ := policy.NewAuthor(repository, loader, evaluator)
	local := time.FixedZone("test", 60)
	tests := []struct {
		name   string
		scope  tenant.Scope
		change func(*policy.AuthorRequest)
	}{
		{name: "scope", scope: tenant.Scope{}},
		{name: "decision", scope: fixture.scope, change: func(request *policy.AuthorRequest) {
			request.DecisionID = id.Decision{}
		}},
		{name: "verification", scope: fixture.scope, change: func(request *policy.AuthorRequest) {
			request.VerificationID = id.Verification{}
		}},
		{name: "evaluation timezone", scope: fixture.scope, change: func(request *policy.AuthorRequest) {
			request.EvaluatedAt = request.EvaluatedAt.In(local)
		}},
		{name: "decision timezone", scope: fixture.scope, change: func(request *policy.AuthorRequest) {
			request.DecidedAt = request.DecidedAt.In(local)
		}},
		{name: "time order", scope: fixture.scope, change: func(request *policy.AuthorRequest) {
			request.DecidedAt = request.EvaluatedAt.Add(-time.Second)
		}},
		{name: "self supersession", scope: fixture.scope, change: func(request *policy.AuthorRequest) {
			request.Supersedes = request.DecisionID
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := fixture.request
			if test.change != nil {
				test.change(&request)
			}
			if _, err := author.Author(t.Context(), test.scope, request); !errors.Is(err, policy.ErrInvalid) {
				t.Fatalf("Author() error = %v", err)
			}
		})
	}
	if repository.findCalls.Load() != 0 || loader.calls.Load() != 0 || evaluator.calls.Load() != 0 {
		t.Fatal("invalid requests reached a dependency")
	}
}

func TestAuthorPropagatesCancellationAndDependencyErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("dependency failed")
	for _, test := range []struct {
		name string
		run  func(testing.TB, authorTestFixture) error
		is   error
	}{
		{name: "cancelled before lookup", is: context.Canceled, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			repository := newAuthorRepository()
			loader := &authorInputLoader{input: fixture.input}
			evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
			author, _ := policy.NewAuthor(repository, loader, evaluator)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := author.Author(ctx, fixture.scope, fixture.request)
			if repository.findCalls.Load() != 0 {
				t.Fatal("cancelled request reached repository")
			}
			return err
		}},
		{name: "lookup", is: boom, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			repository := newAuthorRepository()
			repository.findErr = boom
			author, _ := policy.NewAuthor(repository, &authorInputLoader{},
				fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied))
			_, err := author.Author(context.Background(), fixture.scope, fixture.request)
			return err
		}},
		{name: "input", is: boom, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			author, _ := policy.NewAuthor(newAuthorRepository(), &authorInputLoader{err: boom},
				fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied))
			_, err := author.Author(context.Background(), fixture.scope, fixture.request)
			return err
		}},
		{name: "evaluator", is: boom, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
			evaluator.err = boom
			author, _ := policy.NewAuthor(newAuthorRepository(), &authorInputLoader{input: fixture.input}, evaluator)
			_, err := author.Author(context.Background(), fixture.scope, fixture.request)
			return err
		}},
		{name: "append", is: boom, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			repository := newAuthorRepository()
			repository.appendErr = boom
			author, _ := policy.NewAuthor(repository, &authorInputLoader{input: fixture.input},
				fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied))
			_, err := author.Author(context.Background(), fixture.scope, fixture.request)
			return err
		}},
		{name: "cancelled after input", is: context.Canceled, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			ctx, cancel := context.WithCancel(context.Background())
			loader := &authorInputLoader{input: fixture.input, after: cancel}
			evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
			author, _ := policy.NewAuthor(newAuthorRepository(), loader, evaluator)
			_, err := author.Author(ctx, fixture.scope, fixture.request)
			if evaluator.calls.Load() != 0 {
				t.Fatal("cancelled input reached evaluator")
			}
			return err
		}},
		{name: "cancelled after evaluation", is: context.Canceled, run: func(t testing.TB, fixture authorTestFixture) error {
			t.Helper()
			ctx, cancel := context.WithCancel(context.Background())
			evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
			evaluator.after = cancel
			repository := newAuthorRepository()
			author, _ := policy.NewAuthor(repository, &authorInputLoader{input: fixture.input}, evaluator)
			_, err := author.Author(ctx, fixture.scope, fixture.request)
			if repository.appendCalls.Load() != 0 {
				t.Fatal("cancelled evaluation reached append")
			}
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.run(t, newAuthorTestFixture(t)); !errors.Is(err, test.is) {
				t.Fatalf("Author() error = %v", err)
			}
		})
	}
}

func TestAuthorRejectsNonTerminalAndMalformedEvaluatorOutput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		output func(authorTestFixture) policy.EvaluatorOutput
	}{
		{name: "non-terminal", output: func(fixture authorTestFixture) policy.EvaluatorOutput {
			return fixture.output(policy.DirectiveRequestInput, policy.RequirementInconclusive)
		}},
		{name: "unknown fact", output: func(fixture authorTestFixture) policy.EvaluatorOutput {
			unknown, _ := policy.NewFactKey("check.unknown")
			output := fixture.output(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
			output.Results[0].ContributingFacts = []policy.FactKey{unknown}
			return output
		}},
		{name: "missing verified assurance", output: func(fixture authorTestFixture) policy.EvaluatorOutput {
			output := fixture.output(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
			output.Assurance = ""
			return output
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthorTestFixture(t)
			evaluator := &authorEvaluator{reference: fixture.reference, output: test.output(fixture)}
			repository := newAuthorRepository()
			author, _ := policy.NewAuthor(repository, &authorInputLoader{input: fixture.input}, evaluator)
			if _, err := author.Author(t.Context(), fixture.scope, fixture.request); !errors.Is(err, policy.ErrInvalid) {
				t.Fatalf("Author() error = %v", err)
			}
			if repository.appendCalls.Load() != 0 {
				t.Fatal("invalid evaluator output reached append")
			}
		})
	}
}

func TestAuthorConcurrentExactRequestsConverge(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	repository := newAuthorRepository()
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	author, _ := policy.NewAuthor(repository, loader, evaluator)

	const requests = 32
	digests := make([]string, requests)
	errorsByRequest := make([]error, requests)
	var wait sync.WaitGroup
	for index := range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			decision, err := author.Author(t.Context(), fixture.scope, fixture.request)
			digests[index], errorsByRequest[index] = decision.Digest(), err
		}()
	}
	wait.Wait()
	for index, err := range errorsByRequest {
		if err != nil || digests[index] == "" || digests[index] != digests[0] {
			t.Fatalf("request %d digest=%q error=%v", index, digests[index], err)
		}
	}
}

func TestAuthorDefensivelyCopiesLoaderAndEvaluatorSlices(t *testing.T) {
	t.Parallel()
	fixture := newAuthorTestFixture(t)
	repository := newAuthorRepository()
	loader := &authorInputLoader{input: fixture.input}
	evaluator := fixture.evaluator(policy.DirectiveCompleteVerified, policy.RequirementSatisfied)
	author, _ := policy.NewAuthor(repository, loader, evaluator)
	decision, err := author.Author(t.Context(), fixture.scope, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	loader.input.Facts[0].ReasonCodes[0] = "changed"
	evaluator.output.Results[0].ReasonCodes[0] = "changed"
	if strings.Contains(string(decision.Snapshot().Canonical()), "changed") ||
		strings.Contains(string(decision.Evaluation().Canonical()), "changed") {
		t.Fatal("decision aliases dependency-owned slices")
	}
}

func newAuthorTestFixture(t testing.TB) authorTestFixture {
	t.Helper()
	base := policyFixture(t)
	scope, err := tenant.NewScope(base.input.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	return authorTestFixture{
		scope: scope,
		request: policy.AuthorRequest{
			DecisionID: base.decision, VerificationID: base.input.VerificationID,
			EvaluatedAt: base.now, DecidedAt: base.now.Add(time.Second),
		},
		input: policy.AuthorInput{
			AuthorityID: base.input.AuthorityID, AcknowledgementID: base.input.AcknowledgementID,
			Region: base.input.Region, Policy: base.input.Policy, Facts: base.input.Facts,
		},
		reference: base.input.Evaluator,
		fact:      base.checkFact,
	}
}

func (fixture authorTestFixture) evaluator(
	candidate policy.Directive,
	state policy.RequirementState,
) *authorEvaluator {
	return &authorEvaluator{reference: fixture.reference, output: fixture.output(candidate, state)}
}

func (fixture authorTestFixture) output(
	candidate policy.Directive,
	state policy.RequirementState,
) policy.EvaluatorOutput {
	assurance := ""
	if candidate == policy.DirectiveCompleteVerified {
		assurance = "global_individual_substantial.1"
	}
	return policy.EvaluatorOutput{
		Results: []policy.RequirementResult{{
			Name: "document_authenticity", State: state,
			ContributingFacts: []policy.FactKey{fixture.fact}, Candidate: candidate,
			Priority: 1, ReasonCodes: []string{"document_authenticity_evaluated"},
		}},
		Assurance: assurance,
	}
}
