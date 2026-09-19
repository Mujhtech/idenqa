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
	DocumentRequirement string                         `json:"document_requirement,omitempty"`
	Requirement         string                         `json:"requirement"`
	Region              string                         `json:"region"`
	Purpose             string                         `json:"purpose"`
	Recipient           string                         `json:"recipient"`
	Configuration       modelv1.ConfigurationReference `json:"configuration"`
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
	check := "idenqa.check.passive_pad"
	if binding.DocumentRequirement != "" {
		if binding.DocumentRequirement == binding.Requirement {
			return nil, ErrRequestUnavailable
		}
		check = "idenqa.check.face_match_1to1"
	}
	for _, capability := range manifest.Capabilities {
		if capability.Evaluation != check {
			continue
		}
		if check == "idenqa.check.face_match_1to1" && (manifest.Restrictions.MaximumGrants != 2 || len(capability.RequiredAssurances) != 0 || !slices.Equal(capability.AcceptedEvidence, []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}) || !slices.Equal(capability.OutputSignals, []string{"idenqa.signal.face_match_1to1"})) {
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
			Name:            plan.Capability.Evaluation,
			MaximumDuration: plan.Manifest.Restrictions.MaximumDuration,
			RunnerKind:      verification.RunnerModel,
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

// CaptureRoute supplies the exact reference-only discovery filter.
func (plan *Plan) CaptureRoute() (string, string, string) {
	return plan.Binding.TenantID, plan.Binding.PolicyID, plan.Binding.ProfileDigest
}

// OutputDestination binds evidence redemption to the selected evaluation capability.
func (plan *Plan) OutputDestination() string {
	return "model." + strings.TrimPrefix(plan.Capability.Evaluation, "idenqa.check.")
}
