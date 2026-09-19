// Package certification adapts compact issuer-signed certification
// assertions to the owned review.CertificationVerifier port.
//
// The supported assertion format is v1.<base64url(payload)>.<base64url(signature)>,
// where payload is the closed JSON document
// {issuer,key_id,reviewer_id,certificate,region,issued_at,expires_at} and the
// signature is Ed25519 over the exact decoded payload bytes. Public keys come
// from deployment configuration and never from request input.
package certification

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// SchemePrefix identifies the supported compact assertion version.
	SchemePrefix = "v1"

	maxTokenBytes   = 4096
	maxPayloadBytes = 2048
	maxIssuers      = 32
	maxIssuerKeys   = 8
)

// IssuerKeys maps an issuer identifier to its key identifiers and Ed25519
// public keys.
type IssuerKeys map[string]map[string]ed25519.PublicKey

// Verifier checks issuer-signed assertions against server-configured public
// keys. Every failure is closed and non-disclosing.
type Verifier struct {
	issuers map[string]map[string]ed25519.PublicKey
	now     func() time.Time
}

type assertionPayload struct {
	Issuer      string    `json:"issuer"`
	KeyID       string    `json:"key_id"`
	ReviewerID  string    `json:"reviewer_id"`
	Certificate string    `json:"certificate"`
	Region      string    `json:"region"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// NewVerifier validates and snapshots deployment-owned issuer key material.
func NewVerifier(issuers IssuerKeys, source clock.Clock) (*Verifier, error) {
	if len(issuers) == 0 || len(issuers) > maxIssuers || source == nil {
		return nil, review.ErrInvalid
	}
	snapshot := make(map[string]map[string]ed25519.PublicKey, len(issuers))
	for issuer, keys := range issuers {
		if !certificationLabel(issuer) || len(keys) == 0 || len(keys) > maxIssuerKeys {
			return nil, review.ErrInvalid
		}
		snapshot[issuer] = make(map[string]ed25519.PublicKey, len(keys))
		for keyID, key := range keys {
			if !certificationLabel(keyID) || len(key) != ed25519.PublicKeySize {
				return nil, review.ErrInvalid
			}
			snapshot[issuer][keyID] = append(ed25519.PublicKey(nil), key...)
		}
	}
	return &Verifier{issuers: snapshot, now: source.Now}, nil
}

// Verify enforces the exact reviewer, certificate and region binding, the
// configured issuer and key, the Ed25519 signature and the signed validity
// window. Timestamps are compared in UTC at microsecond precision.
func (verifier *Verifier) Verify(ctx context.Context, _ tenant.Scope, assertion review.CertificateAssertion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := verifier.verifyToken(assertion.Token)
	if err != nil {
		return review.ErrForbidden
	}
	if payload.ReviewerID != assertion.ReviewerID || payload.Certificate != assertion.Certificate || payload.Region != assertion.Region {
		return review.ErrForbidden
	}
	issuedAt := payload.IssuedAt.UTC().Truncate(time.Microsecond)
	expiresAt := payload.ExpiresAt.UTC().Truncate(time.Microsecond)
	if issuedAt.IsZero() || expiresAt.IsZero() || !expiresAt.After(issuedAt) {
		return review.ErrForbidden
	}
	now := verifier.now().UTC().Truncate(time.Microsecond)
	if now.Before(issuedAt) || !now.Before(expiresAt) {
		return review.ErrForbidden
	}
	return nil
}

func (verifier *Verifier) verifyToken(token string) (assertionPayload, error) {
	if len(token) == 0 || len(token) > maxTokenBytes {
		return assertionPayload{}, review.ErrForbidden
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != SchemePrefix {
		return assertionPayload{}, review.ErrForbidden
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(raw) == 0 || len(raw) > maxPayloadBytes {
		return assertionPayload{}, review.ErrForbidden
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return assertionPayload{}, review.ErrForbidden
	}
	var payload assertionPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return assertionPayload{}, review.ErrForbidden
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return assertionPayload{}, review.ErrForbidden
	}
	keys, ok := verifier.issuers[payload.Issuer]
	if !ok {
		return assertionPayload{}, review.ErrForbidden
	}
	public, ok := keys[payload.KeyID]
	if !ok || !ed25519.Verify(public, raw, signature) {
		return assertionPayload{}, review.ErrForbidden
	}
	return payload, nil
}

func certificationLabel(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r > '~' })
}
