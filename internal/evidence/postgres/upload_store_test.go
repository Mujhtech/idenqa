package postgres

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestCaptureProgressUsesSelectedDocumentArtefacts(t *testing.T) {
	t.Parallel()
	profile := verification.Profile{Requirements: []verification.Requirement{{Key: "document", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}, Acquisition: verification.Acquisition{Strategy: verification.StrategyAnyOf}, DocumentOptions: []verification.DocumentOption{{ID: "passport", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}}, {ID: "driver_license", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}}}}}}
	front := `[{"requirement_key":"document","artefact":"idenqa.artefact.document_front","acquisition_method":"idenqa.method.live_camera"}]`
	for _, test := range []struct {
		name, selected, bindings string
		total, complete          uint32
		invalid                  bool
	}{
		{name: "unselected", bindings: `[]`, total: 1},
		{name: "unselected_binding", bindings: front, invalid: true},
		{name: "passport", selected: "passport", bindings: front, total: 1, complete: 1},
		{name: "driver_requires_back", selected: "driver_license", bindings: front, total: 2, complete: 1},
		{name: "outside_branch", selected: "passport", bindings: `[{"requirement_key":"document","artefact":"idenqa.artefact.document_back"}]`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			total, complete, err := captureProfileProgress(profile, test.bindings, map[string]string{"document": test.selected})
			if test.invalid {
				if err == nil {
					t.Fatal("invalid progress accepted")
				}
				return
			}
			if err != nil || total != test.total || complete != test.complete {
				t.Fatal(total, complete, err)
			}
		})
	}
}
