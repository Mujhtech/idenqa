package modelrunner

import "testing"

func TestGateRecomputesRatesAndRejectsMissingCoverage(t *testing.T) {
	t.Parallel()
	counts := Counts{Genuine: 10, Attacks: 10, GenuineScored: 10, AttacksScored: 10}
	report := EvaluationReport{Version: 1, DatasetDigest: "dataset", ConfigurationDigest: "configuration", ThresholdReference: "threshold", EvaluationSubjects: 10, SubjectSplitCheck: "checked_declared_ids", Total: counts, Groups: []EvaluationGroup{{Counts: counts}}}
	gate := EvaluationGate{Version: 1, Reference: "gate-v1", DatasetDigest: "dataset", ConfigurationDigest: "configuration", ThresholdReference: "threshold", MinimumGenuine: 10, MinimumAttacks: 10, MinimumSubjects: 10, RequireKnownSubjects: true}
	result, err := CheckGate(report, gate)
	if err != nil || !result.Passed || result.ProductionAccepted {
		t.Fatal(result, err)
	}
	for _, test := range []struct {
		name   string
		change func(*EvaluationReport)
	}{
		{"forged rates", func(r *EvaluationReport) { r.Total.FalseAccepts = 1 }},
		{"no scored attacks", func(r *EvaluationReport) { r.Total.AttacksScored = 0 }},
		{"unknown subjects", func(r *EvaluationReport) { r.UnknownSubjectSamples = 1 }},
		{"wrong pin", func(r *EvaluationReport) { r.DatasetDigest = "changed" }},
		{"missing groups", func(r *EvaluationReport) { r.Groups = nil }},
		{"group regression", func(r *EvaluationReport) {
			r.Groups = []EvaluationGroup{{Counts: Counts{Genuine: 1, GenuineScored: 1, FalseRejects: 1}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := report
			test.change(&r)
			result, err := CheckGate(r, gate)
			if err != nil || result.Passed || result.ProductionAccepted {
				t.Fatal(result, err)
			}
		})
	}
}
