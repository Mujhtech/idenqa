package proposal

import (
	"context"
	"encoding/json"
	"strings"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
)

// EvidenceChecker verifies that a reference exists and is accessible for the session.
type EvidenceChecker interface {
	Exists(ctx context.Context, tenantID string, verificationID string, reference string) (bool, error)
}

// RegionValidator checks jurisdiction and residency. It resolves the session's
// pinned region internally and never infers a region from client input.
type RegionValidator interface {
	Allowed(ctx context.Context, tenantID string, verificationID string) (bool, error)
}

// AuthorityChecker verifies that a current, active processing authority
// permits the session.
type AuthorityChecker interface {
	Allowed(ctx context.Context, tenantID string, verificationID string) (bool, error)
}

// GuardrailInput is the deterministic context for one validation.
type GuardrailInput struct {
	Proposal      Proposal
	ModeConfig    ModeConfig
	Evidence      EvidenceChecker
	Authority     AuthorityChecker
	Region        RegionValidator
	CostLimiter   CostLimiter
	HumanApproved bool
}

// CostLimiter enforces per-tenant/verification rate and cost caps.
type CostLimiter interface {
	Allow(ctx context.Context, tenantID string, verificationID string, kind proposalv1.ActionKind) (bool, error)
}

// GuardrailResult is the deterministic outcome.
type GuardrailResult struct {
	Allowed bool
	Reason  string
}

// highRiskKinds require human approval even in guardrailed_auto.
var highRiskKinds = map[proposalv1.ActionKind]struct{}{
	proposalv1.ActionPolicyDraftGenerate:       {},
	proposalv1.ActionPolicyAdversarialGenerate: {},
}

// modeAllows checks whether mode permits kind.
func modeAllows(mode proposalv1.AutomationMode, kind proposalv1.ActionKind) bool {
	switch mode {
	case proposalv1.AutomationModeDisabled:
		return false
	case proposalv1.AutomationModeAssist:
		// assist: only review copilot summarization and document layout are allowed without approval
		return kind == proposalv1.ActionReviewCopilotSummarize || kind == proposalv1.ActionDocumentLayoutPropose
	case proposalv1.AutomationModeRecommend:
		// recommend: all kinds allowed but require human approval downstream
		return true
	case proposalv1.AutomationModeGuardrailedAuto:
		// guardrailed_auto: low-risk kinds may auto-approve; high-risk require human
		if _, ok := highRiskKinds[kind]; ok {
			return false
		}
		return kind == proposalv1.ActionReviewCopilotSummarize ||
			kind == proposalv1.ActionRoutingAdaptiveSuggest ||
			kind == proposalv1.ActionExperienceAccessibilityPropose ||
			kind == proposalv1.ActionExperienceExceptionPropose
	case proposalv1.AutomationModeHumanRequired:
		return true
	default:
		return false
	}
}

// ValidateGuardrails runs the ten deterministic guardrail checks. It never contacts a model.
func ValidateGuardrails(ctx context.Context, input GuardrailInput) (GuardrailResult, error) {
	proposal := input.Proposal
	// 1. Output schema validation: per-kind closed schema (minimal — ensure args is object)
	for _, action := range proposal.Actions {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(action.Args, &args); err != nil {
			return GuardrailResult{Allowed: false, Reason: "invalid args json"}, nil //nolint:nilerr // malformed args is a deterministic denial, not a system error
		}
		// reject raw evidence keys
		for key := range args {
			lower := strings.ToLower(key)
			if lower == "raw_evidence" || lower == "image_bytes" || lower == "biometric_template" || lower == "national_id_value" {
				return GuardrailResult{Allowed: false, Reason: "raw evidence in args"}, nil
			}
		}
	}
	// 2. Evidence/signal reference validation
	if input.Evidence != nil {
		for _, ref := range proposal.EvidenceRefs {
			exists, err := input.Evidence.Exists(ctx, proposal.TenantID.String(), proposal.VerificationID.String(), ref)
			if err != nil {
				return GuardrailResult{}, err
			}
			if !exists {
				return GuardrailResult{Allowed: false, Reason: "unknown evidence_ref"}, nil
			}
		}
	}
	// 3. Tool and action allow-list: already validated in contract, but recheck
	for _, action := range proposal.Actions {
		if !proposalv1.IsAllowedKind(action.Kind) {
			return GuardrailResult{Allowed: false, Reason: "unknown kind"}, nil
		}
	}
	// 4. Tenant-policy validation (mode + allow_list_version)
	for _, action := range proposal.Actions {
		if !modeAllows(proposal.Mode, action.Kind) {
			return GuardrailResult{Allowed: false, Reason: "mode does not allow kind"}, nil
		}
		// check mode config allow-list version permits kind
		if input.ModeConfig.AllowListVersion != "" {
			allowed := false
			for _, allowedKind := range input.ModeConfig.AllowedKinds {
				if allowedKind == action.Kind {
					allowed = true
					break
				}
			}
			// if config specifies allow-list, it must include kind
			if len(input.ModeConfig.AllowedKinds) > 0 && !allowed {
				return GuardrailResult{Allowed: false, Reason: "kind not in mode allow-list"}, nil
			}
		}
	}
	// 5. Jurisdiction and residency validation — session-scoped only; policy-scoped
	// proposals are governed by mode allow-lists and human approval instead.
	if proposal.PolicyID == nil {
		if input.Authority != nil {
			allowed, err := input.Authority.Allowed(ctx, proposal.TenantID.String(), proposal.VerificationID.String())
			if err != nil {
				return GuardrailResult{}, err
			}
			if !allowed {
				return GuardrailResult{Allowed: false, Reason: "processing authority not permitted"}, nil
			}
		}
		if input.Region != nil {
			allowed, err := input.Region.Allowed(ctx, proposal.TenantID.String(), proposal.VerificationID.String())
			if err != nil {
				return GuardrailResult{}, err
			}
			if !allowed {
				return GuardrailResult{Allowed: false, Reason: "region not allowed"}, nil
			}
		}
	}
	// 6. Cost and rate-limit validation
	if input.CostLimiter != nil {
		for _, action := range proposal.Actions {
			allowed, err := input.CostLimiter.Allow(ctx, proposal.TenantID.String(), proposal.VerificationID.String(), action.Kind)
			if err != nil {
				return GuardrailResult{}, err
			}
			if !allowed {
				return GuardrailResult{Allowed: false, Reason: "rate limited"}, nil
			}
		}
	}
	// 7. Raw-evidence and sensitive-context restriction: already checked in (1), plus context digest must not imply raw bytes
	if strings.Contains(proposal.ContextDigest, "raw") {
		return GuardrailResult{Allowed: false, Reason: "sensitive context"}, nil
	}
	// 8. Human approval when required
	if proposal.Mode == proposalv1.AutomationModeHumanRequired {
		if !input.HumanApproved {
			return GuardrailResult{Allowed: false, Reason: "human approval required"}, nil
		}
	}
	for _, action := range proposal.Actions {
		if _, ok := highRiskKinds[action.Kind]; ok && !input.HumanApproved {
			return GuardrailResult{Allowed: false, Reason: "high-risk requires human approval"}, nil
		}
	}
	// 9 and 10 are execution and audit linkage — not part of Validate, handled by Service.
	return GuardrailResult{Allowed: true}, nil
}
