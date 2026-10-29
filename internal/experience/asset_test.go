package experience

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type readableAssets struct{ fail bool }

func (*readableAssets) Verify(context.Context, tenant.Scope, contract.Asset) error { return nil }
func (a *readableAssets) Open(_ context.Context, _ tenant.Scope, asset contract.Asset) (io.ReadCloser, error) {
	if a.fail {
		return nil, ErrAssetUnavailable
	}
	return io.NopCloser(strings.NewReader(asset.ObjectVersion)), nil
}

func TestAssetDeliveryRequiresExistingPinAndPreservesItsRevision(t *testing.T) {
	t.Parallel()
	e := newTestEnvironment(t)
	ctx := t.Context()
	assets := &readableAssets{}
	e.service.assets = assets
	request := validDraftRequest()
	request.Assets = []contract.Asset{{Key: "logo", Kind: "image", MIME: "image/png", Size: 2, Digest: strings.Repeat("a", 64), ObjectKey: "experiences/approved/logo", ObjectVersion: "v1"}}
	request.Theme.LogoAssetKey = "logo"
	created, err := e.service.Create(ctx, e.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	published := mustPublish(t, e, created)
	scope := e.authority.TenantScope()
	session := verificationIDForTest(t, 8)
	if _, body, err := e.service.OpenPinnedAsset(ctx, scope, session, "logo"); !errors.Is(err, ErrNotFound) || body != nil {
		t.Fatalf("unpinned read = %v", err)
	}
	if _, err := e.repository.FindPin(ctx, scope, session); !errors.Is(err, ErrNotFound) {
		t.Fatal("image read created a pin")
	}
	if _, err := e.service.ResolveForSession(ctx, scope, session, ResolutionRequest{Workflow: "capture.identity", Country: "NG"}); err != nil {
		t.Fatal(err)
	}
	request.Assets = append([]contract.Asset(nil), request.Assets...)
	request.Assets[0].ObjectVersion = "v2"
	updated, err := e.service.UpdateDraft(ctx, e.authority, created.ID, published.Revision, request)
	if err != nil {
		t.Fatal(err)
	}
	latest := mustPublish(t, e, updated)
	asset, body, err := e.service.OpenPinnedAsset(ctx, scope, session, "logo")
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(body)
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if readErr != nil || string(raw) != "v1" || asset.ObjectVersion != "v1" {
		t.Fatalf("pin moved: %+v %q %v", asset, raw, readErr)
	}
	if _, body, err := e.service.OpenAsset(ctx, access.Context{}, created.ID, 1, "logo"); !errors.Is(err, access.ErrInsufficientScope) || body != nil {
		t.Fatalf("unauthorised read = %v", err)
	}
	if _, body, err := e.service.OpenAsset(ctx, e.authority, created.ID, 1, "other"); !errors.Is(err, ErrNotFound) || body != nil {
		t.Fatalf("unknown key = %v", err)
	}
	assets.fail = true
	if _, body, err := e.service.OpenPinnedAsset(ctx, scope, session, "logo"); !errors.Is(err, ErrAssetUnavailable) || body != nil {
		t.Fatalf("unapproved storage = %v", err)
	}
	assets.fail = false
	if _, err := e.service.Revoke(ctx, e.authority, created.ID, latest.Revision, "withdraw logo"); err != nil {
		t.Fatal(err)
	}
	if _, body, err := e.service.OpenPinnedAsset(ctx, scope, session, "logo"); !errors.Is(err, ErrNotFound) || body != nil {
		t.Fatalf("revoked asset served: %v", err)
	}
}
