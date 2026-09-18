// Package api composes the initial public API process.
package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/go-chi/chi/v5"
)

const (
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
	serverIOGrace     = 30 * time.Second

	// internalAPIVersionPrefix is the versioned boundary for process-owned
	// routes that are not part of the public API.
	internalAPIVersionPrefix = "/internal/v1"
)

// RouteRegistrar registers a public route group. Register receives the
// sub-router mounted at httpapi.VersionPrefix, so paths are relative to /v1.
type RouteRegistrar interface {
	Register(chi.Router)
}

// InternalRouteRegistrar registers a process-owned route group outside the
// public API. RegisterInternal receives the sub-router mounted at
// internalAPIVersionPrefix, so paths are relative to /internal/v1.
type InternalRouteRegistrar interface {
	RegisterInternal(chi.Router)
}

// NewServer builds the API server with conservative timeout defaults.
func NewServer(
	address string,
	state *health.State,
	dependencies httpapi.Dependencies,
	routes []RouteRegistrar,
	internalRoutes []InternalRouteRegistrar,
) (*http.Server, error) {
	handler, err := NewHandler(state, dependencies, routes, internalRoutes)
	if err != nil {
		return nil, err
	}
	requestTimeout := dependencies.RequestTimeout
	if dependencies.EvidenceUploadPolicy != nil &&
		dependencies.EvidenceUploadPolicy.AttemptTimeout() > requestTimeout {
		requestTimeout = dependencies.EvidenceUploadPolicy.AttemptTimeout()
	}
	requestTimeout += serverIOGrace

	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       requestTimeout,
		WriteTimeout:      requestTimeout,
		IdleTimeout:       idleTimeout,
	}, nil
}

// NewHandler returns the health and protected public API surfaces.
func NewHandler(
	state *health.State,
	dependencies httpapi.Dependencies,
	routes []RouteRegistrar,
	internalRoutes []InternalRouteRegistrar,
) (http.Handler, error) {
	if len(routes) == 0 {
		return nil, errors.New("API routes are required")
	}
	for _, registrar := range routes {
		if registrar == nil {
			return nil, errors.New("API routes are required")
		}
	}

	return httpapi.New(dependencies, func(router chi.Router) {
		router.Get("/livez", live)
		router.Get("/startupz", func(writer http.ResponseWriter, _ *http.Request) {
			healthResponse(writer, state.Started())
		})
		router.Get("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
			healthResponse(writer, state.Ready())
		})
		router.Route(internalAPIVersionPrefix, func(internal chi.Router) {
			for _, registrar := range internalRoutes {
				registrar.RegisterInternal(internal)
			}
		})
		router.Route(httpapi.VersionPrefix, func(versioned chi.Router) {
			for _, registrar := range routes {
				registrar.Register(versioned)
			}
		})
	})
}

func live(writer http.ResponseWriter, _ *http.Request) {
	healthResponse(writer, true)
}

func healthResponse(writer http.ResponseWriter, healthy bool) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")

	status := http.StatusOK
	body := "ok\n"
	if !healthy {
		status = http.StatusServiceUnavailable
		body = "unavailable\n"
	}
	writer.WriteHeader(status)

	if _, err := io.WriteString(writer, body); err != nil {
		return
	}
}
