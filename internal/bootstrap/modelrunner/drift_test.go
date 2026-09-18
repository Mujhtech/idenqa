package modelrunner

import "testing"

func TestDriftDetectsPopulationChangeWithoutTrustingRates(t *testing.T) {
	t.Parallel()
	counts := Counts{Genuine: 10, Attacks: 10, GenuineScored: 10, AttacksScored: 10}
	baseline := EvaluationReport{Version: 1, ConfigurationDigest: "pinned", ThresholdReference: "fixed", Total: counts, Groups: []EvaluationGroup{{DeviceClass: "phone", Counts: counts}}}
	limits := DriftLimits{Reference: "experimental-v1", MinimumGenuine: 10, MinimumAttacks: 10, MaximumRateChange: 0.1}
	result, err := CheckDrift(baseline, baseline, limits)
	if err != nil || len(result.Alerts) != 0 || result.ProductionAccepted {
		t.Fatal(result, err)
	}
	current := baseline
	current.Total.FalseAccepts = 3
	result, err = CheckDrift(baseline, current, limits)
	if err != nil || len(result.Alerts) == 0 || result.ProductionAccepted {
		t.Fatal(result, err)
	}
	current = baseline
	current.Groups = nil
	result, err = CheckDrift(baseline, current, limits)
	if err != nil || len(result.Alerts) == 0 {
		t.Fatal(result, err)
	}
	current = baseline
	current.ConfigurationDigest = "different"
	if _, err := CheckDrift(baseline, current, limits); err == nil {
		t.Fatal("compared different model configurations")
	}
}
