// Package managedtenantbridge exposes Core's fixed managed-tenant workflow on
// the private credential-bridge Unix socket.
package managedtenantbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/managedtenant"
)

type service interface {
	Provision(context.Context, managedtenant.Request) (managedtenant.Result, error)
	Renew(context.Context, managedtenant.RenewRequest) (managedtenant.Result, error)
	ProvisionSynthetic(context.Context, managedtenant.SyntheticProvisionRequest) (managedtenant.SyntheticProvisionResult, error)
	RunSynthetic(context.Context, managedtenant.SyntheticRunRequest) (managedtenant.SyntheticRunResult, error)
}

type Handler struct{ service service }

func NewHandler(service service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("managed tenant bridge service is required")
	}
	return &Handler{service: service}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, `{"code":"not_found"}`, http.StatusNotFound)
		return
	}
	switch request.URL.Path {
	case "/local/v1/managed-tenants/renew":
		handler.renew(writer, request)
		return
	case "/local/v1/synthetic-tenants/provision":
		handler.syntheticProvision(writer, request)
		return
	case "/local/v1/synthetic-journeys/run":
		handler.syntheticRun(writer, request)
		return
	case "/local/v1/managed-tenants/provision":
	default:
		http.Error(writer, `{"code":"not_found"}`, http.StatusNotFound)
		return
	}
	var input managedtenant.Request
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	result, err := handler.service.Provision(request.Context(), input)
	if err != nil {
		if errors.Is(err, managedtenant.ErrInvalid) {
			http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		http.Error(writer, `{"code":"provision_failed"}`, http.StatusConflict)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func decode[T any](request *http.Request, input *T) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing request data")
	}
	return nil
}

func (handler *Handler) syntheticProvision(writer http.ResponseWriter, request *http.Request) {
	var input managedtenant.SyntheticProvisionRequest
	if err := decode(request, &input); err != nil {
		http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	result, err := handler.service.ProvisionSynthetic(request.Context(), input)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, managedtenant.ErrInvalid) {
			status = http.StatusBadRequest
		}
		http.Error(writer, `{"code":"synthetic_provision_failed"}`, status)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *Handler) syntheticRun(writer http.ResponseWriter, request *http.Request) {
	var input managedtenant.SyntheticRunRequest
	if err := decode(request, &input); err != nil {
		http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	result, err := handler.service.RunSynthetic(request.Context(), input)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, managedtenant.ErrInvalid) {
			status = http.StatusBadRequest
		}
		http.Error(writer, `{"code":"synthetic_run_failed"}`, status)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}

func (handler *Handler) renew(writer http.ResponseWriter, request *http.Request) {
	var input managedtenant.RenewRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	result, err := handler.service.Renew(request.Context(), input)
	if err != nil {
		if errors.Is(err, managedtenant.ErrInvalid) {
			http.Error(writer, `{"code":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		http.Error(writer, `{"code":"renew_failed"}`, http.StatusConflict)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(result)
}
