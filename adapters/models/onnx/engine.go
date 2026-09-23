// Package onnx isolates ONNX Runtime behind the public model contract.
package onnx

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed engine.py
var script string

//go:embed preparation.py
var preparationScript string

// ErrRuntime redacts native initialization and inference errors.
var ErrRuntime = errors.New("onnx: runtime unavailable or incompatible")

const maximumModelBytes = 64 << 20

// Engine owns one pinned, CPU-only native execution boundary.
// A fresh subprocess per invocation permits hard deadline/cancellation teardown.
type Engine struct {
	mode                       string
	python                     string
	model                      []byte
	modelDigest, runtimeDigest string
	shape                      []int
	slots                      chan struct{}
	detector                   []byte
	detectorDigest             string
}

// RuntimeDigest measures the native runtime and private interpreter program.
func RuntimeDigest(ctx context.Context, python string) (string, error) {
	response, err := invoke(ctx, python, map[string]any{"operation": "runtime"})
	if err != nil {
		return "", err
	}
	return response.RuntimeDigest, nil
}

// NewEngine verifies model contents, runtime identity and exact tensor schemas.
func NewEngine(ctx context.Context, python, modelPath, modelDigest, runtimeDigest string, shape []int) (*Engine, error) {
	return newEngine(ctx, python, modelPath, modelDigest, runtimeDigest, shape, "")
}
func newEngine(ctx context.Context, python, modelPath, modelDigest, runtimeDigest string, shape []int, mode string) (*Engine, error) {
	size := 128
	if mode == "face_match" {
		size = 112
	}

	if !filepath.IsAbs(python) || !filepath.IsAbs(modelPath) || len(shape) != 4 || shape[0] != 1 || shape[1] != 3 || shape[2] != size || shape[3] != size {
		return nil, ErrRuntime
	}
	//nolint:gosec // Absolute operator-mounted model path; contents are bounded and digest verified.
	file, err := os.Open(modelPath)
	if err != nil {
		return nil, ErrRuntime
	}
	defer func() { _ = file.Close() }()
	model, err := io.ReadAll(io.LimitReader(file, maximumModelBytes+1))
	if err != nil || len(model) == 0 || len(model) > maximumModelBytes || digest(model) != modelDigest {
		return nil, ErrRuntime
	}
	engine := &Engine{mode: mode, python: python, model: model, modelDigest: modelDigest, runtimeDigest: runtimeDigest, shape: append([]int(nil), shape...), slots: make(chan struct{}, 1)}
	if _, err := invoke(ctx, python, engine.payload("validate", nil)); err != nil {
		return nil, err
	}
	return engine, nil
}

// Infer executes bounded float32 RGB NCHW input and returns one finite [0,1] score.
func (engine *Engine) Infer(ctx context.Context, values []float32) (float64, error) {
	if engine.mode != "" || len(values) != engine.shape[1]*engine.shape[2]*engine.shape[3] {
		return 0, ErrRuntime
	}
	select {
	case engine.slots <- struct{}{}:
		defer func() { <-engine.slots }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	response, err := invoke(ctx, engine.python, engine.payload("infer", values))
	if err != nil {
		return 0, err
	}
	if response.RuntimeDigest != engine.runtimeDigest || response.Score == nil || math.IsNaN(*response.Score) || math.IsInf(*response.Score, 0) || *response.Score < 0 || *response.Score > 1 {
		return 0, ErrRuntime
	}
	return *response.Score, nil
}
func (engine *Engine) payload(operation string, values []float32) map[string]any {
	return map[string]any{"mode": engine.mode, "detector": engine.detector, "detector_digest": engine.detectorDigest, "operation": operation, "runtime_digest": engine.runtimeDigest, "model_digest": engine.modelDigest, "model": engine.model, "shape": engine.shape, "values": values}
}
func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type response struct {
	RuntimeDigest string   `json:"runtime_digest"`
	Score         *float64 `json:"score,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Codes         []string `json:"codes,omitempty"`
}

func invoke(ctx context.Context, python string, payload any) (response, error) {
	if !filepath.IsAbs(python) {
		return response{}, ErrRuntime
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 96<<20 {
		return response{}, ErrRuntime
	}
	defer clear(raw)
	source := preparationScript + "\n" + script
	program := "SCRIPT_DIGEST = " + fmt.Sprintf("%q", digest([]byte(source))) + "\n" + source
	//nolint:gosec // Explicit operator-owned interpreter path; fixed program and arguments, no shell or request command.
	command := exec.CommandContext(ctx, python, "-I", "-B", "-c", program)
	command.Env = []string{"ORT_DISABLE_TELEMETRY=1", "OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS=1", "MKL_NUM_THREADS=1", "PYTHONHASHSEED=0"}
	command.Stdin = bytes.NewReader(raw)
	command.Stderr = io.Discard
	output := &boundedOutput{}
	command.Stdout = output
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, ErrRuntime
	}
	var value response
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || value.RuntimeDigest == "" {
		return response{}, ErrRuntime
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return response{}, ErrRuntime
	}
	return value, nil
}

type boundedOutput struct{ bytes.Buffer }

func (output *boundedOutput) Write(raw []byte) (int, error) {
	if output.Len()+len(raw) > 4096 {
		return 0, ErrRuntime
	}
	return output.Buffer.Write(raw)
}
