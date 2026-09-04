package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// NativeBootstrapBinder verifies and consumes one native bootstrap redemption.
type NativeBootstrapBinder interface {
	Bind(context.Context, verification.CaptureContext, string, verification.NativeBootstrapRequest) (verification.Session, error)
}

// NativeBootstrapRoutes exposes proof-bound native capture bootstrap.
type NativeBootstrapRoutes struct {
	capture *CaptureAccessMiddleware
	service NativeBootstrapBinder
	catalog evidence.Catalog
	logger  *slog.Logger
}

// NewNativeBootstrapRoutes constructs native bootstrap routes.
func NewNativeBootstrapRoutes(capture *CaptureAccessMiddleware, service NativeBootstrapBinder, catalog evidence.Catalog, logger *slog.Logger) (*NativeBootstrapRoutes, error) {
	if capture == nil || service == nil || catalog.IsZero() || logger == nil {
		return nil, errors.New("native bootstrap route dependencies are invalid")
	}
	return &NativeBootstrapRoutes{capture: capture, service: service, catalog: catalog, logger: logger}, nil
}

// Register mounts the single-use native bootstrap endpoint.
func (routes *NativeBootstrapRoutes) Register(router chi.Router) {
	router.With(routes.capture.Authenticate).Post("/v1/capture/native/bootstrap", routes.bootstrap)
}

type nativeBootstrapRequest struct {
	ApplicationID  string             `json:"application_id"`
	ProofKey       string             `json:"proof_key"`
	ProofCreatedAt int64              `json:"proof_created_at"`
	ProofAlgorithm string             `json:"proof_algorithm"`
	ProofFormat    string             `json:"proof_format"`
	Proof          string             `json:"proof"`
	Attestation    string             `json:"attestation,omitempty"`
	Capabilities   nativeCapabilities `json:"capabilities"`
}

type nativeCapabilities struct {
	Platform                  string   `json:"platform"`
	SDKVersion                string   `json:"sdk_version"`
	ImplementedMethods        []string `json:"implemented_methods"`
	CurrentlyAvailableMethods []string `json:"currently_available_methods"`
}

func (routes *NativeBootstrapRoutes) bootstrap(writer http.ResponseWriter, request *http.Request) {
	authority, ok := CaptureContext(request.Context())
	credential, hasCredential := bearerCredential(request.Header.Values("Authorization"))
	if !ok || !hasCredential {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	body, err := decodeJSONBody[nativeBootstrapRequest](request)
	if err != nil {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	session, err := routes.service.Bind(request.Context(), authority, credential, verification.NativeBootstrapRequest{
		ApplicationID: body.ApplicationID, ProofKey: body.ProofKey, ProofCreatedAt: body.ProofCreatedAt,
		ProofAlgorithm: body.ProofAlgorithm, ProofFormat: body.ProofFormat, Proof: body.Proof, Attestation: body.Attestation,
		Capabilities: verification.NativeCapabilities{Platform: body.Capabilities.Platform, SDKVersion: body.Capabilities.SDKVersion,
			ImplementedMethods: body.Capabilities.ImplementedMethods, CurrentlyAvailableMethods: body.Capabilities.CurrentlyAvailableMethods},
	})
	if errors.Is(err, verification.ErrSessionNotFound) {
		writeCaptureProblem(writer, request, routes.logger, access.ErrInvalidCaptureToken, true)
		return
	}
	if err != nil {
		if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
			routes.logger.ErrorContext(request.Context(), "write native bootstrap failure")
		}
		return
	}
	resource, err := verificationSessionResponse(routes.catalog, session)
	if err != nil {
		if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
			routes.logger.ErrorContext(request.Context(), "write native bootstrap session failure")
		}
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write native bootstrap response")
	}
}
