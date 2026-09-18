//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestReviewRoutingAtomicReplayAndAuthority(t *testing.T) {
	for _, scenario := range []string{"rollback_replay", "withdrawn", "wrong_assignment", "unconfigured", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				base := prepareCompletion(t, f)
				previous := base.Snapshot()
				var responseValue string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.subject_responses WHERE tenant_id=$1 AND authority_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 1`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&responseValue); err != nil {
					t.Fatal(err)
				}
				responseID, err := id.ParseAcknowledgement(responseValue)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: f.scope.ID(), VerificationID: previous.VerificationID(), AuthorityID: previous.AuthorityID(), AcknowledgementID: responseID, Region: previous.Region(), Policy: previous.Policy(), Evaluator: previous.Evaluator(), EvaluatedAt: previous.EvaluatedAt(), Facts: previous.Facts()})
				if err != nil {
					t.Fatal(err)
				}
				results := base.Evaluation().Results()
				for index := range results {
					results[index].Candidate = policy.DirectiveRouteManualReview
				}
				evaluation, err := policy.Resolve(snapshot, results, "")
				if err != nil {
					t.Fatal(err)
				}
				request := policy.AuthorRequest{DecisionID: base.ID(), VerificationID: snapshot.VerificationID(), EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: base.DecidedAt()}
				if scenario == "wrong_assignment" {
					request.DecisionID, err = f.ids.NewDecision()
					if err != nil {
						t.Fatal(err)
					}
				}
				routing, err := policy.NewRouting(f.scope, request, snapshot, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				rule := review.RoutingRule{TenantID: f.scope.ID().String(), PolicyID: snapshot.Policy().ID.String(), Revision: snapshot.Policy().Revision, PolicyDigest: snapshot.Policy().Digest, RequiredCertificate: "document.level2", Oversight: review.OversightDual}
				rules := []review.RoutingRule{rule}
				if scenario == "unconfigured" {
					rules = nil
				}
				source := fixedIntegrationClock{now: f.now}
				if scenario == "expired" {
					source.now = f.creation.Session.ExpiresAt()
				}
				store, err := reviewpostgres.NewRoutingStore(f.runtime, f.ids, source, rules)
				if err != nil {
					t.Fatal(err)
				}
				actor, err := f.ids.NewTask()
				if err != nil {
					t.Fatal(err)
				}
				route := func(fail bool) error {
					return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
						if err := store.RouteWithin(ctx, f.scope, tx, routing, actor); err != nil {
							return err
						}
						if fail {
							return errPlannedQueueFailure
						}
						return nil
					})
				}
				if scenario == "withdrawn" {
					revokeProcessingAuthority(t, f, "withdrawal")
				}
				err = route(scenario == "rollback_replay")
				switch scenario {
				case "rollback_replay":
					if !errors.Is(err, errPlannedQueueFailure) {
						t.Fatalf("rollback=%v", err)
					}
				case "withdrawn", "expired":
					if !errors.Is(err, authority.ErrProcessingNotPermitted) {
						t.Fatalf("authority=%v", err)
					}
				default:
					if !errors.Is(err, policy.ErrInvalid) {
						t.Fatalf("invalid route=%v", err)
					}
				}
				assertReviewRouting(t, f, 0, "processing")
				if scenario != "rollback_replay" {
					return
				}
				if err := route(false); err != nil {
					t.Fatal(err)
				}
				assertReviewRouting(t, f, 1, "manual_review")
				saved, err := store.FindRouting(t.Context(), f.scope, request.DecisionID)
				if err != nil || saved.ValidateReplay(f.scope, request) != nil {
					t.Fatalf("restore=%v", err)
				}
				var encodedCase string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.review_cases WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&encodedCase); err != nil {
					t.Fatal(err)
				}
				caseID, err := id.ParseReviewCase(encodedCase)
				if err != nil {
					t.Fatal(err)
				}
				cases, err := reviewpostgres.New(f.runtime)
				if err != nil {
					t.Fatal(err)
				}
				value, err := cases.FindCase(t.Context(), f.scope, caseID)
				if err != nil || value.RoutingRequest != request.DecisionID || !value.ChallengedDecision.IsZero() {
					t.Fatalf("case=%+v error=%v", value, err)
				}
				otherTenant, err := f.ids.NewTenant()
				if err != nil {
					t.Fatal(err)
				}
				otherScope, err := tenant.NewScope(otherTenant)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.FindRouting(t.Context(), otherScope, request.DecisionID); !errors.Is(err, policy.ErrDecisionNotFound) {
					t.Fatalf("tenant leak=%v", err)
				}
				if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.policy_routing_receipts SET requested_at=requested_at+interval '1 second' WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
					t.Fatal("routing receipt was mutable")
				}
				if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.review_cases SET routing_request_id=NULL WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
					t.Fatal("case origin was mutable")
				}
				changed := request
				changed.DecidedAt = changed.DecidedAt.Add(time.Microsecond)
				if err := saved.ValidateReplay(f.scope, changed); !errors.Is(err, policy.ErrDecisionConflict) {
					t.Fatalf("changed replay=%v", err)
				}
				revokeProcessingAuthority(t, f, "withdrawal")
				// Reconstruct the adapter with expired time and no routing rules, as after restart/config removal.
				store, err = reviewpostgres.NewRoutingStore(f.runtime, f.ids, fixedIntegrationClock{now: f.creation.Session.ExpiresAt()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := route(false); err != nil {
					t.Fatalf("restart replay=%v", err)
				}
				assertReviewRouting(t, f, 1, "manual_review")
			})
		})
	}
}

func TestPolicyWorkflowRoutingTransitions(t *testing.T) {
	for _, test := range []struct {
		name      string
		state     policy.RequirementState
		directive policy.Directive
		wantState string
	}{
		{name: "request input", state: policy.RequirementUnavailable, directive: policy.DirectiveRequestInput, wantState: "awaiting_input"},
		{name: "fail workflow", state: policy.RequirementProhibited, directive: policy.DirectiveFailWorkflow, wantState: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				base := prepareCompletion(t, f)
				previous := base.Snapshot()
				var responseValue string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.subject_responses WHERE tenant_id=$1 AND authority_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 1`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&responseValue); err != nil {
					t.Fatal(err)
				}
				responseID, err := id.ParseAcknowledgement(responseValue)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: f.scope.ID(), VerificationID: previous.VerificationID(), AuthorityID: previous.AuthorityID(), AcknowledgementID: responseID, Region: previous.Region(), Policy: previous.Policy(), Evaluator: previous.Evaluator(), EvaluatedAt: previous.EvaluatedAt(), Facts: previous.Facts()})
				if err != nil {
					t.Fatal(err)
				}
				results := base.Evaluation().Results()
				for index := range results {
					results[index].State = test.state
					results[index].Candidate = test.directive
				}
				evaluation, err := policy.Resolve(snapshot, results, "")
				if err != nil {
					t.Fatal(err)
				}
				request := policy.AuthorRequest{DecisionID: base.ID(), VerificationID: snapshot.VerificationID(), EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: base.DecidedAt()}
				routing, err := policy.NewRouting(f.scope, request, snapshot, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				store, err := reviewpostgres.NewRoutingStore(f.runtime, f.ids, fixedIntegrationClock{now: f.now}, nil)
				if err != nil {
					t.Fatal(err)
				}
				actor, err := f.ids.NewTask()
				if err != nil {
					t.Fatal(err)
				}
				route := func() error {
					return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
						return store.RouteWithin(ctx, f.scope, tx, routing, actor)
					})
				}
				if err := route(); err != nil {
					t.Fatal(err)
				}
				assertWorkflowRouting(t, f, test.wantState)
				if err := route(); err != nil {
					t.Fatalf("replay: %v", err)
				}
				assertWorkflowRouting(t, f, test.wantState)
			})
		})
	}
}

