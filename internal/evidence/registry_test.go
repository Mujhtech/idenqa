package evidence

import (
	"slices"
	"strings"
	"testing"
)

func TestParseName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		kind  Kind
		value string
		valid bool
	}{
		{name: "reserved built-in", kind: KindEvidence, value: "idenqa.evidence.selfie_image", valid: true},
		{name: "owner-namespaced extension", kind: KindMethod, value: "com.example.method.document_scanner", valid: true},
		{name: "extension without owner", kind: KindMethod, value: "example.method.scanner", valid: false},
		{name: "reserved nested namespace", kind: KindMethod, value: "idenqa.partner.method.scanner", valid: false},
		{name: "wrong kind", kind: KindEvidence, value: "idenqa.method.live_camera", valid: false},
		{name: "uppercase segment", kind: KindPurpose, value: "com.Example.purpose.kyc", valid: false},
		{name: "empty segment", kind: KindAssurance, value: "com..assurance.provenance", valid: false},
		{name: "unknown kind", kind: Kind("unknown"), value: "idenqa.unknown.value", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseName(test.kind, test.value)
			if (err == nil) != test.valid {
				t.Fatalf("ParseName(%q, %q) error = %v, valid = %t", test.kind, test.value, err, test.valid)
			}
		})
	}
}

func TestBuiltInRegistryReferenceStable(t *testing.T) {
	t.Parallel()

	registry, err := BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	if got, want := registry.Reference().Digest, "sha256:71ef9df77044f9bf5eeb7ae448da3d98811ff4cb3f2541f459e60b4628a5364f"; got != want {
		t.Fatalf("built-in registry digest = %q, want %q", got, want)
	}
}

func TestPanAfricanRegistryAddsProviderAssuranceWithoutChangingRevisionOne(t *testing.T) {
	t.Parallel()
	registry, err := PanAfricanRegistry()
	if err != nil {
		t.Fatalf("PanAfricanRegistry() error = %v", err)
	}
	if registry.Reference().Revision != 2 {
		t.Fatalf("revision = %d", registry.Reference().Revision)
	}
	for _, assurance := range []Name{
		AssuranceFaceMatchOneToOne,
		AssuranceDocumentAuthenticity,
		AssuranceCaptureQuality,
		AssuranceMRZParsed,
		AssuranceBarcodeParsed,
	} {
		if !registry.Has(KindAssurance, assurance) {
			t.Fatalf("assurance %q is absent", assurance)
		}
	}
}

func TestNewRegistry(t *testing.T) {
	t.Parallel()
	registry, err := BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	reference := registry.Reference()
	if reference.SchemaVersion != RegistrySchemaVersion || reference.Revision != 1 || !strings.HasPrefix(reference.Digest, "sha256:") {
		t.Fatalf("Reference() = %#v", reference)
	}
	canonical, err := registry.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if strings.Contains(string(canonical), `"Name"`) || !strings.Contains(string(canonical), `"value_kind"`) {
		t.Fatalf("CanonicalJSON() = %s", canonical)
	}
	if strings.Contains(string(canonical), "null") {
		t.Fatalf("CanonicalJSON() contains a null collection: %s", canonical)
	}

	method, ok := registry.Method(MethodLiveCamera)
	if !ok {
		t.Fatal("Method(live camera) not found")
	}
	method.Supports[0].Assurances[0] = "com.example.assurance.changed"
	unchanged, ok := registry.Method(MethodLiveCamera)
	if !ok || unchanged.Supports[0].Assurances[0] == method.Supports[0].Assurances[0] {
		t.Fatal("Method returned mutable registry state")
	}
}

func TestNewRegistry_DigestIgnoresDefinitionSetOrder(t *testing.T) {
	t.Parallel()
	first, err := BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	definitions := builtInDefinitionsForTest()
	slices.Reverse(definitions.Artefacts)
	slices.Reverse(definitions.Evidence)
	slices.Reverse(definitions.Methods)
	slices.Reverse(definitions.Purposes)
	slices.Reverse(definitions.Assurances)
	slices.Reverse(definitions.Constraints)
	second, err := NewRegistry(1, definitions)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if first.Reference().Digest != second.Reference().Digest {
		t.Fatalf("digest changed with definition order: %q != %q", first.Reference().Digest, second.Reference().Digest)
	}
}

