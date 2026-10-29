package assetstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/id"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type fakeReader struct {
	body     []byte
	approval []byte
	err      error
}

func (reader fakeReader) Open(_ context.Context, object objectstore.Object) (io.ReadCloser, error) {
	if reader.err != nil {
		return nil, reader.err
	}
	if strings.HasSuffix(object.Record().Key, "/_approval") {
		return io.NopCloser(bytes.NewReader(reader.approval)), nil
	}
	return io.NopCloser(bytes.NewReader(reader.body)), nil
}

func assetFor(body []byte) contract.Asset {
	digest := sha256.Sum256(body)
	return contract.Asset{
		Key: "logo", Kind: "image", MIME: "image/png", Size: int64(len(body)),
		Digest: hex.EncodeToString(digest[:]), ObjectKey: "experiences/ten_01K3P4NQF00000000000000000/approved/logo.png", ObjectVersion: "v1",
	}
}

func TestVerifyAcceptsExactObjectAndRejectsTampering(t *testing.T) {
	t.Parallel()
	body := []byte("vetted-image-bytes")
	asset := assetFor(body)
	tenantID, err := id.ParseTenant("ten_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	_, approval, err := contract.AssetApprovalObject(tenantID.String(), asset)
	if err != nil {
		t.Fatal(err)
	}

	verifier, err := New(fakeReader{body: body, approval: approval})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := verifier.Verify(context.Background(), scope, asset); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	tampered, _ := New(fakeReader{body: []byte("tampered-image-bytes"), approval: approval})
	if err := tampered.Verify(context.Background(), scope, asset); !errors.Is(err, experience.ErrAssetUnavailable) {
		t.Fatalf("Verify() tampered error = %v, want ErrAssetUnavailable", err)
	}
	unavailable, _ := New(fakeReader{err: errors.New("store down")})
	if err := unavailable.Verify(context.Background(), scope, asset); !errors.Is(err, experience.ErrAssetUnavailable) {
		t.Fatalf("Verify() unavailable error = %v, want ErrAssetUnavailable", err)
	}
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) accepted a missing reader")
	}
}

func TestVerifyRefusesUnapprovedStagingAndCrossTenantReferences(t *testing.T) {
	tenantID, err := id.ParseTenant("ten_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("vetted-image-bytes")
	asset := assetFor(body)
	_, approval, err := contract.AssetApprovalObject(tenantID.String(), asset)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"staging", "cross-tenant", "missing-approval", "tampered-approval", "empty-scope"} {
		t.Run(name, func(t *testing.T) {
			ref := asset
			bound := scope
			reader := fakeReader{body: body, approval: approval}
			switch name {
			case "staging":
				ref.ObjectKey = "capture-asset-staging/" + tenantID.String() + "/source"
			case "cross-tenant":
				ref.ObjectKey = "experiences/ten_other/approved/logo.png"
			case "missing-approval":
				reader.approval = nil
			case "tampered-approval":
				reader.approval = []byte("forged")
			case "empty-scope":
				bound = tenant.Scope{}
			}
			verifier, err := New(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err = verifier.Verify(t.Context(), bound, ref); !errors.Is(err, experience.ErrAssetUnavailable) {
				t.Fatalf("unsafe reference accepted: %v", err)
			}
		})
	}
}
