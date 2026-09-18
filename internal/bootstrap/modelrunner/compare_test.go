package modelrunner

import "testing"

func TestCompareReportsKeepsNonresponsesAndNullRates(t *testing.T) {
	t.Parallel()
	first := EvaluationReport{DatasetDigest: "sha256:fixture", RealScoreThreshold: 0.8, Total: Counts{Attacks: 4, AttacksScored: 4, FalseAccepts: 2}}
	second := first
	second.Total = Counts{Attacks: 4, AttacksScored: 2, FalseAccepts: 0}
	first.Total.rates()
	second.Total.rates()
	result, err := compareReports(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProductionAccepted || result.Delta.APCER == nil || *result.Delta.APCER != -0.5 || result.Delta.APNRR == nil || *result.Delta.APNRR != 0.5 || result.Delta.BPCER != nil {
		t.Fatalf("misleading comparison: %+v", result)
	}
	second.Total = Counts{Attacks: 4}
	second.Total.rates()
	result, err = compareReports(first, second)
	if err != nil || result.Delta.APCER != nil {
		t.Fatal("no scored attacks must have null delta")
	}
}
func TestCompareReportsRejectsChangedCohort(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*EvaluationReport)
	}{
		{"dataset", func(r *EvaluationReport) { r.DatasetDigest = "changed" }},
		{"threshold", func(r *EvaluationReport) { r.RealScoreThreshold = 0.5 }},
		{"cohort size", func(r *EvaluationReport) { r.Total.Attacks++ }},
		{"production claim", func(r *EvaluationReport) { r.ProductionAccepted = true }},
		{"group", func(r *EvaluationReport) { r.Groups = []EvaluationGroup{{DeviceClass: "different"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := EvaluationReport{DatasetDigest: "fixture", RealScoreThreshold: 0.8}
			second := first
			tc.change(&second)
			if _, err := compareReports(first, second); err == nil {
				t.Fatal("incompatible reports compared")
			}
		})
	}
}
