package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/managedlaunch"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

// ManagedLaunchService is the signed workload capability consumed by the internal route.
type ManagedLaunchService interface {
	Create(context.Context, managedlaunch.Envelope) (verification.CreatedSession, error)
}

// ManagedLaunchRoutes adapts the signed Cloud workload boundary to HTTP.
type ManagedLaunchRoutes struct {
	handlerBase
	service ManagedLaunchService
}

// NewManagedLaunchRoutes constructs the internal managed-launch route.
func NewManagedLaunchRoutes(service ManagedLaunchService, logger *slog.Logger) (*ManagedLaunchRoutes, error) {
	if service == nil || logger == nil {
		return nil, errors.New("managed launch route dependencies are required")
	}
	return &ManagedLaunchRoutes{handlerBase: newHandlerBase(logger, "managed launch"), service: service}, nil
}

// RegisterInternal mounts the route below /internal/v1.
func (routes *ManagedLaunchRoutes) RegisterInternal(router chi.Router) {
	router.Post("/managed-launch/sessions", routes.create)
}

func (routes *ManagedLaunchRoutes) create(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	envelope, err := decodeJSONBody[managedlaunch.Envelope](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	created, err := routes.service.Create(request.Context(), envelope)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/v1/verifications/"+created.Session.ID().String())
	routes.writeJSON(writer, request, http.StatusCreated, struct {
		VerificationID        string `json:"verificationId"`
		CaptureToken          string `json:"captureToken"`
		OutcomeToken          string `json:"outcomeToken"`
		Region                string `json:"region"`
		ProfileID             string `json:"profileId"`
		ProfileRevision       uint32 `json:"profileRevision"`
		TenantWebhooksEnabled bool   `json:"tenantWebhooksEnabled"`
	}{
		VerificationID: created.Session.ID().String(), CaptureToken: created.CaptureToken.Reveal(),
		OutcomeToken: created.OutcomeToken.Reveal(), Region: created.Session.Region(),
		ProfileID: created.Session.ProfileID().String(), ProfileRevision: created.Session.ProfileRevision(),
		TenantWebhooksEnabled: true,
	})
}
