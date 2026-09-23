package onnx

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// FacePreparation pins the detector and owned contextual transform revision.
// Its fixed quality settings are experimental and do not confer assurance.
type FacePreparation struct {
	Algorithm      string `json:"algorithm"`
	DetectorDigest string `json:"detector_digest"`
	Alignment      string `json:"alignment,omitempty"`
}

func (preparation FacePreparation) matchingValid() bool {
	return preparation.valid() && preparation.Alignment == "arcface-five-point-v1"
}

// FacePreprocessingDigest includes detector, crop, color and interpolation semantics.
func FacePreprocessingDigest(preparation FacePreparation) string {
	raw, _ := json.Marshal([]any{preparation, "bgr-linear-640-zero-pad", "confidence-0.8-nms-0.3-top5000", "min64-margin5", "square-1.5-truncate-reflect101", "rgb-lanczos4-up-area-down-128-nchw-unit"})
	return digest(raw)
}
func (preparation FacePreparation) valid() bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(preparation.DetectorDigest, "sha256:"))
	return preparation.Algorithm == "yunet-context-v1" && strings.HasPrefix(preparation.DetectorDigest, "sha256:") && err == nil && len(raw) == 32
}

// Image contains transient, decoded opaque RGB pixels inside the model adapter.
type Image struct {
	Width, Height int
	RGB           []byte
}

// Evaluation is private evaluation output, never a production liveness verdict.
type Evaluation struct {
	Score  *float64
	Reason string
}

// ImagePredictor prepares and scores one bounded source image inside native isolation.
type ImagePredictor interface {
	InferImage(context.Context, Image) (Evaluation, error)
}

// NewFaceEngine verifies an additional immutable YuNet detector before serving images.
func NewFaceEngine(ctx context.Context, python, modelPath, modelDigest, runtimeDigest, detectorPath string, preparation FacePreparation) (*Engine, error) {
	if !preparation.valid() || !filepath.IsAbs(detectorPath) {
		return nil, ErrRuntime
	}
	engine, err := NewEngine(ctx, python, modelPath, modelDigest, runtimeDigest, []int{1, 3, 128, 128})
	if err != nil {
		return nil, err
	}
	return attachDetector(ctx, engine, detectorPath, preparation)
}
func attachDetector(ctx context.Context, engine *Engine, detectorPath string, preparation FacePreparation) (*Engine, error) {
	if !preparation.valid() || !filepath.IsAbs(detectorPath) {
		return nil, ErrRuntime
	}

	//nolint:gosec // Absolute operator-mounted detector; bytes are bounded and pinned before native use.
	file, err := os.Open(detectorPath)
	if err != nil {
		return nil, ErrRuntime
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 4<<20 || digest(raw) != preparation.DetectorDigest {
		return nil, ErrRuntime
	}
	engine.detector, engine.detectorDigest = raw, preparation.DetectorDigest
	if _, err := invoke(ctx, engine.python, engine.payload("validate", nil)); err != nil {
		return nil, err
	}
	return engine, nil
}

// InferImage runs detector, crop and PAD inference within one bounded execution slot.
func (engine *Engine) InferImage(ctx context.Context, picture Image) (Evaluation, error) {
	if engine.mode != "" || len(engine.detector) == 0 || picture.Width < 1 || picture.Width > 4096 || picture.Height < 1 || picture.Height > 4096 || picture.Width*picture.Height > 4_000_000 || len(picture.RGB) != 3*picture.Width*picture.Height {
		return Evaluation{}, ErrRuntime
	}
	select {
	case engine.slots <- struct{}{}:
		defer func() { <-engine.slots }()
	case <-ctx.Done():
		return Evaluation{}, ctx.Err()
	}
	payload := engine.payload("image", nil)
	payload["rgb"], payload["image_width"], payload["image_height"] = picture.RGB, picture.Width, picture.Height
	value, err := invoke(ctx, engine.python, payload)
	if err != nil {
		return Evaluation{}, err
	}
	if value.RuntimeDigest != engine.runtimeDigest {
		return Evaluation{}, ErrRuntime
	}
	if value.Reason != "" {
		if value.Score != nil {
			return Evaluation{}, ErrRuntime
		}
		switch value.Reason {
		case "face_not_found", "multiple_faces", "face_too_small", "face_at_edge":
			return Evaluation{Reason: value.Reason}, nil
		default:
			return Evaluation{}, ErrRuntime
		}
	}
	if value.Score == nil || math.IsNaN(*value.Score) || math.IsInf(*value.Score, 0) || *value.Score < 0 || *value.Score > 1 {
		return Evaluation{}, ErrRuntime
	}
	return Evaluation{Score: value.Score}, nil
}

// DecodeImage rejects oversized or transparent images before producing transient RGB.
// JPEG EXIF orientation is not applied: input pixels must already be upright.
func DecodeImage(ctx context.Context, raw []byte) (Image, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || len(raw) > 10<<20 || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 4_000_000 {
		return Image{}, ErrRuntime
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Image{}, ErrRuntime
	}
	result := Image{Width: config.Width, Height: config.Height, RGB: make([]byte, 3*config.Width*config.Height)}
	bounds := decoded.Bounds()
	for y := 0; y < config.Height; y++ {
		if ctx.Err() != nil {
			clear(result.RGB)
			return Image{}, ctx.Err()
		}
		for x := 0; x < config.Width; x++ {
			r, g, b, a := decoded.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			if a != 65535 {
				clear(result.RGB)
				return Image{}, ErrRuntime
			}
			offset := 3 * (y*config.Width + x)
			result.RGB[offset], result.RGB[offset+1], result.RGB[offset+2] = byte((r>>8)&255), byte((g>>8)&255), byte((b>>8)&255)
		}
	}
	return result, nil
}
