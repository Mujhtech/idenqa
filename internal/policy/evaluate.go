package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Evaluation is deterministic post-expression resolution. A future CEL
// adapter may produce RequirementResult values, but never owns this contract.
type Evaluation struct {
	snapshotDigest string
	results        []RequirementResult
	considered     []Directive
	selected       Directive
	outcome        Outcome
	assurance      string
	reasonCodes    []string
	canonical      []byte
	digest         string
}

type canonicalEvaluation struct {
	SchemaMajor    uint16                       `json:"schema_major"`
	SchemaMinor    uint16                       `json:"schema_minor"`
	SnapshotDigest string                       `json:"snapshot_digest"`
	Results        []canonicalRequirementResult `json:"results"`
	Considered     []Directive                  `json:"considered"`
	Selected       Directive                    `json:"selected"`
	Outcome        Outcome                      `json:"outcome,omitempty"`
	Assurance      string                       `json:"assurance,omitempty"`
	ReasonCodes    []string                     `json:"reason_codes"`
}

type canonicalRequirementResult struct {
	Name              string           `json:"name"`
	State             RequirementState `json:"state"`
	ContributingFacts []string         `json:"contributing_facts"`
	Candidate         Directive        `json:"candidate"`
	Priority          uint16           `json:"priority"`
	ReasonCodes       []string         `json:"reason_codes"`
}

// Resolve applies explicit policy priorities to bounded requirement results.
// It is pure: no clock, provider, model, storage, filesystem, or network input
// is available through its type surface.
func Resolve(snapshot Snapshot, results []RequirementResult, assurance string) (Evaluation, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return Evaluation{}, err
	}
	if len(results) == 0 || len(results) > MaximumRequirements ||
		(assurance != "" && !validToken(assurance, 128)) {
		return Evaluation{}, ErrInvalid
	}
	facts := make(map[FactKey]struct{}, len(snapshot.facts))
	for _, fact := range snapshot.facts {
		facts[fact.Key] = struct{}{}
	}
	ordered := make([]RequirementResult, len(results))
	for index, result := range results {
		validated, err := validateResult(result, facts)
		if err != nil {
			return Evaluation{}, fmt.Errorf("validate requirement result: %w", err)
		}
		ordered[index] = validated
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Name < ordered[right].Name })
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].Name == ordered[index].Name {
			return Evaluation{}, ErrConflict
		}
	}

	selectedPriority := ordered[0].Priority
	selected := ordered[0].Candidate
	for _, result := range ordered[1:] {
		if result.Priority < selectedPriority {
			selectedPriority, selected = result.Priority, result.Candidate
			continue
		}
		if result.Priority == selectedPriority && result.Candidate != selected {
			return Evaluation{}, ErrConflict
		}
	}
	assuranceGated := false
	achievement, err := snapshot.Achievement()
	if err != nil {
		return Evaluation{}, err
	}
	if selected == DirectiveCompleteVerified && achievement.RequestedDigest != "" && !achievement.Achieved {
		selected = DirectiveRouteManualReview
		assuranceGated = true
	}
	if snapshot.context != nil && snapshot.context.Profile != nil {
		assurance = ""
		if selected == DirectiveCompleteVerified {
			assurance = fmt.Sprintf("%s.v%d", snapshot.context.Profile.Name, snapshot.context.Profile.Revision)
		}
	}
	outcome, err := terminalOutcome(selected, ordered)
	if err != nil {
		return Evaluation{}, err
	}
	if selected == DirectiveCompleteVerified && assurance == "" {
		return Evaluation{}, ErrInvalid
	}
	considered := make([]Directive, 0, len(ordered)+1)
	allReasons := make([]string, 0)
	for _, result := range ordered {
		considered = append(considered, result.Candidate)
		allReasons = append(allReasons, result.ReasonCodes...)
	}
	if assuranceGated {
		considered = append(considered, DirectiveRouteManualReview)
		allReasons = append(allReasons, "assurance.not_achieved")
	}
	reasons, err := uniqueTokens(allReasons, MaximumSnapshotReasons)
	if err != nil {
		return Evaluation{}, err
	}
	canonicalResults := make([]canonicalRequirementResult, len(ordered))
	for index, result := range ordered {
		canonicalResults[index] = canonicalResultOf(result)
	}
	encoded, err := json.Marshal(canonicalEvaluation{
		SchemaMajor: SnapshotSchemaMajor, SchemaMinor: SnapshotSchemaMinor,
		SnapshotDigest: snapshot.digest, Results: canonicalResults, Considered: considered,
		Selected: selected, Outcome: outcome, Assurance: assurance, ReasonCodes: reasons,
	})
	if err != nil {
		return Evaluation{}, fmt.Errorf("encode policy evaluation: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return Evaluation{
		snapshotDigest: snapshot.digest, results: ordered, considered: slices.Clone(considered),
		selected: selected, outcome: outcome, assurance: assurance, reasonCodes: reasons,
		canonical: slices.Clone(encoded), digest: hex.EncodeToString(sum[:]),
	}, nil
}

