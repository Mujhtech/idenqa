package privacy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
)

func requestFixture(t *testing.T, requestType privacy.RequestType, payload string, channel privacy.Channel) (privacy.Request, id.Generator, time.Time) {
	t.Helper()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	identifier, _ := generator.NewPrivacyRequest()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	verification := ""
	if channel == privacy.ChannelSubjectOutcome {
		verificationID, _ := generator.NewVerification()
		verification = verificationID.String()
	}
	value, err := privacy.NewPrivacyRequest(identifier, requestType, channel, "sub_01M11HEQG00000000000000000", verification, "ng-1", []byte(payload), now, now.Add(30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return value, *generator, now
}

func TestPrivacyRequestTransitionsIdempotencyAndExpiry(t *testing.T) {
	request, generator, now := requestFixture(t, privacy.RequestAccess, `{}`, privacy.ChannelTenantAPI)
	reviewed, err := request.BeginReview("actor:digest", now.Add(time.Minute))
	if err != nil || reviewed.State != privacy.RequestStateInReview || reviewed.Version != 2 {
		t.Fatalf("BeginReview() = %+v, %v", reviewed, err)
	}
	replayed, err := reviewed.BeginReview("actor:digest", now.Add(2*time.Minute))
	if err != nil || replayed.Version != reviewed.Version {
		t.Fatalf("BeginReview replay = %+v, %v", replayed, err)
	}
	if _, err := request.Expire(now.Add(time.Hour)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("early expiry error = %v", err)
	}
	cancelled, err := reviewed.Expire(now.Add(30 * 24 * time.Hour))
	if err != nil || cancelled.State != privacy.RequestStateExpired || !cancelled.State.Terminal() {
		t.Fatalf("Expire() = %+v, %v", cancelled, err)
	}
	if _, err := cancelled.BeginReview("actor:digest", now.Add(31*24*time.Hour)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("terminal transition error = %v", err)
	}

	decisionID, _ := generator.NewPrivacyDecision()
	approved, err := reviewed.Decide(decisionID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, "key_01M11HEQG00000000000000000", now.Add(3*time.Minute))
	if err != nil || approved.State != privacy.RequestStateApproved || len(approved.Decisions) != 1 {
		t.Fatalf("Decide() = %+v, %v", approved, err)
	}
	replayDecision, err := approved.Decide(decisionID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, "key_01M11HEQG00000000000000000", now.Add(4*time.Minute))
	if err != nil || replayDecision.Version != approved.Version || len(replayDecision.Decisions) != 1 {
		t.Fatalf("Decide replay = %+v, %v", replayDecision, err)
	}
	if _, err := approved.Decide(decisionID, privacy.OutcomeDenied, privacy.ReasonIdentityUnverified, "key_01M11HEQG00000000000000000", now.Add(4*time.Minute)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("conflicting decision error = %v", err)
	}
	executing, err := approved.BeginExecution("actor:digest", now.Add(5*time.Minute))
	if err != nil || executing.State != privacy.RequestStateExecuting {
		t.Fatalf("BeginExecution() = %+v, %v", executing, err)
	}
	replayExecution, err := executing.BeginExecution("actor:digest", now.Add(6*time.Minute))
	if err != nil || replayExecution.Version != executing.Version {
		t.Fatalf("BeginExecution replay = %+v, %v", replayExecution, err)
	}
	failed, err := executing.Fail("effect_failed", now.Add(7*time.Minute))
	if err != nil || failed.State != privacy.RequestStateFailed || failed.FailureClass != "effect_failed" {
		t.Fatalf("Fail() = %+v, %v", failed, err)
	}
	retried, err := failed.BeginExecution("actor:digest", now.Add(8*time.Minute))
	if err != nil || retried.State != privacy.RequestStateExecuting {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
	completed, err := retried.Complete("subject_export", "bundle.digest.ref", "sha256:digest", now.Add(9*time.Minute))
	if err != nil || completed.State != privacy.RequestStateCompleted || completed.EffectKind != "subject_export" {
		t.Fatalf("Complete() = %+v, %v", completed, err)
	}
	if len(completed.Events) != 6 {
		t.Fatalf("events = %d", len(completed.Events))
	}
	if completed.SubjectProjection().Status != privacy.SubjectStatusCompleted {
		t.Fatalf("projection = %+v", completed.SubjectProjection())
	}
}

func TestPrivacyRequestWithdrawReasonAndDenialEvidence(t *testing.T) {
	request, generator, now := requestFixture(t, privacy.RequestErasure, `{}`, privacy.ChannelTenantAPI)
	withdrawn, err := request.Withdraw("actor:digest", now.Add(time.Minute))
	if err != nil || withdrawn.State != privacy.RequestStateWithdrawn || withdrawn.ReasonCode != privacy.ReasonWithdrawnByTenant {
		t.Fatalf("Withdraw() = %+v, %v", withdrawn, err)
	}
	replay, err := withdrawn.Withdraw("actor:digest", now.Add(2*time.Minute))
	if err != nil || replay.Version != withdrawn.Version {
		t.Fatalf("Withdraw replay = %+v, %v", replay, err)
	}
	if _, err := withdrawn.Complete("subject_export", "ref", "digest", now.Add(3*time.Minute)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("terminal complete error = %v", err)
	}
	decisionID, _ := generator.NewPrivacyDecision()
	zero := privacy.Request{}
	if _, err := zero.Decide(decisionID, privacy.OutcomeDenied, privacy.ReasonLegalObligation, "key", now); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("zero request decide error = %v", err)
	}
}

func TestPrivacyRequestPayloadVocabulary(t *testing.T) {
	if _, err := privacy.ValidatePayload(privacy.RequestAccess, []byte(`{"record_id":"obs_01M11HEQG00000000000000000"}`)); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("access payload error = %v", err)
	}
	if _, err := privacy.ValidatePayload(privacy.RequestObjection, []byte(`{}`)); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("objection payload error = %v", err)
	}
	correction, err := privacy.ValidatePayload(privacy.RequestCorrection, []byte(`{"record_id":"obs_01M11HEQG00000000000000000","name":"identity.name.full","value":"Ada","kind":"observation","normalization":"identity.trim.v1"}`))
	if err != nil || correction.RecordID == "" {
		t.Fatalf("correction payload = %+v, %v", correction, err)
	}
	if _, err := privacy.ValidatePayload(privacy.RequestCorrection, []byte(`{"record_id":"obs_01M11HEQG00000000000000000","decision_id":"dec_01M11HEQG00000000000000000"}`)); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("ambiguous correction payload error = %v", err)
	}
	decision, err := privacy.ValidatePayload(privacy.RequestCorrection, []byte(`{"decision_id":"dec_01M11HEQG00000000000000000"}`))
	if err != nil || decision.DecisionID == "" {
		t.Fatalf("decision correction payload = %+v, %v", decision, err)
	}
	if _, err := privacy.ValidatePayload(privacy.RequestRestriction, []byte(`{"purpose":"liveness","unknown":true}`)); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("unknown field payload error = %v", err)
	}
}

