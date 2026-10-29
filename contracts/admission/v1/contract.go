// Package v1 defines optional, content-free operational admission snapshots.
// Core remains independently usable without an external snapshot issuer.
package v1

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Schema identifies the signed snapshot meaning, independently of its issuer.
const Schema = "idenqa-core.admission-snapshot.v1"

// ErrInvalid identifies malformed or ambiguous snapshot meaning.
var ErrInvalid = errors.New("admission contract: invalid snapshot")

var token = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var operation = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,511}$`)

// Scope identifies one Core tenant in an exact installation and region.
type Scope struct {
	TenantID     string `json:"tenantId"`
	DeploymentID string `json:"deploymentId"`
	Region       string `json:"region"`
}

// Valid reports whether every scope component is an opaque bounded identifier.
func (s Scope) Valid() bool {
	return token.MatchString(s.TenantID) && token.MatchString(s.DeploymentID) && token.MatchString(s.Region)
}

// Limit is an explicitly configured operational ceiling, never a price or meter.
// Rate/usage windows are positive integer nanoseconds; other windows are zero.
type Limit struct {
	Kind    string        `json:"kind"`
	Key     string        `json:"key"`
	Maximum int64         `json:"maximum"`
	Window  time.Duration `json:"windowNanoseconds"`
}

// Budget reserves an explicit quantity for an admitted operation. Lifetime is
// window, terminal or delete; the owning workload proves that resource lifetime.
// Storage quantities are reserved budgets, not assertions of billable bytes.
type Budget struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Quantity int64  `json:"quantity"`
	Lifetime string `json:"lifetime"`
}

// Rule binds an exact workload operation to required capability versions and
// all applicable reservations. Application authority remains independently required.
type Rule struct {
	Operation    string           `json:"operation"`
	Capabilities map[string]int64 `json:"capabilities"`
	Budgets      []Budget         `json:"budgets"`
}

// Snapshot is signed operational policy. Its issuer must be pinned independently.
// Validity, capabilities, quantities and limits have no implicit defaults.
type Snapshot struct {
	Schema       string           `json:"schema"`
	ID           string           `json:"id"`
	Scope        Scope            `json:"scope"`
	Revision     int64            `json:"revision"`
	Issuer       string           `json:"issuer"`
	IssuedAt     time.Time        `json:"issuedAt"`
	ValidFrom    time.Time        `json:"validFrom"`
	ValidUntil   time.Time        `json:"validUntil"`
	Status       string           `json:"status"`
	Capabilities map[string]int64 `json:"capabilities"`
	Limits       []Limit          `json:"limits"`
	Rules        []Rule           `json:"rules"`
	Proof        []byte           `json:"proof,omitempty"`
}

// SigningBytes defines the versioned canonical Go JSON representation. Issuers
// must sort rule and budget arrays before signing; validation rejects ambiguity.
func (s Snapshot) SigningBytes() []byte {
	s.Proof = nil
	raw, _ := json.Marshal(s)
	return append([]byte(Schema+"\x00"), raw...)
}

// Verify checks both the closed snapshot meaning and the pinned signature.
func (s Snapshot) Verify(key ed25519.PublicKey) error {
	if s.Validate() != nil || len(key) != ed25519.PublicKeySize || len(s.Proof) != ed25519.SignatureSize || !ed25519.Verify(key, s.SigningBytes(), s.Proof) {
		return ErrInvalid
	}
	return nil
}

// Validate checks shape, bounded vocabulary and deterministic array order.
func (s Snapshot) Validate() error {
	if s.Schema != Schema || !token.MatchString(s.ID) || !s.Scope.Valid() || s.Revision < 1 || !token.MatchString(s.Issuer) || s.IssuedAt.IsZero() || s.ValidFrom.Before(s.IssuedAt) || !s.ValidUntil.After(s.ValidFrom) || !slices.Contains([]string{"active", "scheduled", "grace", "suspended", "expired", "revoked"}, s.Status) || len(s.Capabilities) == 0 || len(s.Capabilities) > 64 || len(s.Limits) > 64 || len(s.Rules) == 0 || len(s.Rules) > 64 {
		return ErrInvalid
	}
	for key, version := range s.Capabilities {
		if !token.MatchString(key) || version < 1 {
			return ErrInvalid
		}
	}
	limits := map[string]Limit{}
	prior := ""
	for _, limit := range s.Limits {
		name := limit.Kind + ":" + limit.Key
		if !slices.Contains([]string{"rate", "usage", "concurrency", "capacity", "count", "storage"}, limit.Kind) || !token.MatchString(limit.Key) || limit.Maximum < 0 || limit.Window < 0 || ((limit.Kind == "rate" || limit.Kind == "usage") != (limit.Window > 0)) || name <= prior {
			return ErrInvalid
		}
		limits[name] = limit
		prior = name
	}
	prior = ""
	for _, rule := range s.Rules {
		if !operation.MatchString(rule.Operation) || rule.Operation <= prior || len(rule.Capabilities) == 0 || len(rule.Capabilities) > 64 || len(rule.Budgets) > 64 {
			return ErrInvalid
		}
		prior = rule.Operation
		for key, version := range rule.Capabilities {
			if !token.MatchString(key) || version < 1 {
				return ErrInvalid
			}
		}
		previous := ""
		for _, budget := range rule.Budgets {
			name := budget.Kind + ":" + budget.Key
			if _, ok := limits[name]; !ok || name <= previous || budget.Quantity < 1 {
				return ErrInvalid
			}
			previous = name
			want := "delete"
			switch budget.Kind {
			case "rate", "usage":
				want = "window"
			case "concurrency", "capacity":
				want = "terminal"
			}
			if budget.Lifetime != want {
				return ErrInvalid
			}
		}
	}
	if len(s.SigningBytes()) > 128*1024 {
		return ErrInvalid
	}
	return nil
}

// VerificationOperation binds new-session admission to the immutable published
// profile revision and policy. There is no wildcard workflow fallback.
func VerificationOperation(profile string, revision uint32, policy string) (string, error) {
	if !token.MatchString(profile) || revision == 0 || !token.MatchString(policy) {
		return "", ErrInvalid
	}
	raw, _ := json.Marshal(revision)
	return strings.Join([]string{"verifications.create", profile, string(raw), policy}, "/"), nil
}

// RecaptureOperation names an explicitly authorised new recapture session. It
// remains distinct from ordinary creation and never inherits its quantities.
func RecaptureOperation(profile string, revision uint32, policy string) (string, error) {
	create, err := VerificationOperation(profile, revision, policy)
	if err != nil {
		return "", err
	}
	return strings.Replace(create, "verifications.create/", "verifications.recapture/", 1), nil
}
