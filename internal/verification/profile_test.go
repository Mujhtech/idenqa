package verification

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
)

func TestNewProfile_AcquisitionStrategies(t *testing.T) {
	t.Parallel()
	registry := builtInRegistry(t)
	tests := []struct {
		name        string
		acquisition Acquisition
		assurances  []evidence.Name
		valid       bool
	}{
		{
			name:        "subject chooses upload or camera without live assurance",
			acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload, evidence.MethodLiveCamera}},
			valid:       true,
		},
		{
			name:        "any upload branch cannot satisfy live capture",
			acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload, evidence.MethodLiveCamera}},
			assurances:  []evidence.Name{evidence.AssuranceLiveCapture},
			valid:       false,
		},
		{
			name:        "camera satisfies live capture",
			acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera}},
			assurances:  []evidence.Name{evidence.AssuranceLiveCapture},
			valid:       true,
		},
		{
			name:        "all methods combine while every submission remains required",
			acquisition: Acquisition{Strategy: StrategyAllOf, Methods: []evidence.Name{evidence.MethodFileUpload, evidence.MethodLiveCamera}},
			assurances:  []evidence.Name{evidence.AssuranceLiveCapture},
			valid:       true,
		},
		{
			name:        "file upload cannot establish active liveness",
			acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
			assurances:  []evidence.Name{evidence.AssuranceActiveLiveness},
			valid:       false,
		},
		{
			name:        "still camera cannot establish active liveness",
			acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera}},
			assurances:  []evidence.Name{evidence.AssuranceActiveLiveness},
			valid:       false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requirement := selfieRequirement(test.acquisition, test.assurances)
			_, err := NewProfile(registry, []Requirement{requirement})
			if (err == nil) != test.valid {
				t.Fatalf("NewProfile() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestNewProfile_DocumentArtefactCombinations(t *testing.T) {
	t.Parallel()
	registry := builtInRegistry(t)
	requirement := Requirement{
		Key:          "government_id",
		Purpose:      evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceDocumentImage,
		Artefacts:    []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack},
		Acquisition:  Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
		Constraints: []Constraint{
			{Name: evidence.ConstraintAllowedCountries, Value: ConstraintValue{Kind: evidence.ValueStringList, StringList: []string{"NG", "GB"}}},
			{Name: evidence.ConstraintMaximumBytes, Value: ConstraintValue{Kind: evidence.ValueInteger, Integer: 8_000_000}},
		},
	}
	if _, err := NewProfile(registry, []Requirement{requirement}); err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}

	requirement.Artefacts = append(requirement.Artefacts, evidence.ArtefactSelfieImage)
	if _, err := NewProfile(registry, []Requirement{requirement}); err == nil {
		t.Fatal("NewProfile() accepted an artefact from another evidence type")
	}
}

func TestNewProfile_FallbacksCannotWeakenAssurance(t *testing.T) {
	t.Parallel()
	registry := builtInRegistry(t)
	requirement := selfieRequirement(
		Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera}},
		[]evidence.Name{evidence.AssuranceLiveCapture},
	)
	requirement.Fallbacks = []Fallback{{
		On:          []FallbackCondition{FallbackMethodUnavailable},
		Acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
	}}
	if _, err := NewProfile(registry, []Requirement{requirement}); err == nil {
		t.Fatal("NewProfile() accepted an assurance-weakening fallback")
	}

	requirement.RequiredAssurances = nil
	if _, err := NewProfile(registry, []Requirement{requirement}); err != nil {
		t.Fatalf("NewProfile() rejected policy-approved upload fallback: %v", err)
	}
}

func TestNewProfile_RejectsUnknownExtension(t *testing.T) {
	t.Parallel()
	registry := builtInRegistry(t)
	requirement := selfieRequirement(
		Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{"com.example.method.unregistered"}},
		nil,
	)
	if _, err := NewProfile(registry, []Requirement{requirement}); err == nil {
		t.Fatal("NewProfile() accepted an unknown extension")
	}
}

