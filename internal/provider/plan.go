package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// Binding is an explicit deployment-approved tenant/policy/profile route.
// It contains references only; changing it never rewrites a prepared request.
type Binding struct {
	TenantID          string                            `json:"tenant_id"`
	PolicyID          string                            `json:"policy_id"`
	ProfileDigest     string                            `json:"profile_digest"`
	SelfieRequirement string                            `json:"selfie_requirement,omitempty"`
	Inputs            []providerv1.InputReference       `json:"inputs,omitempty"`
	Requirement       string                            `json:"requirement"`
	Region            string                            `json:"region"`
	Purpose           string                            `json:"purpose"`
	Recipient         string                            `json:"recipient"`
	Configuration     providerv1.ConfigurationReference `json:"configuration"`
}

// Plan binds exactly one reviewed document check to a tenant's immutable profile.
type Plan struct {
	Binding             Binding
	Manifest            providerv1.Manifest
	Capability          providerv1.Capability
	configurationDigest string
}

// NewPlan composes a bounded explicit document-analysis route.
func NewPlan(binding Binding, manifest providerv1.Manifest) (*Plan, error) {
	if _, err := id.ParseTenant(binding.TenantID); err != nil {
		return nil, ErrRequestUnavailable
	}
	if _, err := id.ParsePolicy(binding.PolicyID); err != nil {
		return nil, ErrRequestUnavailable
	}
	if len(binding.ProfileDigest) != 71 || !strings.HasPrefix(binding.ProfileDigest, "sha256:") || binding.Requirement == "" || binding.Region == "" || binding.Purpose == "" || binding.Recipient == "" || binding.Configuration.Validate() != nil || manifest.Validate() != nil || binding.Configuration.SchemaDigest != manifest.Configuration.Digest {
		return nil, ErrRequestUnavailable
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(binding.ProfileDigest, "sha256:")); err != nil {
		return nil, ErrRequestUnavailable
	}
	check := "idenqa.check.document_analysis"
	if manifest.Package.AdapterID == "smileid" {
		check = "idenqa.check.document_biometric"
		if binding.SelfieRequirement == "" || binding.SelfieRequirement == binding.Requirement || len(binding.Inputs) != 2 {
			return nil, ErrRequestUnavailable
		}
		names := map[string]bool{}
		for _, input := range binding.Inputs {
			if input.Name != "idenqa.input.country" && input.Name != "idenqa.input.id_type" {
				return nil, ErrRequestUnavailable
			}
			if names[input.Name] {
				return nil, ErrRequestUnavailable
			}
			names[input.Name] = true
		}
	} else if manifest.Package.AdapterID != "dojah" || len(binding.Inputs) != 0 || binding.SelfieRequirement != "" {
		return nil, ErrRequestUnavailable
	}
	for _, capability := range manifest.Capabilities {
		if capability.Check != check {
			continue
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

// ConfigurationDigest returns the pinned configuration digest for the exact
// binding. It is stable for the same deployment template and registration.
func (plan *Plan) ConfigurationDigest() string {
	if plan == nil {
		return ""
	}
	return plan.configurationDigest
}

// Plan returns no route for unrelated tenants, policies or immutable profiles.
func (plan *Plan) Plan(ctx context.Context, input verification.PlanInput) ([]verification.PlannedCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.TenantID.String() != plan.Binding.TenantID || input.PolicyID.String() != plan.Binding.PolicyID || input.ProfileDigest != plan.Binding.ProfileDigest {
		return nil, verification.ErrPlanUnavailable
	}
	return []verification.PlannedCheck{{Asynchronous: plan.Manifest.Package.AdapterID == "smileid", Name: plan.Capability.Check, MaximumDuration: plan.Manifest.Restrictions.MaximumDuration, RunnerKind: verification.RunnerProvider, Provenance: verification.Provenance{RunnerID: plan.Manifest.Package.AdapterID, RunnerVersion: plan.Manifest.Package.AdapterVersion, PackageDigest: strings.TrimPrefix(plan.Manifest.Package.PackageDigest, "sha256:"), ContractMajor: plan.Manifest.Package.Contract.Major, ContractMinor: plan.Manifest.Package.Contract.Minor, RequestDigest: plan.configurationDigest, Configuration: plan.configurationDigest}}}, nil
}

// CaptureRoute supplies the exact reference-only discovery filter.
func (plan *Plan) CaptureRoute() (string, string, string) {
	return plan.Binding.TenantID, plan.Binding.PolicyID, plan.Binding.ProfileDigest
}

// OutputSignals lists the owned normalization keys for the selected reference route.
// The provider v1 capability has no output-key catalogue; keep this in sync with
// the reviewed adapter mappings when adding a new route.
func (plan *Plan) OutputSignals() []string {
	if plan.Manifest.Package.AdapterID == "smileid" {
		return []string{"idenqa.signal.provider_job", "idenqa.signal.liveness", "idenqa.signal.face_match_1to1", "idenqa.signal.document_authenticity"}
	}
	return []string{"idenqa.signal.document_quality"}
}
