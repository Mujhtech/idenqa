package modelrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	"github.com/Mujhtech/idenqa/internal/config"
)

// ImportDataset validates an operator-labelled local inventory and pins image bytes.
// It preserves supplied pins, rejects mismatches, and never infers labels or approval.
// The emitted manifest must remain beside the inventory so relative paths retain meaning.
func ImportDataset(ctx context.Context, path string) (Dataset, error) {
	var dataset Dataset
	if config.ReadClosedFile(path, &dataset, 4<<20) != nil {
		return Dataset{}, errDataset
	}
	if validateDatasetMetadata(dataset, true) != nil {
		return Dataset{}, errDataset
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return Dataset{}, errDataset
	}
	defer func() { _ = root.Close() }()
	for i := range dataset.Samples {
		if ctx.Err() != nil {
			return Dataset{}, ctx.Err()
		}
		sample := &dataset.Samples[i]
		raw, err := readDatasetImage(root, sample.Path)
		if err != nil {
			return Dataset{}, err
		}
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if sample.SHA256 != "" && sample.SHA256 != digest {
			clear(raw)
			return Dataset{}, errDataset
		}
		picture, err := onnx.DecodeImage(ctx, raw)
		clear(raw)
		clear(picture.RGB)
		if err != nil {
			return Dataset{}, errDataset
		}
		sample.SHA256 = digest
	}
	if validateDataset(dataset) != nil {
		return Dataset{}, errDataset
	}
	raw, err := json.Marshal(dataset)
	if err != nil || len(raw) > 4<<20 {
		return Dataset{}, errDataset
	}
	return dataset, nil
}

func readDatasetImage(root *os.Root, path string) ([]byte, error) {
	info, err := root.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 10<<20 {
		return nil, errDataset
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, errDataset
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 10<<20 {
		clear(raw)
		return nil, errDataset
	}
	return raw, nil
}
