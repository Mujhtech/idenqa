package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// RestoreSnapshotCanonical reconstructs a snapshot only from exact canonical
// bytes and their expected digest.
func RestoreSnapshotCanonical(encoded []byte, digest string) (Snapshot, error) {
	if len(encoded) == 0 || len(encoded) > MaximumSnapshotBytes || !validDigest(digest) {
		return Snapshot{}, ErrReproduction
	}
	var stored canonicalSnapshot
	if err := decodeClosed(encoded, &stored); err != nil ||
		stored.SchemaMajor != SnapshotSchemaMajor || stored.SchemaMinor != SnapshotSchemaMinor {
		return Snapshot{}, ErrReproduction
	}
	input, err := snapshotInputOf(stored)
	if err != nil {
		return Snapshot{}, ErrReproduction
	}
	snapshot, err := NewSnapshot(input)
	if err != nil || snapshot.digest != digest || !bytes.Equal(snapshot.canonical, encoded) {
		return Snapshot{}, ErrReproduction
	}
	return snapshot, nil
}

// RestoreEvaluationCanonical reconstructs an evaluation against the exact
// supplied snapshot and rejects any stored derived meaning that differs.
func RestoreEvaluationCanonical(snapshot Snapshot, encoded []byte, digest string) (Evaluation, error) {
	if len(encoded) == 0 || len(encoded) > MaximumEvaluationBytes || !validDigest(digest) ||
		validateSnapshot(snapshot) != nil {
		return Evaluation{}, ErrReproduction
	}
	var stored canonicalEvaluation
	if err := decodeClosed(encoded, &stored); err != nil ||
		stored.SchemaMajor != SnapshotSchemaMajor || stored.SchemaMinor != SnapshotSchemaMinor ||
		stored.SnapshotDigest != snapshot.digest {
		return Evaluation{}, ErrReproduction
	}
	results, err := resultsOf(stored.Results)
	if err != nil {
		return Evaluation{}, ErrReproduction
	}
	evaluation, err := Resolve(snapshot, results, stored.Assurance)
	if err != nil || evaluation.digest != digest || !bytes.Equal(evaluation.canonical, encoded) {
		return Evaluation{}, ErrReproduction
	}
	return evaluation, nil
}

// RestoreDecisionCanonical reconstructs a decision through the same terminal
// authorisation path used for a new decision.
func RestoreDecisionCanonical(
	snapshot Snapshot,
	evaluation Evaluation,
	encoded []byte,
	digest string,
) (Decision, error) {
	if len(encoded) == 0 || len(encoded) > MaximumDecisionBytes || !validDigest(digest) {
		return Decision{}, ErrReproduction
	}
	var stored canonicalDecision
	if err := decodeClosed(encoded, &stored); err != nil ||
		stored.SchemaMajor != SnapshotSchemaMajor || stored.SchemaMinor != SnapshotSchemaMinor {
		return Decision{}, ErrReproduction
	}
	decisionID, err := id.ParseDecision(stored.ID)
	if err != nil {
		return Decision{}, ErrReproduction
	}
	tenantID, err := id.ParseTenant(stored.TenantID)
	if err != nil || tenantID.String() != snapshot.tenantID.String() {
		return Decision{}, ErrReproduction
	}
	verificationID, err := id.ParseVerification(stored.VerificationID)
	if err != nil || verificationID.String() != snapshot.verificationID.String() ||
		stored.SnapshotDigest != snapshot.digest || stored.EvaluationDigest != evaluation.digest {
		return Decision{}, ErrReproduction
	}
	var supersedes id.Decision
	if stored.Supersedes != "" {
		supersedes, err = id.ParseDecision(stored.Supersedes)
		if err != nil {
			return Decision{}, ErrReproduction
		}
	}
	decision, err := NewDecision(DecisionInput{
		ID: decisionID, Snapshot: snapshot, Evaluation: evaluation, Actor: stored.Actor,
		Supersedes: supersedes, DecidedAt: stored.DecidedAt,
	})
	if err != nil || decision.digest != digest || !bytes.Equal(decision.canonical, encoded) {
		return Decision{}, ErrReproduction
	}
	return decision, nil
}

func decodeClosed(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrReproduction
	}
	return nil
}

