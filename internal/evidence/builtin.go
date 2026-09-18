package evidence

const (
	// ArtefactDocumentFront is the image of a document's front side.
	ArtefactDocumentFront Name = "idenqa.artefact.document_front"
	// ArtefactDocumentBack is the image of a document's back side.
	ArtefactDocumentBack Name = "idenqa.artefact.document_back"
	// ArtefactSelfieImage is the portrait component of selfie evidence.
	ArtefactSelfieImage Name = "idenqa.artefact.selfie_image"

	// EvidenceDocumentImage is image evidence for a physical document.
	EvidenceDocumentImage Name = "idenqa.evidence.document_image"
	// EvidenceSelfieImage is a subject selfie image.
	EvidenceSelfieImage Name = "idenqa.evidence.selfie_image"

	// MethodFileUpload obtains an existing file selected by the subject.
	MethodFileUpload Name = "idenqa.method.file_upload"
	// MethodLiveCamera obtains an image directly from a camera session.
	MethodLiveCamera Name = "idenqa.method.live_camera"

	// PurposeIdentityVerification permits identity-verification collection.
	PurposeIdentityVerification Name = "idenqa.purpose.identity_verification"

	// AssuranceFreshness means evidence was acquired in the active session.
	AssuranceFreshness Name = "idenqa.assurance.freshness"
	// AssuranceLiveCapture means an active capture device produced evidence.
	AssuranceLiveCapture Name = "idenqa.assurance.live_capture"
	// AssuranceCaptureIntegrity means the acquisition path protected integrity.
	AssuranceCaptureIntegrity Name = "idenqa.assurance.capture_integrity"
	// AssurancePassiveLiveness means passive presentation-attack checks passed.
	AssurancePassiveLiveness Name = "idenqa.assurance.passive_liveness"
	// AssuranceActiveLiveness means an active liveness challenge passed.
	AssuranceActiveLiveness Name = "idenqa.assurance.active_liveness"
	// AssuranceFaceMatchOneToOne means a provider compared the live subject to one reference portrait.
	AssuranceFaceMatchOneToOne Name = "idenqa.assurance.face_match_1to1"
	// AssuranceDocumentAuthenticity means a provider found the document authentic under a declared pack.
	AssuranceDocumentAuthenticity Name = "idenqa.assurance.document_authenticity"
	// AssuranceCaptureQuality means the applicable provider quality gates passed.
	AssuranceCaptureQuality Name = "idenqa.assurance.capture_quality"
	// AssuranceMRZParsed means a declared provider pack parsed the document MRZ.
	AssuranceMRZParsed Name = "idenqa.assurance.mrz_parsed"
	// AssuranceBarcodeParsed means a declared provider pack parsed the document barcode.
	AssuranceBarcodeParsed Name = "idenqa.assurance.barcode_parsed"

	// ConstraintAllowedCountries limits accepted document countries.
	ConstraintAllowedCountries Name = "idenqa.constraint.allowed_countries"
	// ConstraintAllowedMedia limits accepted media types.
	ConstraintAllowedMedia Name = "idenqa.constraint.allowed_media_types"
	// ConstraintMaximumBytes limits an artefact's encoded byte size.
	ConstraintMaximumBytes Name = "idenqa.constraint.maximum_bytes"
)

// BuiltInRegistry returns the first synthetic, provider-neutral registry.
// Camera capture can establish acquisition freshness and integrity, but a
// still-image method cannot itself establish passive or active liveness.
func BuiltInRegistry() (Registry, error) {
	return NewRegistry(1, builtInDefinitions())
}

// PanAfricanRegistry returns the additive D-014 registry revision. Keeping it
// separate preserves the canonical identity of sessions pinned to revision 1.
func PanAfricanRegistry() (Registry, error) {
	definitions := builtInDefinitions()
	definitions.Assurances = append(definitions.Assurances,
		AssuranceFaceMatchOneToOne,
		AssuranceDocumentAuthenticity,
		AssuranceCaptureQuality,
		AssuranceMRZParsed,
		AssuranceBarcodeParsed,
	)
	return NewRegistry(2, definitions)
}

func builtInDefinitions() Definitions {
	return Definitions{
		Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack, ArtefactSelfieImage},
		Evidence: []Type{
			{Name: EvidenceDocumentImage, Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack}},
			{Name: EvidenceSelfieImage, Artefacts: []Name{ArtefactSelfieImage}},
		},
		Methods: []AcquisitionMethod{
			{
				Name: MethodFileUpload,
				Supports: []MethodSupport{
					{EvidenceType: EvidenceDocumentImage, Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack}},
					{EvidenceType: EvidenceSelfieImage, Artefacts: []Name{ArtefactSelfieImage}},
				},
			},
			{
				Name: MethodLiveCamera,
				Supports: []MethodSupport{
					{EvidenceType: EvidenceDocumentImage, Artefacts: []Name{ArtefactDocumentFront, ArtefactDocumentBack}, Assurances: []Name{AssuranceFreshness, AssuranceLiveCapture, AssuranceCaptureIntegrity}},
					{EvidenceType: EvidenceSelfieImage, Artefacts: []Name{ArtefactSelfieImage}, Assurances: []Name{AssuranceFreshness, AssuranceLiveCapture, AssuranceCaptureIntegrity}},
				},
			},
		},
		Purposes: []Name{PurposeIdentityVerification},
		Assurances: []Name{
			AssuranceFreshness,
			AssuranceLiveCapture,
			AssuranceCaptureIntegrity,
			AssurancePassiveLiveness,
			AssuranceActiveLiveness,
		},
		Constraints: []ConstraintDefinition{
			{Name: ConstraintAllowedCountries, ValueKind: ValueStringList, AppliesTo: []Name{EvidenceDocumentImage}},
			{Name: ConstraintAllowedMedia, ValueKind: ValueStringList},
			{Name: ConstraintMaximumBytes, ValueKind: ValueInteger},
		},
	}
}

// PurposeFraudPrevention permits explicitly disclosed fraud correlation.
const PurposeFraudPrevention Name = "idenqa.purpose.fraud_prevention"

// FraudRegistry adds the purpose in revision 3 without altering older snapshots.
func FraudRegistry() (Registry, error) {
	d := builtInDefinitions()
	d.Assurances = append(d.Assurances, AssuranceFaceMatchOneToOne, AssuranceDocumentAuthenticity, AssuranceCaptureQuality, AssuranceMRZParsed, AssuranceBarcodeParsed)
	d.Purposes = append(d.Purposes, PurposeFraudPrevention)
	return NewRegistry(3, d)
}

// BuiltInCatalog deploys all immutable public registry revisions.
func BuiltInCatalog() (Catalog, error) {
	a, e := BuiltInRegistry()
	if e != nil {
		return Catalog{}, e
	}
	b, e := PanAfricanRegistry()
	if e != nil {
		return Catalog{}, e
	}
	c, e := FraudRegistry()
	if e != nil {
		return Catalog{}, e
	}
	return NewCatalog(a, b, c)
}
