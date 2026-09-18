//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

type failingCompletionIdentifiers struct{}

func (failingCompletionIdentifiers) NewEvent() (id.Event, error) {
	return id.Event{}, errPlannedQueueFailure
}

type reviewEvaluator struct {
	reference   policy.EvaluatorReference
	calls       int
	nonterminal bool
}

func (evaluator *reviewEvaluator) Reference() policy.EvaluatorReference { return evaluator.reference }
func (evaluator *reviewEvaluator) Evaluate(_ context.Context, snapshot policy.Snapshot) (policy.EvaluatorOutput, error) {
	evaluator.calls++
	key, _ := policy.NewFactKey("review.resolution")
	for _, fact := range snapshot.Facts() {
		if fact.Key == key {
			directive := policy.DirectiveCompleteVerified
			assurance := "reviewed.identity"
			if evaluator.nonterminal {
				directive = policy.DirectiveRequestInput
				assurance = ""
			}
			return policy.EvaluatorOutput{Results: []policy.RequirementResult{{Name: "review_resolution", State: fact.State, ContributingFacts: []policy.FactKey{key}, Candidate: directive, Priority: 1, ReasonCodes: []string{"reviewed"}}}, Assurance: assurance}, nil
		}
	}
	return policy.EvaluatorOutput{}, policy.ErrInvalid
}

