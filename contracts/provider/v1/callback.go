package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Callback processing bounds are enforced before any provider-specific crypto
// runs. Raw callback bytes remain inside the isolated adapter and are never
// echoed through the contract.
const (
	// MaxCallbackBodyBytes bounds one raw provider callback body.
	MaxCallbackBodyBytes = 64 << 10
	// MaxCallbackHeaders bounds one raw provider callback header set.
	MaxCallbackHeaders = 32
	// MaxCallbackHeaderNameBytes bounds one raw provider header name.
	MaxCallbackHeaderNameBytes = 128
	// MaxCallbackHeaderValueBytes bounds one raw provider header value.
	MaxCallbackHeaderValueBytes = 1024
)

// CallbackHeader is one bounded raw provider callback header.
type CallbackHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CallbackEnvelope is the bounded raw callback presented for verification. A
// provider adapter authenticates it against the exact persisted request and
// normalises it into Progress; the envelope itself never leaves the runner.
type CallbackEnvelope struct {
	Method  string           `json:"method"`
	Headers []CallbackHeader `json:"headers"`
	Body    []byte           `json:"body,omitempty"`
}

// Validate rejects unbounded, duplicate, or non-UTF-8 callback envelopes.
func (envelope CallbackEnvelope) Validate() error {
	if !validName(envelope.Method, 16) || len(envelope.Body) == 0 || len(envelope.Body) > MaxCallbackBodyBytes ||
		!utf8.Valid(envelope.Body) || len(envelope.Headers) > MaxCallbackHeaders {
		return invalid("callback", "envelope is invalid")
	}
	names := make([]string, 0, len(envelope.Headers))
	for _, header := range envelope.Headers {
		if !validName(header.Name, MaxCallbackHeaderNameBytes) || !validHeaderValue(header.Value) {
			return invalid("callback.headers", "is invalid")
		}
		names = append(names, strings.ToLower(header.Name))
	}
	sort.Strings(names)
	for index := 1; index < len(names); index++ {
		if names[index] == names[index-1] {
			return invalid("callback.headers", "contains duplicates")
		}
	}
	return nil
}

// CallbackRejectionCode is the closed, transport-safe rejection vocabulary.
// Permission-class codes map to an authorization failure; every other code is
// a malformed or unsupported callback.
type CallbackRejectionCode string

// Stable callback rejection codes.
const (
	CallbackRejectionMalformed     CallbackRejectionCode = "invalid.malformed"
	CallbackRejectionUnsupported   CallbackRejectionCode = "invalid.unsupported"
	CallbackRejectionSignature     CallbackRejectionCode = "permission.signature"
	CallbackRejectionStale         CallbackRejectionCode = "permission.stale"
	CallbackRejectionIdentity      CallbackRejectionCode = "permission.identity"
	CallbackRejectionConfiguration CallbackRejectionCode = "permission.configuration"
)

// CallbackRejection is a typed fail-closed callback rejection. It never carries
// provider payload material.
type CallbackRejection struct {
	Code  CallbackRejectionCode
	Field string
}

// Error returns a bounded, payload-free description.
func (rejection *CallbackRejection) Error() string {
	if rejection == nil {
		return "provider callback rejected"
	}
	if rejection.Field == "" {
		return fmt.Sprintf("provider callback rejected: %s", rejection.Code)
	}
	return fmt.Sprintf("provider callback rejected: %s (%s)", rejection.Code, rejection.Field)
}

// Permission reports whether the rejection is an authentication or
// authorization failure rather than a malformed request.
func (rejection *CallbackRejection) Permission() bool {
	return rejection != nil && strings.HasPrefix(string(rejection.Code), "permission.")
}

// Reject constructs one typed callback rejection.
func Reject(code CallbackRejectionCode, field string) error {
	return &CallbackRejection{Code: code, Field: field}
}

// AsCallbackRejection extracts a typed callback rejection.
func AsCallbackRejection(err error) (*CallbackRejection, bool) {
	var rejection *CallbackRejection
	if errors.As(err, &rejection) {
		return rejection, true
	}
	return nil, false
}

// CallbackVerifier authenticates a raw provider callback against the exact
// persisted request and returns normalised pending or terminal progress. The
// provider-specific signature scheme stays behind the adapter boundary.
type CallbackVerifier interface {
	VerifyCallback(context.Context, Request, CallbackEnvelope) (Progress, error)
}

func validHeaderValue(value string) bool {
	if value == "" || len(value) > MaxCallbackHeaderValueBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == '\r' || character == '\n' || character == 0 {
			return false
		}
	}
	return true
}
