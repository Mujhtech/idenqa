package verification

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
)

func documentRequirement() Requirement {
	return Requirement{Key: "document", Purpose: evidence.PurposeIdentityVerification, EvidenceType: evidence.EvidenceDocumentImage, Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}, Acquisition: Acquisition{Strategy: StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload}}, DocumentOptions: []DocumentOption{
		{ID: "passport", Label: "Passport", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}},
		{ID: "driver_license", Label: "Driver licence", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}},
	}}
}

func TestDocumentOptionValidation(t *testing.T) {
	t.Parallel()
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Requirement)
	}{
		{"empty", func(r *Requirement) { r.DocumentOptions = []DocumentOption{} }},
		{"too_many", func(r *Requirement) { r.DocumentOptions = make([]DocumentOption, 17) }},
		{"duplicate_id", func(r *Requirement) { r.DocumentOptions[1].ID = "passport" }},
		{"invalid_id", func(r *Requirement) { r.DocumentOptions[0].ID = "Passport" }},
		{"long_id", func(r *Requirement) { r.DocumentOptions[0].ID = strings.Repeat("a", 65) }},
		{"long_label", func(r *Requirement) { r.DocumentOptions[0].Label = strings.Repeat("a", 81) }},
		{"control_label", func(r *Requirement) { r.DocumentOptions[0].Label = "Pass\nport" }},
		{"back_only", func(r *Requirement) { r.DocumentOptions[0].Artefacts = []evidence.Name{evidence.ArtefactDocumentBack} }},
		{"union_mismatch", func(r *Requirement) { r.Artefacts = []evidence.Name{evidence.ArtefactDocumentFront} }},
		{"non_document", func(r *Requirement) { r.EvidenceType = evidence.EvidenceSelfieImage }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := documentRequirement()
			test.change(&r)
			if _, err := NewProfile(registry, []Requirement{r}); err == nil {
				t.Fatal("invalid document option accepted")
			}
		})
	}
}

func TestDocumentSelectionPreservesSnapshotAndResolvesBranches(t *testing.T) {
	t.Parallel()
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := NewProfile(registry, []Requirement{documentRequirement()})
	if err != nil {
		t.Fatal(err)
	}
	before, err := CanonicalJSON(profile, registry)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseProfileJSON(before, registry)
	if err != nil {
		t.Fatal(err)
	}
	after, err := CanonicalJSON(parsed, registry)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("options did not round trip", err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	session := Session{requirements: profile, state: SessionStateCollecting, version: 1, updatedAt: now, expiresAt: now.Add(time.Hour)}
	if got := EffectiveArtefacts(profile.Requirements[0], session.DocumentSelections()); len(got) != 0 {
		t.Fatal("unselected branch authorised")
	}
	selected, err := session.SelectDocument("document", "passport", 1, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := EffectiveArtefacts(profile.Requirements[0], selected.DocumentSelections()); !slices.Equal(got, []evidence.Name{evidence.ArtefactDocumentFront}) {
		t.Fatal(got)
	}
	if len(session.DocumentSelections()) != 0 {
		t.Fatal("selection mutated previous session")
	}
	if _, err := selected.SelectDocument("document", "driver_license", 2, true, now); !errors.Is(err, ErrSessionConflict) {
		t.Fatal("upload intent did not lock branch", err)
	}
	if _, err := selected.SelectDocument("document", "driver_license", 1, false, now); !errors.Is(err, ErrSessionConflict) {
		t.Fatal("stale selection accepted", err)
	}
	driver, err := selected.SelectDocument("document", "driver_license", 2, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(EffectiveArtefacts(profile.Requirements[0], driver.DocumentSelections())) != 2 {
		t.Fatal("driver back missing")
	}
	driver.state = SessionStateCompleted
	if _, err := driver.SelectDocument("document", "passport", driver.Version(), false, now); !errors.Is(err, ErrSessionConflict) {
		t.Fatal("terminal selection changed", err)
	}
	completed, err := selected.WithCaptureCompletion(now)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []string{"passport", "driver_license"} {
		if _, err := completed.SelectDocument("document", choice, completed.Version(), false, now); !errors.Is(err, ErrSessionConflict) {
			t.Fatal("completed capture selection accepted", choice, err)
		}
	}
	after, err = CanonicalJSON(selected.Requirements(), registry)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("selection changed snapshot", err)
	}
}
