// Package reviewdelegation verifies short-lived, single-operation Cloud
// authority delivered through the protected local deployment-agent boundary.
package reviewdelegation

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid   = errors.New("review delegation is invalid")
	ErrForbidden = errors.New("review delegation is forbidden")
	ErrExpired   = errors.New("review delegation is expired")
	ErrNotFound  = errors.New("review delegation receipt not found")
)

const (
	Version  = "idenqa.cloud/review-authority/v1"
	Audience = "idenqa-core:review-command"
)

type Scope struct {
	OrganisationID string `json:"organisationId"`
	TenantID       string `json:"tenantId"`
	EnvironmentID  string `json:"environmentId"`
	DeploymentID   string `json:"deploymentId"`
	Region         string `json:"region"`
}

type Intent struct {
	CommandID        string    `json:"commandId"`
	Scope            Scope     `json:"scope"`
	ActorID          string    `json:"actorId"`
	Permission       string    `json:"permission"`
	Purpose          string    `json:"purpose"`
	TargetKind       string    `json:"targetKind"`
	TargetID         string    `json:"targetId"`
	Operation        string    `json:"operation"`
	ExpectedVersion  int64     `json:"expectedVersion"`
	ReasonCode       string    `json:"reasonCode"`
	Resolution       string    `json:"resolution,omitempty"`
	EvidenceGrantIDs []string  `json:"evidenceGrantIds,omitempty"`
	ApprovalID       string    `json:"approvalId,omitempty"`
	SupportGrantID   string    `json:"supportGrantId,omitempty"`
	RequestDigest    string    `json:"requestDigest"`
	IssuedAt         time.Time `json:"issuedAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type Claims struct {
	Version  string `json:"version"`
	Audience string `json:"audience"`
	Intent
}

type Envelope struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Claims    Claims `json:"claims"`
	Signature string `json:"signature"`
}

type claimsContextKey struct{}

// WithClaims attaches already verified delegation claims to the lifetime of
// one local review command. It carries identity, not a service dependency.
func WithClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// ClaimsFromContext returns the verified delegation claims, when the request
// entered through the protected local review-command boundary.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(Claims)
	return claims, ok
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
	intent := claims.Intent
	values := []string{
		intent.CommandID, intent.Scope.OrganisationID, intent.Scope.TenantID,
		intent.Scope.EnvironmentID, intent.Scope.DeploymentID, intent.Scope.Region,
		intent.ActorID, intent.Permission, intent.Purpose, intent.TargetKind,
		intent.TargetID, intent.Operation, intent.ReasonCode, intent.RequestDigest,
	}
	if len(intent.EvidenceGrantIDs) > 32 {
		return ErrInvalid
	}
	for _, grantID := range intent.EvidenceGrantIDs {
		if !bounded(grantID, 256) {
			return ErrInvalid
		}
	}
	for _, value := range values {
		if !bounded(value, 256) {
			return ErrInvalid
		}
	}
	if intent.Scope.OrganisationID != binding.OrganisationID || intent.Scope.EnvironmentID != binding.EnvironmentID ||
		intent.Scope.DeploymentID != binding.DeploymentID || intent.Scope.Region != binding.Region {
		return ErrForbidden
	}
	if intent.TargetKind != "review_case" || intent.ExpectedVersion < 1 ||
		(intent.Operation != "claim" && intent.Operation != "submit_finding") ||
		(intent.Permission != "review:claim" && intent.Permission != "review:find") {
		return ErrForbidden
	}
	if intent.Operation == "claim" && intent.Permission != "review:claim" ||
		intent.Operation == "submit_finding" && intent.Permission != "review:find" {
		return ErrForbidden
	}
	if intent.Operation == "claim" && (intent.Resolution != "" || len(intent.EvidenceGrantIDs) != 0) {
		return ErrForbidden
	}
	if intent.Operation == "submit_finding" &&
		(intent.Resolution == "" || len(intent.EvidenceGrantIDs) == 0) {
		return ErrForbidden
	}
	if _, err := hex.DecodeString(intent.RequestDigest); err != nil || len(intent.RequestDigest) != sha256.Size*2 {
		return ErrInvalid
	}
	if intent.IssuedAt.IsZero() || intent.ExpiresAt.IsZero() || intent.IssuedAt.After(now) ||
		!intent.ExpiresAt.After(now) || !intent.ExpiresAt.After(intent.IssuedAt) {
		return ErrExpired
	}
	if intent.ExpiresAt.Sub(intent.IssuedAt) > maxTTL {
		return ErrForbidden
	}
	return nil
}

func validBinding(binding Binding) bool {
	return bounded(binding.OrganisationID, 256) && bounded(binding.EnvironmentID, 256) &&
		bounded(binding.DeploymentID, 256) && bounded(binding.Region, 128)
}

func bounded(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && value == strings.TrimSpace(value) &&
		!strings.ContainsFunc(value, func(character rune) bool { return character < ' ' || character > '~' })
}
