// Package assetstore adapts the owned object-store content port to the
// experience asset-verification boundary. Publication and import verify the
// exact immutable object version, declared size, and SHA-256 content digest
// before an asset reference is trusted.
package assetstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Reader opens one exact immutable ciphertext object version. It is the narrow
// subset of the owned object-store port asset verification consumes.
type Reader interface {
	Open(context.Context, objectstore.Object) (io.ReadCloser, error)
}

// Verifier checks asset references against stored objects.
type Verifier struct {
	reader Reader
}

// New constructs the asset verifier.
func New(reader Reader) (*Verifier, error) {
	if reader == nil {
		return nil, errors.New("assetstore: object reader is required")
	}
	return &Verifier{reader: reader}, nil
}

// Verify reads the exact object version and compares its size and SHA-256
// digest with the asset reference. Every failure is closed.
func (verifier *Verifier) Verify(ctx context.Context, _ tenant.Scope, asset contract.Asset) error {
	if asset.Size <= 0 || asset.Size > contract.MaxAssetBytes {
		return experience.ErrAssetUnavailable
	}
	object, err := objectstore.NewObject(objectstore.ObjectRecord{
		Key: asset.ObjectKey, Version: asset.ObjectVersion, Size: asset.Size,
		Checksum: "sha256:" + asset.Digest,
	})
	if err != nil {
		return experience.ErrAssetUnavailable
	}
	reader, err := verifier.reader.Open(ctx, object)
	if err != nil {
		return experience.ErrAssetUnavailable
	}
	defer func() { _ = reader.Close() }()
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(reader, asset.Size+1))
	if err != nil || written != asset.Size {
		return experience.ErrAssetUnavailable
	}
	if hex.EncodeToString(hasher.Sum(nil)) != asset.Digest {
		return fmt.Errorf("%w: asset digest mismatch", experience.ErrAssetUnavailable)
	}
	return nil
}

var _ experience.AssetStore = (*Verifier)(nil)
