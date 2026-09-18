package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

// PermittedFinding is an explicit resolution/reason pair pinned when routing opens a case.
type PermittedFinding struct {
	Resolution Resolution `json:"resolution"`
	ReasonCode string     `json:"reason_code"`
}

var findingReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

// ValidateFindingRules bounds the operational finding allowlist; empty denies routed findings.
func ValidateFindingRules(rules []PermittedFinding) error {
	if len(rules) > 64 {
		return ErrInvalid
	}
	seen := map[PermittedFinding]bool{}
	for _, rule := range rules {
		if !validResolution(rule.Resolution) || !findingReasonPattern.MatchString(rule.ReasonCode) || seen[rule] {
			return ErrInvalid
		}
		seen[rule] = true
	}
	return nil
}

// EvaluationRequest identifies one immutable resolved-case version, separately from policy.author.
type EvaluationRequest struct {
	CaseID  id.ReviewCase `json:"case_id"`
	Version int64         `json:"version"`
}

// Evaluation retains the original routing and resolved-case input digest alongside policy output.
type Evaluation struct {
	Request    EvaluationRequest
	DecisionID id.Decision
	Snapshot   policy.Snapshot
	Result     policy.Evaluation
	CaseDigest string
}

// AcceptedFact requires complete independent agreement and an explicit pinned allowlist.
func (value Case) AcceptedFact() (policy.Fact, string, error) {
	if value.Validate() != nil || value.RoutingRequest.IsZero() || value.State != CaseResolved || len(value.Findings) == 0 {
		return policy.Fact{}, "", ErrConflict
	}
	required := 1
	if value.Oversight == OversightDual {
		required = 2
	}
	if len(value.Findings) != required {
		return policy.Fact{}, "", ErrConflict
	}
	resolution := value.Findings[0].Resolution
	reviewers := map[string]bool{}
	reasons := []string{}
	for _, finding := range value.Findings {
		if reviewers[finding.ReviewerID] || finding.Resolution != resolution || !slices.Contains(value.PermittedFindings, PermittedFinding{Resolution: finding.Resolution, ReasonCode: finding.ReasonCode}) {
			return policy.Fact{}, "", ErrForbidden
		}
		reviewers[finding.ReviewerID] = true
		reasons = append(reasons, finding.ReasonCode)
	}
	type findingReference struct {
		ID         string
		ReviewerID string
		Resolution Resolution
		Reason     string
		Grants     []string
		At         time.Time
	}
	references := make([]findingReference, 0, len(value.Findings))
	for _, finding := range value.Findings {
		grants := make([]string, len(finding.GrantIDs))
		for index, grant := range finding.GrantIDs {
			grants[index] = grant.String()
		}
		slices.Sort(grants)
		references = append(references, findingReference{finding.ID.String(), finding.ReviewerID, finding.Resolution, finding.ReasonCode, grants, finding.RecordedAt})
	}
	slices.SortFunc(references, func(a, b findingReference) int { return strings.Compare(a.ID, b.ID) })
	encoded, err := json.Marshal(struct {
		ID       string
		Version  int64
		Findings []findingReference
	}{value.ID.String(), value.Version, references})
	if err != nil {
		return policy.Fact{}, "", err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	state := policy.RequirementInconclusive
	switch resolution {
	case ResolutionSatisfy:
		state = policy.RequirementSatisfied
	case ResolutionNotSatisfy:
		state = policy.RequirementNotSatisfied
	}
	key, err := policy.NewFactKey("review.resolution")
	if err != nil {
		return policy.Fact{}, "", err
	}
	slices.Sort(reasons)
	reasons = slices.Compact(reasons)
	return policy.Fact{Key: key, State: state, Source: policy.FactSource{Kind: policy.FactSourceReviewFinding, ReviewFinding: &policy.ReviewFindingSource{Reference: "review:" + digest}}, ObservedAt: value.UpdatedAt, ReasonCodes: reasons}, digest, nil
}

// Snapshot preserves routed policy/facts and adds a single independently sourced resolution.
func Snapshot(value Case, routing policy.Routing) (policy.Snapshot, string, error) {
	fact, digest, err := value.AcceptedFact()
	if err != nil {
		return policy.Snapshot{}, "", err
	}
	original := routing.Snapshot()
	if value.RoutingRequest != routing.Request().DecisionID || value.VerificationID != original.VerificationID() || value.UpdatedAt.Before(original.EvaluatedAt()) || value.UpdatedAt.Nanosecond()%1000 != 0 {
		return policy.Snapshot{}, "", ErrInvalid
	}
	facts := original.Facts()
	for _, prior := range facts {
		if prior.Key == fact.Key {
			return policy.Snapshot{}, "", fmt.Errorf("%w: reserved review fact", ErrInvalid)
		}
	}
	complete := original.Context()
	if complete != nil {
		for _, finding := range value.Findings {
			complete.References = append(complete.References, policy.DecisionReference{Kind: "review_finding", ID: finding.ID.String(), Digest: digest, Parents: []string{value.ID.String(), finding.ReviewerID}})
			complete.Sources = append(complete.Sources, policy.AssuranceSource{Reference: finding.ID.String(), Signal: "review.resolution", State: fact.State, RunnerKind: "review", RunnerID: "idenqa.review", PackageDigest: policy.BuiltinAssuranceDigest("review"), ConfigurationDigest: policy.BuiltinAssuranceDigest("review"), CollectedAt: finding.RecordedAt, Roots: []string{"reviewer." + policy.BuiltinAssuranceDigest(finding.ReviewerID)}})
		}
	}
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{Context: complete, TenantID: original.TenantID(), VerificationID: original.VerificationID(), AuthorityID: original.AuthorityID(), AcknowledgementID: original.AcknowledgementID(), Region: original.Region(), Policy: original.Policy(), Evaluator: original.Evaluator(), EvaluatedAt: value.UpdatedAt, Facts: append(facts, fact)})
	return snapshot, digest, err
}

// Decision constructs a machine decision only when deterministic policy authorizes completion.
func (value Evaluation) Decision() (policy.Decision, error) {
	return policy.NewDecision(policy.DecisionInput{ID: value.DecisionID, Snapshot: value.Snapshot, Evaluation: value.Result, Actor: policy.ActorMachine, DecidedAt: value.Snapshot.EvaluatedAt()})
}

// ValidTime permits a durable microsecond timestamp for accepted findings.
func ValidTime(at time.Time) bool {
	return !at.IsZero() && at.Location() == time.UTC && at.Nanosecond()%1000 == 0
}
