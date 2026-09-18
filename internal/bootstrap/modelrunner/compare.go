package modelrunner

import (
	"context"
	"errors"
)

var errComparison = errors.New("model evaluation: comparison requires the same dataset and operating point")

// RateDelta contains candidate-minus-baseline rates, not statistical significance.
// A nil value means either run has no applicable denominator.
type RateDelta struct {
	APCER *float64 `json:"apcer_scored_only"`
	BPCER *float64 `json:"bpcer_scored_only"`
	APNRR *float64 `json:"attack_nonresponse_rate"`
	BPNRR *float64 `json:"genuine_nonresponse_rate"`
}

// ComparisonGroup reports descriptive rate changes for one declared capture group.
type ComparisonGroup struct {
	DeviceClass      string    `json:"device_class"`
	CaptureCondition string    `json:"capture_condition"`
	AttackType       string    `json:"attack_type"`
	Delta            RateDelta `json:"candidate_minus_baseline"`
}

// Comparison keeps both aggregate reports and their pins; it never selects a winner.
type Comparison struct {
	Version            int               `json:"version"`
	ProductionAccepted bool              `json:"production_accepted"`
	Baseline           EvaluationReport  `json:"baseline"`
	Candidate          EvaluationReport  `json:"candidate"`
	Delta              RateDelta         `json:"candidate_minus_baseline"`
	Groups             []ComparisonGroup `json:"groups"`
}

// Compare evaluates both configurations sequentially with one local dataset and
// fixed threshold. Any failure or manifest change discards the complete comparison.
func Compare(ctx context.Context, baseline, candidate Settings, path string) (Comparison, error) {
	first, err := Evaluate(ctx, baseline, path)
	if err != nil {
		return Comparison{}, err
	}
	second, err := Evaluate(ctx, candidate, path)
	if err != nil {
		return Comparison{}, err
	}
	return compareReports(first, second)
}

func compareReports(first, second EvaluationReport) (Comparison, error) {
	if first.DatasetDigest == "" || first.DatasetDigest != second.DatasetDigest || first.RealScoreThreshold != second.RealScoreThreshold || first.ThresholdReference != second.ThresholdReference || first.ProductionAccepted || second.ProductionAccepted || first.Total.Genuine != second.Total.Genuine || first.Total.Attacks != second.Total.Attacks || len(first.Groups) != len(second.Groups) {
		return Comparison{}, errComparison
	}
	result := Comparison{Version: 1, Baseline: first, Candidate: second, Delta: rateDelta(first.Total, second.Total), Groups: []ComparisonGroup{}}
	// evaluateDataset sorts groups identically for both immutable manifests.
	for i, group := range first.Groups {
		other := second.Groups[i]
		if group.DeviceClass != other.DeviceClass || group.CaptureCondition != other.CaptureCondition || group.AttackType != other.AttackType || group.Counts.Genuine != other.Counts.Genuine || group.Counts.Attacks != other.Counts.Attacks {
			return Comparison{}, errComparison
		}
		result.Groups = append(result.Groups, ComparisonGroup{DeviceClass: group.DeviceClass, CaptureCondition: group.CaptureCondition, AttackType: group.AttackType, Delta: rateDelta(group.Counts, other.Counts)})
	}
	return result, nil
}

func rateDelta(first, second Counts) RateDelta {
	return RateDelta{APCER: difference(first.APCER, second.APCER), BPCER: difference(first.BPCER, second.BPCER), APNRR: difference(first.APNRR, second.APNRR), BPNRR: difference(first.BPNRR, second.BPNRR)}
}
func difference(first, second *float64) *float64 {
	if first == nil || second == nil {
		return nil
	}
	value := *second - *first
	return &value
}
