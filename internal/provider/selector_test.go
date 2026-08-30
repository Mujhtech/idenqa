package provider_test

import (
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/provider"
)

func TestOutageFallbackRequiresExactApprovedMeaning(t *testing.T) {
	t.Parallel()
	required := provider.Semantics{
		Check: "idenqa.check.document_biometric", Country: "NG",
		Evidence:            []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"},
		Assurances:          []string{"idenqa.assurance.active_liveness", "idenqa.assurance.face_match_1to1"},
		ProcessingAuthority: "authority.ng.onboarding.v1", Recipient: "tenant", Region: "africa",
		Purpose: "idenqa.purpose.identity_verification",
	}
	candidates := []provider.Candidate{
		{ProviderID: "smileid", Healthy: false, Approved: true, Semantics: required},
		{ProviderID: "dojah", Healthy: true, Approved: true, Semantics: required},
	}
	selected, err := provider.Select(required, "smileid", candidates)
	if err != nil || selected.ProviderID != "dojah" {
		t.Fatalf("selected = %+v, error = %v", selected, err)
	}

	changed := required
	changed.Region = "global"
	candidates[1].Semantics = changed
	if _, err := provider.Select(required, "smileid", candidates); !errors.Is(err, provider.ErrNoCompatibleProvider) {
		t.Fatalf("region-changing fallback error = %v", err)
	}

	candidates[1].Semantics = required
	candidates[1].Approved = false
	if _, err := provider.Select(required, "smileid", candidates); !errors.Is(err, provider.ErrNoCompatibleProvider) {
		t.Fatalf("unapproved fallback error = %v", err)
	}
}

func TestSelectRejectsAmbiguousOrMalformedCatalogues(t *testing.T) {
	t.Parallel()
	required := provider.Semantics{
		Check: "idenqa.check.document_biometric", Country: "NG",
		Evidence:            []string{"idenqa.evidence.document_image"},
		ProcessingAuthority: "authority.ng.onboarding.v1", Recipient: "tenant", Region: "africa",
		Purpose: "idenqa.purpose.identity_verification",
	}
	candidate := provider.Candidate{ProviderID: "smileid", Healthy: true, Approved: true, Semantics: required}
	if _, err := provider.Select(required, "smileid", []provider.Candidate{candidate, candidate}); !errors.Is(err, provider.ErrInvalidRoute) {
		t.Fatalf("duplicate provider error = %v", err)
	}
	required.Evidence = append(required.Evidence, required.Evidence[0])
	if _, err := provider.Select(required, "smileid", []provider.Candidate{candidate}); !errors.Is(err, provider.ErrInvalidRoute) {
		t.Fatalf("duplicate semantic evidence error = %v", err)
	}
}