func TestRestrictionLiftAndObjectionScope(t *testing.T) {
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	requestID, _ := generator.NewPrivacyRequest()
	restrictionID, _ := generator.NewPrivacyRestriction()
	restriction, err := privacy.NewRestriction(restrictionID, requestID, "sub_01M11HEQG00000000000000000", "", privacy.RestrictionRequestedBySubject, "ng-1", now)
	if err != nil || !restriction.ActiveAt(now.Add(time.Minute)) {
		t.Fatalf("NewRestriction() = %+v, %v", restriction, err)
	}
	lifted, err := restriction.Lift(privacy.RestrictionLiftedByTenant, now.Add(time.Hour))
	if err != nil || lifted.State != privacy.RestrictionLifted || lifted.ActiveAt(now.Add(2*time.Hour)) {
		t.Fatalf("Lift() = %+v, %v", lifted, err)
	}
	replay, err := lifted.Lift(privacy.RestrictionLiftedByTenant, now.Add(2*time.Hour))
	if err != nil || replay.Version != lifted.Version {
		t.Fatalf("Lift replay = %+v, %v", replay, err)
	}
	if _, err := lifted.Lift(privacy.RestrictionLiftedBySubject, now.Add(3*time.Hour)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("conflicting lift error = %v", err)
	}
	objectionID, _ := generator.NewPrivacyRestriction()
	if _, err := privacy.NewObjection(objectionID, requestID, "sub_01M11HEQG00000000000000000", "", privacy.RestrictionRequestedBySubject, "ng-1", now); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("unscoped objection error = %v", err)
	}
	objection, err := privacy.NewObjection(objectionID, requestID, "sub_01M11HEQG00000000000000000", "liveness-check", privacy.RestrictionRequestedBySubject, "ng-1", now)
	if err != nil || objection.Scope != privacy.RestrictionScopePurpose || !objection.ActiveAt(now) {
		t.Fatalf("NewObjection() = %+v, %v", objection, err)
	}
}

func TestDisclosureAndProcessorValidation(t *testing.T) {
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	requestID, _ := generator.NewPrivacyRequest()
	disclosureID, _ := generator.NewPrivacyDisclosure()
	disclosure, err := privacy.NewDisclosure(disclosureID, requestID, "legal.counsel", "access.request", privacy.DisclosureSubjectExport, "controller.contract", "ng-1", "sha256:digest", now)
	if err != nil || disclosure.Validate() != nil {
		t.Fatalf("NewDisclosure() = %+v, %v", disclosure, err)
	}
	if _, err := privacy.NewDisclosure(disclosureID, requestID, "legal.counsel", "access.request", privacy.DisclosureClass("unknown"), "controller.contract", "ng-1", "digest", now); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("unknown disclosure class error = %v", err)
	}

	processorID, _ := generator.NewProcessor()
	processor, err := privacy.NewProcessor(processorID, "Example KYC", privacy.ProcessorRoleProcessor, "identity.verification", []privacy.DataClass{privacy.DataClassRawEvidence, privacy.DataClassDerivedEvidence}, []string{"ng-1", "eu-west-1"}, "standard.contractual_clauses", now)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := processor.Update(1, "Example KYC Ltd", privacy.ProcessorRoleProcessor, "identity.verification", []privacy.DataClass{privacy.DataClassRawEvidence}, []string{"ng-1"}, "standard.contractual_clauses", now.Add(time.Hour))
	if err != nil || updated.Version != 2 || len(updated.DataClasses) != 1 {
		t.Fatalf("Update() = %+v, %v", updated, err)
	}
	if _, err := processor.Update(2, "stale", privacy.ProcessorRoleProcessor, "purpose", []privacy.DataClass{privacy.DataClassBackup}, []string{"ng-1"}, "mechanism", now.Add(2*time.Hour)); !errors.Is(err, privacy.ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	if _, err := privacy.NewProcessor(processorID, "bad", privacy.ProcessorRole("vendor"), "purpose", []privacy.DataClass{privacy.DataClassBackup}, []string{"ng-1"}, "mechanism", now); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("unknown role error = %v", err)
	}
}
