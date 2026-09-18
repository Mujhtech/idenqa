package modelrunner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
)

func TestImportCommandNeedsNoRuntime(t *testing.T) {
	t.Parallel()
	path, _, _ := importFixture(t)
	var stdout, stderr bytes.Buffer
	code := cli.ExecuteArgs(t.Context(), newCommand(buildinfo.Info{}), []string{"--import-dataset", path}, &stdout, &stderr)
	var dataset Dataset
	if code != 0 || json.Unmarshal(stdout.Bytes(), &dataset) != nil || validateDataset(dataset) != nil || stderr.Len() != 0 {
		t.Fatalf("import failed: %d %s", code, stderr.String())
	}
}
func TestCommandRejectsConflictingModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"import and evaluate", []string{"--import-dataset", "missing", "--evaluate-dataset", "missing"}},
		{"import and config", []string{"--import-dataset", "missing", "--config", "missing"}},
		{"comparison missing cohort", []string{"--compare-config", "missing", "--config", "missing"}},
		{"comparison and inspection", []string{"--compare-config", "missing", "--config", "missing", "--evaluate-dataset", "missing", "--runtime-digest"}},
		{"empty import", []string{"--import-dataset="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errout bytes.Buffer
			if code := cli.ExecuteArgs(t.Context(), newCommand(buildinfo.Info{}), tc.args, &out, &errout); code == 0 || out.Len() != 0 {
				t.Fatal("invalid mode produced success output")
			}
		})
	}
}

func TestNativeImportEvaluateCompareWorkflow(t *testing.T) {
	python := os.Getenv("ONNX_TEST_PYTHON")
	if python == "" {
		t.Skip("native ONNX environment required")
	}
	inventory, _, _ := importFixture(t)
	directory := filepath.Dir(inventory)
	run := func(args ...string) (int, []byte, string) {
		t.Helper()
		var out, errout bytes.Buffer
		code := cli.ExecuteArgs(t.Context(), newCommand(buildinfo.Info{}), args, &out, &errout)
		return code, out.Bytes(), errout.String()
	}
	code, raw, message := run("--import-dataset", inventory)
	if code != 0 {
		t.Fatal(message)
	}
	manifest := filepath.Join(directory, "dataset.json")
	if err := os.WriteFile(manifest, raw, 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := onnx.RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot, err := os.OpenRoot("../../../adapters/models/onnx/testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureRoot.Close() }()
	pinned := func(name string) (string, string) {
		t.Helper()
		path, err := filepath.Abs("../../../adapters/models/onnx/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := fixtureRoot.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		return path, "sha256:" + hex.EncodeToString(sum[:])
	}
	modelPath, modelDigest := pinned("pad_fixture.onnx")
	detectorPath, detectorDigest := pinned("detector_fixture.onnx")
	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	preparation := onnx.FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: detectorDigest}
	configuration := onnx.Configuration{TenantID: "ten_" + id, Width: 128, Height: 128, EvaluationOnly: true, FacePreparation: &preparation,
		Registration: modelv1.ConfigurationReference{ModelID: "mdl_" + id, ConfigurationRef: "configuration://model/fixture"},
		Manifest: modelv1.Manifest{Provenance: modelv1.Provenance{ModelID: "mdl_" + id, ModelVersion: "0.1.0", ModelDigest: modelDigest, RuntimeDigest: runtime, PreprocessingDigest: onnx.FacePreprocessingDigest(preparation), OutputSchemaDigest: onnx.OutputSchemaDigest(), Contract: modelv1.CurrentVersion},
			Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}}, Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 10 << 20, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}}}
	configuration.Registration.ConfigurationDigest = onnx.ConfigurationDigest(configuration)
	settings := Settings{Python: python, ModelFile: modelPath, DetectorFile: detectorPath, Model: configuration}
	baseline := filepath.Join(directory, "baseline.json")
	candidate := filepath.Join(directory, "candidate.json")
	writeEvaluationJSON(t, baseline, settings)
	settings.Model.Manifest.Provenance.ModelVersion = "0.2.0"
	settings.Model.Registration.ConfigurationDigest = onnx.ConfigurationDigest(settings.Model)
	writeEvaluationJSON(t, candidate, settings)
	code, raw, message = run("--config", baseline, "--evaluate-dataset", manifest, "--compare-config", candidate)
	var report Comparison
	if code != 0 || json.Unmarshal(raw, &report) != nil {
		t.Fatalf("native comparison failed: %d %s", code, message)
	}
	if report.ProductionAccepted || report.Baseline.Total.GenuineScored != 1 || report.Candidate.Total.GenuineScored != 1 || report.Delta.BPCER == nil || *report.Delta.BPCER != 0 || report.Delta.APCER != nil || len(report.Groups) != 1 || report.Groups[0].Delta.BPCER == nil || *report.Groups[0].Delta.BPCER != 0 || report.Baseline.ConfigurationDigest == report.Candidate.ConfigurationDigest {
		t.Fatalf("unexpected comparison: %+v", report)
	}
	for _, private := range []string{directory, "subject-a", "a.png"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("comparison leaked sample metadata")
		}
	}
	settings.Model.Manifest.Provenance.ModelDigest = "sha256:" + strings.Repeat("0", 64)
	settings.Model.Registration.ConfigurationDigest = onnx.ConfigurationDigest(settings.Model)
	writeEvaluationJSON(t, candidate, settings)
	code, raw, _ = run("--config", baseline, "--evaluate-dataset", manifest, "--compare-config", candidate)
	if code == 0 || len(raw) != 0 {
		t.Fatal("failed candidate produced a partial comparison")
	}
}
