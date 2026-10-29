// Package usagebridge exposes only bounded regional receipt delivery on the
// private Core-local socket. It cannot invoke a provider or retrieve its result.
package usagebridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Store is the narrow receipt delivery port consumed by the local bridge.
type Store interface {
	Read(context.Context, string, int) ([]usagev1.Receipt, error)
	Acknowledge(context.Context, string, string, string, time.Time) error
}

type subscriptionStore interface {
	ReadSubscription(context.Context, string, int, string, string) ([]usagev1.Receipt, error)
	AcknowledgeSubscription(context.Context, string, string, string, time.Time, string, string) error
	CoverageSubscription(context.Context, string, string, string) (usagev1.Coverage, error)
}

// Handler serves the fixed private regional receipt routes.
type Handler struct {
	store Store
	now   func() time.Time
}

// NewHandler constructs the local receipt transport.
func NewHandler(store Store, now func() time.Time) (*Handler, error) {
	if store == nil || now == nil {
		return nil, errors.New("usage bridge dependencies are required")
	}
	return &Handler{store, now}, nil
}

type input struct {
	CoreTenantID  string `json:"coreTenantId"`
	Limit         int    `json:"limit,omitempty"`
	ID            string `json:"id,omitempty"`
	Digest        string `json:"digest,omitempty"`
	ConsumerID    string `json:"consumerId,omitempty"`
	RoutingDigest string `json:"routingDigest,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost || (r.URL.Path != "/local/v1/usage/receipts/read" && r.URL.Path != "/local/v1/usage/receipts/ack" && r.URL.Path != "/local/v1/usage/receipts/coverage") {
		http.NotFound(w, r)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	var v input
	if decoder.Decode(&v) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	if _, err := id.ParseTenant(v.CoreTenantID); err != nil {
		http.Error(w, `{"code":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	if r.URL.Path == "/local/v1/usage/receipts/coverage" {
		store, ok := h.store.(subscriptionStore)
		if !ok || v.ConsumerID == "" || v.RoutingDigest == "" || v.Limit != 0 || v.ID != "" || v.Digest != "" {
			http.Error(w, `{"code":"coverage_unavailable"}`, http.StatusConflict)
			return
		}
		coverage, err := store.CoverageSubscription(r.Context(), v.CoreTenantID, v.ConsumerID, v.RoutingDigest)
		if err != nil {
			http.Error(w, `{"code":"coverage_unavailable"}`, http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(coverage)
		return
	}
	var receipts []usagev1.Receipt
	var err error
	switch {
	case v.ConsumerID != "" || v.RoutingDigest != "":
		store, ok := h.store.(subscriptionStore)
		if !ok || v.ConsumerID == "" || v.RoutingDigest == "" {
			http.Error(w, `{"code":"subscription_unavailable"}`, http.StatusConflict)
			return
		}
		if r.URL.Path == "/local/v1/usage/receipts/ack" {
			err = store.AcknowledgeSubscription(r.Context(), v.CoreTenantID, v.ID, v.Digest, h.now().UTC(), v.ConsumerID, v.RoutingDigest)
		} else {
			receipts, err = store.ReadSubscription(r.Context(), v.CoreTenantID, v.Limit, v.ConsumerID, v.RoutingDigest)
		}
	case r.URL.Path == "/local/v1/usage/receipts/ack":
		err = h.store.Acknowledge(r.Context(), v.CoreTenantID, v.ID, v.Digest, h.now().UTC())
	default:
		receipts, err = h.store.Read(r.Context(), v.CoreTenantID, v.Limit)
	}

	if err != nil {
		http.Error(w, `{"code":"usage_receipt_unavailable"}`, http.StatusConflict)
		return
	}
	if r.URL.Path == "/local/v1/usage/receipts/ack" {
		_ = json.NewEncoder(w).Encode(struct {
			Acknowledged bool `json:"acknowledged"`
		}{true})
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Items []usagev1.Receipt `json:"items"`
	}{receipts})
}
