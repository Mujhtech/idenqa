// Package policyv1 defines the public, engine-neutral Idenqa policy document.
package policyv1

import "errors"

const (
	// SchemaMajor is the supported public policy schema major version.
	SchemaMajor uint16 = 1
	// SchemaMinor is the supported public policy schema minor version.
	SchemaMinor uint16 = 0

	// MaximumDocumentBytes bounds one encoded public document.
	MaximumDocumentBytes = 128 * 1024
	// MaximumRules bounds independently compiled rules in one revision.
	MaximumRules = 128
	// MaximumExpressionBytes bounds one CEL source expression.
	MaximumExpressionBytes = 2048
	// MaximumFactsPerRule bounds explicit provenance for one result.
	MaximumFactsPerRule = 128
	// MaximumReasonsPerRule bounds stable reason codes for one result.
	MaximumReasonsPerRule = 16
)

var (
	// ErrInvalid means a document violates the closed public contract.
	ErrInvalid = errors.New("policy v1: invalid document")
	// ErrVersion means a document uses an unsupported schema version.
	ErrVersion = errors.New("policy v1: unsupported schema version")
	// ErrNonCanonical means bytes do not equal the canonical document encoding.
	ErrNonCanonical = errors.New("policy v1: non-canonical document")
)

// RequirementState is the closed public requirement-result vocabulary.
type RequirementState string

const (
	// RequirementSatisfied means the requirement conclusively passed.
	RequirementSatisfied RequirementState = "satisfied"
	// RequirementNotSatisfied means sufficient evidence conclusively failed.
	RequirementNotSatisfied RequirementState = "not_satisfied"
	// RequirementInconclusive means available evidence cannot decide.
	RequirementInconclusive RequirementState = "inconclusive"
	// RequirementUnavailable means an authorised source could not return a result.
	RequirementUnavailable RequirementState = "unavailable"
	// RequirementProhibited means authority or policy forbids the operation.
	RequirementProhibited RequirementState = "prohibited"
)

// Directive is the closed public workflow-intent vocabulary.
type Directive string

const (
	// DirectiveCompleteVerified authorises a terminal verified decision.
	DirectiveCompleteVerified Directive = "complete_verified"
	// DirectiveCompleteNotVerified authorises a terminal not-verified decision.
	DirectiveCompleteNotVerified Directive = "complete_not_verified"
	// DirectiveCompleteInconclusive authorises a terminal inconclusive decision.
	DirectiveCompleteInconclusive Directive = "complete_inconclusive"
	// DirectiveRequestInput requests additional subject input.
	DirectiveRequestInput Directive = "request_input"
	// DirectiveRunCheck requests a new check execution.
	DirectiveRunCheck Directive = "run_check"
	// DirectiveRetryCheck requests another attempt for an existing check.
	DirectiveRetryCheck Directive = "retry_check"
	// DirectiveUseFallback requests a policy-approved fallback.
	DirectiveUseFallback Directive = "use_fallback"
	// DirectiveRouteManualReview requests authorised human review.
	DirectiveRouteManualReview Directive = "route_manual_review"
	// DirectiveFailWorkflow reports prohibited or invalid workflow continuation.
	DirectiveFailWorkflow Directive = "fail_workflow"
)

// Document is one immutable public policy revision. Expressions remain strings
// in the public contract; no CEL implementation type crosses this boundary.
type Document struct {
	SchemaMajor       uint16 `json:"schema_major"`
	SchemaMinor       uint16 `json:"schema_minor"`
	PolicyID          string `json:"policy_id"`
	Revision          uint32 `json:"revision"`
	VerifiedAssurance string `json:"verified_assurance"`
	Rules             []Rule `json:"rules"`
}

// Rule emits its result only when its bounded boolean expression is true.
type Rule struct {
	Name   string `json:"name"`
	When   string `json:"when"`
	Result Result `json:"result"`
}

// Result is engine-neutral policy output with explicit provenance.
type Result struct {
	State             RequirementState `json:"state"`
	Directive         Directive        `json:"directive"`
	Priority          uint16           `json:"priority"`
	ContributingFacts []string         `json:"contributing_facts"`
	ReasonCodes       []string         `json:"reason_codes"`
}
