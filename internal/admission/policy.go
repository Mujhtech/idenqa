// Package admission owns the optional operational admission policy boundary.
// It never grants application permission, processing authority or identity assurance.
package admission

import (
	"crypto/ed25519"
	"errors"
	"slices"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
)

var (
	// ErrInvalid identifies malformed policy or changed operation meaning.
	ErrInvalid = errors.New("admission: invalid policy")
	// ErrEntitlement denies a new capability without affecting accepted work.
	ErrEntitlement = errors.New("admission: capability unavailable")
	// ErrUnavailable requires recovery of a missing or expired local snapshot.
	ErrUnavailable = errors.New("admission: policy unavailable")
	// ErrQuota identifies an exhausted explicitly configured operational ceiling.
	ErrQuota = errors.New("admission: quota exceeded")
	// ErrConflict identifies a changed replay or attempted snapshot rollback.
	ErrConflict = errors.New("admission: conflicting replay")
)

// Config pins installation authority independently of any snapshot body.
// MaximumLifetime is an explicit deployment input, not an implicit grace period.
type Config struct {
	DeploymentID    string
	Region          string
	MaximumLifetime time.Duration
	Issuers         map[string]ed25519.PublicKey
}

// Policy verifies an installation's immutable signed admission rules.
type Policy struct{ config Config }

// NewPolicy copies trusted public keys and rejects an unbounded configuration.
func NewPolicy(config Config) (*Policy, error) {
	if !(admissionv1.Scope{TenantID: "validation", DeploymentID: config.DeploymentID, Region: config.Region}).Valid() || config.MaximumLifetime <= 0 || len(config.Issuers) == 0 || len(config.Issuers) > 16 {
		return nil, ErrInvalid
	}
	keys := make(map[string]ed25519.PublicKey, len(config.Issuers))
	for name, key := range config.Issuers {
		if !(admissionv1.Scope{TenantID: name, DeploymentID: "validation", Region: "validation"}).Valid() || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalid
		}
		keys[name] = append(ed25519.PublicKey(nil), key...)
	}
	config.Issuers = keys
	return &Policy{config}, nil
}

// Verify authenticates a snapshot without activating a future effective revision.
func (p *Policy) Verify(snapshot admissionv1.Snapshot, now time.Time) error {
	if p == nil || now.IsZero() || snapshot.Scope.DeploymentID != p.config.DeploymentID || snapshot.Scope.Region != p.config.Region || snapshot.IssuedAt.After(now) || snapshot.ValidUntil.After(snapshot.IssuedAt.Add(p.config.MaximumLifetime)) || snapshot.Verify(p.config.Issuers[snapshot.Issuer]) != nil {
		return ErrInvalid
	}
	return nil
}

// Region returns the installation's independently pinned region.
func (p *Policy) Region() string { return p.config.Region }

// Decide resolves only the commercial/operational dimension for new work. The
// consuming application must independently prove permission and processing policy.
// Already accepted work does not call this method again for its continuation.
func (p *Policy) Decide(snapshot admissionv1.Snapshot, operation string, now time.Time) (admissionv1.Rule, error) {
	if err := p.Verify(snapshot, now); err != nil {
		return admissionv1.Rule{}, err
	}
	if now.Before(snapshot.ValidFrom) || !now.Before(snapshot.ValidUntil) {
		return admissionv1.Rule{}, ErrUnavailable
	}
	if !slices.Contains([]string{"active", "grace"}, snapshot.Status) {
		return admissionv1.Rule{}, ErrEntitlement
	}
	for _, rule := range snapshot.Rules {
		if rule.Operation != operation {
			continue
		}
		for capability, version := range rule.Capabilities {
			if snapshot.Capabilities[capability] != version {
				return admissionv1.Rule{}, ErrEntitlement
			}
		}
		return rule, nil
	}
	return admissionv1.Rule{}, ErrEntitlement
}
