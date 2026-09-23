package postgres

import (
	"slices"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// documentArtefacts uses the immutable profile and durable selection, never the
// presence of an uploaded image, to decide which document sides may be released.
func documentArtefacts(profile verification.Profile, selections map[string]string, binding provider.Binding) ([]evidence.Name, error) {
	for _, requirement := range profile.Requirements {
		if requirement.Key != binding.Requirement {
			continue
		}
		if requirement.EvidenceType != evidence.EvidenceDocumentImage || string(requirement.Purpose) != binding.Purpose {
			return nil, provider.ErrRequestUnavailable
		}
		artefacts := verification.EffectiveArtefacts(requirement, selections)
		if len(artefacts) < 1 || len(artefacts) > 2 || !slices.Contains(artefacts, evidence.ArtefactDocumentFront) {
			return nil, provider.ErrRequestUnavailable
		}
		seen := map[evidence.Name]bool{}
		for _, artefact := range artefacts {
			if seen[artefact] || (artefact != evidence.ArtefactDocumentFront && artefact != evidence.ArtefactDocumentBack) {
				return nil, provider.ErrRequestUnavailable
			}
			seen[artefact] = true
		}
		return artefacts, nil
	}
	return nil, provider.ErrRequestUnavailable
}
