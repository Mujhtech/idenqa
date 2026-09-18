package modelrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEvaluationJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func importFixture(t *testing.T) (string, Dataset, []byte) {
	t.Helper()
	directory := t.TempDir()
	picture := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			picture.Set(x, y, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, picture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "a.png"), raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	dataset := datasetFixture()
	dataset.Samples[0].SHA256 = ""
	path := filepath.Join(directory, "inventory.json")
	writeEvaluationJSON(t, path, dataset)
	return path, dataset, raw.Bytes()
}
func TestImportDatasetPinsAndPreservesLabels(t *testing.T) {
	t.Parallel()
	path, original, raw := importFixture(t)
	got, err := ImportDataset(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got.Samples[0].SHA256 != hex.EncodeToString(sum[:]) || got.Samples[0].SubjectID != original.Samples[0].SubjectID || got.ApprovalReference != original.ApprovalReference {
		t.Fatalf("import changed metadata or failed to pin: %+v", got)
	}
	writeEvaluationJSON(t, path, got)
	if _, err := ImportDataset(t.Context(), path); err != nil {
		t.Fatal("valid supplied pin rejected", err)
	}
}
func TestImportDatasetRejectsInvalidInventory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*Dataset)
	}{
		{"wrong supplied hash", func(d *Dataset) { d.Samples[0].SHA256 = strings.Repeat("0", 64) }},
		{"traversal", func(d *Dataset) { d.Samples[0].Path = "../a.png" }},
		{"duplicate bytes", func(d *Dataset) {
			s := d.Samples[0]
			s.ID = "b"
			s.SubjectID = "other"
			d.Samples = append(d.Samples, s)
		}},
		{"missing label", func(d *Dataset) { d.Samples[0].Label = "" }},
		{"unknown subject calibration", func(d *Dataset) {
			d.Samples[0].SubjectID = ""
			s := d.Samples[0]
			s.ID = "b"
			s.Split = "calibration"
			d.Samples = append(d.Samples, s)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, d, _ := importFixture(t)
			tc.change(&d)
			writeEvaluationJSON(t, path, d)
			if _, err := ImportDataset(t.Context(), path); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	path, _, _ := importFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ImportDataset(ctx, path); err == nil {
		t.Fatal("cancelled import accepted")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "a.png"), []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportDataset(t.Context(), path); err == nil {
		t.Fatal("malformed image accepted")
	}
}
