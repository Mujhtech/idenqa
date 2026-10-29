// Package admissionbridge installs independently signed policy through the
// protected Core-local socket. No operation IDs, receipts or evidence leave it.
package admissionbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
)

// Installer is the local transport's only policy mutation capability.
type Installer interface {
	Install(context.Context, admissionv1.Snapshot) error
}

// Handler exposes one fixed bounded installation operation.
type Handler struct {
	installer Installer
	summary   SummaryReader
}

// SummaryReader exposes only bounded regional counter metadata.
type SummaryReader interface {
	Summary(context.Context, admissionv1.Scope) (admissionv1.Summary, error)
}

// WithSummary enables the separately scoped read operation.
func (h *Handler) WithSummary(reader SummaryReader) *Handler { h.summary = reader; return h }

// NewHandler constructs the private policy installation boundary.
func NewHandler(installer Installer) (*Handler, error) {
	if installer == nil {
		return nil, admission.ErrInvalid
	}
	return &Handler{installer: installer}, nil
}

// ServeHTTP acknowledges only a committed, verified snapshot installation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost && r.URL.Path == "/local/v1/admission/summary" {
		h.serveSummary(w, r)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/local/v1/admission/snapshots/install" {
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		http.Error(w, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	var snapshot admissionv1.Snapshot
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 192<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err := h.installer.Install(ctx, snapshot)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "admission_unavailable"
		if errors.Is(err, admission.ErrInvalid) {
			status = http.StatusBadRequest
			code = "invalid_policy"
		}
		if errors.Is(err, admission.ErrConflict) {
			status = http.StatusConflict
			code = "policy_conflict"
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Revision int64  `json:"revision"`
		ID       string `json:"id"`
	}{snapshot.Revision, snapshot.ID})
}

func (h *Handler) serveSummary(w http.ResponseWriter, r *http.Request) {
	if h.summary == nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		http.Error(w, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	var scope admissionv1.Scope
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&scope) != nil || decoder.Decode(&struct{}{}) != io.EOF || !scope.Valid() {
		http.Error(w, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := h.summary.Summary(ctx, scope)
	if err != nil {
		http.Error(w, `{"code":"admission_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
