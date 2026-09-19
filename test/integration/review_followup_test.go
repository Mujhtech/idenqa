//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	reviewtask "github.com/Mujhtech/idenqa/internal/review/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

// fixedReviewAuthority returns one configured principal for every actor. These
// repository-level fixtures exercise the store/domain checks directly;
// production composition resolves durable assignments through Authority.
type fixedReviewAuthority struct{ principal review.Principal }

func (authority fixedReviewAuthority) ResolveReviewer(context.Context, tenant.Scope, review.Actor, string, time.Time) (review.Principal, error) {
	return authority.principal, nil
}

// factDirectiveEvaluator turns one review follow-up fact into a policy result.
type factDirectiveEvaluator struct {
	reference policy.EvaluatorReference
	fact      policy.FactKey
	name      string
	terminal  bool
}

func (evaluator *factDirectiveEvaluator) Reference() policy.EvaluatorReference {
	return evaluator.reference
}

func (evaluator *factDirectiveEvaluator) Evaluate(_ context.Context, snapshot policy.Snapshot) (policy.EvaluatorOutput, error) {
	for _, fact := range snapshot.Facts() {
		if fact.Key != evaluator.fact {
			continue
		}
		directive := policy.DirectiveRequestInput
		assurance := ""
		if evaluator.terminal {
			directive = policy.DirectiveCompleteVerified
			assurance = "reviewed.identity"
		}
		return policy.EvaluatorOutput{
			Results: []policy.RequirementResult{{
				Name: evaluator.name, State: fact.State, ContributingFacts: []policy.FactKey{evaluator.fact},
				Candidate: directive, Priority: 1, ReasonCodes: []string{evaluator.name + "_reviewed"},
			}},
			Assurance: assurance,
		}, nil
	}
	return policy.EvaluatorOutput{}, policy.ErrInvalid
}

func mustFactKey(t *testing.T, value string) policy.FactKey {
	t.Helper()
	key, err := policy.NewFactKey(value)
	if err != nil {
		t.Fatalf("NewFactKey(%q): %v", value, err)
	}
	return key
}

