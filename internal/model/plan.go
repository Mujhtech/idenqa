package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// Binding is an explicit deployment-approved tenant/policy/profile route.
// It contains references only; changing it never rewrites a prepared request.
type Binding struct {
	Registry            *RegistrySelection             `json:"registry,omitempty"`
	TenantID            string                         `json:"tenant_id"`
	PolicyID            string                         `json:"policy_id"`
	ProfileDigest       string                         `json:"profile_digest"`
	Evaluation          string                         `json:"evaluation,omitempty"`
	DocumentRequirement string                         `json:"document_requirement,omitempty"`
	Requirement         string                         `json:"requirement"`
	Region              string                         `json:"region"`
	Purpose             string                         `json:"purpose"`
	Recipient           string                         `json:"recipient"`
	Configuration       modelv1.ConfigurationReference `json:"configuration"`
	Execution           ExecutionBinding               `json:"execution,omitempty"`
}

// ExecutionBinding gives one mounted model a stable check identity and
// explicit graph position. CheckName is optional only for the legacy
// independent route, where the capability evaluation remains the check name.
type ExecutionBinding struct {
	CheckName        string   `json:"check_name,omitempty"`
	Priority         uint16   `json:"priority,omitempty"`
	DependsOn        []string `json:"depends_on,omitempty"`
	FallbackFor      string   `json:"fallback_for,omitempty"`
	CorrelationGroup string   `json:"correlation_group,omitempty"`
}

// Plan binds exactly one bounded model evaluation to a tenant's immutable profile.
type Plan struct {
	Binding             Binding
	Manifest            modelv1.Manifest
	Capability          modelv1.Capability
	configurationDigest string
}

// NewPlan composes a bounded PAD or document/selfie matching route.
func NewPlan(binding Binding, manifest modelv1.Manifest) (*Plan, error) {
	if binding.Registry != nil && binding.Registry.Validate() != nil {
		return nil, ErrRequestUnavailable
	}
	if _, err := id.ParseTenant(binding.TenantID); err != nil {
		return nil, ErrRequestUnavailable
	}
	if _, err := id.ParsePolicy(binding.PolicyID); err != nil {
		return nil, ErrRequestUnavailable
	}
	if len(binding.ProfileDigest) != 71 || !strings.HasPrefix(binding.ProfileDigest, "sha256:") || binding.Requirement == "" || binding.Region == "" || binding.Purpose == "" || binding.Recipient == "" || binding.Configuration.Validate() != nil || manifest.Validate() != nil {
		return nil, ErrRequestUnavailable
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(binding.ProfileDigest, "sha256:")); err != nil {
		return nil, ErrRequestUnavailable
	}
	if !validExecutionBinding(binding.Execution) {
		return nil, ErrRequestUnavailable
	}
	evaluation := binding.Evaluation
	if evaluation == "" {
		evaluation = modelv1.EvaluationPassivePAD
		if binding.DocumentRequirement != "" {
			evaluation = modelv1.EvaluationFaceMatch
		}
	}
	for _, capability := range manifest.Capabilities {
		if capability.Evaluation != evaluation {
			continue
		}
		if !validEvidenceBinding(binding, capability, manifest.Restrictions) {
			return nil, ErrRequestUnavailable
		}
		encoded, err := json.Marshal(binding)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(encoded)
		return &Plan{binding, manifest, capability, hex.EncodeToString(digest[:])}, nil
	}
	return nil, ErrRequestUnavailable
}

func validEvidenceBinding(binding Binding, capability modelv1.Capability, restrictions modelv1.Restrictions) bool {
	if len(capability.RequiredAssurances) != 0 {
		return false
	}
	switch {
	case slices.Equal(capability.AcceptedEvidence, []string{"idenqa.evidence.selfie_image"}):
		if binding.DocumentRequirement != "" {
			return false
		}
		if capability.TemporalEvidence {
			return restrictions.MaximumGrants >= 2
		}
		return restrictions.MaximumGrants == 1
	case slices.Equal(capability.AcceptedEvidence, []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}):
		return !capability.TemporalEvidence && binding.DocumentRequirement != "" &&
			binding.DocumentRequirement != binding.Requirement && restrictions.MaximumGrants == 2
	default:
		return false
	}
}

// Plan returns no route for unrelated tenants, policies or immutable profiles.
func (plan *Plan) Plan(ctx context.Context, input verification.PlanInput) ([]verification.PlannedCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.TenantID.String() != plan.Binding.TenantID || input.PolicyID.String() != plan.Binding.PolicyID || input.ProfileDigest != plan.Binding.ProfileDigest {
		return nil, verification.ErrPlanUnavailable
	}
	return []verification.PlannedCheck{
		{
			Name:            plan.CheckName(),
			MaximumDuration: plan.Manifest.Restrictions.MaximumDuration,
			RunnerKind:      verification.RunnerModel,
			Route: verification.CheckRoute{
				Priority:         plan.Binding.Execution.Priority,
				DependsOn:        slices.Clone(plan.Binding.Execution.DependsOn),
				FallbackFor:      plan.Binding.Execution.FallbackFor,
				CorrelationGroup: plan.Binding.Execution.CorrelationGroup,
			},
			Provenance: verification.Provenance{
				RunnerID:      "onnx." + strings.TrimPrefix(plan.Capability.Evaluation, "idenqa.check."),
				RunnerVersion: plan.Manifest.Provenance.ModelVersion,
				PackageDigest: strings.TrimPrefix(plan.Manifest.Provenance.ModelDigest, "sha256:"),
				ContractMajor: plan.Manifest.Provenance.Contract.Major,
				ContractMinor: plan.Manifest.Provenance.Contract.Minor,
				RequestDigest: plan.configurationDigest,
				Configuration: strings.TrimPrefix(plan.Binding.Configuration.ConfigurationDigest, "sha256:"),
			},
		},
	}, nil
}

// CheckName is the durable workflow identity. The model evaluation remains the
// capability name in the persisted model request.
func (plan *Plan) CheckName() string {
	if plan.Binding.Execution.CheckName != "" {
		return plan.Binding.Execution.CheckName
	}
	return plan.Capability.Evaluation
}

func validExecutionBinding(binding ExecutionBinding) bool {
	valid := func(value string) bool {
		if value == "" {
			return true
		}
		if len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
			return false
		}
		for _, character := range value {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '.' && character != '_' && character != ':' && character != '-' {
				return false
			}
		}
		return true
	}
	if !valid(binding.CheckName) || !valid(binding.FallbackFor) || !valid(binding.CorrelationGroup) || len(binding.DependsOn) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, dependency := range binding.DependsOn {
		if !valid(dependency) || dependency == "" || dependency == binding.CheckName || seen[dependency] {
			return false
		}
		seen[dependency] = true
	}
	return binding.FallbackFor == "" || (binding.FallbackFor != binding.CheckName && !seen[binding.FallbackFor])
}

// CaptureRoute supplies the exact reference-only discovery filter.
func (plan *Plan) CaptureRoute() (string, string, string) {
	return plan.Binding.TenantID, plan.Binding.PolicyID, plan.Binding.ProfileDigest
}

// OutputDestination binds evidence redemption to the selected evaluation capability.
func (plan *Plan) OutputDestination() string {
	return "model." + strings.TrimPrefix(plan.Capability.Evaluation, "idenqa.check.")
}
