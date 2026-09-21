// Package secret owns provider-neutral secret references and resolution.
//
// A reference is a bounded, non-secret identifier such as
// secret://aws/prod/idenqa/runner. Implementations live behind adapters and
// never log, retain, or include resolved plaintext in errors.
package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	// MaxPayloadBytes bounds every resolved secret payload.
	MaxPayloadBytes = 64 * 1024
	maxReference    = 1024
	maxProvider     = 32
	maxVersion      = 256
)

var (
	// ErrInvalid identifies a malformed reference, payload, or operation.
	ErrInvalid = errors.New("secret: invalid reference or payload")
	// ErrNotFound identifies a reference the selected provider does not hold.
	ErrNotFound = errors.New("secret: not found")
	// ErrDenied identifies a provider refusal to release the referenced value.
	ErrDenied = errors.New("secret: access denied")
	// ErrUnavailable identifies a provider or transport availability failure.
	ErrUnavailable = errors.New("secret: unavailable")
)

// Reference is a validated immutable secret identifier. It carries no secret
// value and is safe for audit and diagnostics.
type Reference struct {
	raw      string
	provider string
	path     string
	version  string
}

// ParseReference validates a secret://<provider>/<path> reference with an
// optional single ?version= selector.
func ParseReference(value string) (Reference, error) {
	if value == "" || len(value) > maxReference || strings.TrimSpace(value) != value {
		return Reference{}, ErrInvalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "secret" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Fragment != "" {
		return Reference{}, ErrInvalid
	}
	provider := parsed.Host
	if !validProvider(provider) || !validPath(parsed.Path) {
		return Reference{}, ErrInvalid
	}
	version := ""
	if parsed.RawQuery != "" {
		values, err := url.ParseQuery(parsed.RawQuery)
		if err != nil || len(values) != 1 || len(values["version"]) != 1 {
			return Reference{}, ErrInvalid
		}
		version = values["version"][0]
		if version == "" || len(version) > maxVersion || strings.TrimSpace(version) != version {
			return Reference{}, ErrInvalid
		}
	}
	canonical := "secret://" + provider + parsed.Path
	if version != "" {
		canonical += "?version=" + url.QueryEscape(version)
	}
	if canonical != value {
		return Reference{}, ErrInvalid
	}

	return Reference{raw: canonical, provider: provider, path: parsed.Path, version: version}, nil
}

// String returns the canonical reference text without secret material.
func (reference Reference) String() string { return reference.raw }

// Provider returns the selected provider segment.
func (reference Reference) Provider() string { return reference.provider }

// Path returns the provider-specific path including its leading slash.
func (reference Reference) Path() string { return reference.path }

// Version returns the explicit version selector, or the empty string.
func (reference Reference) Version() string { return reference.version }

// IsZero reports whether the reference has not been initialised.
func (reference Reference) IsZero() bool { return reference.raw == "" }

func validProvider(value string) bool {
	if len(value) == 0 || len(value) > maxProvider || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validPath(value string) bool {
	if len(value) < 2 || len(value) > maxReference || value[0] != '/' || strings.HasSuffix(value, "/") {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f || character == '\\' {
			return false
		}
	}
	if strings.Contains(value, "//") || strings.Contains(value, "/../") ||
		strings.HasSuffix(value, "/..") || strings.Contains(value, "/./") ||
		strings.HasSuffix(value, "/.") {
		return false
	}
	return true
}

// Value is one immutable bounded resolved payload.
type Value struct {
	reference Reference
	version   string
	data      []byte
}

// NewValue validates and snapshots a resolved payload. The provider version is
// an immutable provider-reported revision; it may be empty when the provider
// has no revision concept.
func NewValue(reference Reference, version string, data []byte) (Value, error) {
	if reference.IsZero() || len(data) == 0 || len(data) > MaxPayloadBytes ||
		len(version) > maxVersion || strings.TrimSpace(version) != version {
		return Value{}, ErrInvalid
	}

	return Value{
		reference: reference,
		version:   version,
		data:      append([]byte(nil), data...),
	}, nil
}

// Data returns a defensive copy of the payload.
func (value Value) Data() []byte { return append([]byte(nil), value.data...) }

// Reference returns the exact reference this value was resolved for.
func (value Value) Reference() Reference { return value.reference }

// Version returns the immutable provider-reported revision.
func (value Value) Version() string { return value.version }

// Redacted returns safe diagnostic text without payload material.
func (value Value) Redacted() string {
	return fmt.Sprintf("secret reference=%s version=%s bytes=%d", value.reference, value.version, len(value.data))
}

// Text returns the payload as a single-line string. Credentials and bearer
// values are rejected when they contain control characters so a malformed
// provider payload fails closed instead of corrupting a header or file.
func (value Value) Text() (string, error) {
	if bytes.IndexByte(value.data, 0) >= 0 {
		return "", ErrInvalid
	}
	text := string(value.data)
	if strings.ContainsAny(text, "\r\n") {
		return "", ErrInvalid
	}

	return text, nil
}

// JSON decodes the payload into target as a closed bounded JSON document.
// Unknown fields and trailing content are rejected.
func (value Value) JSON(target any) error {
	if target == nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(value.data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}

	return nil
}

// Resolver releases one bounded payload for an exact reference. Implementations
// must map provider not-found and access-denied outcomes to ErrNotFound and
// ErrDenied, must never retry beyond their SDK defaults, and must not include
// payload bytes in returned errors.
type Resolver interface {
	Resolve(context.Context, Reference) (Value, error)
}
