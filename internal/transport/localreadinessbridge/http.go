// Package localreadinessbridge exposes a fixed, read-only readiness snapshot
// to a colocated deployment agent over the protected Core Unix socket.
package localreadinessbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const ContractVersion = "idenqa.core/local-readiness/v1"

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Snapshot struct {
	Version      string  `json:"version"`
	CoreRevision string  `json:"coreRevision"`
	APIVersion   string  `json:"apiVersion"`
	URIMajor     int     `json:"uriMajor"`
	Checks       []Check `json:"checks"`
}

type Service interface {
	Snapshot(context.Context) (Snapshot, error)
}

type Handler struct{ service Service }

func NewHandler(service Service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("local readiness service is required")
	}
	return &Handler{service: service}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != "/local/v1/readiness" {
		http.Error(writer, `{"code":"not_found"}`, http.StatusNotFound)
		return
	}
	snapshot, err := handler.service.Snapshot(request.Context())
	if err != nil {
		http.Error(writer, `{"code":"readiness_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(snapshot)
}