// SnapshotDigest returns the digest of the evaluated snapshot.
func (evaluation Evaluation) SnapshotDigest() string { return evaluation.snapshotDigest }

// Selected returns the deterministically selected workflow directive.
func (evaluation Evaluation) Selected() Directive { return evaluation.selected }

// Outcome returns the terminal conclusion, or the zero value for non-terminal intent.
func (evaluation Evaluation) Outcome() Outcome { return evaluation.outcome }

// Assurance returns the assurance attached to a verified conclusion.
func (evaluation Evaluation) Assurance() string { return evaluation.assurance }

// Digest returns the canonical evaluation digest.
func (evaluation Evaluation) Digest() string { return evaluation.digest }

// Canonical returns a defensive copy of canonical evaluation bytes.
func (evaluation Evaluation) Canonical() []byte { return slices.Clone(evaluation.canonical) }

// Considered returns a defensive copy of ordered directive candidates.
func (evaluation Evaluation) Considered() []Directive { return slices.Clone(evaluation.considered) }

// ReasonCodes returns a defensive copy of canonical aggregate reason codes.
func (evaluation Evaluation) ReasonCodes() []string { return slices.Clone(evaluation.reasonCodes) }

// Results returns a defensive copy of ordered requirement results.
func (evaluation Evaluation) Results() []RequirementResult {
	result := make([]RequirementResult, len(evaluation.results))
	for index, requirement := range evaluation.results {
		result[index] = cloneResult(requirement)
	}
	return result
}

// AuthorisesCompletion reports only closed terminal directives with a matching
// authoritative outcome. Non-terminal workflow intent never authorises one.
func (evaluation Evaluation) AuthorisesCompletion() bool {
	switch evaluation.selected {
	case DirectiveCompleteVerified:
		return evaluation.outcome == OutcomeVerified
	case DirectiveCompleteNotVerified:
		return evaluation.outcome == OutcomeNotVerified
	case DirectiveCompleteInconclusive:
		return evaluation.outcome == OutcomeInconclusive
	default:
		return false
	}
}

func validateResult(value RequirementResult, facts map[FactKey]struct{}) (RequirementResult, error) {
	if !validToken(value.Name, 128) || !validRequirementState(value.State) || !validDirective(value.Candidate) ||
		value.Priority == 0 || value.Priority > MaximumPriority ||
		len(value.ContributingFacts) == 0 || len(value.ContributingFacts) > MaximumFacts ||
		len(value.ReasonCodes) > MaximumReasonsPerItem {
		return RequirementResult{}, ErrInvalid
	}
	validated := cloneResult(value)
	sort.Slice(validated.ContributingFacts, func(left, right int) bool {
		return validated.ContributingFacts[left] < validated.ContributingFacts[right]
	})
	for index, fact := range validated.ContributingFacts {
		if _, err := NewFactKey(string(fact)); err != nil {
			return RequirementResult{}, ErrInvalid
		}
		if _, exists := facts[fact]; !exists {
			return RequirementResult{}, ErrInvalid
		}
		if index > 0 && fact == validated.ContributingFacts[index-1] {
			return RequirementResult{}, ErrInvalid
		}
	}
	reasons, err := canonicalTokens(validated.ReasonCodes)
	if err != nil {
		return RequirementResult{}, err
	}
	validated.ReasonCodes = reasons
	return validated, nil
}