func reviewActorKey(t *testing.T, f captureAcceptanceFixture) id.APIKey {
	t.Helper()
	var encoded string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT created_by FROM idenqa.processing_authorities WHERE tenant_id=$1 AND id=$2`,
		f.scope.ID().String(), f.declaration.ID().String()).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	key, err := id.ParseAPIKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func reviewPolicySettings(f captureAcceptanceFixture, base policy.Decision, oversight review.Oversight) review.PolicySettings {
	reference := base.Snapshot().Policy()
	return review.PolicySettings{
		RoutingRule: review.RoutingRule{
			TenantID: f.scope.ID().String(), PolicyID: reference.ID.String(), Revision: reference.Revision,
			PolicyDigest: reference.Digest, RequiredCertificate: "document.level2", Oversight: oversight,
			PermittedFindings: []review.PermittedFinding{
				{Resolution: review.ResolutionSatisfy, ReasonCode: "document_reviewed"},
				{Resolution: review.ResolutionNotSatisfy, ReasonCode: "document_mismatch"},
			},
		},
		Priority: 1, SLASeconds: 3600, Language: "en-NG", Reason: "manual_review_requested",
		Assurance: "reviewed.identity", Risk: "standard", SamplePercent: 0,
		AppealWindowSeconds: 3600, EscalationCertificate: "supervisor.level3",
	}
}

func seedReviewPolicyRevision(t *testing.T, f captureAcceptanceFixture, reference policy.Reference, evaluator policy.EvaluatorReference) {
	t.Helper()
	_, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.policy_revisions
 (tenant_id,policy_id,revision,schema_major,schema_minor,digest,evaluator_major,evaluator_minor,evaluator_digest,canonical,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		f.scope.ID().String(), reference.ID.String(), int64(reference.Revision), reference.SchemaMajor, reference.SchemaMinor,
		reference.Digest, evaluator.Major, evaluator.Minor, evaluator.Digest, `{"policy":"review-integration"}`, f.now)
	if err != nil {
		t.Fatalf("seed review policy revision: %v", err)
	}
}

func administerReviewPolicy(t *testing.T, f captureAcceptanceFixture, settings review.PolicySettings) {
	t.Helper()
	actor := reviewActorKey(t, f)
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewpostgres.New(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	input := review.Administration{
		Kind: "policy", Reference: fmt.Sprintf("%s/%d", settings.PolicyID, settings.Revision), ExpectedVersion: 0,
		Configuration: body, Actor: review.Actor{ID: actor.String()}, At: f.now,
		Retry: integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.admin.policy", "review-policy-settings", body, f.now),
	}
	if _, err := store.Administer(t.Context(), f.scope, input); err != nil {
		t.Fatalf("administer review policy: %v", err)
	}
}

func routeReviewWithSettings(t *testing.T, f captureAcceptanceFixture, base policy.Decision) (review.Case, policy.Routing) {
	t.Helper()
	previous := base.Snapshot()
	var responseValue string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.subject_responses WHERE tenant_id=$1 AND authority_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 1`,
		f.scope.ID().String(), f.declaration.ID().String()).Scan(&responseValue); err != nil {
		t.Fatal(err)
	}
	responseID, err := id.ParseAcknowledgement(responseValue)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: f.scope.ID(), VerificationID: previous.VerificationID(),
		AuthorityID: previous.AuthorityID(), AcknowledgementID: responseID, Region: previous.Region(), Policy: previous.Policy(),
		Evaluator: previous.Evaluator(), EvaluatedAt: previous.EvaluatedAt(), Facts: previous.Facts()})
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
	routing, err := policy.NewRouting(f.scope, policy.AuthorRequest{DecisionID: base.ID(), VerificationID: snapshot.VerificationID(),
		EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: base.DecidedAt()}, snapshot, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewpostgres.NewRoutingStore(f.runtime, integrationProtector{}, f.ids, fixedIntegrationClock{now: f.now}, nil)
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
	cases, err := reviewpostgres.New(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := cases.FindCase(t.Context(), f.scope, caseID)
	if err != nil {
		t.Fatal(err)
	}
	return value, routing
}

func claimReviewCase(t *testing.T, f captureAcceptanceFixture, value review.Case, principal review.Principal, at time.Time) review.Case {
	t.Helper()
	cases, err := reviewpostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	next, err := value.Claim(principal, value.Version, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := cases.SaveCase(t.Context(), f.scope, review.Actor{ID: "test.operator"}, next, value.Version, nil); err != nil {
		t.Fatal(err)
	}
	return next
}

func seedReviewEvidenceGrant(t *testing.T, f captureAcceptanceFixture, caseID id.ReviewCase, caseVersion int64, reviewer string) id.Grant {
	t.Helper()
	grantID, err := f.ids.NewGrant()
	if err != nil {
		t.Fatal(err)
	}
	redemptionID, err := f.ids.NewRedemption()
	if err != nil {
		t.Fatal(err)
	}
	checkReference := "review." + strings.ToLower(caseID.String())
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.evidence_processing_grants
 (id,tenant_id,subject_id,verification_id,evidence_id,requirement_key,authority_id,response_id,check_reference,runner_identity,workload_version,purpose,operation,permitted_variants,region,recipient_reference,output_destination,policy_reference,maximum_uses,uses,created_at,expires_at)
 SELECT $1,u.tenant_id,u.subject_id,u.verification_id,u.evidence_id,u.requirement_key,u.authority_id,u.response_id,$2,$3,'review.v1',u.purpose,'evidence.plaintext.read',ARRAY['evidence.variant.original'],u.region,$3,'review.findings','policy.review',1,0,$4,$5
 FROM idenqa.evidence_upload_intents u WHERE u.tenant_id=$6 AND u.verification_id=$7 AND u.state='accepted' ORDER BY u.id LIMIT 1`,
		grantID.String(), checkReference, reviewer, f.now, f.now.Add(time.Hour), f.scope.ID().String(), f.creation.Session.ID().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.evidence_processing_grants SET uses=1 WHERE id=$1`, grantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.review_evidence_access(tenant_id,grant_id,redemption_id,case_id,case_version,evidence_id,reviewer_id,redactions,recorded_at) SELECT tenant_id,id,$2,$3,$4,evidence_id,$5,'[]',$6 FROM idenqa.evidence_processing_grants WHERE id=$1`,
		grantID.String(), redemptionID.String(), caseID.String(), caseVersion, reviewer, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.evidence_grant_redemptions(id,tenant_id,grant_id,runner_identity,workload_version,claimed_use,attempted_at) VALUES($1,$2,$3,$4,'review.v1',1,$5)`,
		redemptionID.String(), f.scope.ID().String(), grantID.String(), reviewer, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.evidence_grant_redemption_outcomes(tenant_id,redemption_id,grant_id,outcome,occurred_at) VALUES($1,$2,$3,'succeeded',$4)`,
		f.scope.ID().String(), redemptionID.String(), grantID.String(), f.now); err != nil {
		t.Fatal(err)
	}
	return grantID
}

func recordReviewFinding(t *testing.T, f captureAcceptanceFixture, value review.Case, principal review.Principal, resolution review.Resolution, reason string, at time.Time) review.Case {
	t.Helper()
	cases, err := reviewpostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	grantID := seedReviewEvidenceGrant(t, f, value.ID, value.Version, principal.ID)
	findingID, err := f.ids.NewFinding()
	if err != nil {
		t.Fatal(err)
	}
	finding := review.Finding{ID: findingID, ReviewerID: principal.ID, Resolution: resolution, ReasonCode: reason, GrantIDs: []id.Grant{grantID}, RecordedAt: at}
	next, err := value.SubmitFinding(principal, finding, []review.EvidenceGrant{{ID: grantID, ReviewerID: principal.ID, Region: value.Region, ExpiresAt: at.Add(time.Hour)}}, value.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := cases.SaveCase(t.Context(), f.scope, review.Actor{ID: "test.operator"}, next, value.Version, &finding); err != nil {
		t.Fatal(err)
	}
	return next
}

func completeIntegrationVerification(t *testing.T, f captureAcceptanceFixture, decision policy.Decision) {
	t.Helper()
	source := fixedIntegrationClock{now: f.now}
	decisions, err := policypostgres.NewGuarded(f.runtime, integrationProtector{}, source)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, f.ids, source)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := f.ids.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := decisions.AppendWithin(ctx, f.scope, tx, decision); err != nil {
			return err
		}
		return completion.CompleteWithin(ctx, f.scope, tx, decision, actor)
	}); err != nil {
		t.Fatalf("complete verification: %v", err)
	}
}

func newReviewFollowupStore(t *testing.T, f captureAcceptanceFixture, principal review.Principal, evaluator policy.Evaluator, at time.Time) *reviewpostgres.FollowupStore {
	t.Helper()
	store, err := reviewpostgres.NewFollowupStore(f.runtime, integrationProtector{}, fixedReviewAuthority{principal}, evaluator, f.ids, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func selectReviewDecisionBytes(t *testing.T, f captureAcceptanceFixture, decision id.Decision) (string, string) {
	t.Helper()
	var canonical, digest string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT canonical,decision_digest FROM idenqa.verification_decisions WHERE tenant_id=$1 AND id=$2`,
		f.scope.ID().String(), decision.String()).Scan(&canonical, &digest); err != nil {
		t.Fatal(err)
	}
	return canonical, digest
}

func countReviewRows(t *testing.T, f captureAcceptanceFixture, query string, arguments ...any) int {
	t.Helper()
	var count int
	if err := f.admin.Native().QueryRow(t.Context(), query, arguments...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestReviewEscalationArbitrationPolicyDecision(t *testing.T) {
	for _, test := range []struct {
		name     string
		terminal bool
	}{
		{name: "terminal", terminal: true},
		{name: "nonterminal", terminal: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
				base := prepareCompletion(t, f)
				settings := reviewPolicySettings(f, base, review.OversightDual)
				seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
				administerReviewPolicy(t, f, settings)
				value, routing := routeReviewWithSettings(t, f, base)

				firstReviewer := review.Principal{ID: "reviewer.one", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
				secondReviewer := review.Principal{ID: "reviewer.two", Permissions: []review.Permission{review.PermissionFind}, Certifications: []string{"document.level2"}}
				claimed := claimReviewCase(t, f, value, firstReviewer, f.now)
				first := recordReviewFinding(t, f, claimed, firstReviewer, review.ResolutionSatisfy, "document_reviewed", f.now)
				if first.State != review.CaseAwaitingSecond {
					t.Fatalf("first finding state=%s", first.State)
				}
				escalated := recordReviewFinding(t, f, first, secondReviewer, review.ResolutionNotSatisfy, "document_mismatch", f.now)
				if escalated.State != review.CaseEscalated {
					t.Fatalf("conflicting findings state=%s", escalated.State)
				}

				supervisor := review.Principal{ID: "supervisor.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2", "supervisor.level3"}}
				evaluator := &factDirectiveEvaluator{reference: routing.Snapshot().Evaluator(), fact: mustFactKey(t, "review.arbitration"), name: "arbitration", terminal: test.terminal}
				store := newReviewFollowupStore(t, f, supervisor, evaluator, f.now)
				actor := reviewActorKey(t, f)
				input := review.FollowupInput{
					CaseID: value.ID, Version: escalated.Version, Resolution: review.ResolutionSatisfy, Reason: "document_reviewed",
					Actor: review.Actor{ID: actor.String()}, At: f.now,
					Retry: integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.arbitrate", "arbitration-first", []byte(escalated.ID.String()), f.now),
				}
				result, err := store.Arbitrate(t.Context(), f.scope, input)
				if err != nil {
					t.Fatalf("Arbitrate() error = %v", err)
				}
				if result.CaseID != value.ID.String() || result.Version != escalated.Version+1 {
					t.Fatalf("arbitration result = %+v", result)
				}
				replay, err := store.Arbitrate(t.Context(), f.scope, input)
				if err != nil || replay != result {
					t.Fatalf("arbitration replay = %+v, %v", replay, err)
				}
				if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_arbitrations WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
					t.Fatalf("arbitration rows=%d", count)
				}
				if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.review_arbitrations SET reason_code='rewritten' WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
					t.Fatal("arbitration was mutable")
				}
				var state string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT state FROM idenqa.review_cases WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), value.ID.String()).Scan(&state); err != nil || state != string(review.CaseResolved) {
					t.Fatalf("arbitrated case state=%s error=%v", state, err)
				}

				completion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, f.ids, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				evaluations, err := reviewpostgres.NewEvaluationStore(f.runtime, integrationProtector{}, evaluator, completion, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				request := review.EvaluationRequest{CaseID: value.ID, Version: escalated.Version + 1}
				targets, err := evaluations.ListReady(t.Context(), f.now, 100)
				if err != nil || len(targets) != 1 || targets[0].Request != request {
					t.Fatalf("arbitration discovery=%+v error=%v", targets, err)
				}
				candidate, err := evaluations.Build(t.Context(), f.scope, request)
				if err != nil {
					t.Fatal(err)
				}
				if candidate.Snapshot.Policy() != routing.Snapshot().Policy() {
					t.Fatal("arbitration changed the pinned policy")
				}
				factKey := mustFactKey(t, "review.arbitration")
				found := false
				for _, fact := range candidate.Snapshot.Facts() {
					if fact.Key == factKey {
						found = fact.State == policy.RequirementSatisfied && fact.Source.Kind == policy.FactSourceReviewFinding
					}
				}
				if !found {
					t.Fatalf("missing satisfied review.arbitration fact: %+v", candidate.Snapshot.Facts())
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
				if err := commit(); err != nil {
					t.Fatalf("arbitration evaluation commit: %v", err)
				}
				if err := commit(); err != nil {
					t.Fatalf("arbitration evaluation replay: %v", err)
				}
				saved, err := evaluations.Find(t.Context(), f.scope, request)
				if err != nil || saved.Snapshot.Digest() != candidate.Snapshot.Digest() {
					t.Fatalf("committed arbitration evaluation = %+v, %v", saved, err)
				}
				persisted := false
				for _, fact := range saved.Snapshot.Facts() {
					if fact.Key == factKey {
						persisted = fact.State == policy.RequirementSatisfied
					}
				}
				if !persisted {
					t.Fatalf("committed snapshot lost review.arbitration: %+v", saved.Snapshot.Facts())
				}
				var sessionState string
				var decisions, evaluationsCount, audits int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,
 (SELECT count(*) FROM idenqa.verification_decisions),
 (SELECT count(*) FROM idenqa.review_evaluations WHERE tenant_id=$1 AND case_id=$2),
 (SELECT count(*) FROM idenqa.audit_records WHERE event_type='review.case.evaluated')
 FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$3`, f.scope.ID().String(), value.ID.String(), value.VerificationID.String()).Scan(&sessionState, &decisions, &evaluationsCount, &audits); err != nil {
					t.Fatal(err)
				}
				if evaluationsCount != 1 || audits != 1 {
					t.Fatalf("evaluation rows=%d audits=%d", evaluationsCount, audits)
				}
				if test.terminal {
					if sessionState != "completed" || decisions != 1 {
						t.Fatalf("terminal arbitration session=%s decisions=%d", sessionState, decisions)
					}
				} else if sessionState != "manual_review" || decisions != 0 {
					t.Fatalf("nonterminal arbitration session=%s decisions=%d", sessionState, decisions)
				}
				targets, err = evaluations.ListReady(t.Context(), f.now, 100)
				if err != nil || len(targets) != 0 {
					t.Fatalf("consumed arbitration discovery=%+v error=%v", targets, err)
				}
			}, "tenant-region-ng", "tenant-region-ng")
		})
	}
}

func TestReviewArbitrationNegativeAuthority(t *testing.T) {
	for _, test := range []struct {
		name      string
		conflict  bool
		principal review.Principal
		expect    error
	}{
		{
			name: "non_escalated_case", conflict: false,
			principal: review.Principal{ID: "supervisor.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2", "supervisor.level3"}},
			expect:    review.ErrConflict,
		},
		{
			name: "finding_reviewer", conflict: true,
			principal: review.Principal{ID: "reviewer.one", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2", "supervisor.level3"}},
			expect:    review.ErrForbidden,
		},
		{
			name: "missing_escalation_certificate", conflict: true,
			principal: review.Principal{ID: "supervisor.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2"}},
			expect:    review.ErrForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
				base := prepareCompletion(t, f)
				settings := reviewPolicySettings(f, base, review.OversightDual)
				seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
				administerReviewPolicy(t, f, settings)
				value, routing := routeReviewWithSettings(t, f, base)

				firstReviewer := review.Principal{ID: "reviewer.one", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
				secondReviewer := review.Principal{ID: "reviewer.two", Permissions: []review.Permission{review.PermissionFind}, Certifications: []string{"document.level2"}}
				claimed := claimReviewCase(t, f, value, firstReviewer, f.now)
				firstResolution, firstReason := review.ResolutionSatisfy, "document_reviewed"
				if !test.conflict {
					firstResolution, firstReason = review.ResolutionNotSatisfy, "document_mismatch"
				}
				first := recordReviewFinding(t, f, claimed, firstReviewer, firstResolution, firstReason, f.now)
				escalated := recordReviewFinding(t, f, first, secondReviewer, review.ResolutionNotSatisfy, "document_mismatch", f.now)
				if test.conflict && escalated.State != review.CaseEscalated {
					t.Fatalf("expected escalation, state=%s", escalated.State)
				}
				if !test.conflict && escalated.State != review.CaseResolved {
					t.Fatalf("expected resolution, state=%s", escalated.State)
				}

				evaluator := &factDirectiveEvaluator{reference: routing.Snapshot().Evaluator(), fact: mustFactKey(t, "review.arbitration"), name: "arbitration", terminal: true}
				store := newReviewFollowupStore(t, f, test.principal, evaluator, f.now)
				actor := reviewActorKey(t, f)
				requests := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_evaluation_requests WHERE tenant_id=$1`, f.scope.ID().String())
				input := review.FollowupInput{
					CaseID: value.ID, Version: escalated.Version, Resolution: review.ResolutionSatisfy, Reason: "document_reviewed",
					Actor: review.Actor{ID: actor.String()}, At: f.now,
					Retry: integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.arbitrate", "arbitration-denied", []byte(value.ID.String()), f.now),
				}
				if _, err := store.Arbitrate(t.Context(), f.scope, input); !errors.Is(err, test.expect) {
					t.Fatalf("Arbitrate() error = %v, want %v", err, test.expect)
				}
				if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_arbitrations WHERE tenant_id=$1`, f.scope.ID().String()); count != 0 {
					t.Fatalf("denied arbitration rows=%d", count)
				}
				if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_evaluation_requests WHERE tenant_id=$1`, f.scope.ID().String()); count != requests {
					t.Fatalf("denied arbitration requests=%d want %d", count, requests)
				}
			}, "tenant-region-ng", "tenant-region-ng")
		})
	}
}

