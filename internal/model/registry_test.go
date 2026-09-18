package model_test

import (
	"math"
	"testing"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
)

func registryFixture(t *testing.T) (model.Registration, model.ThresholdSet) {
	t.Helper()
	r := runtimeRequest(t)
	return model.Registration{Manifest: modelv1.Manifest{Provenance: r.Provenance, Capabilities: []modelv1.Capability{r.Capability}, Restrictions: r.Restrictions}, Configuration: r.Configuration, Owner: "fixture", License: "fixture-only", TrainingProvenance: "synthetic", IntendedUse: "evaluation", ProhibitedUse: "production", Regions: []string{"ng"}, HardwareClass: "cpu", EvaluationOnly: true}, model.ThresholdSet{Configuration: r.Configuration, Provenance: r.Provenance, ScoreName: "real_score", Minimum: 0, Maximum: 1, Cutoff: 0.5, HigherIsGenuine: true, EvaluationReportDigest: r.Provenance.ModelDigest, EvaluationOnly: true}
}
func TestRegistryRejectsProductionAndIncompatibleThresholds(t *testing.T) {
	t.Parallel()
	registration, threshold := registryFixture(t)
	deployment := model.Deployment{ModelRevision: 1, ThresholdRevision: 1, Region: "ng"}
	if err := model.ValidateDeployment(deployment, registration, threshold); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*model.Registration, *model.ThresholdSet, *model.Deployment)
	}{
		{"production model", func(r *model.Registration, _ *model.ThresholdSet, _ *model.Deployment) { r.EvaluationOnly = false }},
		{"production threshold", func(_ *model.Registration, v *model.ThresholdSet, _ *model.Deployment) { v.EvaluationOnly = false }},
		{"runtime mismatch", func(_ *model.Registration, v *model.ThresholdSet, _ *model.Deployment) {
			v.Provenance.RuntimeDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{"unapproved region", func(_ *model.Registration, _ *model.ThresholdSet, d *model.Deployment) { d.Region = "us" }},
		{"nan", func(_ *model.Registration, v *model.ThresholdSet, _ *model.Deployment) { v.Cutoff = math.NaN() }},
		{"out of range", func(_ *model.Registration, v *model.ThresholdSet, _ *model.Deployment) { v.Cutoff = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, v, d := registration, threshold, deployment
			test.change(&r, &v, &d)
			if model.ValidateDeployment(d, r, v) == nil {
				t.Fatal("invalid deployment accepted")
			}
		})
	}
}
