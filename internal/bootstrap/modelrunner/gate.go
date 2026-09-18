package modelrunner

import (
	"math"

	"github.com/Mujhtech/idenqa/internal/model"
)

// EvaluationGate is a versioned experimental acceptance contract supplied by the operator.
// Passing it is engineering evidence and cannot authorize production inference.
type EvaluationGate struct {
	Version              int     `json:"version"`
	Reference            string  `json:"reference"`
	DatasetDigest        string  `json:"dataset_digest"`
	ConfigurationDigest  string  `json:"configuration_digest"`
	ThresholdReference   string  `json:"threshold_reference"`
	MinimumGenuine       int     `json:"minimum_genuine"`
	MinimumAttacks       int     `json:"minimum_attacks"`
	MinimumSubjects      int     `json:"minimum_subjects"`
	MaximumAPCER         float64 `json:"maximum_apcer"`
	MaximumBPCER         float64 `json:"maximum_bpcer"`
	MaximumAPNRR         float64 `json:"maximum_attack_nonresponse_rate"`
	MaximumBPNRR         float64 `json:"maximum_genuine_nonresponse_rate"`
	RequireKnownSubjects bool    `json:"require_known_subjects"`
}

// GateResult contains explicit reasons and input digests; no production approval bit can be set.
type GateResult struct {
	Passed             bool     `json:"passed"`
	ProductionAccepted bool     `json:"production_accepted"`
	GateDigest         string   `json:"gate_digest"`
	ReportDigest       string   `json:"report_digest"`
	Reasons            []string `json:"reasons"`
}

// CheckGate fails closed on absent denominators and tests every applicable capture group.
func CheckGate(report EvaluationReport, gate EvaluationGate) (GateResult, error) {
	if gate.Version != 1 || !datasetToken.MatchString(gate.Reference) || gate.DatasetDigest == "" || gate.ConfigurationDigest == "" || gate.ThresholdReference == "" || gate.MinimumGenuine < 1 || gate.MinimumAttacks < 1 || gate.MinimumSubjects < 1 || report.Version != 1 || report.ProductionAccepted {
		return GateResult{}, errDataset
	}
	for _, value := range []float64{gate.MaximumAPCER, gate.MaximumBPCER, gate.MaximumAPNRR, gate.MaximumBPNRR} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return GateResult{}, errDataset
		}
	}
	result := GateResult{Reasons: []string{}}
	var err error
	result.GateDigest, err = model.RevisionDigest(gate)
	if err != nil {
		return result, err
	}
	result.ReportDigest, err = model.RevisionDigest(report)
	if err != nil {
		return result, err
	}
	if report.DatasetDigest != gate.DatasetDigest || report.ConfigurationDigest != gate.ConfigurationDigest || report.ThresholdReference != gate.ThresholdReference {
		result.Reasons = append(result.Reasons, "input_pin_mismatch")
	}
	if report.Total.Genuine < gate.MinimumGenuine || report.Total.Attacks < gate.MinimumAttacks || report.EvaluationSubjects < gate.MinimumSubjects {
		result.Reasons = append(result.Reasons, "insufficient_coverage")
	}
	if gate.RequireKnownSubjects && (report.UnknownSubjectSamples != 0 || report.SubjectSplitCheck != "checked_declared_ids") {
		result.Reasons = append(result.Reasons, "subject_coverage_unverified")
	}
	if !gateCounts(report.Total, gate, true) {
		result.Reasons = append(result.Reasons, "aggregate_limit_exceeded_or_invalid")
	}
	if !consistentGroups(report) {
		result.Reasons = append(result.Reasons, "inconsistent_capture_group_totals")
	}
	if len(report.Groups) == 0 {
		result.Reasons = append(result.Reasons, "missing_capture_groups")
	}
	for _, group := range report.Groups {
		if !gateCounts(group.Counts, gate, false) {
			result.Reasons = append(result.Reasons, "capture_group_limit_exceeded_or_invalid")
			break
		}
	}
	result.Passed = len(result.Reasons) == 0
	return result, nil
}
func gateCounts(c Counts, g EvaluationGate, requireBoth bool) bool {
	if c.Genuine < 0 || c.Attacks < 0 || c.Genuine > 10000 || c.Attacks > 10000 || c.Genuine+c.Attacks > 10000 || c.GenuineScored < 0 || c.AttacksScored < 0 || c.GenuineScored > c.Genuine || c.AttacksScored > c.Attacks || c.FalseAccepts < 0 || c.FalseAccepts > c.AttacksScored || c.FalseRejects < 0 || c.FalseRejects > c.GenuineScored || c.Genuine+c.Attacks == 0 {
		return false
	}
	if requireBoth && (c.Genuine == 0 || c.Attacks == 0) {
		return false
	}
	// Recompute from counts: never trust caller-supplied rate fields.
	if c.Genuine > 0 && (c.GenuineScored == 0 || float64(c.FalseRejects)/float64(c.GenuineScored) > g.MaximumBPCER || float64(c.Genuine-c.GenuineScored)/float64(c.Genuine) > g.MaximumBPNRR) {
		return false
	}
	if c.Attacks > 0 && (c.AttacksScored == 0 || float64(c.FalseAccepts)/float64(c.AttacksScored) > g.MaximumAPCER || float64(c.Attacks-c.AttacksScored)/float64(c.Attacks) > g.MaximumAPNRR) {
		return false
	}
	return true
}

func consistentGroups(report EvaluationReport) bool {
	if len(report.Groups) == 0 || len(report.Groups) > 10000 {
		return false
	}
	type key struct{ device, condition, attack string }
	seen := map[key]bool{}
	total := Counts{}
	for _, group := range report.Groups {
		identity := key{group.DeviceClass, group.CaptureCondition, group.AttackType}
		if seen[identity] {
			return false
		}
		seen[identity] = true
		c := group.Counts
		if c.Genuine < 0 || c.Genuine > 10000 || c.Attacks < 0 || c.Attacks > 10000 || c.GenuineScored < 0 || c.GenuineScored > c.Genuine || c.AttacksScored < 0 || c.AttacksScored > c.Attacks || c.FalseAccepts < 0 || c.FalseAccepts > c.AttacksScored || c.FalseRejects < 0 || c.FalseRejects > c.GenuineScored {
			return false
		}
		total.Genuine += c.Genuine
		total.Attacks += c.Attacks
		total.GenuineScored += c.GenuineScored
		total.AttacksScored += c.AttacksScored
		total.FalseAccepts += c.FalseAccepts
		total.FalseRejects += c.FalseRejects
	}
	c := report.Total
	return total.Genuine == c.Genuine && total.Attacks == c.Attacks && total.GenuineScored == c.GenuineScored && total.AttacksScored == c.AttacksScored && total.FalseAccepts == c.FalseAccepts && total.FalseRejects == c.FalseRejects
}
