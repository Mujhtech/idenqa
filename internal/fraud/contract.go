// Package fraud owns tenant-scoped correlation and deterministic risk rules.
package fraud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

var (
	// ErrInvalid rejects malformed or untrusted input.
	ErrInvalid = errors.New("fraud: invalid input")
	// ErrConflict reports a stale revision or changed source identity.
	ErrConflict = errors.New("fraud: conflicting revision or receipt")
	// ErrNotFound hides missing and other-tenant resources.
	ErrNotFound = errors.New("fraud: resource not found")
	// ErrUnavailable reports missing cryptographic custody.
	ErrUnavailable = errors.New("fraud: key custody unavailable")
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
)

// Kinds are purpose-separated token domains. Subject is an explicit tenant
// reference; Core's verification-local subject ID is never treated as a person.
var Kinds = []string{"subject", "device", "identifier", "document", "portrait", "address", "provider_event", "network", "capture"}

// Signals is the closed no-risk requirement vocabulary.
var Signals = []string{"device_reuse", "identifier_reuse", "document_reuse", "portrait_reuse", "capture_replay", "verification_velocity", "network_anomaly", "provider_inconsistency", "identity_inconsistency", "session_timing", "failed_liveness", "high_risk_model"}

// Source authorizes one tenant integration to contribute a specific token
// namespace. This is a tenant attestation, never device or issuer attestation.
type Source struct {
	KeyID             string   `json:"key_id"`
	Namespace         string   `json:"namespace"`
	Kinds             []string `json:"kinds"`
	PortraitPermitted bool     `json:"portrait_permitted"`
}

// Mapping pins a risk interpretation to a normalized, immutable runner signal.
type Mapping struct {
	Signal        string `json:"signal"`
	RunnerKind    string `json:"runner_kind"`
	RunnerID      string `json:"runner_id"`
	PackageDigest string `json:"package_digest"`
	Observation   string `json:"observation"`
	RiskOutcome   string `json:"risk_outcome"`
	Group         string `json:"group"`
}

// Configuration is an immutable activation revision; zero thresholds disable
// detection and produce inconclusive facts, never a clean risk result.
type Configuration struct {
	Enabled               bool           `json:"enabled"`
	Region                string         `json:"region"`
	WindowSeconds         int64          `json:"window_seconds"`
	RetentionSeconds      int64          `json:"retention_seconds"`
	MinimumSessionSeconds int64          `json:"minimum_session_seconds"`
	MaximumSessionSeconds int64          `json:"maximum_session_seconds"`
	Thresholds            map[string]int `json:"thresholds"`
	Sources               []Source       `json:"sources"`
	Mappings              []Mapping      `json:"mappings"`
}

