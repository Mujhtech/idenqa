package httpapi

import (
	"context"
	"io"
	"net/http"
	"strconv"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type experienceAssetService interface {
	OpenAsset(context.Context, access.Context, id.Experience, uint32, string) (contract.Asset, io.ReadCloser, error)
	OpenPinnedAsset(context.Context, tenant.Scope, id.Verification, string) (contract.Asset, io.ReadCloser, error)
}

func (routes *ExperienceRoutes) asset(writer http.ResponseWriter, request *http.Request) {
	authority, ok := routes.authority(writer, request)
	if !ok {
		return
	}
	identifier, err := routes.identifier(request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	version, err := strconv.ParseUint(chi.URLParam(request, "version"), 10, 32)
	if err != nil || version == 0 {
		routes.problem(writer, request, experience.ErrInvalid)
		return
	}
	service, ok := routes.service.(experienceAssetService)
	if !ok {
		routes.problem(writer, request, experience.ErrAssetUnavailable)
		return
	}
	asset, body, err := service.OpenAsset(request.Context(), authority, identifier, uint32(version), chi.URLParam(request, "assetKey"))
	routes.writeAsset(writer, request, asset, body, err)
}

func (routes *ExperienceRoutes) pinnedAsset(writer http.ResponseWriter, request *http.Request) {
	actor, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	service, ok := routes.service.(experienceAssetService)
	if !ok {
		routes.problem(writer, request, experience.ErrAssetUnavailable)
		return
	}
	asset, body, err := service.OpenPinnedAsset(request.Context(), actor.TenantScope(), actor.Session().ID(), chi.URLParam(request, "assetKey"))
	routes.writeAsset(writer, request, asset, body, err)
}

func (routes *ExperienceRoutes) writeAsset(writer http.ResponseWriter, request *http.Request, asset contract.Asset, body io.ReadCloser, err error) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	if body != nil {
		defer func() { _ = body.Close() }()
	}
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	if body == nil || asset.Size < 1 || asset.Size > contract.MaxAssetBytes || (asset.MIME != "image/png" && asset.MIME != "image/jpeg") {
		routes.problem(writer, request, experience.ErrAssetUnavailable)
		return
	}
	// Complete the bounded read before writing headers, so truncation and a
	// changed digest fail closed instead of returning a partial successful image.
	raw, err := io.ReadAll(io.LimitReader(body, asset.Size+1))
	if err != nil || int64(len(raw)) != asset.Size || contract.DigestCanonical(raw) != asset.Digest {
		routes.problem(writer, request, experience.ErrAssetUnavailable)
		return
	}
	writer.Header().Set("Content-Type", asset.MIME)
	writer.Header().Set("Content-Length", strconv.FormatInt(asset.Size, 10))
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if _, err := writer.Write(raw); err != nil {
		routes.logger.ErrorContext(request.Context(), "write experience asset response")
	}
}
