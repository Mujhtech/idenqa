package assetstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type fakeReader struct {
	body []byte
	err  error
}

func (reader fakeReader) Open(context.Context, objectstore.Object) (io.ReadCloser, error) {
	if reader.err != nil {
		return nil, reader.err
	}
	return io.NopCloser(bytes.NewReader(reader.body)), nil
}

func assetFor(body []byte) contract.Asset {
	digest := sha256.Sum256(body)
	return contract.Asset{
		Key: "logo", Kind: "image", MIME: "image/png", Size: int64(len(body)),
		Digest: hex.EncodeToString(digest[:]), ObjectKey: "experiences/acme/logo.png", ObjectVersion: "v1",
	}
}

func TestVerifyAcceptsExactObjectAndRejectsTampering(t *testing.T) {
	t.Parallel()
	body := []byte("vetted-image-bytes")
	asset := assetFor(body)

	verifier, err := New(fakeReader{body: body})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := verifier.Verify(context.Background(), tenant.Scope{}, asset); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	tampered, _ := New(fakeReader{body: []byte("tampered-image-bytes")})
	if err := tampered.Verify(context.Background(), tenant.Scope{}, asset); !errors.Is(err, experience.ErrAssetUnavailable) {
		t.Fatalf("Verify() tampered error = %v, want ErrAssetUnavailable", err)
	}
	unavailable, _ := New(fakeReader{err: errors.New("store down")})
	if err := unavailable.Verify(context.Background(), tenant.Scope{}, asset); !errors.Is(err, experience.ErrAssetUnavailable) {
		t.Fatalf("Verify() unavailable error = %v, want ErrAssetUnavailable", err)
	}
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) accepted a missing reader")
	}
}