func TestNewProfile_AcceptsPinnedNamespacedExtension(t *testing.T) {
	t.Parallel()
	const (
		artefact  evidence.Name = "com.example.artefact.portrait"
		typeName  evidence.Name = "com.example.evidence.portrait"
		method    evidence.Name = "com.example.method.secure_camera"
		purpose   evidence.Name = "com.example.purpose.account_recovery"
		assurance evidence.Name = "com.example.assurance.secure_enclave_capture"
	)
	registry, err := evidence.NewRegistry(7, evidence.Definitions{
		Artefacts:  []evidence.Name{artefact},
		Evidence:   []evidence.Type{{Name: typeName, Artefacts: []evidence.Name{artefact}}},
		Methods:    []evidence.AcquisitionMethod{{Name: method, Supports: []evidence.MethodSupport{{EvidenceType: typeName, Artefacts: []evidence.Name{artefact}, Assurances: []evidence.Name{assurance}}}}},
		Purposes:   []evidence.Name{purpose},
		Assurances: []evidence.Name{assurance},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	profile, err := NewProfile(registry, []Requirement{{
		Key:                "recovery_portrait",
		Purpose:            purpose,
		EvidenceType:       typeName,
		Artefacts:          []evidence.Name{artefact},
		Acquisition:        Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{method}},
		RequiredAssurances: []evidence.Name{assurance},
	}})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	if profile.Registry != registry.Reference() {
		t.Fatalf("profile registry = %#v, want %#v", profile.Registry, registry.Reference())
	}
}

func TestCanonicalJSONAndDigest(t *testing.T) {
	t.Parallel()
	registry := builtInRegistry(t)
	requirement := Requirement{
		Key:                "government_id",
		Purpose:            evidence.PurposeIdentityVerification,
		EvidenceType:       evidence.EvidenceDocumentImage,
		Artefacts:          []evidence.Name{evidence.ArtefactDocumentBack, evidence.ArtefactDocumentFront},
		Acquisition:        Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
		RequiredAssurances: nil,
		Constraints: []Constraint{{
			Name:  evidence.ConstraintAllowedCountries,
			Value: ConstraintValue{Kind: evidence.ValueStringList, StringList: []string{"NG", "GB"}},
		}},
	}
	first, err := NewProfile(registry, []Requirement{requirement})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	requirement.Artefacts = slices.Clone(requirement.Artefacts)
	slices.Reverse(requirement.Artefacts)
	slices.Reverse(requirement.Constraints[0].Value.StringList)
	second, err := NewProfile(registry, []Requirement{requirement})
	if err != nil {
		t.Fatalf("NewProfile() reordered error = %v", err)
	}
	firstDigest, err := Digest(first, registry)
	if err != nil {
		t.Fatalf("Digest(first) error = %v", err)
	}
	secondDigest, err := Digest(second, registry)
	if err != nil {
		t.Fatalf("Digest(second) error = %v", err)
	}
	if firstDigest != secondDigest || !strings.HasPrefix(firstDigest, "sha256:") {
		t.Fatalf("digests = %q and %q", firstDigest, secondDigest)
	}
	canonical, err := CanonicalJSON(first, registry)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if strings.Contains(string(canonical), "kind") {
		t.Fatalf("canonical JSON leaked tagged-value implementation: %s", canonical)
	}
	if strings.Contains(string(canonical), "null") {
		t.Fatalf("canonical JSON contains a null collection: %s", canonical)
	}
	activationDigest, err := ValidateForActivation(first, registry)
	if err != nil {
		t.Fatalf("ValidateForActivation() error = %v", err)
	}
	if activationDigest != firstDigest {
		t.Fatalf("ValidateForActivation() = %q, want %q", activationDigest, firstDigest)
	}
}

func TestPortableSelfieUploadProfileDigestStable(t *testing.T) {
	t.Parallel()

	registry := builtInRegistry(t)
	profile, err := NewProfile(registry, []Requirement{selfieRequirement(
		Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
		nil,
	)})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	digest, err := Digest(profile, registry)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if want := "sha256:284a4418399974a7d1ce691f96ea534e1cd97ee1a9bc0d09de25d4a3b9d48969"; digest != want {
		t.Fatalf("portable selfie-upload profile digest = %q, want %q", digest, want)
	}
}

func TestParseProfileJSONRoundTripAndStrictness(t *testing.T) {
	t.Parallel()

	registry := builtInRegistry(t)
	original, err := NewProfile(registry, []Requirement{{
		Key:          "government_id",
		Purpose:      evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceDocumentImage,
		Artefacts:    []evidence.Name{evidence.ArtefactDocumentFront},
		Acquisition:  Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
		Constraints: []Constraint{
			{Name: evidence.ConstraintMaximumBytes, Value: ConstraintValue{Kind: evidence.ValueInteger, Integer: 8_000_000}},
			{Name: evidence.ConstraintAllowedMedia, Value: ConstraintValue{Kind: evidence.ValueStringList, StringList: []string{"image/jpeg"}}},
		},
	}})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	canonical, err := CanonicalJSON(original, registry)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	parsed, err := ParseProfileJSON(canonical, registry)
	if err != nil {
		t.Fatalf("ParseProfileJSON() error = %v", err)
	}
	parsedCanonical, err := CanonicalJSON(parsed, registry)
	if err != nil {
		t.Fatalf("CanonicalJSON(parsed) error = %v", err)
	}
	if string(parsedCanonical) != string(canonical) {
		t.Fatalf("round-trip canonical JSON = %s, want %s", parsedCanonical, canonical)
	}

	for _, encoded := range [][]byte{
		append(append([]byte(nil), canonical...), []byte(` {}`)...),
		[]byte(`{"schema_version":1,"registry":{"schema_version":1,"revision":1,"digest":"wrong"},"requirements":[],"unknown":true}`),
	} {
		if _, err := ParseProfileJSON(encoded, registry); err == nil {
			t.Fatalf("ParseProfileJSON(%s) error = nil", encoded)
		}
	}
}

func TestEligibleMethods(t *testing.T) {
	t.Parallel()
	registry := builtInRegistry(t)
	requirement := selfieRequirement(
		Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera, evidence.MethodFileUpload}},
		nil,
	)
	eligible, err := EligibleMethods(registry, requirement, evidence.Capabilities{
		Registry: registry.Reference(),
		Methods:  []evidence.Name{evidence.MethodFileUpload},
	})
	if err != nil {
		t.Fatalf("EligibleMethods() error = %v", err)
	}
	if !slices.Equal(eligible, []evidence.Name{evidence.MethodFileUpload}) {
		t.Fatalf("EligibleMethods() = %v", eligible)
	}

	requirement.Acquisition.Strategy = StrategyAllOf
	if _, err := EligibleMethods(registry, requirement, evidence.Capabilities{Registry: registry.Reference(), Methods: []evidence.Name{evidence.MethodFileUpload}}); err == nil {
		t.Fatal("EligibleMethods() accepted incomplete all_of capabilities")
	}
}

