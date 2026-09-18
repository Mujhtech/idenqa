package onnx

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

func TestDecodeImageBoundsAndTransparency(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		width, height int
		transparent   bool
		valid         bool
	}{
		{"source image", 256, 192, false, true}, {"transparent", 128, 128, true, false}, {"too wide", 4097, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			picture := image.NewRGBA(image.Rect(0, 0, tc.width, tc.height))
			if !tc.transparent {
				for y := 0; y < tc.height; y++ {
					for x := 0; x < tc.width; x++ {
						picture.Set(x, y, color.RGBA{R: 128, A: 255})
					}
				}
			}
			var raw bytes.Buffer
			if err := png.Encode(&raw, picture); err != nil {
				t.Fatal(err)
			}
			result, err := DecodeImage(t.Context(), raw.Bytes())
			if (err == nil) != tc.valid {
				t.Fatalf("decode validity=%v expected %v", err, tc.valid)
			}
			if tc.valid && (len(result.RGB) != 3*tc.width*tc.height || result.RGB[0] != 128) {
				t.Fatal("RGB conversion")
			}
		})
	}
}
func TestContextualNativePipeline(t *testing.T) {
	python := os.Getenv("ONNX_TEST_PYTHON")
	if python == "" {
		t.Skip("native ONNX environment required")
	}
	runtime, err := RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	modelPath, err := filepath.Abs("testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	detectorPath, err := filepath.Abs("testdata/detector_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	modelBytes, err := os.ReadFile("testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	detectorBytes, err := os.ReadFile("testdata/detector_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	preparation := FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: digest(detectorBytes)}
	engine, err := NewFaceEngine(t.Context(), python, modelPath, digest(modelBytes), runtime, detectorPath, preparation)
	if err != nil {
		t.Fatal(err)
	}
	picture := Image{Width: 256, Height: 256, RGB: bytes.Repeat([]byte{128, 64, 32}, 256*256)}
	result, err := engine.InferImage(t.Context(), picture)
	if err != nil || result.Score == nil || result.Reason != "" {
		t.Fatalf("contextual inference failed: %+v %v", result, err)
	}
	configuration, request, _, now := adapterFixture(t)
	configuration.FacePreparation = &preparation
	configuration.Manifest.Provenance.PreprocessingDigest = FacePreprocessingDigest(preparation)
	configuration.Registration.ConfigurationDigest = ConfigurationDigest(configuration)
	request.Configuration = configuration.Registration
	request.Provenance = configuration.Manifest.Provenance
	var encoded bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			source.Set(x, y, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		}
	}
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(configuration, engine, &testReader{raw: encoded.Bytes()}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	value, err := adapter.Execute(t.Context(), request)
	if err != nil || len(value.Signals) != 1 || value.Signals[0].Outcome != modelv1.SignalOutcomeInconclusive {
		t.Fatalf("live pipeline asserted assurance: %+v %v", value, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := engine.InferImage(ctx, picture); err == nil {
		t.Fatal("cancelled image inference accepted")
	}
}

func TestReviewedYuNetCandidateSmoke(t *testing.T) {
	python, detector := os.Getenv("ONNX_TEST_PYTHON"), os.Getenv("ONNX_TEST_FACE_DETECTOR")
	if python == "" || detector == "" {
		t.Skip("optional external reviewed YuNet detector")
	}
	runtime, err := RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	modelPath, err := filepath.Abs("testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	preparation := FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: "sha256:8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4"}
	engine, err := NewFaceEngine(t.Context(), python, modelPath, digest(raw), runtime, detector, preparation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.InferImage(t.Context(), Image{Width: 640, Height: 640, RGB: make([]byte, 640*640*3)})
	if err != nil || result.Score != nil || result.Reason != "face_not_found" {
		t.Fatalf("blank input was not rejected: %+v %v", result, err)
	}
	preparation.DetectorDigest = "sha256:" + strings.Repeat("0", 64)
	if _, err := NewFaceEngine(t.Context(), python, modelPath, digest(raw), runtime, detector, preparation); err == nil {
		t.Fatal("wrong detector pin accepted")
	}
}
