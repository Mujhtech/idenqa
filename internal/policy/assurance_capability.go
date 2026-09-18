package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// AssuranceCapability describes platform-owned meaning, independently of runner
// advertisements. An exact successful observation is still required.
type AssuranceCapability struct {
	Name                      string   `json:"name"`
	Dimensions                []string `json:"dimensions"`
	Signals                   []string `json:"signals"`
	SignalPrefix              string   `json:"signal_prefix,omitempty"`
	RunnerKinds               []string `json:"runner_kinds"`
	RequiredCaptureAssurances []string `json:"required_capture_assurances,omitempty"`
}

// AssuranceCapabilities returns the version-one semantic catalog. It deliberately
// has no ranking, jurisdiction certification, or production biometric thresholds.
func AssuranceCapabilities() []AssuranceCapability {
	live := []string{"idenqa.assurance.freshness", "idenqa.assurance.live_capture"}
	return []AssuranceCapability{
		{Name: "idenqa.assurance.authority_record", Dimensions: []string{"identity_resolution", "evidence_validation", "source_independence"}, Signals: []string{"idenqa.signal.authority_record"}, RunnerKinds: []string{"provider"}},
		{Name: "idenqa.assurance.document_authenticity", Dimensions: []string{"evidence_validation", "source_independence"}, Signals: []string{"idenqa.signal.document_authenticity"}, RunnerKinds: []string{"provider", "model"}},
		{Name: "idenqa.assurance.capture_quality", Dimensions: []string{"evidence_validation"}, Signals: []string{"idenqa.signal.document_quality"}, RunnerKinds: []string{"provider", "model"}},
		{Name: "idenqa.assurance.face_match_1to1", Dimensions: []string{"applicant_binding"}, Signals: []string{"idenqa.signal.face_match_1to1"}, RunnerKinds: []string{"provider", "model"}, RequiredCaptureAssurances: slices.Clone(live)},
		{Name: "idenqa.assurance.passive_liveness", Dimensions: []string{"liveness"}, Signals: []string{"idenqa.signal.passive_liveness", "idenqa.signal.passive_pad"}, RunnerKinds: []string{"provider", "model"}, RequiredCaptureAssurances: slices.Clone(live)},
		{Name: "idenqa.assurance.active_liveness", Dimensions: []string{"liveness"}, Signals: []string{"idenqa.signal.liveness", "idenqa.signal.active_liveness"}, RunnerKinds: []string{"provider", "model"}, RequiredCaptureAssurances: append(slices.Clone(live), "idenqa.assurance.active_liveness")},
		{Name: "idenqa.assurance.freshness", Dimensions: []string{"freshness"}, Signals: []string{"capture.freshness"}, RunnerKinds: []string{"capture"}, RequiredCaptureAssurances: slices.Clone(live)},
		{Name: "idenqa.assurance.live_capture", Dimensions: []string{"provenance"}, Signals: []string{"capture.live_capture"}, RunnerKinds: []string{"capture"}, RequiredCaptureAssurances: slices.Clone(live)},
		{Name: "idenqa.assurance.capture_integrity", Dimensions: []string{"integrity"}, Signals: []string{"capture.capture_integrity"}, RunnerKinds: []string{"capture"}, RequiredCaptureAssurances: []string{"idenqa.assurance.capture_integrity"}},
		{Name: "idenqa.assurance.human_review", Dimensions: []string{"human_oversight"}, Signals: []string{"review.resolution"}, RunnerKinds: []string{"review"}},
		{Name: "idenqa.assurance.identity_corroboration", Dimensions: []string{"identity_resolution", "evidence_validation"}, Signals: []string{}, SignalPrefix: "identity.", RunnerKinds: []string{"identity"}},
		{Name: "idenqa.assurance.fraud_controls", Dimensions: []string{"fraud_resistance"}, Signals: []string{}, SignalPrefix: "fraud.", RunnerKinds: []string{"fraud"}},
	}
}
func assuranceCapability(name string) (AssuranceCapability, bool) {
	for _, c := range AssuranceCapabilities() {
		if c.Name == name {
			return c, true
		}
	}
	return AssuranceCapability{}, false
}
func capabilityMappingValid(m AssuranceMapping) bool {
	c, ok := assuranceCapability(m.Capability)
	return ok && slices.Contains(c.RunnerKinds, m.RunnerKind) && (slices.Contains(c.Signals, m.Signal) || (c.SignalPrefix != "" && strings.HasPrefix(m.Signal, c.SignalPrefix)))
}
func sourceCapabilityEligible(s AssuranceSource, m AssuranceMapping) bool {
	c, ok := assuranceCapability(m.Capability)
	if !ok {
		return false
	}
	for _, a := range c.RequiredCaptureAssurances {
		if !slices.Contains(s.CaptureAssurances, a) {
			return false
		}
	}
	return true
}

// BuiltinAssuranceDigest pins the owned derivation contract for core capabilities.
// It is not a provider, model, or capture assurance declaration.
func BuiltinAssuranceDigest(kind string) string {
	h := sha256.Sum256([]byte("idenqa.assurance." + kind + ".v1"))
	return hex.EncodeToString(h[:])
}
