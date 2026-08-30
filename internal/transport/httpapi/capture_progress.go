package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// CaptureProgressFinder is the token-scoped accepted-progress capability consumed by HTTP.
type CaptureProgressFinder interface {
	Find(context.Context, evidence.UploadPrincipal) (evidence.CaptureProgress, error)
}

// CaptureProgressRoutes adapts authoritative capture recovery to HTTP.
type CaptureProgressRoutes struct {
	capture *CaptureAccessMiddleware
	finder  CaptureProgressFinder
	logger  *slog.Logger
}

// NewCaptureProgressRoutes constructs the capture progress read surface.
func NewCaptureProgressRoutes(
	capture *CaptureAccessMiddleware,
	finder CaptureProgressFinder,
	logger *slog.Logger,
) (*CaptureProgressRoutes, error) {
	if capture == nil || finder == nil || logger == nil {
		return nil, errors.New("capture progress route dependencies are required")
	}

	return &CaptureProgressRoutes{capture: capture, finder: finder, logger: logger}, nil
}

// Register adds the exact capture-token-scoped recovery endpoint.
func (routes *CaptureProgressRoutes) Register(router chi.Router) {
	router.With(routes.capture.Authenticate).Get("/v1/capture/progress", routes.find)
}

func (routes *CaptureProgressRoutes) find(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, accessFailure(), true)

		return
	}
	progress, err := routes.finder.Find(request.Context(), evidence.UploadPrincipal{
		Scope: captureContext.TenantScope(), CaptureTokenID: captureContext.TokenID(),
		VerificationID: captureContext.Session().ID(),
	})
	if err != nil {
		if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
			routes.logger.ErrorContext(request.Context(), "write capture progress problem response")
		}

		return
	}
	completions := make([]openapiv1.CaptureCompletion, len(progress.Completions))
	digest := sha256.New()
	_, _ = digest.Write([]byte(progress.VerificationID.String()))
	for index, completion := range progress.Completions {
		completions[index] = openapiv1.CaptureCompletion{
			UploadID: completion.UploadID.String(), EvidenceID: completion.EvidenceID.String(),
			RequirementKey: completion.RequirementKey, EvidenceType: string(completion.EvidenceType),
			Artefact: string(completion.Artefact), AcquisitionMethod: string(completion.AcquisitionMethod),
			FallbackCondition: optionalCaptureFallbackCondition(completion.FallbackCondition),
		}
		for _, value := range []string{
			completion.UploadID.String(), completion.EvidenceID.String(), completion.RequirementKey,
			string(completion.EvidenceType), string(completion.Artefact),
			string(completion.AcquisitionMethod), completion.FallbackCondition,
		} {
			_, _ = digest.Write([]byte{0})
			_, _ = digest.Write([]byte(value))
		}
	}
	etag := strconv.Quote("sha256:" + hex.EncodeToString(digest.Sum(nil)))
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "private, no-cache")
	if matchesEntityTag(request.Header.Values("If-None-Match"), etag) {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	if err := respond.JSON(writer, request, http.StatusOK, openapiv1.CaptureProgress{
		VerificationID: progress.VerificationID.String(), Completions: completions,
	}); err != nil {
		routes.logger.ErrorContext(request.Context(), "write capture progress response")
	}
}

func matchesEntityTag(values []string, current string) bool {
	for _, value := range values {
		for candidate := range strings.SplitSeq(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || candidate == current {
				return true
			}
		}
	}
	return false
}