func openCorrectionCase(t *testing.T, f captureAcceptanceFixture, oversight review.Oversight) (policy.Decision, review.Case, policy.Evaluator) {
	t.Helper()
	base := prepareCompletion(t, f)
	settings := reviewPolicySettings(f, base, oversight)
	seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
	administerReviewPolicy(t, f, settings)
	completeIntegrationVerification(t, f, base)
	actor := reviewActorKey(t, f)
	evaluator := &factDirectiveEvaluator{reference: base.Snapshot().Evaluator(), fact: mustFactKey(t, "review.correction"), name: "correction", terminal: true}
	retry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.intake", "correction-open", []byte(base.ID().String()), f.now)
	store := newReviewFollowupStore(t, f,
		review.Principal{ID: "corrector.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2"}},
		evaluator, f.now)
	result, err := store.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, retry)
	if err != nil {
		t.Fatal(err)
	}
	caseID, err := id.ParseReviewCase(result.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := reviewpostgres.New(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := cases.FindCase(t.Context(), f.scope, caseID)
	if err != nil {
		t.Fatal(err)
	}
	claimer := review.Principal{ID: "reviewer.correction", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
	claimed := claimReviewCase(t, f, value, claimer, f.now)
	resolved := recordReviewFinding(t, f, claimed, claimer, review.ResolutionSatisfy, "document_reviewed", f.now)
	return base, resolved, evaluator
}

func TestReviewCorrectionIntakeEvaluationAndLineage(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base := prepareCompletion(t, f)
		settings := reviewPolicySettings(f, base, review.OversightSingle)
		seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
		administerReviewPolicy(t, f, settings)

		decisions, err := policypostgres.NewGuarded(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		if err := decisions.Append(t.Context(), f.scope, base); err != nil {
			t.Fatal(err)
		}
		actor := reviewActorKey(t, f)
		principal := review.Principal{ID: "corrector.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2"}}
		evaluator := &factDirectiveEvaluator{reference: base.Snapshot().Evaluator(), fact: mustFactKey(t, "review.correction"), name: "correction", terminal: true}

		// A persisted decision is not enough: the session must be completed.
		store := newReviewFollowupStore(t, f, principal, evaluator, f.now)
		retry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.intake", "correction-first", []byte(base.ID().String()), f.now)
		if _, err := store.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, retry); !errors.Is(err, review.ErrConflict) {
			t.Fatalf("intake with processing session = %v", err)
		}
		// The appeal window is mandatory too.
		late := newReviewFollowupStore(t, f, principal, evaluator, f.now.Add(2*time.Hour))
		lateRetry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.intake", "correction-late", []byte(base.ID().String()), f.now)
		if _, err := late.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, lateRetry); !errors.Is(err, review.ErrConflict) {
			t.Fatalf("intake outside appeal window = %v", err)
		}

		originalCanonical, originalDigest := selectReviewDecisionBytes(t, f, base.ID())
		completeIntegrationVerification(t, f, base)

		result, err := store.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, retry)
		if err != nil {
			t.Fatalf("OpenCorrection() error = %v", err)
		}
		if result.CaseID == "" || result.Version != 1 {
			t.Fatalf("correction intake result = %+v", result)
		}
		caseID, err := id.ParseReviewCase(result.CaseID)
		if err != nil {
			t.Fatal(err)
		}
		if replay, err := store.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, retry); err != nil || replay != result {
			t.Fatalf("correction intake replay = %+v, %v", replay, err)
		}
		conflictRetry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.intake", "correction-first", []byte("different"), f.now)
		if _, err := store.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, conflictRetry); !errors.Is(err, idempotency.ErrConflict) {
			t.Fatalf("correction intake key conflict = %v", err)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_intakes WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
			t.Fatalf("correction intakes=%d", count)
		}

		cases, err := reviewpostgres.New(f.runtime, integrationProtector{})
		if err != nil {
			t.Fatal(err)
		}
		caseValue, err := cases.FindCase(t.Context(), f.scope, caseID)
		if err != nil || caseValue.ChallengedDecision != base.ID() || !caseValue.RoutingRequest.IsZero() {
			t.Fatalf("correction case = %+v error=%v", caseValue, err)
		}
		claimer := review.Principal{ID: "reviewer.correction", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
		claimed := claimReviewCase(t, f, caseValue, claimer, f.now)
		resolved := recordReviewFinding(t, f, claimed, claimer, review.ResolutionSatisfy, "document_reviewed", f.now)

		correctionInput := review.FollowupInput{
			CaseID: caseID, Version: resolved.Version, Resolution: review.ResolutionSatisfy, Reason: "document_reviewed",
			Actor: review.Actor{ID: actor.String()}, At: f.now,
			Retry: integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.evaluate", "correction-evaluate", []byte(caseID.String()), f.now),
		}
		correction, err := store.EvaluateCorrection(t.Context(), f.scope, correctionInput)
		if err != nil {
			t.Fatalf("EvaluateCorrection() error = %v", err)
		}
		if correction.Directive != policy.DirectiveCompleteVerified || correction.DecisionID == "" || correction.Version != resolved.Version+1 {
			t.Fatalf("correction result = %+v", correction)
		}
		successorID, err := id.ParseDecision(correction.DecisionID)
		if err != nil {
			t.Fatal(err)
		}
		policies, err := policypostgres.New(f.runtime, integrationProtector{})
		if err != nil {
			t.Fatal(err)
		}
		successor, err := policies.Find(t.Context(), f.scope, successorID)
		if err != nil {
			t.Fatal(err)
		}
		if successor.Supersedes() != base.ID() || successor.Snapshot().Policy() != base.Snapshot().Policy() {
			t.Fatal("successor lineage changed the challenged decision or pinned policy")
		}
		correctionKey := mustFactKey(t, "review.correction")
		found := false
		for _, fact := range successor.Snapshot().Facts() {
			if fact.Key == correctionKey {
				found = fact.State == policy.RequirementSatisfied && fact.Source.Kind == policy.FactSourceReviewFinding
			}
		}
		if !found {
			t.Fatalf("missing review.correction fact: %+v", successor.Snapshot().Facts())
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_evaluations WHERE tenant_id=$1 AND decision_id=$2`, f.scope.ID().String(), successorID.String()); count != 1 {
			t.Fatalf("correction evaluations=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_evaluations e JOIN idenqa.verification_decisions d ON d.tenant_id=e.tenant_id AND d.id=$3 WHERE e.tenant_id=$1 AND e.case_id=$2 AND e.snapshot_digest=d.snapshot_digest`,
			f.scope.ID().String(), caseID.String(), successorID.String()); count != 1 {
			t.Fatalf("successor evaluation receipts=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.outbox_events WHERE tenant_id=$1 AND event_type=$2`, f.scope.ID().String(), "verification.decision.corrected"); count != 1 {
			t.Fatalf("correction outbox=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.webhook_events WHERE tenant_id=$1 AND event_type=$2`, f.scope.ID().String(), string(webhookv1.DecisionCorrected)); count != 1 {
			t.Fatalf("correction catalogue=%d", count)
		}

		canonical, digest := selectReviewDecisionBytes(t, f, base.ID())
		if canonical != originalCanonical || digest != originalDigest {
			t.Fatal("original decision bytes changed")
		}
		if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_decisions SET canonical='{}' WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), base.ID().String()); err == nil {
			t.Fatal("original decision was mutable")
		}
		if replay, err := store.EvaluateCorrection(t.Context(), f.scope, correctionInput); err != nil || replay != correction {
			t.Fatalf("correction replay = %+v, %v", replay, err)
		}
		duplicate := correctionInput
		duplicate.Version = correction.Version
		duplicate.Retry = integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.evaluate", "correction-duplicate", []byte(caseID.String()), f.now)
		if _, err := store.EvaluateCorrection(t.Context(), f.scope, duplicate); !errors.Is(err, review.ErrConflict) {
			t.Fatalf("duplicate successor = %v", err)
		}
		denied := correctionInput
		denied.Version = correction.Version
		denied.Retry = integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.evaluate", "correction-denied", []byte(caseID.String()), f.now)
		noResolve := newReviewFollowupStore(t, f, review.Principal{ID: "corrector.independent", Permissions: []review.Permission{review.PermissionFind}, Certifications: []string{"document.level2"}}, evaluator, f.now)
		if _, err := noResolve.EvaluateCorrection(t.Context(), f.scope, denied); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("uncertified correction = %v", err)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_evaluations WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
			t.Fatalf("correction evaluations after replay=%d", count)
		}
	}, "tenant-region-ng", "tenant-region-ng")
}

