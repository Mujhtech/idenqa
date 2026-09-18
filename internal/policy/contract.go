// Package policy owns deterministic policy inputs, evaluation results, and
// immutable decisions. Expression-language adapters remain outside this package.
package policy

import (
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Snapshot schema versions and resource limits bound deterministic evaluation.
const (
	SnapshotSchemaMajor    = 1
	SnapshotSchemaMinor    = 0
	MaximumFacts           = 128
	MaximumRequirements    = 128
	MaximumReasonsPerItem  = 16
	MaximumSnapshotReasons = 256
	MaximumObservations    = 64
	MaximumPriority        = 1000
	MaximumSnapshotBytes   = 256 * 1024
	MaximumEvaluationBytes = 256 * 1024
	MaximumDecisionBytes   = 16 * 1024
	MaximumBundleBytes     = MaximumSnapshotBytes + MaximumEvaluationBytes + MaximumDecisionBytes + 16*1024
)

var (
	// ErrInvalid means a policy value failed closed validation.
	ErrInvalid = errors.New("policy: invalid value")
	// ErrConflict means equal policy inputs express incompatible meaning.
	ErrConflict = errors.New("policy: conflicting meaning")
	// ErrVersion means a policy, evaluator, or snapshot version is unsupported.
	ErrVersion = errors.New("policy: unsupported version")
	// ErrStaleFact means a fact is not valid at the explicit evaluation time.
	ErrStaleFact = errors.New("policy: stale fact")
	// ErrReproduction means stored decision meaning cannot be reproduced exactly.
	ErrReproduction = errors.New("policy: reproduction mismatch")
	// ErrDecisionNotFound means no decision is visible in the tenant scope.
	ErrDecisionNotFound = errors.New("policy: decision not found")
	// ErrDecisionConflict means a durable decision conflicts with existing lineage.
	ErrDecisionConflict = errors.New("policy: decision conflict")
	// ErrRevisionNotFound means no policy revision is visible in the tenant scope.
	ErrRevisionNotFound = errors.New("policy: revision not found")
	// ErrRevisionConflict means immutable revision meaning conflicts with durable state.
	ErrRevisionConflict = errors.New("policy: revision conflict")
	// ErrActivationNotFound means the policy has no active revision in the tenant scope.
	ErrActivationNotFound = errors.New("policy: activation not found")
	// ErrActivationConflict means the expected activation version is stale or invalid.
	ErrActivationConflict = errors.New("policy: activation conflict")

	tokenPattern   = regexp.MustCompile(`^[a-z][a-z0-9._:-]*$`)
	factKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// RequirementState is one closed policy-requirement result.
type RequirementState string

// Requirement states form the complete result-state vocabulary.
const (
	RequirementSatisfied    RequirementState = "satisfied"
	RequirementNotSatisfied RequirementState = "not_satisfied"
	RequirementInconclusive RequirementState = "inconclusive"
	RequirementUnavailable  RequirementState = "unavailable"
	RequirementProhibited   RequirementState = "prohibited"
)

// Directive identifies workflow intent without performing an effect.
type Directive string

// Directives form the complete workflow-intent vocabulary.
const (
	DirectiveCompleteVerified     Directive = "complete_verified"
	DirectiveCompleteNotVerified  Directive = "complete_not_verified"
	DirectiveCompleteInconclusive Directive = "complete_inconclusive"
	DirectiveRequestInput         Directive = "request_input"
	DirectiveRunCheck             Directive = "run_check"
	DirectiveRetryCheck           Directive = "retry_check"
	DirectiveUseFallback          Directive = "use_fallback"
	DirectiveRouteManualReview    Directive = "route_manual_review"
	DirectiveFailWorkflow         Directive = "fail_workflow"
)

// Outcome is an authoritative terminal conclusion, not workflow state.
type Outcome string

// Outcomes form the complete terminal conclusion vocabulary.
const (
	OutcomeVerified     Outcome = "verified"
	OutcomeNotVerified  Outcome = "not_verified"
	OutcomeInconclusive Outcome = "inconclusive"
)

// ActorClass identifies who authored an immutable decision.
type ActorClass string

// Actor classes distinguish machine and authorised human decisions.
const (
	ActorMachine ActorClass = "machine"
	ActorHuman   ActorClass = "human"
)

// FactSourceKind identifies one closed source-reference shape.
type FactSourceKind string

// Fact source kinds form the complete source-reference vocabulary.
const (
	FactSourceCheck               FactSourceKind = "check"
	FactSourceProcessingAuthority FactSourceKind = "processing_authority"
	FactSourceSubjectResponse     FactSourceKind = "subject_response"
	FactSourceReviewFinding       FactSourceKind = "review_finding"
	FactSourceFraud               FactSourceKind = "fraud"
	FactSourceIdentity            FactSourceKind = "identity"
)

// Reference pins exact policy meaning independently of source syntax.
type Reference struct {
	ID          id.Policy
	Revision    uint32
	SchemaMajor uint16
	SchemaMinor uint16
	Digest      string
}

// EvaluatorReference pins the evaluator implementation that produced results.
type EvaluatorReference struct {
	Major  uint16
	Minor  uint16
	Digest string
}

// FactKey is a bounded namespaced identifier, never a dynamic value.
type FactKey string

// NewFactKey validates one namespaced fact key.
func NewFactKey(value string) (FactKey, error) {
	if len(value) == 0 || len(value) > 128 || !factKeyPattern.MatchString(value) {
		return "", ErrInvalid
	}
	return FactKey(value), nil
}

// CheckSource pins exact normalised check provenance.
type CheckSource struct {
	CheckID              id.Check
	CheckVersion         int64
	AttemptID            id.Attempt
	ObservationIDs       []id.Observation
	ContractDigest       string
	ImplementationDigest string
}

// AuthoritySource pins the processing authority evaluated as a fact.
type AuthoritySource struct{ AuthorityID id.Authority }

// SubjectResponseSource pins the exact subject response evaluated as a fact.
type SubjectResponseSource struct{ AcknowledgementID id.Acknowledgement }

// ReviewFindingSource identifies a pinned accepted-case input digest. The review
// evaluation receipt binds that digest to the immutable case version and findings.
type ReviewFindingSource struct{ Reference string }

// FraudSource pins an immutable tenant risk evaluation receipt.
type FraudSource struct{ ReceiptDigest string }

// IdentitySource pins an immutable typed identity interpretation receipt.
type IdentitySource struct{ ReceiptDigest string }

// FactSource is a closed tagged union. Exactly one matching pointer is set.
type FactSource struct {
	Identity        *IdentitySource
	Kind            FactSourceKind
	Check           *CheckSource
	Authority       *AuthoritySource
	SubjectResponse *SubjectResponseSource
	ReviewFinding   *ReviewFindingSource
	Fraud           *FraudSource
}

// Fact contains only typed state and immutable lineage. It cannot carry a raw
// signal value, evidence bytes, credentials, locations, or arbitrary maps.
type Fact struct {
	Key         FactKey
	State       RequirementState
	Source      FactSource
	ObservedAt  time.Time
	ExpiresAt   *time.Time
	ReasonCodes []string
}

// RequirementResult is expression-engine output constrained by owned types.
type RequirementResult struct {
	Name              string
	State             RequirementState
	ContributingFacts []FactKey
	Candidate         Directive
	Priority          uint16
	ReasonCodes       []string
}

func (reference Reference) validate() error {
	if reference.ID.IsZero() || reference.Revision == 0 ||
		reference.SchemaMajor != SnapshotSchemaMajor || reference.SchemaMinor != SnapshotSchemaMinor ||
		!validDigest(reference.Digest) {
		return ErrVersion
	}
	return nil
}

func (reference EvaluatorReference) validate() error {
	if reference.Major != SnapshotSchemaMajor || reference.Minor != SnapshotSchemaMinor ||
		!validDigest(reference.Digest) {
		return ErrVersion
	}
	return nil
}

func validRequirementState(value RequirementState) bool {
	switch value {
	case RequirementSatisfied, RequirementNotSatisfied, RequirementInconclusive,
		RequirementUnavailable, RequirementProhibited:
		return true
	default:
		return false
	}
}

func validDirective(value Directive) bool {
	switch value {
	case DirectiveCompleteVerified, DirectiveCompleteNotVerified, DirectiveCompleteInconclusive,
		DirectiveRequestInput, DirectiveRunCheck, DirectiveRetryCheck, DirectiveUseFallback,
		DirectiveRouteManualReview, DirectiveFailWorkflow:
		return true
	default:
		return false
	}
}

func validActor(value ActorClass) bool { return value == ActorMachine || value == ActorHuman }

func validToken(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && tokenPattern.MatchString(value)
}

func validDigest(value string) bool { return digestPattern.MatchString(value) }

func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func cloneFact(value Fact) Fact {
	cloned := value
	cloned.ReasonCodes = slices.Clone(value.ReasonCodes)
	if value.ExpiresAt != nil {
		expiresAt := *value.ExpiresAt
		cloned.ExpiresAt = &expiresAt
	}
	cloned.Source = cloneSource(value.Source)
	return cloned
}

func cloneSource(value FactSource) FactSource {
	cloned := value
	if value.Identity != nil {
		source := *value.Identity
		cloned.Identity = &source
	}
	if value.Fraud != nil {
		source := *value.Fraud
		cloned.Fraud = &source
	}
	if value.Check != nil {
		check := *value.Check
		check.ObservationIDs = slices.Clone(value.Check.ObservationIDs)
		cloned.Check = &check
	}
	if value.Authority != nil {
		authority := *value.Authority
		cloned.Authority = &authority
	}
	if value.SubjectResponse != nil {
		response := *value.SubjectResponse
		cloned.SubjectResponse = &response
	}
	if value.ReviewFinding != nil {
		finding := *value.ReviewFinding
		cloned.ReviewFinding = &finding
	}
	return cloned
}

func cloneResult(value RequirementResult) RequirementResult {
	cloned := value
	cloned.ContributingFacts = slices.Clone(value.ContributingFacts)
	cloned.ReasonCodes = slices.Clone(value.ReasonCodes)
	return cloned
}
