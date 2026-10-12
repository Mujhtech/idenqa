// Package managedlaunch verifies narrowly scoped Cloud authority and creates
// real Core verification sessions for published-flow previews.
package managedlaunch

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrInvalid reports malformed claims or verifier configuration.
	ErrInvalid = errors.New("managed launch authority is invalid")
	// ErrForbidden reports authority that is not valid for this deployment.
	ErrForbidden = errors.New("managed launch authority is forbidden")
	// ErrExpired reports authority outside its short validity interval.
	ErrExpired = errors.New("managed launch authority is expired")
)

const (
	// Version identifies the signed managed-launch claims contract.
	Version = "idenqa.cloud/managed-launch-authority/v1"
	// Audience identifies the only Core operation that may consume the authority.
	Audience = "idenqa-core:managed-launch"
	// Purpose identifies the operator-facing published-flow preview.
	Purpose = "published-flow-preview"
	// ProviderMode requires the environment's provider-sandbox routing.
	ProviderMode = "sandbox"
	// WebhookMode makes explicit that normal tenant webhook delivery remains enabled.
	WebhookMode = "tenant-enabled"
	clockSkew   = 5 * time.Second
)

// Scope binds authority to one commercial control-plane placement.
type Scope struct {
	OrganisationID string `json:"organisationId"`
	TenantID       string `json:"tenantId"`
	EnvironmentID  string `json:"environmentId"`
	DeploymentID   string `json:"deploymentId"`
	Region         string `json:"region"`
}

// Claims is the complete signed command. Browser-controlled data is absent.
type Claims struct {
	Version         string    `json:"version"`
	Issuer          string    `json:"issuer"`
	Audience        string    `json:"audience"`
	CommandID       string    `json:"commandId"`
	WorkloadID      string    `json:"workloadId"`
	Scope           Scope     `json:"scope"`
	CoreTenantID    string    `json:"coreTenantId"`
	ActorID         string    `json:"actorId"`
	Purpose         string    `json:"purpose"`
	ProfileID       string    `json:"profileId"`
	ProfileRevision uint32    `json:"profileRevision"`
	PolicyID        string    `json:"policyId"`
	Origin          string    `json:"origin"`
	Locale          string    `json:"locale,omitempty"`
	ProviderMode    string    `json:"providerMode"`
	WebhookMode     string    `json:"webhookMode"`
	RequestDigest   string    `json:"requestDigest"`
	IssuedAt        time.Time `json:"issuedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// Envelope carries claims with key-selection and signature metadata.
type Envelope struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Claims    Claims `json:"claims"`
	Signature string `json:"signature"`
}

// Binding identifies the deployment context accepted by a verifier.
type Binding struct {
	OrganisationID string
	EnvironmentID  string
	DeploymentID   string
	Region         string
}

// Verifier validates signed, short-lived managed-launch envelopes.
type Verifier struct {
	keys    map[string]ed25519.PublicKey
	binding Binding
	issuer  string
	now     func() time.Time
	maxTTL  time.Duration
}

// NewVerifier constructs a verifier from an overlapping set of trusted public keys.
func NewVerifier(encodedKeys map[string]string, binding Binding, issuer string, maxTTL time.Duration, now func() time.Time) (*Verifier, error) {
	if len(encodedKeys) == 0 || !bounded(issuer, 256) || maxTTL <= 0 || maxTTL > 5*time.Minute || now == nil || !validBinding(binding) {
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
	return &Verifier{keys: keys, binding: binding, issuer: issuer, maxTTL: maxTTL, now: now}, nil
}

// Verify authenticates and validates one exact managed-launch command.
func (verifier *Verifier) Verify(envelope Envelope) (Claims, error) {
	if verifier == nil || envelope.Algorithm != "Ed25519" || envelope.Claims.Version != Version ||
		envelope.Claims.Audience != Audience || envelope.Claims.Issuer != verifier.issuer {
		return Claims{}, ErrForbidden
	}
	key, ok := verifier.keys[envelope.KeyID]
	if !ok {
		return Claims{}, ErrForbidden
	}
	if err := validateClaims(envelope.Claims, verifier.binding, verifier.maxTTL, verifier.now().UTC()); err != nil {
		return Claims{}, err
	}
	encoded, err := json.Marshal(envelope.Claims)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, digest[:], signature) {
		return Claims{}, ErrForbidden
	}
	return envelope.Claims, nil
}

func validateClaims(claims Claims, binding Binding, maxTTL time.Duration, now time.Time) error {
	values := []string{
		claims.CommandID, claims.WorkloadID, claims.Scope.OrganisationID, claims.Scope.TenantID,
		claims.Scope.EnvironmentID, claims.Scope.DeploymentID, claims.Scope.Region, claims.CoreTenantID,
		claims.ActorID, claims.ProfileID, claims.PolicyID, claims.RequestDigest,
	}
	for _, value := range values {
		if !bounded(value, 256) {
			return ErrInvalid
		}
	}
	if claims.Scope.OrganisationID != binding.OrganisationID || claims.Scope.EnvironmentID != binding.EnvironmentID ||
		claims.Scope.DeploymentID != binding.DeploymentID || claims.Scope.Region != binding.Region {
		return ErrForbidden
	}
	if !strings.HasPrefix(claims.CommandID, "pln_") || !strings.HasPrefix(claims.WorkloadID, "wld_") ||
		!strings.HasPrefix(claims.ActorID, "usr_") || claims.ProfileRevision == 0 ||
		claims.Purpose != Purpose || claims.ProviderMode != ProviderMode || claims.WebhookMode != WebhookMode ||
		!validOrigin(claims.Origin) || len(claims.Locale) > 35 {
		return ErrForbidden
	}
	if _, err := hex.DecodeString(claims.RequestDigest); err != nil || len(claims.RequestDigest) != sha256.Size*2 {
		return ErrInvalid
	}
	if claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || claims.IssuedAt.After(now.Add(clockSkew)) ||
		!claims.ExpiresAt.After(now) || !claims.ExpiresAt.After(claims.IssuedAt) {
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
	if err != nil || parsed.String() != value || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func bounded(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && value == strings.TrimSpace(value) &&
		!strings.ContainsFunc(value, func(character rune) bool { return character < ' ' || character > '~' })
}
