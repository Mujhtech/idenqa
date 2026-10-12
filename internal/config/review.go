package config

import (
	"crypto/ed25519"
	"encoding/base64"

	certification "github.com/Mujhtech/idenqa/adapters/certification/ed25519"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/review"
)

const reviewCertificationMaximumBytes = 256 << 10

// ReviewCertificationFile supplies external certification issuer keys only.
// It is reread for each action so removal or invalid replacement fails closed.
type ReviewCertificationFile struct{ Path string }

// reviewCertificationDocument is a closed issuer configuration document.
// certification_issuers maps issuer -> key id ->
// standard-base64 Ed25519 public key.
type reviewCertificationDocument struct {
	CertificationIssuers map[string]map[string]string `json:"certification_issuers"`
}

// Validate checks mounted configuration at startup. An absent path retains tenant-attested certifications.
func (file ReviewCertificationFile) Validate() error {
	if file.Path == "" {
		return nil
	}
	_, err := file.CertificationVerifier()
	return err
}

func (file ReviewCertificationFile) document() (reviewCertificationDocument, error) {
	var document reviewCertificationDocument
	if file.Path == "" {
		return document, review.ErrForbidden
	}
	if err := ReadClosedFile(file.Path, &document, reviewCertificationMaximumBytes); err != nil {
		return document, review.ErrForbidden
	}
	return document, nil
}

// verifier builds the owned external certification verifier. An empty issuer
// map preserves the existing tenant-attested behaviour.
func (document reviewCertificationDocument) verifier() (review.CertificationVerifier, error) {
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

// CertificationVerifier supplies the deployment external verifier to durable
// authority adapters. An absent path or issuer map keeps tenant-attested resolution.
func (file ReviewCertificationFile) CertificationVerifier() (review.CertificationVerifier, error) {
	if file.Path == "" {
		return nil, nil
	}
	document, err := file.document()
	if err != nil {
		return nil, err
	}
	return document.verifier()
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
