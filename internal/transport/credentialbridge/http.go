// Package credentialbridge exposes the fixed local administration protocol
// used by the deployment agent. It is intended for a Unix-domain socket only.
package credentialbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maxRequestBytes = 64 << 10

type service interface {
	Issue(context.Context, access.BridgeCommand, access.AdminAction, tenant.Scope, access.IssueInput, access.CredentialSealer) (access.BridgeIssueResult, error)
	Rotate(context.Context, access.BridgeCommand, access.AdminAction, tenant.Scope, access.RotateInput, access.CredentialSealer) (access.BridgeIssueResult, error)
	Revoke(context.Context, access.BridgeCommand, access.AdminAction, tenant.Scope, id.APIKey, int64) (access.Key, bool, error)
	Observe(context.Context, access.BridgeCommand, tenant.Scope) (access.Key, access.CredentialEnvelope, error)
}

// Handler implements the closed, versioned local credential protocol.
type Handler struct{ service service }

// NewHandler constructs the local credential protocol handler.
func NewHandler(service service) (*Handler, error) {
	if service == nil {
		return nil, errors.New("credential bridge HTTP service is required")
	}

	return &Handler{service: service}, nil
}

// ServeHTTP accepts only the explicitly owned issue and observe operations.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	switch request.URL.Path {
	case "/local/v1/credential-commands/issue":
		handler.issue(writer, request)
	case "/local/v1/credential-commands/observe":
		handler.observe(writer, request)
	case "/local/v1/credential-commands/rotate":
		handler.rotate(writer, request)
	case "/local/v1/credential-commands/revoke":
		handler.revoke(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not_found")
	}
}

