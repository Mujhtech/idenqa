package postgres

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestCaptureStepRequiresSelectedDocumentBranch(t *testing.T) {
	t.Parallel()
	profile := verification.Profile{Requirements: []verification.Requirement{{Key: "document", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}, Acquisition: verification.Acquisition{Methods: []evidence.Name{evidence.MethodLiveCamera}}, DocumentOptions: []verification.DocumentOption{{ID: "passport", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}}}}}}
	step := realtime.CaptureStepUpdate{RequirementKey: "document", Artefact: string(evidence.ArtefactDocumentFront), AcquisitionMethod: string(evidence.MethodLiveCamera)}
	if captureStepPermitted(profile, step) {
		t.Fatal("unselected command permitted")
	}
	if !captureStepPermitted(profile, step, map[string]string{"document": "passport"}) {
		t.Fatal("selected front denied")
	}
	step.Artefact = string(evidence.ArtefactDocumentBack)
	if captureStepPermitted(profile, step, map[string]string{"document": "passport"}) {
		t.Fatal("outside branch command permitted")
	}
}
