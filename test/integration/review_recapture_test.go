//go:build integration

package integration_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func testLinkedRecapture(t *testing.T, f captureAcceptanceFixture, value review.Case, candidate review.Evaluation, terminal bool) {
	t.Helper()
	store, err := reviewpostgres.NewRecaptureStore(f.runtime, integrationProtector{}, f.catalog, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	actor := f.creation.Session // only original configuration is copied
	var keyValue string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT created_by FROM idenqa.processing_authorities WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&keyValue); err != nil {
		t.Fatal(err)
	}
	key, err := id.ParseAPIKey(keyValue)
	if err != nil {
		t.Fatal(err)
	}
	mutation := newIntegrationSessionMutation(t, f.ids, f.scope.ID(), key, actor.ProfileID(), candidate.Snapshot.Policy().ID, f.now, "recapture-first", actor.Region())
	mutation.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecapture, "recapture-first", []byte(`{"case":"`+value.ID.String()+`"}`), f.now)
	broken := mutation
	var eventValue string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.outbox_events LIMIT 1`).Scan(&eventValue); err != nil {
		t.Fatal(err)
	}
	broken.EventID, err = id.ParseEvent(eventValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version, broken); err == nil {
		t.Fatal("expected outbox rollback")
	}
	var count int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.verification_sessions WHERE id=$1`, mutation.SessionID.String()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan child=%d %v", count, err)
	}
	child, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if child.Session.ID() == actor.ID() || child.Session.ProfileRevision() != actor.ProfileRevision() || child.Session.ProfileDigest() != actor.ProfileDigest() || child.Session.State() != verification.SessionStateCollecting {
		t.Fatal("wrong child configuration")
	}
	var fresh bool
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT authority_id IS NULL AND subject_id IS NULL AND notice_id IS NULL AND capture_completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM idenqa.evidence_upload_intents u WHERE u.verification_id=s.id) AND NOT EXISTS(SELECT 1 FROM idenqa.verification_checks c WHERE c.verification_id=s.id) FROM idenqa.verification_sessions s WHERE s.id=$1`, child.Session.ID().String()).Scan(&fresh); err != nil || !fresh {
		t.Fatalf("child inherited processing authority/evidence: %v %v", fresh, err)
	}
	if _, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version-1, mutation); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("stale version=%v", err)
	}
	err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, f.scope.ID().String()); err != nil {
			return err
		}
		pin, err := (policypostgres.SessionPolicySelector{}).SelectPinnedPolicy(ctx, tx, f.scope, child.Session.ID())
		if err != nil {
			return err
		}
		if pin == nil || *pin != candidate.Snapshot.Policy() {
			t.Fatal("lost policy pin")
		}
		facts, err := (reviewpostgres.RecaptureFactProjector{}).ProjectWithin(ctx, tx, f.scope, policypostgres.Projection{
			VerificationID: child.Session.ID(),
			EvaluatedAt:    f.now,
		})
		if err != nil {
			return err
		}
		if len(facts) != 1 || facts[0].Key != policy.FactKey("review.recapture.requested") || facts[0].State != policy.RequirementSatisfied || facts[0].Source.Kind != policy.FactSourceReviewFinding {
			t.Fatalf("wrong recapture policy fact: %+v", facts)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, retryKey := range []string{"recapture-first", "recapture-second"} {
		retry := newIntegrationSessionMutation(t, f.ids, f.scope.ID(), key, actor.ProfileID(), candidate.Snapshot.Policy().ID, f.now, retryKey, actor.Region())
		retry.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecapture, retryKey, []byte(`{"case":"`+value.ID.String()+`"}`), f.now)
		restored, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version, retry)
		if err != nil || restored.Session.ID() != child.Session.ID() || restored.Credential.ID() != child.Credential.ID() {
			t.Fatalf("replay=%v", err)
		}
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.review_recaptures SET created_at=created_at WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
		t.Fatal("mutable recapture lineage")
	}
	testRecaptureRenewal(t, f, store, value, key, child, terminal)

}

func testRecaptureRenewal(t *testing.T, f captureAcceptanceFixture, store *reviewpostgres.RecaptureStore, value review.Case, key id.APIKey, child verification.SessionCreation, terminal bool) {
	t.Helper()
	token, err := f.ids.NewCaptureToken()
	if err != nil {
		t.Fatal(err)
	}
	// Prepare (but do not commit) an old-token response before credential renewal.
	record := f.declaration.Record()
	record.ID, err = f.ids.NewAuthority()
	if err != nil {
		t.Fatal(err)
	}
	record.SubjectID, err = f.ids.NewSubject()
	if err != nil {
		t.Fatal(err)
	}
	record.VerificationID = child.Session.ID()
	record.CreatedAt = f.now
	record.UpdatedAt = f.now
	declaration, err := authority.New(record)
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.authorities.Declare(t.Context(), f.scope, authority.DeclarationMutation{Authority: declaration, EventID: eventID, Idempotency: integrationIdempotencyRequest(t, f.scope.ID(), key, "authorities.declare", "child-declare", []byte(`{}`), f.now)}); err != nil {
		t.Fatal(err)
	}
	responseID, err := f.ids.NewAcknowledgement()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := authority.NewResponse(authority.ResponseRecord{ID: responseID, TenantID: f.scope.ID(), AuthorityID: record.ID, NoticeID: record.NoticeID, SubjectID: record.SubjectID, VerificationID: child.Session.ID(), CaptureTokenID: child.Credential.ID(), Action: authority.ResponseConsent, Locale: "en-NG", RenderedExperienceVersion: "capture.notice.v1", RecordedAt: f.now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	at := child.Credential.ExpiresAt().Add(time.Second)
	input := verification.CaptureRenewal{ExpectedToken: child.Credential.ID(), Token: token, Actor: key, KeyVersion: child.Credential.KeyVersion(), At: at, ExpiresAt: at.Add(48 * time.Hour), Idempotency: integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureRenew, "renew-first", []byte(`{"expected":"`+child.Credential.ID().String()+`"}`), at)}
	if _, err := store.RenewRecapture(t.Context(), f.scope, value.ID, value.Version, input); !errors.Is(err, review.ErrInvalid) {
		t.Fatalf("future renewal=%v", err)
	}
	store, err = reviewpostgres.NewRecaptureStore(f.runtime, integrationProtector{}, f.catalog, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	rollbackStore, err := reviewpostgres.NewRecaptureStore(recaptureRollback{f.runtime}, integrationProtector{}, f.catalog, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollbackStore.RenewRecapture(t.Context(), f.scope, value.ID, value.Version, input); !errors.Is(err, errRecaptureRollback) {
		t.Fatalf("rollback=%v", err)
	}
	var unchanged bool
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT revoked_at IS NULL AND NOT EXISTS(SELECT 1 FROM idenqa.capture_tokens WHERE id=$2) FROM idenqa.capture_tokens WHERE id=$1`, child.Credential.ID().String(), token.String()).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("partial rotation=%v %v", unchanged, err)
	}
	renewed, err := store.RenewRecapture(t.Context(), f.scope, value.ID, value.Version, input)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Session.ID() != child.Session.ID() || renewed.Credential.ID() == child.Credential.ID() || renewed.Credential.ExpiresAt().After(child.Session.ExpiresAt()) {
		t.Fatal("invalid renewal")
	}
	responseStore, err := authoritypostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	responseEvent, err := f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	responseRetry, err := idempotency.NewRequest(f.scope.ID(), child.Credential.ID(), "authorities.respond", "delayed-response", []byte(`{"action":"consent"}`), pending.Record().RecordedAt, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responseStore.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{Response: pending, EventID: responseEvent, Idempotency: responseRetry}); !errors.Is(err, authority.ErrProcessingNotPermitted) {
		t.Fatalf("old response crossed renewal=%v", err)
	}
	var revoked bool
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT revoked_at IS NOT NULL FROM idenqa.capture_tokens WHERE id=$1`, child.Credential.ID().String()).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("old credential active=%v", err)
	}
	replay, err := store.RenewRecapture(t.Context(), f.scope, value.ID, value.Version, input)
	if err != nil || replay.Credential.ID() != renewed.Credential.ID() {
		t.Fatalf("renewal replay=%v", err)
	}
	input.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureRenew, "renew-stale", []byte(`{}`), at)
	if _, err := store.RenewRecapture(t.Context(), f.scope, value.ID, value.Version, input); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("stale credential=%v", err)
	}
	statuses, err := store.ListRecaptures(t.Context(), f.scope, value.ID)
	if err != nil || len(statuses) != 1 || statuses[0].CaptureTokenID != renewed.Credential.ID() || !statuses[0].DecisionID.IsZero() {
		t.Fatalf("status=%v %v", statuses, err)
	}
	pendingDecision, _ := f.ids.NewDecision()
	pendingAck := review.AcknowledgeRecapture{CaseID: value.ID, CaseVersion: value.Version, DecisionID: pendingDecision, ActorID: key, ReviewerID: "operator.followup", At: at, Idempotency: integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureAcknowledge, "ack-incomplete", []byte(`{}`), at)}
	if _, err := store.AcknowledgeRecapture(t.Context(), f.scope, pendingAck); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("acknowledged incomplete child=%v", err)
	}
	// Seed a committed child decision to test only the read projection. The separate
	// completion suite owns authority/check execution and atomic delivery evidence.
	decisionID, err := f.ids.NewDecision()
	if err != nil {
		t.Fatal(err)
	}
	decision := integrationDecision(t, f.ids, f.scope.ID(), child.Session.ID(), decisionID, id.Decision{}, policy.ActorMachine, at)
	policies, err := policypostgres.New(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if err := policies.Append(t.Context(), f.scope, decision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions SET state='completed',completed_decision_id=$2,version=version+1,updated_at=$3 WHERE id=$1`, child.Session.ID().String(), decisionID.String(), at); err != nil {
		t.Fatal(err)
	}
	statuses, err = store.ListRecaptures(t.Context(), f.scope, value.ID)
	if err != nil || len(statuses) != 1 || statuses[0].DecisionID != decisionID || statuses[0].Outcome != decision.Evaluation().Outcome() {
		t.Fatalf("child outcome=%v %v", statuses, err)
	}
	missingAck := review.AcknowledgeRecapture{CaseID: value.ID, CaseVersion: value.Version, DecisionID: decisionID, ActorID: key, ReviewerID: "operator.followup", At: at, Idempotency: integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureReevaluate, "without-ack", []byte(`{}`), at)}
	if _, err := store.ReevaluateRecapture(t.Context(), f.scope, missingAck); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("reevaluation without acknowledgement=%v", err)
	}
	testRecaptureAcknowledgement(t, f, store, value, key, decisionID, at)
	testRecaptureReevaluation(t, f, store, value, key, decisionID, at, terminal)
	expectedState := "manual_review"
	if terminal {
		expectedState = "completed"
	}
	var parentState string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT state FROM idenqa.verification_sessions WHERE id=$1`, value.VerificationID.String()).Scan(&parentState); err != nil || parentState != expectedState {
		t.Fatalf("parent changed=%s %v", parentState, err)
	}

}

var errRecaptureRollback = errors.New("planned recapture transaction rollback")

type recaptureRollback struct{ *pg.Pool }

func (pool recaptureRollback) WithinTransaction(ctx context.Context, options pg.TransactionOptions, effect func(context.Context, pg.Transaction) error) error {
	return pool.Pool.WithinTransaction(ctx, options, func(ctx context.Context, tx pg.Transaction) error {
		if err := effect(ctx, tx); err != nil {
			return err
		}
		return errRecaptureRollback
	})
}

func testRecaptureAcknowledgement(t *testing.T, f captureAcceptanceFixture, store *reviewpostgres.RecaptureStore, value review.Case, key id.APIKey, decision id.Decision, at time.Time) {
	t.Helper()
	input := review.AcknowledgeRecapture{CaseID: value.ID, CaseVersion: value.Version, DecisionID: decision, ActorID: key, ReviewerID: "operator.followup", At: at, Idempotency: integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureAcknowledge, "ack-first", []byte(`{"decision":"`+decision.String()+`"}`), at)}
	wrong := input
	wrong.DecisionID, _ = f.ids.NewDecision()
	if _, err := store.AcknowledgeRecapture(t.Context(), f.scope, wrong); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("wrong outcome=%v", err)
	}
	stale := input
	stale.CaseVersion--
	if _, err := store.AcknowledgeRecapture(t.Context(), f.scope, stale); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("stale case=%v", err)
	}
	rollback, err := reviewpostgres.NewRecaptureStore(recaptureRollback{f.runtime}, integrationProtector{}, f.catalog, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollback.AcknowledgeRecapture(t.Context(), f.scope, input); !errors.Is(err, errRecaptureRollback) {
		t.Fatalf("ack rollback=%v", err)
	}
	var count int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.review_recapture_acknowledgements`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial acknowledgement=%d %v", count, err)
	}
	var audits, retries int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.audit_records WHERE event_type='review.case.child_outcome_acknowledged'),(SELECT count(*) FROM idenqa.idempotency_records WHERE operation='reviews.recapture.acknowledge')`).Scan(&audits, &retries); err != nil || audits != 0 || retries != 0 {
		t.Fatalf("partial acknowledgement audit/replay=%d %d %v", audits, retries, err)
	}
	saved, err := store.AcknowledgeRecapture(t.Context(), f.scope, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, retryKey := range []string{"ack-first", "ack-new-key"} {
		retry := input
		retry.ReviewerID = "operator.other"
		retry.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureAcknowledge, retryKey, []byte(`{"decision":"`+decision.String()+`"}`), at)
		restored, err := store.AcknowledgeRecapture(t.Context(), f.scope, retry)
		if err != nil || restored != saved {
			t.Fatalf("ack replay=%v", err)
		}
	}
	statuses, err := store.ListRecaptures(t.Context(), f.scope, value.ID)
	if err != nil || len(statuses) != 1 || statuses[0].AcknowledgedAt == nil || statuses[0].AcknowledgedBy != input.ReviewerID {
		t.Fatalf("ack status=%v %v", statuses, err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.review_recapture_acknowledgements SET reviewer_id='other'`); err == nil {
		t.Fatal("mutable acknowledgement")
	}
	var requests, deliveries int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.review_evaluation_requests),(SELECT count(*) FROM idenqa.webhook_deliveries)`).Scan(&requests, &deliveries); err != nil || requests != 1 || deliveries != 0 {
		t.Fatalf("ack added workflow effects=%d %d %v", requests, deliveries, err)
	}
}

func testRecaptureReevaluation(t *testing.T, f captureAcceptanceFixture, store *reviewpostgres.RecaptureStore, value review.Case, key id.APIKey, decision id.Decision, at time.Time, terminal bool) {
	t.Helper()
	input := review.AcknowledgeRecapture{CaseID: value.ID, CaseVersion: value.Version, DecisionID: decision, ActorID: key, ReviewerID: "operator.followup", At: at, Idempotency: integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureReevaluate, "reeval-first", []byte(`{}`), at)}
	wrong := input
	wrong.DecisionID, _ = f.ids.NewDecision()
	if _, err := store.ReevaluateRecapture(t.Context(), f.scope, wrong); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("wrong child=%v", err)
	}
	rollback, err := reviewpostgres.NewRecaptureStore(recaptureRollback{f.runtime}, integrationProtector{}, f.catalog, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollback.ReevaluateRecapture(t.Context(), f.scope, input); !errors.Is(err, errRecaptureRollback) {
		t.Fatalf("reeval rollback=%v", err)
	}
	var version, count int64
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT version,(SELECT count(*) FROM idenqa.review_recapture_evaluation_requests) FROM idenqa.review_cases WHERE id=$1`, value.ID.String()).Scan(&version, &count); err != nil || version != value.Version || count != 0 {
		t.Fatalf("partial reeval=%d %d %v", version, count, err)
	}
	saved, err := store.ReevaluateRecapture(t.Context(), f.scope, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.TargetVersion != value.Version+1 {
		t.Fatal("missing next version")
	}
	input.ReviewerID = "operator.other"
	for _, keyValue := range []string{"reeval-first", "reeval-again"} {
		input.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), key, review.OperationRecaptureReevaluate, keyValue, []byte(`{}`), at)
		replay, err := store.ReevaluateRecapture(t.Context(), f.scope, input)
		if err != nil || replay != saved {
			t.Fatalf("reeval replay=%v", err)
		}
	}
	policies, err := policypostgres.New(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	routing, err := policies.FindRouting(t.Context(), f.scope, value.RoutingRequest)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, f.ids, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	evaluator := &recaptureEvaluator{reference: routing.Snapshot().Evaluator(), terminal: terminal}
	evaluations, err := reviewpostgres.NewEvaluationStore(f.runtime, integrationProtector{}, evaluator, completion, fixedIntegrationClock{now: at})
	if err != nil {
		t.Fatal(err)
	}
	targets, err := evaluations.ListReady(t.Context(), at, 100)
	if err != nil || len(targets) != 1 || targets[0].Request.Version != saved.TargetVersion {
		t.Fatalf("reeval discovery=%v %v", targets, err)
	}
	candidate, err := evaluations.Build(t.Context(), f.scope, targets[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := evaluations.Find(t.Context(), f.scope, review.EvaluationRequest{CaseID: value.ID, Version: value.Version})
	if err != nil {
		t.Fatal(err)
	}
	original := routing.Snapshot()
	collisionFacts := original.Facts()
	collision := collisionFacts[0]
	collision.Key, _ = policy.NewFactKey("review.recapture")
	collisionSnapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: original.TenantID(), VerificationID: original.VerificationID(), AuthorityID: original.AuthorityID(), AcknowledgementID: original.AcknowledgementID(), Region: original.Region(), Policy: original.Policy(), Evaluator: original.Evaluator(), EvaluatedAt: original.EvaluatedAt(), Facts: append(collisionFacts, collision)})
	if err != nil {
		t.Fatal(err)
	}
	collisionEvaluation, err := policy.Resolve(collisionSnapshot, routing.Evaluation().Results(), routing.Evaluation().Assurance())
	if err != nil {
		t.Fatal(err)
	}
	collisionRouting, err := policy.NewRouting(f.scope, routing.Request(), collisionSnapshot, collisionEvaluation)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := review.RecaptureSnapshot(value, collisionRouting, previous.Snapshot, saved, review.RecaptureAcknowledgement{}, policy.Decision{}); !errors.Is(err, review.ErrInvalid) {
		t.Fatalf("overwrote original recapture fact=%v", err)
	}
	for _, oldFact := range previous.Snapshot.Facts() {
		preserved := false
		for _, newFact := range candidate.Snapshot.Facts() {
			if oldFact.Key == newFact.Key && reflect.DeepEqual(oldFact, newFact) {
				preserved = true
			}
		}
		if !preserved {
			t.Fatalf("original fact changed: %s", oldFact.Key)
		}
	}
	found := false
	for _, fact := range candidate.Snapshot.Facts() {
		if string(fact.Key) == "review.recapture" {
			found = true
		}
	}
	if !found || candidate.Snapshot.Policy() != routing.Snapshot().Policy() {
		t.Fatal("lost child fact or pinned policy")
	}
	taskID, err := f.ids.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	tampered := candidate
	tampered.CaseDigest = strings.Repeat("0", 64)
	if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		return evaluations.CommitWithin(ctx, f.scope, tx, tampered, taskID)
	}); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("tampered recapture=%v", err)
	}
	if terminal {
		failingCompletion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, failingCompletionIdentifiers{}, fixedIntegrationClock{now: at})
		if err != nil {
			t.Fatal(err)
		}
		failing, err := reviewpostgres.NewEvaluationStore(f.runtime, integrationProtector{}, evaluator, failingCompletion, fixedIntegrationClock{now: at})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			return failing.CommitWithin(ctx, f.scope, tx, candidate, taskID)
		}); !errors.Is(err, errPlannedQueueFailure) {
			t.Fatalf("reeval completion rollback=%v", err)
		}
		if _, err := evaluations.Find(t.Context(), f.scope, candidate.Request); !errors.Is(err, review.ErrEvaluationNotFound) {
			t.Fatalf("partial reevaluation=%v", err)
		}
	}
	for range 2 {
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			return evaluations.CommitWithin(ctx, f.scope, tx, candidate, taskID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.review_recapture_evaluation_requests SET reviewer_id='other'`); err == nil {
		t.Fatal("mutable reeval receipt")
	}
}

type recaptureEvaluator struct {
	reference policy.EvaluatorReference
	terminal  bool
}

func (e *recaptureEvaluator) Reference() policy.EvaluatorReference { return e.reference }
func (e *recaptureEvaluator) Evaluate(_ context.Context, snapshot policy.Snapshot) (policy.EvaluatorOutput, error) {
	key, _ := policy.NewFactKey("review.recapture")
	for _, fact := range snapshot.Facts() {
		if fact.Key == key {
			directive := policy.DirectiveRequestInput
			assurance := ""
			if e.terminal {
				directive = policy.DirectiveCompleteVerified
				assurance = "reviewed.identity"
			}
			return policy.EvaluatorOutput{Results: []policy.RequirementResult{{Name: "recapture", State: fact.State, ContributingFacts: []policy.FactKey{key}, Candidate: directive, Priority: 1, ReasonCodes: []string{"recapture_reviewed"}}}, Assurance: assurance}, nil
		}
	}
	return policy.EvaluatorOutput{}, policy.ErrInvalid
}