func snapshotInputOf(stored canonicalSnapshot) (SnapshotInput, error) {
	tenantID, err := id.ParseTenant(stored.TenantID)
	if err != nil {
		return SnapshotInput{}, err
	}
	verificationID, err := id.ParseVerification(stored.VerificationID)
	if err != nil {
		return SnapshotInput{}, err
	}
	authorityID, err := id.ParseAuthority(stored.AuthorityID)
	if err != nil {
		return SnapshotInput{}, err
	}
	acknowledgementID, err := id.ParseAcknowledgement(stored.AcknowledgementID)
	if err != nil {
		return SnapshotInput{}, err
	}
	policyID, err := id.ParsePolicy(stored.Policy.ID)
	if err != nil {
		return SnapshotInput{}, err
	}
	facts := make([]Fact, len(stored.Facts))
	for index, fact := range stored.Facts {
		facts[index], err = factOf(fact)
		if err != nil {
			return SnapshotInput{}, err
		}
	}
	return SnapshotInput{
		TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID,
		AcknowledgementID: acknowledgementID, Region: stored.Region,
		Policy: Reference{ID: policyID, Revision: stored.Policy.Revision,
			SchemaMajor: stored.Policy.SchemaMajor, SchemaMinor: stored.Policy.SchemaMinor, Digest: stored.Policy.Digest},
		Evaluator: EvaluatorReference{Major: stored.Evaluator.Major, Minor: stored.Evaluator.Minor,
			Digest: stored.Evaluator.Digest},
		EvaluatedAt: stored.EvaluatedAt, Facts: facts, Context: stored.Context,
	}, nil
}

func factOf(stored canonicalFact) (Fact, error) {
	key, err := NewFactKey(stored.Key)
	if err != nil {
		return Fact{}, err
	}
	source, err := sourceOf(stored.Source)
	if err != nil {
		return Fact{}, err
	}
	return Fact{Key: key, State: stored.State, Source: source, ObservedAt: stored.ObservedAt,
		ExpiresAt: stored.ExpiresAt, ReasonCodes: slices.Clone(stored.ReasonCodes)}, nil
}

func sourceOf(stored canonicalSource) (FactSource, error) {
	switch stored.Kind {
	case FactSourceCheck:
		checkID, err := id.ParseCheck(stored.CheckID)
		if err != nil {
			return FactSource{}, err
		}
		attemptID, err := id.ParseAttempt(stored.AttemptID)
		if err != nil {
			return FactSource{}, err
		}
		observations := make([]id.Observation, len(stored.ObservationIDs))
		for index, encoded := range stored.ObservationIDs {
			observations[index], err = id.ParseObservation(encoded)
			if err != nil {
				return FactSource{}, err
			}
		}
		return FactSource{Kind: stored.Kind, Check: &CheckSource{
			CheckID: checkID, CheckVersion: stored.CheckVersion, AttemptID: attemptID,
			ObservationIDs: observations, ContractDigest: stored.ContractDigest,
			ImplementationDigest: stored.ImplementationDigest,
		}}, nil
	case FactSourceProcessingAuthority:
		authorityID, err := id.ParseAuthority(stored.AuthorityID)
		if err != nil {
			return FactSource{}, err
		}
		return FactSource{Kind: stored.Kind, Authority: &AuthoritySource{AuthorityID: authorityID}}, nil
	case FactSourceSubjectResponse:
		acknowledgementID, err := id.ParseAcknowledgement(stored.AcknowledgementID)
		if err != nil {
			return FactSource{}, err
		}
		return FactSource{Kind: stored.Kind,
			SubjectResponse: &SubjectResponseSource{AcknowledgementID: acknowledgementID}}, nil
	case FactSourceIdentity:
		return FactSource{Kind: stored.Kind, Identity: &IdentitySource{ReceiptDigest: stored.IdentityReceipt}}, nil
	case FactSourceFraud:
		return FactSource{Kind: stored.Kind, Fraud: &FraudSource{ReceiptDigest: stored.FraudReceipt}}, nil
	case FactSourceReviewFinding:
		return FactSource{Kind: stored.Kind,
			ReviewFinding: &ReviewFindingSource{Reference: stored.ReviewFinding}}, nil
	default:
		return FactSource{}, ErrReproduction
	}
}

func resultsOf(stored []canonicalRequirementResult) ([]RequirementResult, error) {
	results := make([]RequirementResult, len(stored))
	for index, result := range stored {
		facts := make([]FactKey, len(result.ContributingFacts))
		for factIndex, encoded := range result.ContributingFacts {
			fact, err := NewFactKey(encoded)
			if err != nil {
				return nil, err
			}
			facts[factIndex] = fact
		}
		results[index] = RequirementResult{Name: result.Name, State: result.State,
			ContributingFacts: facts, Candidate: result.Candidate, Priority: result.Priority,
			ReasonCodes: slices.Clone(result.ReasonCodes)}
	}
	return results, nil
}
