package verification

import (
	"slices"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/pack"
)

func packSupportRegistry(t *testing.T) *pack.Registry {
	t.Helper()
	seeds, err := pack.Seeds()
	if err != nil {
		t.Fatalf("Seeds() error = %v", err)
	}
	registry, err := pack.NewRegistry(seeds, nil, func() time.Time {
		return time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func definitiveAnalysis(documentType string, issuingState string) document.Analysis {
	return document.Analysis{
		Side:           document.SideFront,
		Fields:         []document.Field{},
		PlanSides:      []document.Side{},
		Classification: document.Classification{DocumentType: documentType, IssuingState: issuingState, Status: document.ClassificationDefinitive, Reasons: []string{}},
	}
}

func classificationFrom(signals []Signal) Signal {
	for _, signal := range signals {
		if signal.Name == SignalDocumentClassification {
			return signal
		}
	}
	return Signal{}
}

func TestDocumentSupportMarksUnsupportedDocumentsProvisional(t *testing.T) {
	t.Parallel()

	registry := packSupportRegistry(t)
	at := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)

	signals, err := documentSignals(resultOptions{
		analyses: []document.Analysis{definitiveAnalysis(document.DocumentTypeNationalID, "NGA")},
		support:  registry,
	}, at)
	if err != nil {
		t.Fatalf("documentSignals() error = %v", err)
	}
	classification := classificationFrom(signals)
	if classification.Outcome != SignalInconclusive || !slices.Equal(classification.ReasonCodes, []string{reasonDocumentSupportUnsupported}) {
		t.Fatalf("unsupported classification = %+v", classification)
	}

	signals, err = documentSignals(resultOptions{
		analyses: []document.Analysis{definitiveAnalysis(document.DocumentTypeNationalID, "NGA")},
	}, at)
	if err != nil {
		t.Fatalf("documentSignals() without support error = %v", err)
	}
	classification = classificationFrom(signals)
	if classification.Outcome != SignalSatisfied {
		t.Fatalf("unresolved classification = %+v", classification)
	}
}

func TestDocumentSupportLeavesSupportedPinClassificationsUnchanged(t *testing.T) {
	t.Parallel()

	registry := packSupportRegistry(t)
	at := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{"NGA", "UTO"} {
		signals, err := documentSignals(resultOptions{
			analyses: []document.Analysis{definitiveAnalysis(document.DocumentTypePassport, state)},
			support:  registry,
		}, at)
		if err != nil {
			t.Fatalf("documentSignals(%s) error = %v", state, err)
		}
		classification := classificationFrom(signals)
		if classification.Outcome != SignalSatisfied {
			t.Fatalf("classification for %s = %+v", state, classification)
		}
	}
}