type commandRequest struct {
	CommandID         string   `json:"commandId"`
	RequestDigest     string   `json:"requestDigest"`
	TenantID          string   `json:"tenantId"`
	Actor             string   `json:"actor,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Label             string   `json:"label,omitempty"`
	Permissions       []string `json:"permissions,omitempty"`
	ExpiresAt         *string  `json:"expiresAt,omitempty"`
	NoExpiry          bool     `json:"noExpiry,omitempty"`
	DeliveryPublicKey string   `json:"deliveryPublicKey,omitempty"`
	TargetKeyID       string   `json:"targetKeyId,omitempty"`
	ExpectedVersion   int64    `json:"expectedVersion,omitempty"`
	OverlapSeconds    int64    `json:"overlapSeconds,omitempty"`
}

func (handler *Handler) rotate(writer http.ResponseWriter, request *http.Request) {
	payload, command, scope, ok := decodeCommand(writer, request)
	if !ok {
		return
	}
	identifier, err := id.ParseAPIKey(payload.TargetKeyID)
	if err != nil || payload.ExpectedVersion < 1 || payload.OverlapSeconds < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	sealer, err := newCredentialSealer(payload.DeliveryPublicKey, payload.CommandID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_delivery_key")
		return
	}
	expiry, ok := expiryFromRequest(writer, payload)
	if !ok {
		return
	}
	patterns, ok := patternsFromRequest(writer, payload.Permissions)
	if !ok {
		return
	}
	result, err := handler.service.Rotate(request.Context(), command, access.AdminAction{Actor: payload.Actor, Reason: payload.Reason}, scope, access.RotateInput{
		KeyID: identifier, Expiry: expiry, Overlap: time.Duration(payload.OverlapSeconds) * time.Second, Patterns: patterns,
	}, sealer)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	response := responseFor(payload.CommandID, result.Key, result.Created)
	response.CredentialEnvelope = &result.Envelope
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) revoke(writer http.ResponseWriter, request *http.Request) {
	payload, command, scope, ok := decodeCommand(writer, request)
	if !ok {
		return
	}
	identifier, err := id.ParseAPIKey(payload.TargetKeyID)
	if err != nil || payload.ExpectedVersion < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	key, created, err := handler.service.Revoke(request.Context(), command, access.AdminAction{Actor: payload.Actor, Reason: payload.Reason}, scope, identifier, payload.ExpectedVersion)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, responseFor(payload.CommandID, key, created))
}

type keyResponse struct {
	CommandID          string                     `json:"commandId"`
	KeyID              string                     `json:"keyId"`
	TenantID           string                     `json:"tenantId"`
	Label              string                     `json:"label"`
	Permissions        []string                   `json:"permissions"`
	Version            int64                      `json:"version"`
	CreatedAt          string                     `json:"createdAt"`
	ExpiresAt          *string                    `json:"expiresAt"`
	CredentialEnvelope *access.CredentialEnvelope `json:"credentialEnvelope,omitempty"`
	Created            bool                       `json:"created"`
}

func (handler *Handler) issue(writer http.ResponseWriter, request *http.Request) {
	payload, command, scope, ok := decodeCommand(writer, request)
	if !ok {
		return
	}
	sealer, err := newCredentialSealer(payload.DeliveryPublicKey, payload.CommandID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_delivery_key")
		return
	}
	patterns, ok := patternsFromRequest(writer, payload.Permissions)
	if !ok {
		return
	}
	expiry, ok := expiryFromRequest(writer, payload)
	if !ok {
		return
	}
	result, err := handler.service.Issue(request.Context(), command, access.AdminAction{
		Actor: payload.Actor, Reason: payload.Reason,
	}, scope, access.IssueInput{Label: payload.Label, Patterns: patterns, Expiry: expiry}, sealer)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	response := responseFor(payload.CommandID, result.Key, result.Created)
	response.CredentialEnvelope = &result.Envelope
	writeJSON(writer, http.StatusOK, response)
}

func patternsFromRequest(writer http.ResponseWriter, permissions []string) ([]access.Pattern, bool) {
	patterns := make([]access.Pattern, len(permissions))
	for index, permission := range permissions {
		parsed, err := access.ParsePattern(permission)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request")
			return nil, false
		}
		patterns[index] = parsed
	}
	return patterns, true
}

func expiryFromRequest(writer http.ResponseWriter, payload commandRequest) (access.ExpiryIntent, bool) {
	switch {
	case payload.NoExpiry && payload.ExpiresAt == nil:
		return access.WithoutExpiry(), true
	case !payload.NoExpiry && payload.ExpiresAt != nil:
		value, err := time.Parse(time.RFC3339Nano, *payload.ExpiresAt)
		if err == nil {
			return access.ExpiringAt(value), true
		}
	}
	writeError(writer, http.StatusBadRequest, "invalid_request")
	return access.ExpiryIntent{}, false
}

func (handler *Handler) observe(writer http.ResponseWriter, request *http.Request) {
	payload, command, scope, ok := decodeCommand(writer, request)
	if !ok {
		return
	}
	key, envelope, err := handler.service.Observe(request.Context(), command, scope)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	response := responseFor(payload.CommandID, key, false)
	response.CredentialEnvelope = &envelope
	writeJSON(writer, http.StatusOK, response)
}

func decodeCommand(writer http.ResponseWriter, request *http.Request) (commandRequest, access.BridgeCommand, tenant.Scope, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload commandRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return commandRequest{}, access.BridgeCommand{}, tenant.Scope{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return commandRequest{}, access.BridgeCommand{}, tenant.Scope{}, false
	}
	digestBytes, err := hex.DecodeString(payload.RequestDigest)
	if err != nil || len(digestBytes) != sha256.Size {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return commandRequest{}, access.BridgeCommand{}, tenant.Scope{}, false
	}
	var digest [sha256.Size]byte
	copy(digest[:], digestBytes)
	tenantID, err := id.ParseTenant(payload.TenantID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return commandRequest{}, access.BridgeCommand{}, tenant.Scope{}, false
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return commandRequest{}, access.BridgeCommand{}, tenant.Scope{}, false
	}

	return payload, access.BridgeCommand{ID: payload.CommandID, RequestDigest: digest}, scope, true
}

func responseFor(commandID string, key access.Key, created bool) keyResponse {
	permissions := key.Grant().Permissions()
	values := make([]string, len(permissions))
	for index, permission := range permissions {
		values[index] = string(permission)
	}
	var expiresAt *string
	if value := key.ExpiresAt(); value != nil {
		formatted := value.Format(time.RFC3339Nano)
		expiresAt = &formatted
	}

	return keyResponse{
		CommandID: commandID, KeyID: key.ID().String(), TenantID: key.TenantID().String(),
		Label: key.Label(), Permissions: values, Version: key.Version(), CreatedAt: key.CreatedAt().Format(time.RFC3339Nano),
		ExpiresAt: expiresAt, Created: created,
	}
}

func writeServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrKeyNotFound):
		writeError(writer, http.StatusNotFound, "command_not_found")
	case errors.Is(err, access.ErrKeyConflict):
		writeError(writer, http.StatusConflict, "command_conflict")
	default:
		writeError(writer, http.StatusUnprocessableEntity, "command_rejected")
	}
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"code": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