func terminalOutcome(selected Directive, results []RequirementResult) (Outcome, error) {
	hasNotSatisfied, hasInconclusive, hasUnavailable, hasProhibited := false, false, false, false
	allSatisfied := true
	for _, result := range results {
		allSatisfied = allSatisfied && result.State == RequirementSatisfied
		switch result.State {
		case RequirementNotSatisfied:
			hasNotSatisfied = true
		case RequirementInconclusive:
			hasInconclusive = true
		case RequirementUnavailable:
			hasUnavailable = true
		case RequirementProhibited:
			hasProhibited = true
		}
	}
	if hasProhibited && selected != DirectiveFailWorkflow {
		return "", ErrInvalid
	}
	switch selected {
	case DirectiveCompleteVerified:
		if !allSatisfied {
			return "", ErrInvalid
		}
		return OutcomeVerified, nil
	case DirectiveCompleteNotVerified:
		if !hasNotSatisfied || hasProhibited {
			return "", ErrInvalid
		}
		return OutcomeNotVerified, nil
	case DirectiveCompleteInconclusive:
		if (!hasInconclusive && !hasUnavailable) || hasNotSatisfied || hasProhibited {
			return "", ErrInvalid
		}
		return OutcomeInconclusive, nil
	default:
		return "", nil
	}
}

func uniqueTokens(values []string, maximum int) ([]string, error) {
	if len(values) > maximum {
		return nil, ErrInvalid
	}
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) > 0 && value == result[len(result)-1] {
			continue
		}
		result = append(result, value)
	}
	return slices.Clone(result), nil
}

func canonicalResultOf(value RequirementResult) canonicalRequirementResult {
	facts := make([]string, len(value.ContributingFacts))
	for index, fact := range value.ContributingFacts {
		facts[index] = string(fact)
	}
	return canonicalRequirementResult{
		Name: value.Name, State: value.State, ContributingFacts: facts,
		Candidate: value.Candidate, Priority: value.Priority, ReasonCodes: slices.Clone(value.ReasonCodes),
	}
}

// DecisionInput binds an authoritative terminal decision to exact evaluation meaning.
type DecisionInput struct {
	ID         id.Decision
	Snapshot   Snapshot
	Evaluation Evaluation
	Actor      ActorClass
	Supersedes id.Decision
	DecidedAt  time.Time
}

// Decision is immutable and contains everything required for reproduction.
type Decision struct {
	id         id.Decision
	snapshot   Snapshot
	evaluation Evaluation
	actor      ActorClass
	supersedes id.Decision
	decidedAt  time.Time
	canonical  []byte
	digest     string
}

type canonicalDecision struct {
	SchemaMajor      uint16     `json:"schema_major"`
	SchemaMinor      uint16     `json:"schema_minor"`
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	VerificationID   string     `json:"verification_id"`
	SnapshotDigest   string     `json:"snapshot_digest"`
	EvaluationDigest string     `json:"evaluation_digest"`
	Actor            ActorClass `json:"actor"`
	Supersedes       string     `json:"supersedes,omitempty"`
	DecidedAt        time.Time  `json:"decided_at"`
}

