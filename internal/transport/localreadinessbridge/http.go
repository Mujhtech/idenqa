// Package localreadinessbridge exposes a fixed, read-only readiness snapshot
// to a colocated deployment agent over the protected Core Unix socket.
package localreadinessbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ContractVersion identifies the local readiness response contract.
const ContractVersion = "idenqa.core/local-readiness/v1"

// Check reports the state of one local readiness dependency.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Snapshot contains the readiness state exposed by the local bridge.
type Snapshot struct {
	Version      string  `json:"version"`
	CoreRevision string  `json:"coreRevision"`
	APIVersion   string  `json:"apiVersion"`
	URIMajor     int     `json:"uriMajor"`
	Checks       []Check `json:"checks"`
}

// Service provides a sanitized snapshot of local workload readiness.
type Service interface {
	Snapshot(context.Context) (Snapshot, error)
}

// Handler serves the local readiness HTTP contract.
type Handler struct{ service Service }

// NewHandler constructs a readiness handler backed by service.
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
