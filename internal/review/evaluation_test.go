package review_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
)

func TestAcceptedFindingRequiresPermittedIndependentConsensus(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	ids, _ := id.NewSystemGenerator()
	caseID, _ := ids.NewReviewCase()
	verificationID, _ := ids.NewVerification()
	requestID, _ := ids.NewDecision()
	for _, resolution := range []review.Resolution{review.ResolutionSatisfy, review.ResolutionNotSatisfy, review.ResolutionRequestInput} {
		t.Run(string(resolution), func(t *testing.T) {
			value, err := review.NewRoutedCase(caseID, verificationID, requestID, "tenant.region.ng", "document.level2", review.OversightDual, now)
			if err != nil {
				t.Fatal(err)
			}
			value.PermittedFindings = []review.PermittedFinding{{Resolution: review.ResolutionSatisfy, ReasonCode: "reviewed"}, {Resolution: review.ResolutionNotSatisfy, ReasonCode: "reviewed"}, {Resolution: review.ResolutionRequestInput, ReasonCode: "reviewed"}}
			first := review.Principal{ID: "operator.one", Permissions: []review.Permission{review.PermissionClaim, review.PermissionFind}, Certifications: []string{"document.level2"}}
			value, err = value.Claim(first, 1, now)
			if err != nil {
				t.Fatal(err)
			}
			grantID, _ := ids.NewGrant()
			findingID, _ := ids.NewFinding()
			firstFinding := review.Finding{ID: findingID, ReviewerID: first.ID, Resolution: resolution, ReasonCode: "reviewed", GrantIDs: []id.Grant{grantID}, RecordedAt: now}
			grant := review.EvidenceGrant{ID: grantID, ReviewerID: first.ID, Region: value.Region, ExpiresAt: now.Add(time.Hour)}
			invalid := firstFinding
			invalid.ReasonCode = "unapproved"
			if _, err := value.SubmitFinding(first, invalid, []review.EvidenceGrant{grant}, value.Version); !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("unapproved reason=%v", err)
			}
			awaiting, err := value.SubmitFinding(first, firstFinding, []review.EvidenceGrant{grant}, value.Version)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := awaiting.AcceptedFact(); err == nil {
				t.Fatal("partial consensus accepted")
			}
			second := first
			second.ID = "operator.two"
			secondFinding := firstFinding
			secondFinding.ID, _ = ids.NewFinding()
			secondFinding.ReviewerID = second.ID
			secondGrant := grant
			secondGrant.ReviewerID = second.ID
			resolved, err := awaiting.SubmitFinding(second, secondFinding, []review.EvidenceGrant{secondGrant}, awaiting.Version)
			if err != nil {
				t.Fatal(err)
			}
			fact, digest, err := resolved.AcceptedFact()
			if err != nil || len(digest) != 64 {
				t.Fatalf("accepted=%v", err)
			}
			want := policy.RequirementInconclusive
			if resolution == review.ResolutionSatisfy {
				want = policy.RequirementSatisfied
			}
			if resolution == review.ResolutionNotSatisfy {
				want = policy.RequirementNotSatisfied
			}
			if fact.State != want || fact.Source.Kind != policy.FactSourceReviewFinding {
				t.Fatalf("fact=%+v", fact)
			}
			secondFinding.Resolution = review.ResolutionNotSatisfy
			if resolution == review.ResolutionNotSatisfy {
				secondFinding.Resolution = review.ResolutionSatisfy
			}
			conflict, err := awaiting.SubmitFinding(second, secondFinding, []review.EvidenceGrant{secondGrant}, awaiting.Version)
			if err != nil || conflict.State != review.CaseEscalated {
				t.Fatalf("conflict=%+v %v", conflict, err)
			}
			if _, _, err := conflict.AcceptedFact(); err == nil {
				t.Fatal("conflicting reviewers accepted")
			}
		})
	}
}