// TestReviewCorrectionRejectsSupersededDecision proves the intake eligibility
// guard for an already-superseded decision even when no intake row exists.
func TestReviewCorrectionRejectsSupersededDecision(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base := prepareCompletion(t, f)
		settings := reviewPolicySettings(f, base, review.OversightSingle)
		seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
		administerReviewPolicy(t, f, settings)
		completeIntegrationVerification(t, f, base)

		successorID, err := f.ids.NewDecision()
		if err != nil {
			t.Fatal(err)
		}
		successor, err := policy.NewDecision(policy.DecisionInput{ID: successorID, Snapshot: base.Snapshot(), Evaluation: base.Evaluation(),
			Actor: policy.ActorMachine, Supersedes: base.ID(), DecidedAt: base.DecidedAt().Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		// Fixture-only direct lineage: production always records a correction
		// intake before authoring this successor.
		unguarded, err := policypostgres.New(f.runtime, integrationProtector{})
		if err != nil {
			t.Fatal(err)
		}
		if err := unguarded.Append(t.Context(), f.scope, successor); err != nil {
			t.Fatal(err)
		}
		actor := reviewActorKey(t, f)
		evaluator := &factDirectiveEvaluator{reference: base.Snapshot().Evaluator(), fact: mustFactKey(t, "review.correction"), name: "correction", terminal: true}
		store := newReviewFollowupStore(t, f, review.Principal{ID: "corrector.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2"}}, evaluator, f.now)
		retry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.intake", "correction-superseded", []byte(base.ID().String()), f.now)
		if _, err := store.OpenCorrection(t.Context(), f.scope, base.ID(), review.Actor{ID: actor.String()}, retry); !errors.Is(err, review.ErrConflict) {
			t.Fatalf("superseded decision intake = %v", err)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_intakes WHERE tenant_id=$1`, f.scope.ID().String()); count != 0 {
			t.Fatalf("superseded decision intakes=%d", count)
		}
	}, "tenant-region-ng", "tenant-region-ng")
}

func createReviewAppeal(t *testing.T, f captureAcceptanceFixture, store *reviewpostgres.Store, base policy.Decision, caseID id.ReviewCase, deadline time.Time, key string) review.Appeal {
	t.Helper()
	appealID, err := f.ids.NewAppeal()
	if err != nil {
		t.Fatal(err)
	}
	actor := reviewActorKey(t, f)
	appeal := review.Appeal{
		ID: appealID, CaseID: caseID, ChallengedDecision: base.ID(), OriginalReviewers: []string{"reviewer.correction"},
		State: review.AppealRequested, Deadline: deadline, Version: 1,
	}
	retry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.appeal.request", key, []byte(appealID.String()), f.now)
	created, err := store.CreateAppealWithKey(t.Context(), f.scope, review.Actor{ID: actor.String()}, appeal, f.now, retry)
	if err != nil {
		t.Fatalf("CreateAppealWithKey() error = %v", err)
	}
	return created
}

func TestReviewAppealLifecycleIndependenceAndWithdrawal(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base, correctionCase, _ := openCorrectionCase(t, f, review.OversightSingle)
		store, err := reviewpostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now.Add(5 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		actor := reviewActorKey(t, f)

		// A deadline beyond the configured appeal window is rejected.
		lateID, err := f.ids.NewAppeal()
		if err != nil {
			t.Fatal(err)
		}
		late := review.Appeal{ID: lateID, CaseID: correctionCase.ID, ChallengedDecision: base.ID(), OriginalReviewers: []string{"reviewer.correction"}, State: review.AppealRequested, Deadline: f.now.Add(2 * time.Hour), Version: 1}
		lateRetry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.appeal.request", "appeal-late", []byte(lateID.String()), f.now)
		if _, err := store.CreateAppealWithKey(t.Context(), f.scope, review.Actor{ID: actor.String()}, late, f.now, lateRetry); !errors.Is(err, review.ErrInvalid) {
			t.Fatalf("appeal beyond window = %v", err)
		}

		appeal := createReviewAppeal(t, f, store, base, correctionCase.ID, f.now.Add(30*time.Minute), "appeal-first")
		if !appeal.Deadline.Equal(f.now.Add(30*time.Minute)) || appeal.State != review.AppealRequested {
			t.Fatalf("appeal intake = %+v", appeal)
		}
		restored, err := store.FindAppeal(t.Context(), f.scope, appeal.ID)
		if err != nil || !restored.Deadline.Equal(appeal.Deadline) {
			t.Fatalf("stored appeal deadline = %+v, %v", restored, err)
		}
		retry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.appeal.request", "appeal-first", []byte(appeal.ID.String()), f.now)
		if replay, err := store.CreateAppealWithKey(t.Context(), f.scope, review.Actor{ID: actor.String()}, appeal, f.now, retry); err != nil || replay.ID != appeal.ID {
			t.Fatalf("appeal intake replay = %+v, %v", replay, err)
		}
		conflictRetry := integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.appeal.request", "appeal-first", []byte("different"), f.now)
		if _, err := store.CreateAppealWithKey(t.Context(), f.scope, review.Actor{ID: actor.String()}, appeal, f.now, conflictRetry); !errors.Is(err, idempotency.ErrConflict) {
			t.Fatalf("appeal intake key conflict = %v", err)
		}

		self := review.Principal{ID: "reviewer.correction", Permissions: []review.Permission{review.PermissionAppeal}, Certifications: []string{"document.level2"}}
		if _, err := restored.Assign(self, restored.Version, f.now.Add(time.Minute)); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("self appeal assignment = %v", err)
		}
		independent := review.Principal{ID: "appeal.reviewer", Permissions: []review.Permission{review.PermissionAppeal}, Certifications: []string{"document.level2"}}
		assigned, err := restored.Assign(independent, restored.Version, f.now.Add(time.Minute))
		if err != nil || assigned.State != review.AppealIndependentReview {
			t.Fatalf("appeal assignment = %+v, %v", assigned, err)
		}
		if err := store.SaveAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, assigned, restored.Version, f.now.Add(time.Minute)); err != nil {
			t.Fatalf("persist appeal assignment = %v", err)
		}
		if _, err := assigned.Resolve(self, review.AppealMoreInput, "needs_more_evidence", id.Decision{}, assigned.Version, f.now.Add(2*time.Minute)); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("original reviewer resolution = %v", err)
		}
		awaiting, err := assigned.Resolve(independent, review.AppealMoreInput, "needs_more_evidence", id.Decision{}, assigned.Version, f.now.Add(2*time.Minute))
		if err != nil || awaiting.State != review.AppealAwaitingInput || awaiting.AssignedReviewer != independent.ID {
			t.Fatalf("more-input resolution = %+v, %v", awaiting, err)
		}
		if err := store.SaveAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, awaiting, assigned.Version, f.now.Add(2*time.Minute)); err != nil {
			t.Fatalf("persist more-input resolution = %v", err)
		}
		upheld, err := awaiting.Resolve(independent, review.AppealUpheld, "original_upheld", id.Decision{}, awaiting.Version, f.now.Add(3*time.Minute))
		if err != nil || upheld.State != review.AppealResolved || upheld.Outcome != review.AppealUpheld {
			t.Fatalf("upheld resolution = %+v, %v", upheld, err)
		}
		if err := store.SaveAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, upheld, awaiting.Version, f.now.Add(3*time.Minute)); err != nil {
			t.Fatalf("persist upheld resolution = %v", err)
		}

		withdrawal := createReviewAppeal(t, f, store, base, correctionCase.ID, f.now.Add(45*time.Minute), "appeal-withdraw")
		if _, err := store.WithdrawAppeal(t.Context(), f.scope, review.Actor{ID: "someone.else"}, withdrawal.ID, withdrawal.Version); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("stranger withdrawal = %v", err)
		}
		if _, err := store.WithdrawAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, withdrawal.ID, withdrawal.Version+1); !errors.Is(err, review.ErrConflict) {
			t.Fatalf("inexact withdrawal version = %v", err)
		}
		withdrawn, err := store.WithdrawAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, withdrawal.ID, withdrawal.Version)
		if err != nil || withdrawn.State != review.AppealState("withdrawn") {
			t.Fatalf("requester withdrawal = %+v, %v", withdrawn, err)
		}
		if replayed, err := store.WithdrawAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, withdrawal.ID, withdrawal.Version); err != nil || replayed.State != withdrawn.State {
			t.Fatalf("withdrawal replay = %+v, %v", replayed, err)
		}
		var requested, independentReviews, awaitingInputs, resolvedCount, withdrawnCount int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT
 count(*) FILTER (WHERE record->>'state'='requested'),
 count(*) FILTER (WHERE record->>'state'='independent_review'),
 count(*) FILTER (WHERE record->>'state'='awaiting_input'),
 count(*) FILTER (WHERE record->>'state'='resolved'),
 count(*) FILTER (WHERE record->>'state'='withdrawn')
 FROM idenqa.review_appeal_history WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&requested, &independentReviews, &awaitingInputs, &resolvedCount, &withdrawnCount); err != nil {
			t.Fatal(err)
		}
		if requested != 2 || independentReviews != 1 || awaitingInputs != 1 || resolvedCount != 1 || withdrawnCount != 1 {
			t.Fatalf("appeal history requested=%d assigned=%d awaiting=%d resolved=%d withdrawn=%d", requested, independentReviews, awaitingInputs, resolvedCount, withdrawnCount)
		}
	}, "tenant-region-ng", "tenant-region-ng")
}

func prepareReviewTaskAdapter(t *testing.T, f captureAcceptanceFixture) *taskheadgate.Adapter {
	t.Helper()
	migrator, err := taskheadgate.OpenMigrator(t.Context(), f.database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := f.runtime.Native().QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	f.database.grantHeadgateRuntime(t, role)
	adapter, err := taskheadgate.NewPostgres(f.runtime.Native(), taskheadgate.DefaultConfig("idenqa-review-integration"))
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestReviewAppealOverturnReceiptAndWorkerExpiry(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base, correctionCase, evaluator := openCorrectionCase(t, f, review.OversightSingle)
		actor := reviewActorKey(t, f)
		store, err := reviewpostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now.Add(10 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}

		adapter := prepareReviewTaskAdapter(t, f)
		completion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, f.ids, fixedIntegrationClock{now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		expiryAt := f.now.Add(2 * time.Minute)
		evaluations, err := reviewpostgres.NewEvaluationStore(f.runtime, integrationProtector{}, evaluator, completion, fixedIntegrationClock{now: expiryAt})
		if err != nil {
			t.Fatal(err)
		}
		worker, err := reviewtask.New(evaluations, f.ids, adapter, fixedIntegrationClock{now: expiryAt})
		if err != nil {
			t.Fatal(err)
		}
		expiring := createReviewAppeal(t, f, store, base, correctionCase.ID, f.now.Add(time.Minute), "appeal-expiring")
		if err := worker.Schedule(t.Context()); err != nil {
			t.Fatal(err)
		}
		expired, err := store.FindAppeal(t.Context(), f.scope, expiring.ID)
		if err != nil || expired.State != review.AppealState("expired") {
			t.Fatalf("worker expiry = %+v, %v", expired, err)
		}
		if err := worker.Schedule(t.Context()); err != nil {
			t.Fatal(err)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_appeal_history WHERE tenant_id=$1 AND appeal_id=$2 AND record->>'state'='expired'`, f.scope.ID().String(), expiring.ID.String()); count != 1 {
			t.Fatalf("expiry history=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.audit_records WHERE event_type='review.appeal.expired'`); count != 1 {
			t.Fatalf("expiry audits=%d", count)
		}

		// A corrected successor can only be attached with its evaluation receipt.
		appeal := createReviewAppeal(t, f, store, base, correctionCase.ID, f.now.Add(30*time.Minute), "appeal-overturn")
		independent := review.Principal{ID: "appeal.reviewer", Permissions: []review.Permission{review.PermissionAppeal}, Certifications: []string{"document.level2"}}
		assigned, err := appeal.Assign(independent, appeal.Version, f.now.Add(5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, assigned, appeal.Version, f.now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		unknown, err := f.ids.NewDecision()
		if err != nil {
			t.Fatal(err)
		}
		unreceipted, err := assigned.Resolve(independent, review.AppealOverturned, "new_evidence", unknown, assigned.Version, f.now.Add(6*time.Minute))
		if err != nil {
			t.Fatalf("overturn resolution = %v", err)
		}
		if err := store.SaveAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, unreceipted, assigned.Version, f.now.Add(6*time.Minute)); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("overturn without receipt = %v", err)
		}

		followup := newReviewFollowupStore(t, f,
			review.Principal{ID: "corrector.independent", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2"}},
			evaluator, f.now)
		correctionInput := review.FollowupInput{
			CaseID: correctionCase.ID, Version: correctionCase.Version, Resolution: review.ResolutionSatisfy, Reason: "document_reviewed",
			Actor: review.Actor{ID: actor.String()}, At: f.now,
			Retry: integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.correction.evaluate", "appeal-correction-evaluate", []byte(correctionCase.ID.String()), f.now),
		}
		correction, err := followup.EvaluateCorrection(t.Context(), f.scope, correctionInput)
		if err != nil {
			t.Fatal(err)
		}
		successorID, err := id.ParseDecision(correction.DecisionID)
		if err != nil {
			t.Fatal(err)
		}
		overturned, err := assigned.Resolve(independent, review.AppealOverturned, "new_evidence", successorID, assigned.Version, f.now.Add(7*time.Minute))
		if err != nil || overturned.State != review.AppealResolved || overturned.SupersedingDecision != successorID {
			t.Fatalf("overturn with receipt = %+v, %v", overturned, err)
		}
		if err := store.SaveAppeal(t.Context(), f.scope, review.Actor{ID: actor.String()}, overturned, assigned.Version, f.now.Add(7*time.Minute)); err != nil {
			t.Fatalf("persist overturn = %v", err)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_appeal_history WHERE tenant_id=$1 AND appeal_id=$2 AND record->>'state'='resolved' AND record->>'outcome'='overturned'`, f.scope.ID().String(), appeal.ID.String()); count != 1 {
			t.Fatalf("overturn history=%d", count)
		}
	}, "tenant-region-ng", "tenant-region-ng")
}

type interruptOnceReviewRepository struct {
	reviewtask.Repository
	interrupt bool
	attempts  int
}

func (repository *interruptOnceReviewRepository) CommitWithin(ctx context.Context, scope tenant.Scope, tx pg.Transaction, candidate review.Evaluation, actor id.Task) error {
	repository.attempts++
	if repository.interrupt {
		repository.interrupt = false
		return errors.New("injected review evaluation interruption")
	}
	return repository.Repository.CommitWithin(ctx, scope, tx, candidate, actor)
}

func TestReviewEvaluationWorkerRestartConsistency(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		value, routing := prepareEvaluationReview(t, f)
		reviewer := review.Principal{ID: "operator.one", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
		claimed := claimReviewCase(t, f, value, reviewer, f.now)
		resolved := recordReviewFinding(t, f, claimed, reviewer, review.ResolutionSatisfy, "document_reviewed", f.now)
		request := review.EvaluationRequest{CaseID: value.ID, Version: resolved.Version}

		adapter := prepareReviewTaskAdapter(t, f)
		source := fixedIntegrationClock{now: f.now}
		completion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, f.ids, source)
		if err != nil {
			t.Fatal(err)
		}
		evaluator := &reviewEvaluator{reference: routing.Snapshot().Evaluator()}
		evaluations, err := reviewpostgres.NewEvaluationStore(f.runtime, integrationProtector{}, evaluator, completion, source)
		if err != nil {
			t.Fatal(err)
		}
		failing := &interruptOnceReviewRepository{Repository: evaluations, interrupt: true}
		firstWorker, err := reviewtask.New(failing, f.ids, adapter, source)
		if err != nil {
			t.Fatal(err)
		}
		targets, err := failing.ListReady(t.Context(), f.now, 100)
		if err != nil || len(targets) != 1 || targets[0].Request != request {
			t.Fatalf("pending evaluation discovery=%+v error=%v", targets, err)
		}
		// The worker coordinator's deadline is wall-clock bounded, so enqueue the
		// exact durable intent shape with a live deadline.
		taskNow := time.Now().UTC()
		taskID, err := f.ids.NewTask()
		if err != nil {
			t.Fatal(err)
		}
		firstIntent, err := platformtask.NewIntent(platformtask.IntentSpec{ID: taskID, TenantID: f.scope.ID(), Key: reviewtask.EvaluationKey,
			Queue: taskheadgate.QueueVerification, PartitionKey: f.scope.ID().String(),
			IdempotencyKey: fmt.Sprintf("review.evaluate:%s:%d", value.ID.String(), resolved.Version),
			Payload: struct {
				CaseID  string `json:"case_id"`
				Version int64  `json:"version"`
			}{value.ID.String(), resolved.Version},
			ScheduledAt: taskNow, Deadline: taskNow.Add(2 * time.Minute),
			Retry:     platformtask.RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Second, MaximumBackoff: 30 * time.Second, JitterPercent: 20},
			Retention: 30 * 24 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if err := adapter.Enqueue(t.Context(), firstIntent); err != nil {
			t.Fatal(err)
		}
		firstRegistry := platformtask.NewRegistry()
		if err := firstRegistry.Register(reviewtask.EvaluationKey, firstWorker); err != nil {
			t.Fatal(err)
		}
		firstTaskWorker, err := adapter.NewWorker(firstRegistry, taskheadgate.DefaultWorkerConfig(), nil)
		if err != nil {
			t.Fatal(err)
		}
		completed, err := firstTaskWorker.Drain(t.Context(), 1)
		if err != nil || len(completed) != 1 {
			t.Fatalf("interrupted drain=%v error=%v", completed, err)
		}
		if failing.interrupt || failing.attempts != 1 {
			t.Fatalf("interruption was not injected: attempts=%d interrupt=%t", failing.attempts, failing.interrupt)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_evaluations WHERE tenant_id=$1`, f.scope.ID().String()); count != 0 {
			t.Fatalf("interrupted evaluation committed %d rows", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.audit_records WHERE event_type='review.case.evaluated'`); count != 0 {
			t.Fatalf("interrupted evaluation committed %d audits", count)
		}

		replacementEvaluations, err := reviewpostgres.NewEvaluationStore(f.runtime, integrationProtector{}, evaluator, completion, source)
		if err != nil {
			t.Fatal(err)
		}
		replacementWorker, err := reviewtask.New(replacementEvaluations, f.ids, adapter, source)
		if err != nil {
			t.Fatal(err)
		}
		replacementRegistry := platformtask.NewRegistry()
		if err := replacementRegistry.Register(reviewtask.EvaluationKey, replacementWorker); err != nil {
			t.Fatal(err)
		}
		replacementTaskWorker, err := adapter.NewWorker(replacementRegistry, taskheadgate.DefaultWorkerConfig(), nil)
		if err != nil {
			t.Fatal(err)
		}
		replacementContext, stopReplacement := context.WithCancel(t.Context())
		replacementDone := make(chan error, 1)
		go func() { replacementDone <- replacementTaskWorker.Run(replacementContext) }()
		deadline := time.NewTimer(15 * time.Second)
		ticker := time.NewTicker(20 * time.Millisecond)
		evaluated := false
		for !evaluated {
			select {
			case <-deadline.C:
				stopReplacement()
				t.Fatal("replacement worker did not commit the evaluation")
			case <-ticker.C:
				var count int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.review_evaluations WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&count); err != nil {
					stopReplacement()
					t.Fatal(err)
				}
				evaluated = count == 1
			}
		}
		deadline.Stop()
		ticker.Stop()
		stopReplacement()
		if err := <-replacementDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		var state string
		var decisions, evaluationsCount, audits int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,
 (SELECT count(*) FROM idenqa.verification_decisions),
 (SELECT count(*) FROM idenqa.review_evaluations WHERE tenant_id=$1),
 (SELECT count(*) FROM idenqa.audit_records WHERE event_type='review.case.evaluated')
 FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), value.VerificationID.String()).Scan(&state, &decisions, &evaluationsCount, &audits); err != nil {
			t.Fatal(err)
		}
		if state != "completed" || decisions != 1 || evaluationsCount != 1 || audits != 1 {
			t.Fatalf("restarted evaluation state=%s decisions=%d evaluations=%d audits=%d", state, decisions, evaluationsCount, audits)
		}
		targets, err = replacementEvaluations.ListReady(t.Context(), f.now, 100)
		if err != nil || len(targets) != 0 {
			t.Fatalf("consumed evaluation discovery=%+v error=%v", targets, err)
		}

		// Exact replay of the same delivery after restart is harmless. Reuse the
		// committed intent identity; only the attempt differs.
		work, result := replacementWorker.Prepare(t.Context(), platformtask.Delivery{Intent: firstIntent, Attempt: 2, Fence: 2})
		if result.Outcome != platformtask.OutcomeComplete || work == nil {
			t.Fatalf("replay prepare=%#v", result)
		}
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			returned := work(ctx, tx)
			return returned.Err
		}); err != nil {
			t.Fatal(err)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_evaluations WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
			t.Fatalf("replay duplicated evaluations=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.verification_decisions WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
			t.Fatalf("replay duplicated decisions=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.audit_records WHERE event_type='review.case.evaluated'`); count != 1 {
			t.Fatalf("replay duplicated audits=%d", count)
		}
	}, "tenant-region-ng", "tenant-region-ng")
}
