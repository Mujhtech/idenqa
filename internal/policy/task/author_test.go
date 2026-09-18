package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestHandlerAuthorsThroughBoundedTransactionWork(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	store := newDecisionStoreStub()
	builder := &decisionBuilderStub{decision: fixture.decision}
	handler, err := NewHandler(store, builder)
	if err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), fixture.delivery(t))
	if result.Outcome != platformtask.OutcomeComplete || work == nil || builder.calls != 1 || store.appendCalls != 0 {
		t.Fatalf("Prepare() result=%+v work=%v builder=%d append=%d", result, work != nil, builder.calls, store.appendCalls)
	}
	result = work(t.Context(), transactionStub{})
	if result.Outcome != platformtask.OutcomeComplete || store.appendCalls != 1 {
		t.Fatalf("work() result=%+v append=%d", result, store.appendCalls)
	}
	stored, err := store.Find(t.Context(), fixture.scope, fixture.request.DecisionID)
	if err != nil || stored.Digest() != fixture.decision.Digest() {
		t.Fatalf("stored decision = %q, %v", stored.Digest(), err)
	}
}

func TestHandlerExactReplayBypassesEvaluation(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	store := newDecisionStoreStub()
	store.decisions[fixture.request.DecisionID.String()] = fixture.decision
	builder := &decisionBuilderStub{err: errors.New("must not evaluate")}
	handler, _ := NewHandler(store, builder)
	work, result := handler.Prepare(t.Context(), fixture.delivery(t))
	if result.Outcome != platformtask.OutcomeComplete || work == nil || builder.calls != 0 {
		t.Fatalf("Prepare() result=%+v work=%v builder=%d", result, work != nil, builder.calls)
	}
	if result := work(t.Context(), transactionStub{}); result.Outcome != platformtask.OutcomeComplete || store.appendCalls != 0 {
		t.Fatalf("replay work = %+v, append=%d", result, store.appendCalls)
	}
}

func TestHandlerConcurrentReplayInsideEffectDoesNotAppend(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	store := newDecisionStoreStub()
	builder := &decisionBuilderStub{decision: fixture.decision}
	handler, _ := NewHandler(store, builder)
	work, result := handler.Prepare(t.Context(), fixture.delivery(t))
	if result.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("Prepare() = %+v", result)
	}
	store.mu.Lock()
	store.decisions[fixture.request.DecisionID.String()] = fixture.decision
	store.mu.Unlock()
	if result := work(t.Context(), transactionStub{}); result.Outcome != platformtask.OutcomeComplete || store.appendCalls != 0 {
		t.Fatalf("concurrent replay = %+v, append=%d", result, store.appendCalls)
	}
}