func TestAcceptedReviewEvaluationAtomicCompletionAndReplay(t *testing.T) {
	for _, scenario := range []string{"terminal", "nonterminal", "recapture_terminal", "withdrawn", "revoked_grant", "wrong_case_grant"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
				value, routing := prepareEvaluationReview(t, f)
				source := fixedIntegrationClock{now: f.now}
				cases, err := reviewpostgres.NewWithClock(f.runtime, source)
				if err != nil {
					t.Fatal(err)
				}
				principal := review.Principal{ID: "operator.one", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
				claimed, err := value.Claim(principal, value.Version, f.now)
				if err != nil {
					t.Fatal(err)
				}
				actor := review.Actor{ID: "test.operator"}
				if err := cases.SaveCase(t.Context(), f.scope, actor, claimed, value.Version, nil); err != nil {
					t.Fatal(err)
				}
				grantID, err := f.ids.NewGrant()
				if err != nil {
					t.Fatal(err)
				}
				checkRef := "review." + strings.ToLower(value.ID.String())
				if scenario == "wrong_case_grant" {
					checkRef = "review.other"
				}
				_, err = f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.evidence_processing_grants
 (id,tenant_id,subject_id,verification_id,evidence_id,requirement_key,authority_id,response_id,check_reference,runner_identity,workload_version,purpose,operation,permitted_variants,region,recipient_reference,output_destination,policy_reference,maximum_uses,uses,created_at,expires_at)
 SELECT $1,u.tenant_id,u.subject_id,u.verification_id,u.evidence_id,u.requirement_key,u.authority_id,u.response_id,$2,'operator.one','review.v1',u.purpose,'evidence.plaintext.read',ARRAY['evidence.variant.original'],u.region,'operator.one','review.findings','policy.review',1,0,$3,$4
 FROM idenqa.evidence_upload_intents u WHERE tenant_id=$5 AND verification_id=$6 AND state='accepted' ORDER BY id LIMIT 1`, grantID.String(), checkRef, f.now, f.now.Add(time.Hour), f.scope.ID().String(), value.VerificationID.String())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.evidence_processing_grants SET uses=1 WHERE id=$1`, grantID.String()); err != nil {
					t.Fatal(err)
				}
				// Preserve the existing synthetic fixture with the current display binding and successful-read receipt.
				redemptionID, err := f.ids.NewRedemption()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.review_evidence_access(tenant_id,grant_id,redemption_id,case_id,case_version,evidence_id,reviewer_id,redactions,recorded_at) SELECT tenant_id,id,$2,$3,$4,evidence_id,'operator.one','[]',$5 FROM idenqa.evidence_processing_grants WHERE id=$1`, grantID.String(), redemptionID.String(), value.ID.String(), claimed.Version, f.now); err != nil {
					t.Fatal(err)
				}
				if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.evidence_grant_redemptions(id,tenant_id,grant_id,runner_identity,workload_version,claimed_use,attempted_at) VALUES($1,$2,$3,'operator.one','review.v1',1,$4)`, redemptionID.String(), f.scope.ID().String(), grantID.String(), f.now); err != nil {
					t.Fatal(err)
				}
				if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.evidence_grant_redemption_outcomes(tenant_id,redemption_id,grant_id,outcome,occurred_at) VALUES($1,$2,$3,'succeeded',$4)`, f.scope.ID().String(), redemptionID.String(), grantID.String(), f.now); err != nil {
					t.Fatal(err)
				}
				findingID, err := f.ids.NewFinding()
				if err != nil {
					t.Fatal(err)
				}
				finding := review.Finding{ID: findingID, ReviewerID: principal.ID, Resolution: review.ResolutionSatisfy, ReasonCode: "document_reviewed", GrantIDs: []id.Grant{grantID}, RecordedAt: f.now}
				resolved, err := claimed.SubmitFinding(principal, finding, []review.EvidenceGrant{{ID: grantID, ReviewerID: principal.ID, Region: value.Region, ExpiresAt: f.now.Add(time.Hour)}}, claimed.Version)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "revoked_grant" {
					if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.evidence_processing_grants SET revoked_at=$2 WHERE id=$1`, grantID.String(), f.now); err != nil {
						t.Fatal(err)
					}
				}
				err = cases.SaveCase(t.Context(), f.scope, actor, resolved, claimed.Version, &finding)
				if scenario == "revoked_grant" || scenario == "wrong_case_grant" {
					if !errors.Is(err, review.ErrForbidden) {
						t.Fatalf("grant denial=%v", err)
					}
					var count int
					if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.review_evaluation_requests`).Scan(&count); err != nil || count != 0 {
						t.Fatalf("denied finding enqueued: %d %v", count, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				evaluator := &reviewEvaluator{reference: routing.Snapshot().Evaluator(), nonterminal: scenario == "nonterminal" || scenario == "recapture_terminal"}
				identifiers := verificationpostgres.CompletionIdentifiers(f.ids)
				if scenario == "terminal" {
					identifiers = failingCompletionIdentifiers{}
				}
				completion, err := verificationpostgres.NewCompletionStore(f.runtime, identifiers, source)
				if err != nil {
					t.Fatal(err)
				}
				evaluations, err := reviewpostgres.NewEvaluationStore(f.runtime, evaluator, completion, source)
				if err != nil {
					t.Fatal(err)
				}
				targets, err := evaluations.ListReady(t.Context(), f.now, 100)
				if err != nil || len(targets) != 1 {
					t.Fatalf("discovery=%d %v", len(targets), err)
				}
				request := review.EvaluationRequest{CaseID: value.ID, Version: resolved.Version}
				candidate, err := evaluations.Build(t.Context(), f.scope, request)
				if err != nil {
					t.Fatal(err)
				}
				if candidate.Snapshot.Policy() != routing.Snapshot().Policy() || candidate.Snapshot.Facts()[0].Source.Kind == policy.FactSourceReviewFinding {
					t.Fatal("lost original pinned policy/facts")
				}
				taskID, err := f.ids.NewTask()
				if err != nil {
					t.Fatal(err)
				}
				commit := func() error {
					return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
						return evaluations.CommitWithin(ctx, f.scope, tx, candidate, taskID)
					})
				}
				if scenario == "terminal" {
					decision, err := candidate.Decision()
					if err != nil {
						t.Fatal(err)
					}
					guarded, err := policypostgres.NewGuarded(f.runtime, source)
					if err != nil {
						t.Fatal(err)
					}
					err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
						return guarded.AppendWithin(ctx, f.scope, tx, decision)
					})
					if !errors.Is(err, policy.ErrInvalid) {
						t.Fatalf("missing review provenance=%v", err)
					}
					original := candidate
					candidate.CaseDigest = strings.Repeat("0", 64)
					if err := commit(); !errors.Is(err, review.ErrConflict) {
						t.Fatalf("changed digest=%v", err)
					}
					candidate = original
					candidate.Request.Version--
					if err := commit(); !errors.Is(err, review.ErrConflict) {
						t.Fatalf("stale version=%v", err)
					}
					candidate = original
				}
				if scenario == "withdrawn" {
					revokeProcessingAuthority(t, f, "withdrawal")
					if err := commit(); !errors.Is(err, authority.ErrProcessingNotPermitted) {
						t.Fatalf("withdrawn=%v", err)
					}
					return
				}
				if scenario == "terminal" {
					if err := commit(); !errors.Is(err, errPlannedQueueFailure) {
						t.Fatalf("completion rollback=%v", err)
					}
					var count int
					if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.review_evaluations`).Scan(&count); err != nil || count != 0 {
						t.Fatalf("partial evaluation=%d %v", count, err)
					}
				}
				completion, err = verificationpostgres.NewCompletionStore(f.runtime, f.ids, source)
				if err != nil {
					t.Fatal(err)
				}
				evaluations, err = reviewpostgres.NewEvaluationStore(f.runtime, evaluator, completion, source)
				if err != nil {
					t.Fatal(err)
				}
				if err := commit(); err != nil {
					t.Fatal(err)
				}
				var state string
				var decisionCount, deliveryCount int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,(SELECT count(*) FROM idenqa.verification_decisions),(SELECT count(*) FROM idenqa.webhook_deliveries) FROM idenqa.verification_sessions WHERE id=$1`, value.VerificationID.String()).Scan(&state, &decisionCount, &deliveryCount); err != nil {
					t.Fatal(err)
				}
				if scenario == "terminal" {
					if state != "completed" || decisionCount != 1 || deliveryCount != 0 {
						t.Fatalf("completion=%s %d %d", state, decisionCount, deliveryCount)
					}
				} else if state != "manual_review" || decisionCount != 0 || deliveryCount != 0 {
					t.Fatalf("nonterminal=%s %d %d", state, decisionCount, deliveryCount)
				}
				saved, err := evaluations.Find(t.Context(), f.scope, request)
				if err != nil || saved.CaseDigest != candidate.CaseDigest {
					t.Fatalf("saved=%v", err)
				}
				if scenario == "nonterminal" || scenario == "recapture_terminal" {
					testLinkedRecapture(t, f, resolved, candidate, scenario == "recapture_terminal")
				}
				revokeProcessingAuthority(t, f, "withdrawal")
				if err := commit(); err != nil {
					t.Fatalf("replay after withdrawal=%v", err)
				}
				targets, err = evaluations.ListReady(t.Context(), f.now, 100)
				if err != nil || len(targets) != 0 {
					t.Fatalf("completed discovery=%d %v", len(targets), err)
				}
			}, "tenant-region-ng", "tenant-region-ng")
		})
	}
}

func prepareEvaluationReview(t *testing.T, f captureAcceptanceFixture) (review.Case, policy.Routing) {
	t.Helper()
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
	routing, err := policy.NewRouting(f.scope, policy.AuthorRequest{DecisionID: base.ID(), VerificationID: snapshot.VerificationID(), EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: base.DecidedAt()}, snapshot, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	rules := []review.RoutingRule{{TenantID: f.scope.ID().String(), PolicyID: snapshot.Policy().ID.String(), Revision: snapshot.Policy().Revision, PolicyDigest: snapshot.Policy().Digest, RequiredCertificate: "document.level2", Oversight: review.OversightSingle, PermittedFindings: []review.PermittedFinding{{Resolution: review.ResolutionSatisfy, ReasonCode: "document_reviewed"}}}}
	store, err := reviewpostgres.NewRoutingStore(f.runtime, f.ids, fixedIntegrationClock{now: f.now}, rules)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := f.ids.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		return store.RouteWithin(ctx, f.scope, tx, routing, actor)
	}); err != nil {
		t.Fatal(err)
	}
	var caseValue string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.review_cases WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&caseValue); err != nil {
		t.Fatal(err)
	}
	caseID, err := id.ParseReviewCase(caseValue)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := reviewpostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	value, err := cases.FindCase(t.Context(), f.scope, caseID)
	if err != nil {
		t.Fatal(err)
	}
	return value, routing
}
