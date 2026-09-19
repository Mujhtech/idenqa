package config

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"time"

	certification "github.com/Mujhtech/idenqa/adapters/certification/ed25519"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const reviewAuthorityMaximumBytes = 256 << 10

// ReviewAuthorityFile is an operator-managed, reference-only assignment file.
// It is reread for each action so removal, expiry and invalid replacement fail closed.
type ReviewAuthorityFile struct{ Path string }

// reviewAuthorityDocument is the closed additive assignment and issuer
// configuration document. certification_issuers maps issuer -> key id ->
// standard-base64 Ed25519 public key.
type reviewAuthorityDocument struct {
	Assignments          []review.Assignment          `json:"assignments"`
	CertificationIssuers map[string]map[string]string `json:"certification_issuers"`
}

// Validate checks mounted configuration at startup. An absent path disables review writes.
func (file ReviewAuthorityFile) Validate() error {
	if file.Path == "" {
		return nil
	}
	_, err := file.load()
	return err
}

func (file ReviewAuthorityFile) document() (reviewAuthorityDocument, error) {
	var document reviewAuthorityDocument
	if file.Path == "" {
		return document, review.ErrForbidden
	}
	if err := ReadClosedFile(file.Path, &document, reviewAuthorityMaximumBytes); err != nil {
		return document, review.ErrForbidden
	}
	return document, nil
}

// verifier builds the owned external certification verifier. An empty issuer
// map preserves the existing tenant-attested behaviour.
func (document reviewAuthorityDocument) verifier() (review.CertificationVerifier, error) {
	if len(document.CertificationIssuers) == 0 {
		return nil, nil
	}
	issuers := make(certification.IssuerKeys, len(document.CertificationIssuers))
	for issuer, keys := range document.CertificationIssuers {
		issuers[issuer] = make(map[string]ed25519.PublicKey, len(keys))
		for keyID, encoded := range keys {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(decoded) != ed25519.PublicKeySize {
				return nil, review.ErrForbidden
			}
			issuers[issuer][keyID] = ed25519.PublicKey(decoded)
		}
	}
	verifier, err := certification.NewVerifier(issuers, clock.System{})
	if err != nil {
		return nil, review.ErrForbidden
	}
	return verifier, nil
}

func (file ReviewAuthorityFile) load() (*review.Registry, error) {
	document, err := file.document()
	if err != nil {
		return nil, err
	}
	registry, err := review.NewRegistry(document.Assignments)
	if err != nil {
		return nil, err
	}
	verifier, err := document.verifier()
	if err != nil {
		return nil, err
	}
	return registry.WithCertificationVerifier(verifier), nil
}

// CertificationVerifier supplies the deployment external verifier to durable
// authority adapters. An absent path or issuer map keeps tenant-attested resolution.
func (file ReviewAuthorityFile) CertificationVerifier() (review.CertificationVerifier, error) {
	if file.Path == "" {
		return nil, nil
	}
	document, err := file.document()
	if err != nil {
		return nil, err
	}
	return document.verifier()
}

// ResolveReviewer reloads authority and never accepts privileges from a request body.
func (file ReviewAuthorityFile) ResolveReviewer(ctx context.Context, scope tenant.Scope, actor review.Actor, region string, now time.Time) (review.Principal, error) {
	registry, err := file.load()
	if err != nil {
		return review.Principal{}, review.ErrForbidden
	}
	return registry.ResolveReviewer(ctx, scope, actor, region, now)
}

// LoadReviewRouting reads bounded explicit case requirements for exact policy revisions.
func LoadReviewRouting(path string) ([]review.RoutingRule, error) {
	if path == "" {
		return nil, nil
	}
	var document struct {
		Rules []review.RoutingRule `json:"rules"`
	}
	if err := ReadClosedFile(path, &document, 256<<10); err != nil {
		return nil, err
	}
	return document.Rules, nil
}
