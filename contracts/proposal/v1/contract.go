// Package proposal defines the public, transport-independent proposal
// contract. Signal execution (contracts/model/v1) and proposal execution are
// separate contracts with different authority. Raw evidence, biometric
// templates, national identifier values, credentials, and provider payloads
// are never in the envelope — only validated references.
//
//nolint:revive // public ProposalStatus and ProposalRequest stutter is intentional for contract clarity; exported consts are self-documenting
package proposal

import (
	"context"
	"encoding/json"
	"time"
)

// Version bounds for the v1 proposal contract.
const (
	MajorVersion          uint16 = 1
	MinorVersion          uint16 = 0
	MaxProposalBytes             = 64 * 1024
	MaxActions                   = 8
	MaxArgBytes                  = 8192
	MaxEvidenceRefs              = 32
	MaxSignalRefs                = 32
	MaxReasonBytes               = 512
	MaxContextDigestBytes        = 64
)

// Version identifies one compatible proposal contract revision.
type Version struct {
	Major uint16 `json:"major"`
	Minor uint16 `json:"minor"`
}

// CurrentVersion is the contract implemented by this package.
var CurrentVersion = Version{Major: MajorVersion, Minor: MinorVersion}

// Accepts reports whether this implementation can consume requested.
func (version Version) Accepts(requested Version) bool {
	return version.Major != 0 && version.Major == requested.Major && version.Minor >= requested.Minor
}

// AutomationMode controls whether the model may produce proposals and whether
// they may auto-execute. Zero value is invalid (Unknown).
type AutomationMode int

const (
	// AutomationModeUnknown is the zero value and is invalid.
	AutomationModeUnknown AutomationMode = iota
	AutomationModeDisabled
	AutomationModeAssist
	AutomationModeRecommend
	AutomationModeGuardrailedAuto
	AutomationModeHumanRequired
)

// String returns the canonical wire representation.
func (mode AutomationMode) String() string {
	switch mode {
	case AutomationModeDisabled:
		return "disabled"
	case AutomationModeAssist:
		return "assist"
	case AutomationModeRecommend:
		return "recommend"
	case AutomationModeGuardrailedAuto:
		return "guardrailed_auto"
	case AutomationModeHumanRequired:
		return "human_required"
	default:
		return "unknown"
	}
}

// ParseAutomationMode parses the wire string.
func ParseAutomationMode(value string) (AutomationMode, bool) {
	switch value {
	case "disabled":
		return AutomationModeDisabled, true
	case "assist":
		return AutomationModeAssist, true
	case "recommend":
		return AutomationModeRecommend, true
	case "guardrailed_auto":
		return AutomationModeGuardrailedAuto, true
	case "human_required":
		return AutomationModeHumanRequired, true
	default:
		return AutomationModeUnknown, false
	}
}

// ProposalStatus is the immutable lifecycle state.
type ProposalStatus int

const (
	// ProposalStatusUnknown is the zero value and is invalid.
	ProposalStatusUnknown ProposalStatus = iota
	ProposalStatusPending
	ProposalStatusApproved
	ProposalStatusRejected
	ProposalStatusExpired
	ProposalStatusCancelled
	ProposalStatusSuperseded
)

func (status ProposalStatus) String() string {
	switch status {
	case ProposalStatusPending:
		return "pending"
	case ProposalStatusApproved:
		return "approved"
	case ProposalStatusRejected:
		return "rejected"
	case ProposalStatusExpired:
		return "expired"
	case ProposalStatusCancelled:
		return "cancelled"
	case ProposalStatusSuperseded:
		return "superseded"
	default:
		return "unknown"
	}
}

// ParseProposalStatus parses the wire string.
func ParseProposalStatus(value string) (ProposalStatus, bool) {
	switch value {
	case "pending":
		return ProposalStatusPending, true
	case "approved":
		return ProposalStatusApproved, true
	case "rejected":
		return ProposalStatusRejected, true
	case "expired":
		return ProposalStatusExpired, true
	case "cancelled":
		return ProposalStatusCancelled, true
	case "superseded":
		return ProposalStatusSuperseded, true
	default:
		return ProposalStatusUnknown, false
	}
}

// ActionKind is the closed allow-list of proposal actions.
// Adding a kind requires a contract minor version bump and explicit
// guardrail approval; unknown kinds fail closed.
type ActionKind string

const (
	ActionReviewCopilotSummarize         ActionKind = "review.copilot.summarize"
	ActionRoutingAdaptiveSuggest         ActionKind = "routing.adaptive.suggest"
	ActionPolicyDraftGenerate            ActionKind = "policy.draft.generate"
	ActionPolicyDiffGenerate             ActionKind = "policy.diff.generate"
	ActionPolicyAdversarialGenerate      ActionKind = "policy.adversarial.generate"
	ActionExperienceAccessibilityPropose ActionKind = "experience.accessibility.propose"
	ActionExperienceExceptionPropose     ActionKind = "experience.exception.propose"
	ActionDocumentLayoutPropose          ActionKind = "document.layout.propose"
)

