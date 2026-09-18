package modelrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
)

func TestThresholdRevisionRequiresExactPinsAndOperatingPoint(t *testing.T) {
	t.Parallel()
	inventory, _, _ := importFixture(t)
	dataset, err := ImportDataset(t.Context(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	hash := "sha256:" + strings.Repeat("a", 64)
	ref := modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://model/test", ConfigurationDigest: hash}
	p := modelv1.Provenance{ModelID: ref.ModelID, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: hash, OutputSchemaDigest: hash, Contract: modelv1.CurrentVersion}
	threshold := model.ThresholdSet{Configuration: ref, Provenance: p, ScoreName: "real_score", Minimum: 0, Maximum: 1, Cutoff: *dataset.RealScoreThreshold, HigherIsGenuine: true, EvaluationReportDigest: hash, EvaluationOnly: true}
	digest, err := model.RevisionDigest(threshold)
	if err != nil {
		t.Fatal(err)
	}
	revision := model.RegistryRevision{Kind: "threshold", Revision: 1, Digest: digest, Thresholds: &threshold}
	dataset.ThresholdReference = "threshold-sha256-" + strings.TrimPrefix(digest, "sha256:")
	root := t.TempDir()
	datasetPath, revisionPath := filepath.Join(root, "dataset.json"), filepath.Join(root, "revision.json")
	write := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(datasetPath, dataset)
	write(revisionPath, revision)
	settings := Settings{}
	settings.Model.Manifest.Provenance = p
	settings.Model.Registration = ref
	if err := CheckThresholdRevision(settings, datasetPath, revisionPath); err != nil {
		t.Fatal(err)
	}
	threshold.Cutoff = 0.99
	write(revisionPath, revision)
	if err := CheckThresholdRevision(settings, datasetPath, revisionPath); err == nil {
		t.Fatal("changed threshold accepted under old digest")
	}
}
