// Package idempotency owns transport-neutral durable command replay contracts.
package idempotency

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const fingerprintDomain = "idq-idempotency-request\x00v1\x00"

var (
	// ErrConflict means a scoped key was reused for different input.
	ErrConflict = errors.New("idempotency: key reused with different input")
	// ErrInProgress means another transaction currently owns the scoped key.
	ErrInProgress = errors.New("idempotency: operation is in progress")
)

// Fingerprint is the canonical request digest persisted with a reservation.
type Fingerprint [sha256.Size]byte

// NewFingerprint hashes an operation's canonical request representation.
func NewFingerprint(canonical []byte) (Fingerprint, error) {
	if len(canonical) == 0 {
		return Fingerprint{}, errors.New("idempotency: canonical request is required")
	}

	return sha256.Sum256(append([]byte(fingerprintDomain), canonical...)), nil
}

// ParseFingerprint validates stored digest material.
func ParseFingerprint(value []byte) (Fingerprint, error) {
	if len(value) != sha256.Size {
		return Fingerprint{}, errors.New("idempotency: fingerprint must contain 32 bytes")
	}
	var fingerprint Fingerprint
	copy(fingerprint[:], value)

	return fingerprint, nil
}

// Bytes returns a defensive fingerprint copy for persistence.
func (fingerprint Fingerprint) Bytes() []byte {
	return append([]byte(nil), fingerprint[:]...)
}

// Request is the complete scope and input identity for one durable command.
type Request struct {
	tenantID    id.Tenant
	principal   Principal
	operation   string
	key         string
	fingerprint Fingerprint
	createdAt   time.Time
	expiresAt   time.Time
}

// PrincipalValue is an authenticated command principal accepted by the
// idempotency boundary. API keys and capture-token records both satisfy it.
type PrincipalValue interface {
	String() string
	IsZero() bool
}

// Principal is the validated, non-secret principal identity retained in a
// durable idempotency scope.
type Principal struct{ value string }

// String returns the stable non-secret principal identifier.
func (principal Principal) String() string { return principal.value }

// IsZero reports whether the principal has not been initialised.
func (principal Principal) IsZero() bool { return principal.value == "" }

// NewRequest validates a command scope and applies its declared retention.
func NewRequest(
	tenantID id.Tenant,
	principal PrincipalValue,
	operation string,
	key string,
	canonical []byte,
	now time.Time,
	retention time.Duration,
) (Request, error) {
	if tenantID.IsZero() || principal == nil || principal.IsZero() ||
		!validPrincipal(principal.String()) || !validOperation(operation) || !validKey(key) ||
		now.IsZero() || retention <= 0 {
		return Request{}, errors.New("idempotency: valid scope, key, time, and retention are required")
	}
	fingerprint, err := NewFingerprint(canonical)
	if err != nil {
		return Request{}, err
	}
	now = now.UTC()

	return Request{
		tenantID:    tenantID,
		principal:   Principal{value: principal.String()},
		operation:   operation,
		key:         key,
		fingerprint: fingerprint,
		createdAt:   now,
		expiresAt:   now.Add(retention),
	}, nil
}

// Result is the safe committed application result retained for exact replay.
type Result struct {
	status int
	body   json.RawMessage
}

// NewResult validates a safe object-shaped result snapshot.
func NewResult(status int, body json.RawMessage) (Result, error) {
	if status < 200 || status > 599 || len(body) == 0 || !json.Valid(body) {
		return Result{}, errors.New("idempotency: valid result status and JSON are required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return Result{}, errors.New("idempotency: result must be a JSON object")
	}

	return Result{status: status, body: append(json.RawMessage(nil), body...)}, nil
}

// Status returns the replay HTTP-compatible status selected by the operation.
func (result Result) Status() int { return result.status }

// Body returns a defensive copy of the safe result snapshot.
func (result Result) Body() json.RawMessage {
	return append(json.RawMessage(nil), result.body...)
}

// TenantID returns the authenticated tenant in the idempotency scope.
func (request Request) TenantID() id.Tenant { return request.tenantID }

// Principal returns the authenticated non-secret actor in the idempotency scope.
func (request Request) Principal() Principal { return request.principal }

// Operation returns the stable command name in the idempotency scope.
func (request Request) Operation() string { return request.operation }

// Key returns the caller-supplied idempotency key.
func (request Request) Key() string { return request.key }

// Fingerprint returns the canonical request identity.
func (request Request) Fingerprint() Fingerprint { return request.fingerprint }

// CreatedAt returns the reservation start time.
func (request Request) CreatedAt() time.Time { return request.createdAt }

// ExpiresAt returns the exclusive replay-retention boundary.
func (request Request) ExpiresAt() time.Time { return request.expiresAt }

func validOperation(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		isLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		isSeparator := character == '_' || character == '.' || character == ':' || character == '-'
		if (index == 0 && !isLetter) || (index > 0 && !isLetter && !isDigit && !isSeparator) {
			return false
		}
	}

	return true
}

func validKey(value string) bool {
	if value == "" || len(value) > 255 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}

	return true
}

func validPrincipal(value string) bool {
	if _, err := id.ParseAPIKey(value); err == nil {
		return true
	}
	_, err := id.ParseCaptureToken(value)
	return err == nil
}

// String deliberately avoids accidentally logging command input identity.
func (Request) String() string { return "[REDACTED]" }

// GoString deliberately avoids accidentally logging command input identity.
func (Request) GoString() string { return "[REDACTED]" }

// ValidateStoredResult converts persistence data through the public invariants.
func ValidateStoredResult(status int32, body []byte) (Result, error) {
	result, err := NewResult(int(status), body)
	if err != nil {
		return Result{}, fmt.Errorf("idempotency: restore result: %w", err)
	}

	return result, nil
}
