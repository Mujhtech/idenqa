// Package identity owns tenant-scoped subjects and immutable identity provenance.
package identity

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrInvalid identifies an invalid identity command without including personal data.
	ErrInvalid = errors.New("identity: invalid input")
	// ErrNotFound deliberately does not distinguish absent and inaccessible identity data.
	ErrNotFound = errors.New("identity: resource not found")
	// ErrConflict identifies a stale version, branched correction or invalid lifecycle transition.
	ErrConflict = errors.New("identity: conflicting state")
	// ErrUnavailable identifies unavailable key custody or bounded projection coverage.
	ErrUnavailable = errors.New("identity: data unavailable")
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,31})(\.[0-9]{1,18})?$`)
)

// Value keeps precision explicit: even numeric wire values are canonical strings.
// Values are transient sensitive data and must never be logged or placed in tasks.
type Value struct {
	Type string `json:"type"`
	Text string `json:"value"`
}

// Validate checks the closed typed-value vocabulary and canonical representation.
func (v Value) Validate() error {
	if len(v.Text) > 4096 || !utf8.ValidString(v.Text) || strings.ContainsRune(v.Text, 0) {
		return ErrInvalid
	}
	switch v.Type {
	case "string":
		if v.Text == "" {
			return ErrInvalid
		}
	case "boolean":
		if v.Text != "true" && v.Text != "false" {
			return ErrInvalid
		}
	case "integer":
		n, e := strconv.ParseInt(v.Text, 10, 64)
		if e != nil || strconv.FormatInt(n, 10) != v.Text {
			return ErrInvalid
		}
	case "decimal":
		if !decimalPattern.MatchString(v.Text) || canonicalDecimal(v.Text) != v.Text {
			return ErrInvalid
		}
	case "date":
		d, e := time.Parse("2006-01-02", v.Text)
		if e != nil || d.Format("2006-01-02") != v.Text {
			return ErrInvalid
		}
	case "timestamp":
		d, e := time.Parse(time.RFC3339Nano, v.Text)
		if e != nil || d.UTC().Format(time.RFC3339Nano) != v.Text {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func canonicalDecimal(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

// Normalize applies a named deterministic transformation, never locale guessing.
func Normalize(v Value, transformation string) (Value, error) {
	if e := v.Validate(); e != nil {
		return Value{}, e
	}
	switch transformation {
	case "identity.exact.v1":
	case "identity.trim.v1":
		if v.Type != "string" {
			return Value{}, ErrInvalid
		}
		v.Text = strings.TrimSpace(v.Text)
	case "identity.ascii_upper.v1":
		if v.Type != "string" {
			return Value{}, ErrInvalid
		}
		for _, r := range v.Text {
			if r > 127 {
				return Value{}, ErrInvalid
			}
		}
		v.Text = strings.ToUpper(strings.TrimSpace(v.Text))
	default:
		return Value{}, ErrInvalid
	}
	return v, v.Validate()
}

// MaskIdentifier never discloses a complete short identifier.
func MaskIdentifier(value string) string {
	chars := []rune(value)
	if len(chars) <= 4 {
		return "••••"
	}
	return "••••" + string(chars[len(chars)-4:])
}
func validName(s string) bool    { return len(s) >= 3 && len(s) <= 128 && namePattern.MatchString(s) }
func validTime(t time.Time) bool { return !t.IsZero() && t.Location() == time.UTC }
