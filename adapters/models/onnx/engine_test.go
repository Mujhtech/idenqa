package onnx

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEngineNativeInferenceAndPins(t *testing.T) {
	python := os.Getenv("ONNX_TEST_PYTHON")
	if python == "" {
		t.Skip("set ONNX_TEST_PYTHON for native ONNX conformance")
	}
	path, err := filepath.Abs("testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/pad_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	nativeDirectory := t.TempDir()
	t.Chdir(nativeDirectory)
	t.Cleanup(func() {
		entries, err := os.ReadDir(nativeDirectory)
		if err != nil || len(entries) != 0 {
			t.Errorf("native runtime wrote unexpected files: %v %v", entries, err)
		}
	})
	runtime, err := RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(t.Context(), python, path, digest(raw), runtime, []int{1, 3, 128, 128})
	if err != nil {
		t.Fatal(err)
	}
	values := make([]float32, 3*128*128)
	for i := range values {
		values[i] = 0.5
	}
	score, err := engine.Infer(t.Context(), values)
	if err != nil || math.Abs(score-1/(1+math.Exp(-1))) > 1e-6 {
		t.Fatalf("real ONNX result=%f error=%v", score, err)
	}
	t.Run("model digest mismatch", func(t *testing.T) {
		if _, err := NewEngine(t.Context(), python, path, "sha256:"+strings.Repeat("0", 64), runtime, []int{1, 3, 128, 128}); err == nil {
			t.Fatal("unverified model accepted")
		}
	})
	t.Run("runtime mismatch", func(t *testing.T) {
		if _, err := NewEngine(t.Context(), python, path, digest(raw), "sha256:"+strings.Repeat("0", 64), []int{1, 3, 128, 128}); err == nil {
			t.Fatal("unverified runtime accepted")
		}
	})
	t.Run("wrong output schema", func(t *testing.T) {
		changed := bytes.ReplaceAll(raw, []byte("output"), []byte("badout"))
		bad := filepath.Join(t.TempDir(), "bad.onnx")
		//nolint:gosec // Test-owned temporary directory and constant basename.
		if err := os.WriteFile(bad, changed, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewEngine(t.Context(), python, bad, digest(changed), runtime, []int{1, 3, 128, 128}); err == nil {
			t.Fatal("wrong output schema accepted")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := engine.Infer(ctx, values); err == nil {
			t.Fatal("cancelled inference accepted")
		}
	})
	t.Run("native deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		defer cancel()
		if _, err := engine.Infer(ctx, values); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("native process deadline was not enforced: %v", err)
		}
	})
	t.Run("busy slot cancellation", func(t *testing.T) {
		engine.slots <- struct{}{}
		defer func() { <-engine.slots }()
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		defer cancel()
		if _, err := engine.Infer(ctx, values); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued inference ignored deadline: %v", err)
		}
	})
	t.Run("nonfinite input", func(t *testing.T) {
		values[0] = float32(math.NaN())
		if _, err := engine.Infer(t.Context(), values); err == nil {
			t.Fatal("nonfinite accepted")
		}
	})
}
func TestBoundedOutput(t *testing.T) {
	var output boundedOutput
	if _, err := output.Write(make([]byte, 4097)); err == nil {
		t.Fatal("output bound ignored")
	}
}

func TestEngineReviewedPADCandidateSmoke(t *testing.T) {
	python, path := os.Getenv("ONNX_TEST_PYTHON"), os.Getenv("ONNX_TEST_PAD_MODEL")
	if python == "" || path == "" {
		t.Skip("optional reviewed external PAD candidate; weights are not bundled")
	}
	runtime, err := RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(t.Context(), python, path, "sha256:af2381b88f38769222ed93379e12444e2a50814575de1c46170de570c55a42b6", runtime, []int{1, 3, 128, 128})
	if err != nil {
		t.Fatal(err)
	}
	score, err := engine.Infer(t.Context(), make([]float32, 3*128*128))
	if err != nil || score < 0 || score > 1 {
		t.Fatalf("candidate inference failed: %v", err)
	}
	// A zero tensor establishes runtime compatibility only, never PAD accuracy.
}
