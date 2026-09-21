package pack

import "testing"

func TestSeedsAreCanonicalAndHonest(t *testing.T) {
	t.Parallel()

	packs, err := Seeds()
	if err != nil {
		t.Fatalf("Seeds() error = %v", err)
	}
	if len(packs) != 4 {
		t.Fatalf("seed count = %d, want 4", len(packs))
	}
	want := []string{"GH", "KE", "NG", "ZA"}
	for index, value := range packs {
		if value.Country != want[index] {
			t.Fatalf("seed country = %q, want %q", value.Country, want[index])
		}
		if value.Lifecycle != LifecycleActive || value.LegalReview.State != LegalReviewNotReviewed {
			t.Fatalf("seed %s lifecycle = %q, legal review = %+v", value.Country, value.Lifecycle, value.LegalReview)
		}
		passport, ok := value.Document(DocumentTypePassport)
		if !ok || passport.SupportLevel != SupportStructurallySupported {
			t.Fatalf("seed %s passport = %+v, ok = %v", value.Country, passport, ok)
		}
		if passport.MRZ.State != DeclarationSupported || len(passport.MRZ.Formats) != 1 || passport.MRZ.Formats[0] != "td3" {
			t.Fatalf("seed %s passport MRZ = %+v", value.Country, passport.MRZ)
		}
		if len(passport.SecurityChecks) != 0 {
			t.Fatalf("seed %s passport claims security checks = %v", value.Country, passport.SecurityChecks)
		}
		for _, mapping := range passport.AssuranceMappings {
			if mapping.State != AssuranceNotMapped {
				t.Fatalf("seed %s passport claims an assurance mapping = %+v", value.Country, mapping)
			}
		}
		for _, kind := range []DocumentType{DocumentTypeNationalID, DocumentTypeDriverLicence} {
			entry, ok := value.Document(kind)
			if !ok || entry.SupportLevel != SupportUnsupported || len(entry.Evidence) != 0 || len(entry.KnownLimitations) == 0 {
				t.Fatalf("seed %s %s = %+v, ok = %v", value.Country, kind, entry, ok)
			}
		}
		encoded, err := value.CanonicalJSON()
		if err != nil {
			t.Fatalf("seed %s CanonicalJSON() error = %v", value.Country, err)
		}
		parsed, err := Parse(encoded)
		if err != nil {
			t.Fatalf("seed %s Parse() error = %v", value.Country, err)
		}
		if parsed.Digest != value.Digest {
			t.Fatalf("seed %s digest changed on round trip", value.Country)
		}
	}
}

func TestSeedsRegistryResolvesEveryCountry(t *testing.T) {
	t.Parallel()

	packs, err := Seeds()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(packs, nil, fixedNow)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	for _, country := range []string{"NG", "GH", "KE", "ZA", "nga", "GHA", "ken", "zaf"} {
		projection, ok := registry.Support(country, "passport")
		if !ok || projection.SupportLevel != SupportStructurallySupported || projection.LegalReview.State != LegalReviewNotReviewed {
			t.Fatalf("Support(%q) = %+v, ok = %v", country, projection, ok)
		}
	}
}
