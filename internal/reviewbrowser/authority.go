// Package reviewbrowser owns direct, case-bound browser evidence sessions
// delegated by an authorised Cloud workforce boundary.
package reviewbrowser

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalid   = errors.New("review browser session is invalid")
	ErrForbidden = errors.New("review browser session is forbidden")
	ErrExpired   = errors.New("review browser session is expired")
	ErrReplay    = errors.New("review browser bootstrap was already consumed")
)

const (
	Version  = "idenqa.cloud/review-evidence-bootstrap/v1"
	Audience = "idenqa-core:review-evidence-session"
	Purpose  = "manual-review"
)

type Scope struct {
	OrganisationID string `json:"organisationId"`
	TenantID       string `json:"tenantId"`
	EnvironmentID  string `json:"environmentId"`
	DeploymentID   string `json:"deploymentId"`
	Region         string `json:"region"`
}

type Claims struct {
	Version         string    `json:"version"`
	Audience        string    `json:"audience"`
	BootstrapID     string    `json:"bootstrapId"`
	Scope           Scope     `json:"scope"`
	ActorID         string    `json:"actorId"`
	ReviewCaseID    string    `json:"reviewCaseId"`
	ExpectedVersion int64     `json:"expectedVersion"`
	Purpose         string    `json:"purpose"`
	Origin          string    `json:"origin"`
	Nonce           string    `json:"nonce"`
	IssuedAt        time.Time `json:"issuedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type Envelope struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Claims    Claims `json:"claims"`
	Signature string `json:"signature"`
}

type Binding struct {
	OrganisationID string
	EnvironmentID  string
	DeploymentID   string
	Region         string
}

type Verifier struct {
	keys    map[string]ed25519.PublicKey
	binding Binding
	now     func() time.Time
	maxTTL  time.Duration
}

func NewVerifier(encodedKeys map[string]string, binding Binding, maxTTL time.Duration, now func() time.Time) (*Verifier, error) {
	if len(encodedKeys) == 0 || maxTTL <= 0 || now == nil || !validBinding(binding) {
		return nil, ErrInvalid
	}
	keys := make(map[string]ed25519.PublicKey, len(encodedKeys))
	for keyID, encoded := range encodedKeys {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if !bounded(keyID, 128) || err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, ErrInvalid
		}
		keys[keyID] = ed25519.PublicKey(append([]byte(nil), decoded...))
	}
	return &Verifier{keys: keys, binding: binding, maxTTL: maxTTL, now: now}, nil
}

func (verifier *Verifier) Verify(envelope Envelope) (Claims, error) {
	claims := envelope.Claims
	if verifier == nil || envelope.Algorithm != "Ed25519" || claims.Version != Version || claims.Audience != Audience {
		return Claims{}, ErrForbidden
	}
	key, ok := verifier.keys[envelope.KeyID]
	if !ok {
		return Claims{}, ErrForbidden
	}
	if err := validateClaims(claims, verifier.binding, verifier.maxTTL, verifier.now().UTC()); err != nil {
		return Claims{}, err
	}
	encoded, err := json.Marshal(claims)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, digest[:], signature) {
		return Claims{}, ErrForbidden
	}
	return claims, nil
}

func validateClaims(claims Claims, binding Binding, maxTTL time.Duration, now time.Time) error {
	values := []string{claims.BootstrapID, claims.Scope.OrganisationID, claims.Scope.TenantID, claims.Scope.EnvironmentID,
		claims.Scope.DeploymentID, claims.Scope.Region, claims.ActorID, claims.ReviewCaseID, claims.Purpose, claims.Origin, claims.Nonce}
	for _, value := range values {
		if !bounded(value, 512) {
			return ErrInvalid
		}
	}
	if claims.Scope.OrganisationID != binding.OrganisationID || claims.Scope.EnvironmentID != binding.EnvironmentID ||
		claims.Scope.DeploymentID != binding.DeploymentID || claims.Scope.Region != binding.Region || claims.Purpose != Purpose ||
		claims.ExpectedVersion < 1 || !validOrigin(claims.Origin) {
		return ErrForbidden
	}
	if claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || claims.IssuedAt.After(now) || !claims.ExpiresAt.After(now) ||
		!claims.ExpiresAt.After(claims.IssuedAt) {
		return ErrExpired
	}
	if claims.ExpiresAt.Sub(claims.IssuedAt) > maxTTL {
		return ErrForbidden
	}
	return nil
}

func validBinding(binding Binding) bool {
	return bounded(binding.OrganisationID, 256) && bounded(binding.EnvironmentID, 256) &&
		bounded(binding.DeploymentID, 256) && bounded(binding.Region, 128)
}

func validOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.String() == value && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
		parsed.Path == "" && parsed.Host != "" && (parsed.Scheme == "https" || (parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1")))
}

func bounded(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && value == strings.TrimSpace(value) &&
		!strings.ContainsFunc(value, func(character rune) bool { return character < ' ' || character > '~' })
}