var allowedKinds = map[ActionKind]struct{}{
	ActionReviewCopilotSummarize:         {},
	ActionRoutingAdaptiveSuggest:         {},
	ActionPolicyDraftGenerate:            {},
	ActionPolicyDiffGenerate:             {},
	ActionPolicyAdversarialGenerate:      {},
	ActionExperienceAccessibilityPropose: {},
	ActionExperienceExceptionPropose:     {},
	ActionDocumentLayoutPropose:          {},
}

// IsAllowedKind reports whether kind is in the closed allow-list.
func IsAllowedKind(kind ActionKind) bool {
	_, ok := allowedKinds[kind]
	return ok
}

// AllowedKinds returns the sorted allow-list (defensive copy).
func AllowedKinds() []ActionKind {
	kinds := make([]ActionKind, 0, len(allowedKinds))
	for kind := range allowedKinds {
		kinds = append(kinds, kind)
	}
	// stable order for canonical encoding
	for i := 0; i < len(kinds); i++ {
		for j := i + 1; j < len(kinds); j++ {
			if kinds[j] < kinds[i] {
				kinds[i], kinds[j] = kinds[j], kinds[i]
			}
		}
	}
	return kinds
}

// BoundedAction is one allow-listed action with closed argument schema.
type BoundedAction struct {
	Kind ActionKind      `json:"kind"`
	Args json.RawMessage `json:"args"`
}

// AgentProposal is the immutable, non-authoritative proposal envelope.
// It is the only non-deterministic output that may enter the deterministic
// pipeline; every other field is deterministic and versioned.
type AgentProposal struct {
	ProposalID     string          `json:"proposal_id"`
	TenantID       string          `json:"tenant_id"`
	VerificationID string          `json:"verification_id,omitempty"`
	PolicyID       string          `json:"policy_id,omitempty"`
	Mode           string          `json:"mode"`
	Status         string          `json:"status"`
	Actions        []BoundedAction `json:"actions"`
	EvidenceRefs   []string        `json:"evidence_refs,omitempty"`
	SignalRefs     []string        `json:"signal_refs,omitempty"`
	ModelID        string          `json:"model_id"`
	ModelVersion   string          `json:"model_version"`
	PromptVersion  string          `json:"prompt_version"`
	ContextDigest  string          `json:"context_digest"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Supersedes     *string         `json:"supersedes,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	Reason         string          `json:"reason,omitempty"`
}

// ProposalRequest is the creation-time redacted bounded context. Raw evidence
// or PII values are never carried; callers supply only validated references.
// Exactly one of VerificationID or PolicyID anchors the proposal.
type ProposalRequest struct {
	TenantID       string
	VerificationID string
	PolicyID       string
	Mode           AutomationMode
	Actions        []BoundedAction
	EvidenceRefs   []string
	SignalRefs     []string
	ModelID        string
	ModelVersion   string
	PromptVersion  string
	ContextDigest  string
	ExpiresAt      time.Time
	Supersedes     *string
}

// AcceptedCommand is the deterministic, versioned command derived from an
// approved proposal. Replay re-executes the stored command without
// re-querying the model.
type AcceptedCommand struct {
	CommandID      string          `json:"command_id"`
	ProposalID     string          `json:"proposal_id"`
	TenantID       string          `json:"tenant_id"`
	VerificationID string          `json:"verification_id,omitempty"`
	PolicyID       string          `json:"policy_id,omitempty"`
	Kind           ActionKind      `json:"kind"`
	Args           json.RawMessage `json:"args"`
	ModelID        string          `json:"model_id"`
	ModelVersion   string          `json:"model_version"`
	PromptVersion  string          `json:"prompt_version"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ContextDigest helpers.
// ProposalContext is the redacted bounded context that is hashed to produce
// ContextDigest. It contains only validated references, never raw bytes.
type ProposalContext struct {
	TenantID       string   `json:"tenant_id"`
	VerificationID string   `json:"verification_id"`
	SignalRefs     []string `json:"signal_refs,omitempty"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
	Mode           string   `json:"mode"`
	Actions        []string `json:"actions"`
}

// ProposalModel is the non-deterministic port. It never executes tools or
// mutates state — it only returns a bounded proposal for guardrail review.
type ProposalModel interface {
	Propose(ctx context.Context, request ProposalRequest) (AgentProposal, error)
}

// CommandExecutor is the deterministic port that executes an already
// guardrail-approved AcceptedCommand. It must be replay-safe.
type CommandExecutor interface {
	Execute(ctx context.Context, command AcceptedCommand) error
}

// HealthState reports proposal-model readiness without exposing internal detail.
type HealthState string

const (
	HealthReady    HealthState = "ready"
	HealthDegraded HealthState = "degraded"
	HealthNotReady HealthState = "not_ready"
)

type Health struct {
	State     HealthState `json:"state"`
	Code      string      `json:"code"`
	CheckedAt time.Time   `json:"checked_at"`
}

// HealthChecker reports readiness.
type HealthChecker interface {
	Health(ctx context.Context) (Health, error)
}
