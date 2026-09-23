package postgres

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestDocumentArtefacts(t *testing.T) {
	t.Parallel()
	front, back := evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack
	for _, tt := range []struct {
		name         string
		artefacts    []evidence.Name
		choice       string
		options      bool
		wrongPurpose bool
		want         []evidence.Name
	}{
		{"front only", []evidence.Name{front}, "", false, false, []evidence.Name{front}},
		{"two sides", []evidence.Name{front, back}, "", false, false, []evidence.Name{front, back}},
		{"passport selection", []evidence.Name{front, back}, "passport", true, false, []evidence.Name{front}},
		{"card selection", []evidence.Name{front, back}, "card", true, false, []evidence.Name{front, back}},
		{"unselected", []evidence.Name{front, back}, "", true, false, nil},
		{"unknown choice", []evidence.Name{front, back}, "unknown", true, false, nil},
		{"wrong purpose", []evidence.Name{front}, "", false, true, nil},
		{"missing front", []evidence.Name{back}, "", false, false, nil},
		{"duplicate side", []evidence.Name{front, front}, "", false, false, nil},
		{"unrelated artefact", []evidence.Name{front, evidence.ArtefactSelfieImage}, "", false, false, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requirement := verification.Requirement{Key: "document", Purpose: evidence.PurposeIdentityVerification, EvidenceType: evidence.EvidenceDocumentImage, Artefacts: tt.artefacts}
			if tt.options {
				requirement.DocumentOptions = []verification.DocumentOption{{ID: "passport", Artefacts: []evidence.Name{front}}, {ID: "card", Artefacts: []evidence.Name{front, back}}}
			}
			binding := provider.Binding{Requirement: "document", Purpose: string(evidence.PurposeIdentityVerification)}
			if tt.wrongPurpose {
				binding.Purpose = "idenqa.purpose.other"
			}
			got, err := documentArtefacts(verification.Profile{Requirements: []verification.Requirement{requirement}}, map[string]string{"document": tt.choice}, binding)
			if tt.want == nil {
				if !errors.Is(err, provider.ErrRequestUnavailable) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("artefacts = %v, error = %v", got, err)
			}
		})
	}
}
