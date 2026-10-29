// Package assetstore adapts the owned object-store content port to the
// experience asset-verification boundary. Publication and import verify the
// exact immutable object version, declared size, and SHA-256 content digest
// before an asset reference is trusted.
package assetstore

import (
	"bytes"
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
func (verifier *Verifier) Verify(ctx context.Context, scope tenant.Scope, asset contract.Asset) error {
	reader, err := verifier.Open(ctx, scope, asset)
	if err != nil {
		return err
	}
	return reader.Close()
}

// Open admits only an approved reference in this tenant's namespace. The
// approval is checked before reading image bytes, including on publication,
// import and delivery. The object writer is a separate trusted capability.
func (verifier *Verifier) Open(ctx context.Context, scope tenant.Scope, asset contract.Asset) (io.ReadCloser, error) {
	if scope.ID().IsZero() {
		return nil, experience.ErrAssetUnavailable
	}
	approval, expected, err := contract.AssetApprovalObject(scope.ID().String(), asset)
	if err != nil {
		return nil, experience.ErrAssetUnavailable
	}
	actual, err := verifier.read(ctx, contract.Asset{ObjectKey: approval.ObjectKey, ObjectVersion: approval.ObjectVersion, Size: approval.Size, Digest: approval.Digest})
	if err != nil || !bytes.Equal(actual, expected) {
		return nil, experience.ErrAssetUnavailable
	}
	body, err := verifier.read(ctx, asset)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}
func (verifier *Verifier) read(ctx context.Context, asset contract.Asset) ([]byte, error) {
	object, err := objectstore.NewObject(objectstore.ObjectRecord{Key: asset.ObjectKey, Version: asset.ObjectVersion, Size: asset.Size, Checksum: "sha256:" + asset.Digest})
	if err != nil {
		return nil, experience.ErrAssetUnavailable
	}
	reader, err := verifier.reader.Open(ctx, object)
	if err != nil {
		return nil, experience.ErrAssetUnavailable
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(io.LimitReader(reader, asset.Size+1))
	if err != nil || int64(len(body)) != asset.Size {
		return nil, experience.ErrAssetUnavailable
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != asset.Digest {
		return nil, fmt.Errorf("%w: asset digest mismatch", experience.ErrAssetUnavailable)
	}
	return body, nil
}

var _ experience.AssetStore = (*Verifier)(nil)