// Validate checks bounded rules and exact source/runner contracts.
func (c Configuration) Validate() error {
	if c.Thresholds == nil || c.Sources == nil || c.Mappings == nil || (len(c.Region) > 64 || !namePattern.MatchString(c.Region)) || c.WindowSeconds < 1 || c.WindowSeconds > 30*86400 || c.RetentionSeconds < c.WindowSeconds || c.RetentionSeconds > 30*86400 || c.MinimumSessionSeconds < 0 || c.MaximumSessionSeconds < c.MinimumSessionSeconds || c.MaximumSessionSeconds > 30*86400 || len(c.Sources) > 64 || len(c.Mappings) > 64 {
		return ErrInvalid
	}
	for k, v := range c.Thresholds {
		if !slices.Contains(Signals, k) || v < 1 || v > 10000 {
			return ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, s := range c.Sources {
		if _, err := id.ParseAPIKey(s.KeyID); err != nil {
			return ErrInvalid
		}
		if !namePattern.MatchString(s.Namespace) || s.Namespace == "core" || len(s.Kinds) == 0 || len(s.Kinds) > len(Kinds) || seen[s.KeyID+"/"+s.Namespace] {
			return ErrInvalid
		}
		seen[s.KeyID+"/"+s.Namespace] = true
		for _, k := range s.Kinds {
			if !slices.Contains(Kinds, k) || k == "document" || k == "capture" || (k == "portrait" && !s.PortraitPermitted) {
				return ErrInvalid
			}
		}
	}
	mappingSeen := map[string]bool{}
	for _, m := range c.Mappings {
		if m.RiskOutcome == "disagreement" && (m.RunnerKind != "provider" || m.Signal != "provider_inconsistency") {
			return ErrInvalid
		}
		key := m.Signal + "/" + m.RunnerKind + "/" + m.RunnerID + "/" + m.PackageDigest + "/" + m.Observation
		if mappingSeen[key] {
			return ErrInvalid
		}
		mappingSeen[key] = true
		if m.Signal == "high_risk_model" && m.RunnerKind != "model" {
			return ErrInvalid
		}
		b, e := hex.DecodeString(m.PackageDigest)
		if !slices.Contains([]string{"network_anomaly", "provider_inconsistency", "identity_inconsistency", "failed_liveness", "high_risk_model"}, m.Signal) || !slices.Contains([]string{"provider", "model"}, m.RunnerKind) || !namePattern.MatchString(m.RunnerID) || !namePattern.MatchString(m.Observation) || !namePattern.MatchString(m.Group) || e != nil || len(b) != 32 || !slices.Contains([]string{"satisfied", "not_satisfied", "disagreement"}, m.RiskOutcome) {
			return ErrInvalid
		}
	}
	return nil
}

// Attribute carries a canonical integration value transiently. Only its keyed
// token is persisted. Namespace revisions must change when canonicalization does.
type Attribute struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Input attaches evidence-backed tenant attestations to one verification.
type Input struct {
	VerificationID  string      `json:"verification_id"`
	EvidenceID      string      `json:"evidence_id"`
	Namespace       string      `json:"namespace"`
	SourceReference string      `json:"source_reference"`
	Attributes      []Attribute `json:"attributes"`
}

// Validate checks canonical transient integration input.
func (v Input) Validate() error {
	if _, e := id.ParseVerification(v.VerificationID); e != nil {
		return ErrInvalid
	}
	if _, e := id.ParseEvidence(v.EvidenceID); e != nil {
		return ErrInvalid
	}
	if !namePattern.MatchString(v.Namespace) || !namePattern.MatchString(v.SourceReference) || len(v.Attributes) < 1 || len(v.Attributes) > 16 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, a := range v.Attributes {
		if !slices.Contains(Kinds, a.Kind) || len(a.Value) < 1 || len(a.Value) > 2048 || seen[a.Kind] {
			return ErrInvalid
		}
		if strings.ContainsAny(a.Value, "\r\n\x00") {
			return ErrInvalid
		}
		if a.Kind == "network" {
			ip, e := netip.ParseAddr(a.Value)
			if e != nil || ip.Zone() != "" || ip.Unmap().String() != a.Value {
				return ErrInvalid
			}
		}
		if a.Kind == "portrait" {
			b, e := hex.DecodeString(a.Value)
			if e != nil || len(b) != 32 || hex.EncodeToString(b) != a.Value {
				return ErrInvalid
			}
		}
		seen[a.Kind] = true
	}
	return nil
}

// Token uses a random, tenant/region-specific key with explicit schema and kind
// domain separation. No public digest or shared key is a correlation token.
func Token(key []byte, tenantID, region, namespace, kind, value string) string {
	h := hmac.New(sha256.New, key)
	b, _ := json.Marshal([]string{"idenqa.fraud.token.v1", tenantID, region, namespace, kind, value})
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// Digest returns the canonical JSON content fingerprint for owned records.
func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Finding states express whether the no-risk requirement is satisfied.
type Finding struct {
	Signal  string   `json:"signal"`
	State   string   `json:"state"`
	Count   int      `json:"count"`
	Groups  []string `json:"groups,omitempty"`
	Sources []string `json:"sources,omitempty"`
	Reason  string   `json:"reason"`
}

// Receipt pins the configuration, cutoff, coverage and exact source lineage.
// Graph tokens and other subjects' identifiers never enter a customer summary.
type Receipt struct {
	Digest               string    `json:"digest"`
	VerificationID       string    `json:"verification_id"`
	ConfigurationVersion int64     `json:"configuration_version"`
	ConfigurationDigest  string    `json:"configuration_digest"`
	At                   time.Time `json:"at"`
	Findings             []Finding `json:"findings"`
}

// Proposal is a bounded, evidence-linked hypothesis. It cannot change a fact,
// decision, policy, authority or graph edge.
type Proposal struct {
	ReceiptDigest string   `json:"receipt_digest"`
	Hypothesis    string   `json:"hypothesis"`
	Signals       []string `json:"signals"`
}