func TestHandlerClassifiesFailures(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	tests := []struct {
		name        string
		findErr     error
		buildErr    error
		appendErr   error
		wantOutcome platformtask.Outcome
		wantClass   platformtask.RetryClass
	}{
		{name: "read unavailable", findErr: errors.New("database unavailable"),
			wantOutcome: platformtask.OutcomeRetry, wantClass: platformtask.RetryClassUnavailable},
		{name: "builder cancelled", buildErr: context.Canceled,
			wantOutcome: platformtask.OutcomeRetry, wantClass: platformtask.RetryClassTransient},
		{name: "activation absent", buildErr: policy.ErrActivationNotFound,
			wantOutcome: platformtask.OutcomeRetry, wantClass: platformtask.RetryClassConflict},
		{name: "invalid facts", buildErr: policy.ErrInvalid,
			wantOutcome: platformtask.OutcomeQuarantine},
		{name: "append conflict", appendErr: policy.ErrDecisionConflict,
			wantOutcome: platformtask.OutcomeRetry, wantClass: platformtask.RetryClassConflict},
		{name: "authority withdrawn before append", appendErr: authority.ErrProcessingNotPermitted,
			wantOutcome: platformtask.OutcomeQuarantine},
		{name: "subject response no longer permits authoring", buildErr: authority.ErrSubjectResponseRequired,
			wantOutcome: platformtask.OutcomeQuarantine},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newDecisionStoreStub()
			store.findErr, store.appendErr = test.findErr, test.appendErr
			builder := &decisionBuilderStub{decision: fixture.decision, err: test.buildErr}
			handler, _ := NewHandler(store, builder)
			work, result := handler.Prepare(t.Context(), fixture.delivery(t))
			if result.Outcome == platformtask.OutcomeComplete && work != nil {
				result = work(t.Context(), transactionStub{})
			}
			if result.Outcome != test.wantOutcome || result.Class != test.wantClass {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestHandlerRejectsChangedAndMalformedReplay(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	store := newDecisionStoreStub()
	store.decisions[fixture.request.DecisionID.String()] = fixture.decision
	handler, _ := NewHandler(store, &decisionBuilderStub{})
	changed := fixture.request
	changed.DecidedAt = changed.DecidedAt.Add(time.Second)
	if _, result := handler.Prepare(t.Context(), fixture.deliveryFor(t, changed)); result.Outcome != platformtask.OutcomeQuarantine {
		t.Fatalf("changed replay result = %+v", result)
	}
	delivery := fixture.delivery(t)
	delivery.Intent = rebuildIntentPayload(t, delivery.Intent, []byte(`{"decision_id":true}`))
	if _, result := handler.Prepare(t.Context(), delivery); result.Outcome != platformtask.OutcomeQuarantine {
		t.Fatalf("malformed payload result = %+v", result)
	}
	if result := handler.Handle(t.Context(), fixture.delivery(t)); result.Outcome != platformtask.OutcomeQuarantine {
		t.Fatalf("Handle() result = %+v", result)
	}
}

type taskFixture struct {
	scope    tenant.Scope
	request  policy.AuthorRequest
	decision policy.Decision
}

func newTaskFixture(t *testing.T) taskFixture {
	t.Helper()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	decisionID, _ := id.ParseDecision("dec_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	request := policy.AuthorRequest{
		DecisionID: decisionID, VerificationID: verificationID,
		EvaluatedAt: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		DecidedAt:   time.Date(2026, time.August, 31, 12, 0, 1, 0, time.UTC),
	}
	return taskFixture{scope: scope, request: request, decision: taskDecision(t, scope, request)}
}

func (fixture taskFixture) delivery(t *testing.T) platformtask.Delivery {
	t.Helper()
	return fixture.deliveryFor(t, fixture.request)
}

func (fixture taskFixture) deliveryFor(t *testing.T, request policy.AuthorRequest) platformtask.Delivery {
	t.Helper()
	intent, err := NewAuthorIntent(
		taskIDGenerator{id: mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FB9")},
		fixture.scope, request,
		IntentMetadata{ScheduledAt: request.DecidedAt, Deadline: request.DecidedAt.Add(time.Minute)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return platformtask.Delivery{Intent: intent, Attempt: 1, Fence: 1}
}

func taskDecision(t *testing.T, scope tenant.Scope, request policy.AuthorRequest) policy.Decision {
	t.Helper()
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	authorityID, _ := id.ParseAuthority("aut_01ARZ3NDEKTSV4RRFFQ69G5FAZ")
	acknowledgementID, _ := id.ParseAcknowledgement("ack_01ARZ3NDEKTSV4RRFFQ69G5FB0")
	checkID, _ := id.ParseCheck("chk_01ARZ3NDEKTSV4RRFFQ69G5FB1")
	attemptID, _ := id.ParseAttempt("atm_01ARZ3NDEKTSV4RRFFQ69G5FB2")
	observationID, _ := id.ParseObservation("obs_01ARZ3NDEKTSV4RRFFQ69G5FB3")
	factKey, _ := policy.NewFactKey("document.authenticity")
	fact := policy.Fact{
		Key: factKey, State: policy.RequirementSatisfied,
		Source: policy.FactSource{Kind: policy.FactSourceCheck, Check: &policy.CheckSource{
			CheckID: checkID, CheckVersion: 3, AttemptID: attemptID,
			ObservationIDs: []id.Observation{observationID}, ContractDigest: strings.Repeat("a", 64),
			ImplementationDigest: strings.Repeat("b", 64),
		}},
		ObservedAt: request.EvaluatedAt,
	}
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{
		TenantID: scope.ID(), VerificationID: request.VerificationID,
		AuthorityID: authorityID, AcknowledgementID: acknowledgementID, Region: "local",
		Policy:      policy.Reference{ID: policyID, Revision: 1, SchemaMajor: 1, SchemaMinor: 0, Digest: strings.Repeat("c", 64)},
		Evaluator:   policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("d", 64)},
		EvaluatedAt: request.EvaluatedAt, Facts: []policy.Fact{fact},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := policy.Resolve(snapshot, []policy.RequirementResult{{
		Name: "authentic", State: policy.RequirementSatisfied,
		ContributingFacts: []policy.FactKey{factKey}, Candidate: policy.DirectiveCompleteVerified,
		Priority: 1, ReasonCodes: []string{"document_authentic"},
	}}, "document_verified")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: request.DecisionID, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorMachine, Supersedes: request.Supersedes, DecidedAt: request.DecidedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

type decisionBuilderStub struct {
	decision policy.Decision
	err      error
	calls    int
}

func (builder *decisionBuilderStub) Build(
	context.Context,
	tenant.Scope,
	policy.AuthorRequest,
) (policy.Decision, error) {
	builder.calls++
	return builder.decision, builder.err
}

type decisionStoreStub struct {
	mu          sync.Mutex
	decisions   map[string]policy.Decision
	findErr     error
	appendErr   error
	appendCalls int
}

func newDecisionStoreStub() *decisionStoreStub {
	return &decisionStoreStub{decisions: make(map[string]policy.Decision)}
}

func (store *decisionStoreStub) Find(
	_ context.Context,
	_ tenant.Scope,
	decisionID id.Decision,
) (policy.Decision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.findErr != nil {
		return policy.Decision{}, store.findErr
	}
	decision, exists := store.decisions[decisionID.String()]
	if !exists {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	return decision, nil
}

func (store *decisionStoreStub) FindWithin(
	ctx context.Context,
	scope tenant.Scope,
	_ postgres.Transaction,
	decisionID id.Decision,
) (policy.Decision, error) {
	return store.Find(ctx, scope, decisionID)
}

func (store *decisionStoreStub) AppendWithin(
	_ context.Context,
	_ tenant.Scope,
	_ postgres.Transaction,
	decision policy.Decision,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.appendCalls++
	if store.appendErr != nil {
		return store.appendErr
	}
	store.decisions[decision.ID().String()] = decision
	return nil
}

type transactionStub struct{}

func (transactionStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (transactionStub) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (transactionStub) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

func rebuildIntentPayload(t *testing.T, source platformtask.Intent, payload []byte) platformtask.Intent {
	t.Helper()
	intent, err := platformtask.NewIntent(platformtask.IntentSpec{
		ID: source.ID(), TenantID: source.TenantID(), Key: source.Key(), Queue: source.Queue(),
		PartitionKey: source.PartitionKey(), IdempotencyKey: source.IdempotencyKey(), Payload: json.RawMessage(payload),
		ScheduledAt: source.ScheduledAt(), Deadline: source.Deadline(), Retry: source.Retry(), Retention: source.Retention(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

type completionStoreStub struct {
	calls    int
	decision policy.Decision
	actor    id.Task
	err      error
}

func (store *completionStoreStub) CompleteWithin(_ context.Context, _ tenant.Scope, _ postgres.Transaction, decision policy.Decision, actor id.Task) error {
	store.calls++
	store.decision, store.actor = decision, actor
	return store.err
}

func TestHandlerCompletesNewAndReplayedDecisionInEffect(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"new", "existing", "concurrent", "completion_failure", "authority_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			fixture := newTaskFixture(t)
			store := newDecisionStoreStub()
			builder := &decisionBuilderStub{decision: fixture.decision}
			completion := &completionStoreStub{}
			if scenario == "existing" {
				store.decisions[fixture.decision.ID().String()] = fixture.decision
			}
			if scenario == "completion_failure" {
				completion.err = errors.New("queue insertion failed")
			}
			if scenario == "authority_revoked" {
				completion.err = authority.ErrProcessingNotPermitted
			}
			handler, err := NewHandlerWithCompletion(store, builder, completion)
			if err != nil {
				t.Fatal(err)
			}
			delivery := fixture.delivery(t)
			work, result := handler.Prepare(t.Context(), delivery)
			if result.Outcome != platformtask.OutcomeComplete || work == nil || completion.calls != 0 {
				t.Fatalf("prepare=%+v calls=%d", result, completion.calls)
			}
			if scenario == "concurrent" {
				store.decisions[fixture.decision.ID().String()] = fixture.decision
			}
			result = work(t.Context(), transactionStub{})
			want := platformtask.OutcomeComplete
			if scenario == "completion_failure" {
				want = platformtask.OutcomeRetry
			}
			if scenario == "authority_revoked" {
				want = platformtask.OutcomeQuarantine
			}
			if result.Outcome != want || completion.calls != 1 || completion.decision.Digest() != fixture.decision.Digest() || completion.actor != delivery.Intent.ID() {
				t.Fatalf("completion effect=%+v calls=%d actor=%s", result, completion.calls, completion.actor)
			}
		})
	}
}
