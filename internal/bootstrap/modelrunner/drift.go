package modelrunner

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/spf13/cobra"
)

// DriftLimits defines explicit experimental population-monitoring limits.
type DriftLimits struct {
	Reference         string  `json:"reference"`
	MinimumGenuine    int     `json:"minimum_genuine"`
	MinimumAttacks    int     `json:"minimum_attacks"`
	MaximumRateChange float64 `json:"maximum_rate_change"`
}

// DriftReport preserves input pins and alerts without changing a deployment.
type DriftReport struct {
	BaselineDigest     string   `json:"baseline_digest"`
	CurrentDigest      string   `json:"current_digest"`
	LimitsDigest       string   `json:"limits_digest"`
	Alerts             []string `json:"alerts"`
	ProductionAccepted bool     `json:"production_accepted"`
}

// CheckDrift compares labelled aggregate populations under identical model and threshold meaning.
// This detects descriptive changes, not their cause or statistical significance.
func CheckDrift(baseline, current EvaluationReport, limits DriftLimits) (DriftReport, error) {
	if !datasetToken.MatchString(limits.Reference) || limits.MinimumGenuine < 1 || limits.MinimumAttacks < 1 || math.IsNaN(limits.MaximumRateChange) || math.IsInf(limits.MaximumRateChange, 0) || limits.MaximumRateChange < 0 || limits.MaximumRateChange > 1 || baseline.Version != 1 || current.Version != 1 || baseline.ProductionAccepted || current.ProductionAccepted {
		return DriftReport{}, errDataset
	}
	if baseline.Provenance != current.Provenance || baseline.ConfigurationDigest == "" || baseline.ConfigurationDigest != current.ConfigurationDigest || baseline.ThresholdReference == "" || baseline.ThresholdReference != current.ThresholdReference || baseline.RealScoreThreshold != current.RealScoreThreshold {
		return DriftReport{}, errComparison
	}
	result := DriftReport{Alerts: []string{}}
	if !consistentGroups(baseline) || !consistentGroups(current) {
		result.Alerts = append(result.Alerts, "inconsistent_capture_group_totals")
	}
	var err error
	result.BaselineDigest, err = model.RevisionDigest(baseline)
	if err != nil {
		return result, err
	}
	result.CurrentDigest, err = model.RevisionDigest(current)
	if err != nil {
		return result, err
	}
	result.LimitsDigest, err = model.RevisionDigest(limits)
	if err != nil {
		return result, err
	}
	if baseline.Total.Genuine < limits.MinimumGenuine || current.Total.Genuine < limits.MinimumGenuine || baseline.Total.Attacks < limits.MinimumAttacks || current.Total.Attacks < limits.MinimumAttacks {
		result.Alerts = append(result.Alerts, "insufficient_population")
	}
	if !stableCounts(baseline.Total, current.Total, limits.MaximumRateChange) {
		result.Alerts = append(result.Alerts, "aggregate_change_or_missing_denominator")
	}
	type groupKey struct{ device, condition, attack string }
	groups := map[groupKey]Counts{}
	for _, group := range baseline.Groups {
		key := groupKey{group.DeviceClass, group.CaptureCondition, group.AttackType}
		if _, exists := groups[key]; exists {
			return result, errDataset
		}
		groups[key] = group.Counts
	}
	changed := len(groups) == 0 || len(groups) != len(current.Groups)
	seen := map[groupKey]bool{}
	for _, group := range current.Groups {
		key := groupKey{group.DeviceClass, group.CaptureCondition, group.AttackType}
		if seen[key] {
			return result, errDataset
		}
		seen[key] = true
		prior, ok := groups[key]
		if !ok || !stableCounts(prior, group.Counts, limits.MaximumRateChange) {
			changed = true
		}
	}
	if changed {
		result.Alerts = append(result.Alerts, "capture_group_change_or_missing_coverage")
	}
	return result, nil
}
func stableCounts(a, b Counts, maximum float64) bool {
	permissive := EvaluationGate{MaximumAPCER: 1, MaximumBPCER: 1, MaximumAPNRR: 1, MaximumBPNRR: 1}
	if !gateCounts(a, permissive, false) || !gateCounts(b, permissive, false) {
		return false
	}
	rate := func(n, d int) (float64, bool) {
		if d == 0 {
			return 0, false
		}
		return float64(n) / float64(d), true
	}
	for _, values := range [][4]int{{a.FalseAccepts, a.AttacksScored, b.FalseAccepts, b.AttacksScored}, {a.FalseRejects, a.GenuineScored, b.FalseRejects, b.GenuineScored}, {a.Attacks - a.AttacksScored, a.Attacks, b.Attacks - b.AttacksScored, b.Attacks}, {a.Genuine - a.GenuineScored, a.Genuine, b.Genuine - b.GenuineScored, b.Genuine}} {
		first, firstOK := rate(values[0], values[1])
		second, secondOK := rate(values[2], values[3])
		if firstOK != secondOK || (firstOK && math.Abs(first-second) > maximum) {
			return false
		}
	}
	return true
}
func newDriftCommand() *cobra.Command {
	var baselinePath, currentPath, limitsPath string
	command := &cobra.Command{Use: "check-drift", Short: "Compare labelled aggregate populations without changing model deployment", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if baselinePath == "" || currentPath == "" || limitsPath == "" {
			return fmt.Errorf("--baseline, --current and --limits are required")
		}
		var baseline, current EvaluationReport
		var limits DriftLimits
		if err := config.ReadClosedFile(baselinePath, &baseline, 4<<20); err != nil {
			return err
		}
		if err := config.ReadClosedFile(currentPath, &current, 4<<20); err != nil {
			return err
		}
		if err := config.ReadClosedFile(limitsPath, &limits, 64<<10); err != nil {
			return err
		}
		result, err := CheckDrift(baseline, current, limits)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
			return err
		}
		if len(result.Alerts) > 0 {
			return fmt.Errorf("experimental drift alerts require review")
		}
		return nil
	}}
	command.Flags().StringVar(&baselinePath, "baseline", "", "baseline aggregate report")
	command.Flags().StringVar(&currentPath, "current", "", "current aggregate report")
	command.Flags().StringVar(&limitsPath, "limits", "", "experimental monitoring limits")
	return command
}
