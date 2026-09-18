package modelrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
)

func datasetFixture() Dataset {
	threshold := 0.8
	return Dataset{Version: 1, Reference: "fixture.v1", ApprovalReference: "test-only", ThresholdReference: "experiment.v1", RealScoreThreshold: &threshold, Samples: []Sample{{ID: "a", SubjectID: "subject-a", Split: "evaluation", Path: "a.png", SHA256: strings.Repeat("a", 64), Label: "genuine", DeviceClass: "fixture", CaptureCondition: "fixture"}}}
}
func TestDatasetRejectsLeakageAndAmbiguousLabels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*Dataset)
	}{
		{"missing threshold", func(d *Dataset) { d.RealScoreThreshold = nil }},
		{"absolute path", func(d *Dataset) { d.Samples[0].Path = "/outside.png" }},
		{"parent path", func(d *Dataset) { d.Samples[0].Path = "../outside.png" }},
		{"missing approval", func(d *Dataset) { d.ApprovalReference = "" }},
		{"missing attack type", func(d *Dataset) { d.Samples[0].Label = "attack" }},
		{"duplicate content", func(d *Dataset) {
			other := d.Samples[0]
			other.ID = "b"
			other.SubjectID = "subject-b"
			d.Samples = append(d.Samples, other)
		}},
		{"subject leakage", func(d *Dataset) {
			other := d.Samples[0]
			other.ID = "b"
			other.SHA256 = strings.Repeat("b", 64)
			other.Split = "calibration"
			d.Samples = append(d.Samples, other)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := datasetFixture()
			tc.change(&d)
			if validateDataset(d) == nil {
				t.Fatal("invalid evaluation dataset accepted")
			}
		})
	}
	unknown := datasetFixture()
	unknown.Samples[0].SubjectID = ""
	if err := validateDataset(unknown); err != nil {
		t.Fatal("evaluation-only data may explicitly omit subject identity")
	}
	if err := validateDataset(datasetFixture()); err != nil {
		t.Fatal(err)
	}
}

type evaluationSequence struct {
	values []*float64
	index  int
}

func (s *evaluationSequence) InferImage(context.Context, onnx.Image) (onnx.Evaluation, error) {
	value := s.values[s.index]
	s.index++
	if value == nil {
		return onnx.Evaluation{Reason: "face_not_found"}, nil
	}
	return onnx.Evaluation{Score: value}, nil
}
func floatPointer(value float64) *float64 { return &value }
func TestEvaluationSeparatesErrorsFromNonresponses(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	dataset := datasetFixture()
	dataset.Samples = nil
	for i, label := range []string{"genuine", "genuine", "attack", "attack", "attack"} {
		picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
		for y := 0; y < 2; y++ {
			for x := 0; x < 2; x++ {
				picture.Set(x, y, color.RGBA{R: byte(i), A: 255})
			}
		}
		var raw bytes.Buffer
		if err := png.Encode(&raw, picture); err != nil {
			t.Fatal(err)
		}
		name := string(rune('a' + i))
		path := name + ".png"
		if err := root.WriteFile(path, raw.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(raw.Bytes())
		sample := Sample{ID: name, SubjectID: name, Split: "evaluation", Path: path, SHA256: hex.EncodeToString(hash[:]), Label: label, DeviceClass: "fixture", CaptureCondition: "fixture"}
		if label == "attack" {
			sample.AttackType = "print"
		}
		dataset.Samples = append(dataset.Samples, sample)
	}
	sequence := &evaluationSequence{values: []*float64{floatPointer(0.8), floatPointer(0.3), floatPointer(0.8), floatPointer(0.1), nil}}
	report, err := evaluateDataset(t.Context(), dataset, onnx.Configuration{}, root, sequence)
	if err != nil {
		t.Fatal(err)
	}
	c := report.Total
	if report.ProductionAccepted || c.APCER == nil || *c.APCER != 0.5 || c.BPCER == nil || *c.BPCER != 0.5 || c.APNRR == nil || *c.APNRR != 1.0/3 || report.Nonresponses["face_not_found"] != 1 || report.EvaluationSubjects != 5 {
		t.Fatalf("misleading metrics: %+v", report)
	}
	dataset.Samples = dataset.Samples[2:]
	sequence = &evaluationSequence{values: []*float64{floatPointer(0.8), floatPointer(0.1), nil}}
	report, err = evaluateDataset(t.Context(), dataset, onnx.Configuration{}, root, sequence)
	if err != nil || report.Coverage != "attack_only" || report.Total.BPCER != nil {
		t.Fatalf("attack-only report implied genuine accuracy: %+v %v", report, err)
	}
	dataset.Samples[0].SHA256 = strings.Repeat("0", 64)
	if _, err := evaluateDataset(t.Context(), dataset, onnx.Configuration{}, root, sequence); err == nil {
		t.Fatal("changed dataset bytes accepted")
	}
}
func TestEvaluationRejectsEscapingSymlink(t *testing.T) {
	directory := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "outside.png")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "a.png")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err := evaluateDataset(t.Context(), datasetFixture(), onnx.Configuration{}, root, &evaluationSequence{}); err == nil {
		t.Fatal("dataset escaped its root")
	}
}