func FuzzNewProfile_UploadNeverEstablishesLiveAssurance(f *testing.F) {
	for _, assurance := range []string{string(evidence.AssuranceFreshness), string(evidence.AssuranceLiveCapture), string(evidence.AssuranceActiveLiveness)} {
		f.Add(assurance)
	}
	f.Fuzz(func(t *testing.T, assuranceValue string) {
		registry := builtInRegistry(t)
		assurance := evidence.Name(assuranceValue)
		if !registry.Has(evidence.KindAssurance, assurance) {
			t.Skip()
		}
		requirement := selfieRequirement(
			Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}},
			[]evidence.Name{assurance},
		)
		_, err := NewProfile(registry, []Requirement{requirement})
		if assurance == evidence.AssuranceFreshness || assurance == evidence.AssuranceLiveCapture || assurance == evidence.AssuranceActiveLiveness || assurance == evidence.AssurancePassiveLiveness || assurance == evidence.AssuranceCaptureIntegrity {
			if err == nil {
				t.Fatalf("file upload established %q", assurance)
			}
		}
	})
}

func builtInRegistry(t *testing.T) evidence.Registry {
	t.Helper()
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}

	return registry
}

func selfieRequirement(acquisition Acquisition, assurances []evidence.Name) Requirement {
	return Requirement{
		Key:                "selfie",
		Purpose:            evidence.PurposeIdentityVerification,
		EvidenceType:       evidence.EvidenceSelfieImage,
		Artefacts:          []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition:        acquisition,
		RequiredAssurances: assurances,
	}
}