// NewDecision creates an authoritative record only from a terminal evaluation.
func NewDecision(input DecisionInput) (Decision, error) {
	if input.ID.IsZero() || !validActor(input.Actor) || !validUTC(input.DecidedAt) ||
		input.DecidedAt.Before(input.Snapshot.evaluatedAt) || !input.Evaluation.AuthorisesCompletion() ||
		input.Evaluation.snapshotDigest != input.Snapshot.digest ||
		(!input.Supersedes.IsZero() && input.Supersedes.String() == input.ID.String()) {
		return Decision{}, ErrInvalid
	}
	if err := validateSnapshot(input.Snapshot); err != nil {
		return Decision{}, err
	}
	reproduced, err := Resolve(input.Snapshot, input.Evaluation.Results(), input.Evaluation.assurance)
	if err != nil || reproduced.digest != input.Evaluation.digest {
		return Decision{}, ErrReproduction
	}
	canonical := canonicalDecision{
		SchemaMajor: SnapshotSchemaMajor, SchemaMinor: SnapshotSchemaMinor,
		ID: input.ID.String(), TenantID: input.Snapshot.tenantID.String(),
		VerificationID: input.Snapshot.verificationID.String(), SnapshotDigest: input.Snapshot.digest,
		EvaluationDigest: input.Evaluation.digest, Actor: input.Actor, DecidedAt: input.DecidedAt,
	}
	if !input.Supersedes.IsZero() {
		canonical.Supersedes = input.Supersedes.String()
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return Decision{}, fmt.Errorf("encode policy decision: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return Decision{
		id: input.ID, snapshot: cloneSnapshot(input.Snapshot), evaluation: cloneEvaluation(input.Evaluation),
		actor: input.Actor, supersedes: input.Supersedes, decidedAt: input.DecidedAt,
		canonical: slices.Clone(encoded), digest: hex.EncodeToString(sum[:]),
	}, nil
}

// RestoreDecision verifies stored digests before accepting durable state.
func RestoreDecision(input DecisionInput, snapshotDigest string, evaluationDigest string) (Decision, error) {
	if input.Snapshot.digest != snapshotDigest || input.Evaluation.digest != evaluationDigest ||
		!validDigest(snapshotDigest) || !validDigest(evaluationDigest) {
		return Decision{}, ErrReproduction
	}
	return NewDecision(input)
}

// Reproduce recomputes a decision using only its stored snapshot and results.
func Reproduce(decision Decision) (Evaluation, error) {
	if decision.id.IsZero() || !validActor(decision.actor) || !validUTC(decision.decidedAt) {
		return Evaluation{}, ErrReproduction
	}
	reproduced, err := Resolve(decision.snapshot, decision.evaluation.Results(), decision.evaluation.assurance)
	if err != nil {
		return Evaluation{}, fmt.Errorf("reproduce decision: %w", err)
	}
	if reproduced.digest != decision.evaluation.digest ||
		reproduced.selected != decision.evaluation.selected || reproduced.outcome != decision.evaluation.outcome ||
		!slices.Equal(reproduced.reasonCodes, decision.evaluation.reasonCodes) {
		return Evaluation{}, ErrReproduction
	}
	return reproduced, nil
}

// ID returns the decision identifier.
func (decision Decision) ID() id.Decision { return decision.id }

// Snapshot returns a defensive copy of the exact decision snapshot.
func (decision Decision) Snapshot() Snapshot { return cloneSnapshot(decision.snapshot) }

// Evaluation returns a defensive copy of the exact policy evaluation.
func (decision Decision) Evaluation() Evaluation { return cloneEvaluation(decision.evaluation) }

// Actor returns the class of actor that authored the decision.
func (decision Decision) Actor() ActorClass { return decision.actor }

// Supersedes returns the preceding decision identifier, if any.
func (decision Decision) Supersedes() id.Decision { return decision.supersedes }

// DecidedAt returns the explicit decision time.
func (decision Decision) DecidedAt() time.Time { return decision.decidedAt }

// Canonical returns a defensive copy of canonical decision bytes.
func (decision Decision) Canonical() []byte { return slices.Clone(decision.canonical) }

// Digest returns the canonical decision digest.
func (decision Decision) Digest() string { return decision.digest }

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := snapshot
	cloned.canonical = slices.Clone(snapshot.canonical)
	cloned.facts = snapshot.Facts()
	return cloned
}

func cloneEvaluation(evaluation Evaluation) Evaluation {
	cloned := evaluation
	cloned.results = evaluation.Results()
	cloned.considered = slices.Clone(evaluation.considered)
	cloned.reasonCodes = slices.Clone(evaluation.reasonCodes)
	cloned.canonical = slices.Clone(evaluation.canonical)
	return cloned
}
