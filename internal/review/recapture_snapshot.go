package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/policy"
)

// RecaptureSnapshot adds the explicitly requested child outcome without replacing parent facts.
func RecaptureSnapshot(value Case, routing policy.Routing, snapshot policy.Snapshot, request RecaptureReevaluation, ack RecaptureAcknowledgement, child policy.Decision) (policy.Snapshot, string, error) {
	if err := value.Validate(); err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("validate recapture case: %w", err)
	}
	if value.State != CaseResolved {
		return policy.Snapshot{}, "", fmt.Errorf("recapture case must be resolved: %w", ErrConflict)
	}
	original := routing.Snapshot()
	for _, fact := range original.Facts() {
		if string(fact.Key) == "review.recapture" {
			return policy.Snapshot{}, "", fmt.Errorf("original routing contains reserved recapture fact: %w", ErrInvalid)
		}
	}

	if snapshot.TenantID() != original.TenantID() || snapshot.VerificationID() != value.VerificationID || value.RoutingRequest != routing.Request().DecisionID || !policy.SameAssuranceRequest(snapshot, original) || snapshot.Policy() != original.Policy() || snapshot.AuthorityID() != original.AuthorityID() || snapshot.AcknowledgementID() != original.AcknowledgementID() || snapshot.Region() != original.Region() || request.RecordedAt.Before(snapshot.EvaluatedAt()) {
		return policy.Snapshot{}, "", fmt.Errorf("recapture parent snapshot conflicts with routing: %w", ErrConflict)
	}
	if request.CaseID != value.ID || request.TargetVersion != value.Version || request.SourceVersion+1 != request.TargetVersion || !request.RecordedAt.Equal(value.UpdatedAt) || ack.CaseID != value.ID || ack.CaseVersion != request.SourceVersion || ack.DecisionID != request.DecisionID || child.ID() != request.DecisionID || child.Snapshot().VerificationID() != ack.ChildID || child.Snapshot().TenantID() != snapshot.TenantID() || request.RecordedAt.Before(ack.RecordedAt) || request.RecordedAt.Before(child.Snapshot().EvaluatedAt()) {
		return policy.Snapshot{}, "", fmt.Errorf("recapture request, acknowledgement, or child lineage conflicts: %w", ErrConflict)
	}
	encoded, err := json.Marshal(struct {
		Request         RecaptureReevaluation
		Acknowledgement RecaptureAcknowledgement
		ChildDigest     string
		SnapshotDigest  string
	}{request, ack, child.Digest(), snapshot.Digest()})
	if err != nil {
		return policy.Snapshot{}, "", err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	state := policy.RequirementInconclusive
	switch child.Evaluation().Outcome() {
	case policy.OutcomeVerified:
		state = policy.RequirementSatisfied
	case policy.OutcomeNotVerified:
		state = policy.RequirementNotSatisfied
	case policy.OutcomeInconclusive:
	default:
		return policy.Snapshot{}, "", fmt.Errorf("recapture child has unsupported outcome %q: %w", child.Evaluation().Outcome(), ErrInvalid)
	}
	key, err := policy.NewFactKey("review.recapture")
	if err != nil {
		return policy.Snapshot{}, "", err
	}
	facts := make([]policy.Fact, 0, len(snapshot.Facts())+1)
	for _, fact := range snapshot.Facts() {
		if fact.Key != key {
			facts = append(facts, fact)
		}
	}
	facts = append(facts, policy.Fact{Key: key, State: state, Source: policy.FactSource{Kind: policy.FactSourceReviewFinding, ReviewFinding: &policy.ReviewFindingSource{Reference: "review:" + digest}}, ObservedAt: child.Snapshot().EvaluatedAt(), ReasonCodes: []string{"recapture.child_" + string(child.Evaluation().Outcome())}})
	complete, err := policy.MergeRecaptureContext(snapshot, child)
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("merge recapture decision context: %w", err)
	}
	result, err := policy.NewSnapshot(policy.SnapshotInput{Context: complete, TenantID: snapshot.TenantID(), VerificationID: snapshot.VerificationID(), AuthorityID: snapshot.AuthorityID(), AcknowledgementID: snapshot.AcknowledgementID(), Region: snapshot.Region(), Policy: snapshot.Policy(), Evaluator: snapshot.Evaluator(), EvaluatedAt: request.RecordedAt, Facts: facts})
	if err != nil {
		return policy.Snapshot{}, "", fmt.Errorf("construct recapture snapshot: %w", err)
	}
	return result, digest, nil
}