func TestNewRegistry_NamespacedExtension(t *testing.T) {
	t.Parallel()
	definitions := builtInDefinitionsForTest()
	const customMethod Name = "com.example.method.document_scanner"
	definitions.Methods = append(definitions.Methods, AcquisitionMethod{
		Name: customMethod,
		Supports: []MethodSupport{{
			EvidenceType: EvidenceDocumentImage,
			Artefacts:    []Name{ArtefactDocumentFront, ArtefactDocumentBack},
			Assurances:   []Name{AssuranceCaptureIntegrity},
		}},
	})
	registry, err := NewRegistry(2, definitions)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if !registry.Has(KindMethod, customMethod) {
		t.Fatal("custom method was not registered")
	}
}

func TestNewRegistry_RejectsUnknownAndReservedExtensions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Definitions)
	}{
		{
			name: "unknown assurance",
			mutate: func(definitions *Definitions) {
				definitions.Methods[0].Supports[0].Assurances = []Name{"com.example.assurance.unregistered"}
			},
		},
		{
			name: "reserved extension namespace",
			mutate: func(definitions *Definitions) {
				definitions.Assurances = append(definitions.Assurances, "idenqa.partner.assurance.untrusted")
			},
		},
		{
			name: "duplicate definition",
			mutate: func(definitions *Definitions) {
				definitions.Artefacts = append(definitions.Artefacts, ArtefactSelfieImage)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definitions := builtInDefinitionsForTest()
			test.mutate(&definitions)
			if _, err := NewRegistry(2, definitions); err == nil {
				t.Fatal("NewRegistry() error = nil")
			}
		})
	}
}

func TestRegistry_ValidateCapabilities(t *testing.T) {
	t.Parallel()
	registry, err := BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	tests := []struct {
		name         string
		capabilities Capabilities
		valid        bool
	}{
		{name: "known methods", capabilities: Capabilities{Registry: registry.Reference(), Methods: []Name{MethodLiveCamera, MethodFileUpload}}, valid: true},
		{name: "unknown method", capabilities: Capabilities{Registry: registry.Reference(), Methods: []Name{"com.example.method.unknown"}}, valid: false},
		{name: "duplicate method", capabilities: Capabilities{Registry: registry.Reference(), Methods: []Name{MethodLiveCamera, MethodLiveCamera}}, valid: false},
		{name: "wrong registry", capabilities: Capabilities{Registry: Reference{SchemaVersion: 1, Revision: 2, Digest: registry.Reference().Digest}, Methods: []Name{MethodLiveCamera}}, valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := registry.ValidateCapabilities(test.capabilities)
			if (err == nil) != test.valid {
				t.Fatalf("ValidateCapabilities() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func builtInDefinitionsForTest() Definitions {
	return Definitions{
		Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack, ArtefactSelfieImage},
		Evidence: []Type{
			{Name: EvidenceDocumentImage, Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack}},
			{Name: EvidenceSelfieImage, Artefacts: []Name{ArtefactSelfieImage}},
		},
		Methods: []AcquisitionMethod{
			{Name: MethodFileUpload, Supports: []MethodSupport{{EvidenceType: EvidenceDocumentImage, Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack}}, {EvidenceType: EvidenceSelfieImage, Artefacts: []Name{ArtefactSelfieImage}}}},
			{Name: MethodLiveCamera, Supports: []MethodSupport{{EvidenceType: EvidenceDocumentImage, Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack}, Assurances: []Name{AssuranceFreshness, AssuranceLiveCapture, AssuranceCaptureIntegrity}}, {EvidenceType: EvidenceSelfieImage, Artefacts: []Name{ArtefactSelfieImage}, Assurances: []Name{AssuranceFreshness, AssuranceLiveCapture, AssuranceCaptureIntegrity}}}},
		},
		Purposes:   []Name{PurposeIdentityVerification},
		Assurances: []Name{AssuranceFreshness, AssuranceLiveCapture, AssuranceCaptureIntegrity, AssurancePassiveLiveness, AssuranceActiveLiveness},
		Constraints: []ConstraintDefinition{
			{Name: ConstraintAllowedCountries, ValueKind: ValueStringList, AppliesTo: []Name{EvidenceDocumentImage}},
			{Name: ConstraintAllowedMedia, ValueKind: ValueStringList},
			{Name: ConstraintMaximumBytes, ValueKind: ValueInteger},
		},
	}
}