func assertReviewRouting(t *testing.T, f captureAcceptanceFixture, count int, state string) {
	t.Helper()
	var receipts, cases, snapshots, evaluations, decisions, deliveries, transitions int
	var actual string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM idenqa.policy_routing_receipts),(SELECT count(*) FROM idenqa.review_cases),
 (SELECT count(*) FROM idenqa.policy_snapshots),(SELECT count(*) FROM idenqa.policy_evaluations),
 (SELECT count(*) FROM idenqa.verification_decisions),(SELECT count(*) FROM idenqa.webhook_deliveries),
 (SELECT count(*) FROM idenqa.verification_transitions WHERE to_state='manual_review'),
 (SELECT state FROM idenqa.verification_sessions WHERE id=$1)`, f.creation.Session.ID().String()).Scan(&receipts, &cases, &snapshots, &evaluations, &decisions, &deliveries, &transitions, &actual); err != nil {
		t.Fatal(err)
	}
	if receipts != count || cases != count || snapshots != count || evaluations != count || transitions != count || decisions != 0 || deliveries != 0 || actual != state {
		t.Fatalf("routing receipt/case/snapshot/evaluation/transition=%d/%d/%d/%d/%d terminal=%d/%d state=%s", receipts, cases, snapshots, evaluations, transitions, decisions, deliveries, actual)
	}
}

func assertWorkflowRouting(t *testing.T, f captureAcceptanceFixture, state string) {
	t.Helper()
	var receipts, cases, snapshots, evaluations, decisions, deliveries, transitions int
	var actual string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM idenqa.policy_routing_receipts),(SELECT count(*) FROM idenqa.review_cases),
 (SELECT count(*) FROM idenqa.policy_snapshots),(SELECT count(*) FROM idenqa.policy_evaluations),
 (SELECT count(*) FROM idenqa.verification_decisions),(SELECT count(*) FROM idenqa.webhook_deliveries),
 (SELECT count(*) FROM idenqa.verification_transitions WHERE to_state=$2),
 (SELECT state FROM idenqa.verification_sessions WHERE id=$1)`, f.creation.Session.ID().String(), state).Scan(&receipts, &cases, &snapshots, &evaluations, &decisions, &deliveries, &transitions, &actual); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || cases != 0 || snapshots != 1 || evaluations != 1 || transitions != 1 || decisions != 0 || deliveries != 0 || actual != state {
		t.Fatalf("routing receipt/case/snapshot/evaluation/transition=%d/%d/%d/%d/%d terminal=%d/%d state=%s", receipts, cases, snapshots, evaluations, transitions, decisions, deliveries, actual)
	}
}
