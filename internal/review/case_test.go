package review_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
)

func TestCaseConcurrencyAuthorityDualControlAndCorrection(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	generator, _ := id.NewSystemGenerator()
	caseID, _ := generator.NewReviewCase()
	verificationID, _ := generator.NewVerification()
	challenged, _ := generator.NewDecision()
	reviewCase, err := review.NewCase(caseID, verificationID, challenged, "ng-1", "document.level2", review.OversightDual, now)
	if err != nil {
		t.Fatal(err)
	}
	first := review.Principal{ID: "reviewer-1", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
	claimed, err := reviewCase.Claim(first, 1, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewCase.Claim(first, 1, now.Add(time.Minute)); err != nil {
		t.Fatalf("independent original claim should be valid: %v", err)
	}
	if _, err := claimed.Claim(first, 1, now.Add(2*time.Minute)); !errors.Is(err, review.ErrConflict) {
		t.Fatalf("double claim error = %v", err)
	}
	grantID, _ := generator.NewGrant()
	findingID, _ := generator.NewFinding()
	grant := review.EvidenceGrant{ID: grantID, ReviewerID: first.ID, Region: "ng-1", ExpiresAt: now.Add(time.Hour)}
	finding := review.Finding{ID: findingID, ReviewerID: first.ID, Resolution: review.ResolutionSatisfy, ReasonCode: "document_authentic", GrantIDs: []id.Grant{grantID}, RecordedAt: now.Add(2 * time.Minute)}
	awaiting, err := claimed.SubmitFinding(first, finding, []review.EvidenceGrant{grant}, claimed.Version)
	if err != nil || awaiting.State != review.CaseAwaitingSecond {
		t.Fatalf("first finding = %+v, %v", awaiting, err)
	}
	if _, err := awaiting.SubmitFinding(first, finding, []review.EvidenceGrant{grant}, awaiting.Version); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("same reviewer dual control error = %v", err)
	}
	second := review.Principal{ID: "reviewer-2", Permissions: []review.Permission{review.PermissionFind}, Certifications: []string{"document.level2"}}
	secondGrantID, _ := generator.NewGrant()
	secondFindingID, _ := generator.NewFinding()
	secondFinding := review.Finding{ID: secondFindingID, ReviewerID: second.ID, Resolution: review.ResolutionSatisfy, ReasonCode: "second_approval", GrantIDs: []id.Grant{secondGrantID}, RecordedAt: now.Add(3 * time.Minute)}
	uncertified := second
	uncertified.Certifications = nil
	if _, err := awaiting.SubmitFinding(uncertified, secondFinding, []review.EvidenceGrant{{ID: secondGrantID, ReviewerID: second.ID, Region: "ng-1", ExpiresAt: now.Add(time.Hour)}}, awaiting.Version); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("uncertified second reviewer error = %v", err)
	}
	resolved, err := awaiting.SubmitFinding(second, secondFinding, []review.EvidenceGrant{{ID: secondGrantID, ReviewerID: second.ID, Region: "ng-1", ExpiresAt: now.Add(time.Hour)}}, awaiting.Version)
	if err != nil || resolved.State != review.CaseResolved {
		t.Fatalf("second finding = %+v, %v", resolved, err)
	}
	decisionID, _ := generator.NewDecision()
	if _, err := resolved.AuthorCorrection(first, decisionID, resolved.Version, now.Add(4*time.Minute)); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("self correction error = %v", err)
	}
	resolver := review.Principal{ID: "supervisor", Permissions: []review.Permission{review.PermissionResolve}}
	corrected, err := resolved.AuthorCorrection(resolver, decisionID, resolved.Version, now.Add(4*time.Minute))
	if err != nil || corrected.SupersedesDecision.String() != decisionID.String() {
		t.Fatalf("correction = %+v, %v", corrected, err)
	}
}

func TestCaseRejectsProhibitedEvidenceAndAppealSelfReview(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	generator, _ := id.NewSystemGenerator()
	caseID, _ := generator.NewReviewCase()
	verificationID, _ := generator.NewVerification()
	decisionID, _ := generator.NewDecision()
	reviewCase, _ := review.NewCase(caseID, verificationID, decisionID, "ng-1", "basic", review.OversightSingle, now)
	principal := review.Principal{ID: "reviewer-1", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind, review.PermissionAppeal}, Certifications: []string{"basic"}}
	claimed, _ := reviewCase.Claim(principal, 1, now.Add(time.Minute))
	grantID, _ := generator.NewGrant()
	findingID, _ := generator.NewFinding()
	finding := review.Finding{ID: findingID, ReviewerID: principal.ID, Resolution: review.ResolutionNotSatisfy, ReasonCode: "mismatch", GrantIDs: []id.Grant{grantID}, RecordedAt: now.Add(2 * time.Minute)}
	if _, err := claimed.SubmitFinding(principal, finding, []review.EvidenceGrant{{ID: grantID, ReviewerID: principal.ID, Region: "eu-1", ExpiresAt: now.Add(time.Hour)}}, claimed.Version); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("wrong-region grant error = %v", err)
	}
	appealID, _ := generator.NewAppeal()
	appeal := review.Appeal{ID: appealID, CaseID: caseID, ChallengedDecision: decisionID, OriginalReviewers: []string{principal.ID}, State: review.AppealRequested, Deadline: now.Add(24 * time.Hour), Version: 1}
	if _, err := appeal.Assign(principal, 1, now.Add(time.Hour)); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("self appeal error = %v", err)
	}
	independent := review.Principal{ID: "reviewer-2", Permissions: []review.Permission{review.PermissionAppeal}}
	assigned, err := appeal.Assign(independent, 1, now.Add(time.Hour))
	if err != nil || assigned.State != review.AppealIndependentReview {
		t.Fatalf("Assign() = %+v, %v", assigned, err)
	}
	if _, err := assigned.Resolve(principal, review.AppealUpheld, "original_correct", id.Decision{}, assigned.Version, now.Add(2*time.Hour)); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("original reviewer appeal resolution error = %v", err)
	}
	successor, _ := generator.NewDecision()
	resolved, err := assigned.Resolve(independent, review.AppealOverturned, "new_evidence", successor, assigned.Version, now.Add(2*time.Hour))
	if err != nil || resolved.State != review.AppealResolved || resolved.SupersedingDecision.String() != successor.String() {
		t.Fatalf("Resolve() = %+v, %v", resolved, err)
	}
}
