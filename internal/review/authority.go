package review

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Authority resolves server-managed reviewer assignments for an authenticated actor.
// Implementations must enforce tenant, region, validity and revocation on every call.
type Authority interface {
	ResolveReviewer(context.Context, tenant.Scope, Actor, string, time.Time) (Principal, error)
}

// CertificationVerifier verifies an issuer-signed certification assertion for
// the exact reviewer, certificate and region binding. Implementations must
// fail closed: an unknown issuer or key, malformed or tampered assertion,
// signature mismatch, binding mismatch, or missing, future or expired validity
// must return an error.
type CertificationVerifier interface {
	Verify(context.Context, tenant.Scope, CertificateAssertion) error
}

// CertificationVerifierSource supplies the deployment external certification
// verifier to durable authority adapters. A nil verifier keeps the existing
// tenant-attested behaviour.
type CertificationVerifierSource interface {
	CertificationVerifier() (CertificationVerifier, error)
}

// CertificateAssertion is a compact issuer-signed token plus the reviewer,
// certificate and region binding it must attest. The signed payload separately
// binds the issuer, key identifier and issued/expiry times. Assertions come
// only from server-managed configuration, never from caller request bodies.
type CertificateAssertion struct {
	Token       string `json:"token"`
	ReviewerID  string `json:"reviewer_id,omitempty"`
	Certificate string `json:"certificate"`
	Region      string `json:"region"`
}

// Assignment binds a credential to a stable operator identity. Key rotation must
// retain OperatorID so multiple credentials cannot bypass reviewer independence.
type Assignment struct {
	TenantID                string                 `json:"tenant_id"`
	APIKeyID                string                 `json:"api_key_id"`
	OperatorID              string                 `json:"operator_id"`
	Permissions             []Permission           `json:"permissions"`
	Certifications          []string               `json:"certifications"`
	CertificationAssertions []CertificateAssertion `json:"certification_assertions,omitempty"`
	Regions                 []string               `json:"regions"`
	NotBefore               time.Time              `json:"not_before"`
	ExpiresAt               time.Time              `json:"expires_at"`
}

// Registry is an immutable snapshot of validated operator assignments.
type Registry struct {
	assignments []Assignment
	verifier    CertificationVerifier
}

// NewRegistry rejects ambiguous credentials and unbounded authority inputs.
func NewRegistry(assignments []Assignment) (*Registry, error) {
	if len(assignments) > 256 {
		return nil, ErrInvalid
	}
	registry := &Registry{}
	seen := map[string]bool{}
	for _, assignment := range assignments {
		if _, err := id.ParseTenant(assignment.TenantID); err != nil {
			return nil, ErrInvalid
		}
		if _, err := id.ParseAPIKey(assignment.APIKeyID); err != nil {
			return nil, ErrInvalid
		}
		key := assignment.TenantID + "/" + assignment.APIKeyID
		if seen[key] || !authorityLabel(assignment.OperatorID) || assignment.NotBefore.IsZero() || !assignment.ExpiresAt.After(assignment.NotBefore) || len(assignment.Permissions) == 0 || len(assignment.Permissions) > 4 || len(assignment.Regions) == 0 || len(assignment.Regions) > 32 || len(assignment.Certifications) > 32 {
			return nil, ErrInvalid
		}
		seen[key] = true
		for _, permission := range assignment.Permissions {
			if permission != PermissionClaim && permission != PermissionFind && permission != PermissionResolve && permission != PermissionAppeal {
				return nil, ErrInvalid
			}
		}
		for _, label := range append(slices.Clone(assignment.Regions), assignment.Certifications...) {
			if !authorityLabel(label) {
				return nil, ErrInvalid
			}
		}
		if len(assignment.CertificationAssertions) > 32 {
			return nil, ErrInvalid
		}
		seenAssertions := map[string]bool{}
		for _, assertion := range assignment.CertificationAssertions {
			key := assertion.Certificate + "/" + assertion.Region
			if !authorityLabel(assertion.Certificate) || !slices.Contains(assignment.Certifications, assertion.Certificate) ||
				!slices.Contains(assignment.Regions, assertion.Region) || !assertionToken(assertion.Token) ||
				(assertion.ReviewerID != "" && assertion.ReviewerID != assignment.OperatorID) || seenAssertions[key] {
				return nil, ErrInvalid
			}
			seenAssertions[key] = true
		}
		assignment.Permissions = slices.Clone(assignment.Permissions)
		assignment.Certifications = slices.Clone(assignment.Certifications)
		assignment.CertificationAssertions = slices.Clone(assignment.CertificationAssertions)
		assignment.Regions = slices.Clone(assignment.Regions)
		registry.assignments = append(registry.assignments, assignment)
	}
	return registry, nil
}

// WithCertificationVerifier returns a registry that additionally requires a
// valid issuer-signed assertion for every certification. A nil verifier keeps
// tenant-attested resolution exactly as before.
func (registry *Registry) WithCertificationVerifier(verifier CertificationVerifier) *Registry {
	if registry == nil {
		return nil
	}
	result := *registry
	result.verifier = verifier
	return &result
}

// ResolveReviewer grants only the current exact tenant, credential and region assignment.
func (registry *Registry) ResolveReviewer(ctx context.Context, scope tenant.Scope, actor Actor, region string, now time.Time) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if registry == nil || now.IsZero() {
		return Principal{}, ErrForbidden
	}
	for _, assignment := range registry.assignments {
		if assignment.TenantID == scope.ID().String() && assignment.APIKeyID == actor.ID && !now.Before(assignment.NotBefore) && now.Before(assignment.ExpiresAt) && slices.Contains(assignment.Regions, region) {
			if registry.verifier != nil {
				for _, certificate := range assignment.Certifications {
					assertion, ok := assignment.certificationAssertion(certificate, region)
					if !ok {
						return Principal{}, ErrForbidden
					}
					assertion.ReviewerID = assignment.OperatorID
					if err := registry.verifier.Verify(ctx, scope, assertion); err != nil {
						if ctxErr := ctx.Err(); ctxErr != nil {
							return Principal{}, ctxErr
						}
						return Principal{}, ErrForbidden
					}
				}
			}
			return Principal{ID: assignment.OperatorID, Permissions: slices.Clone(assignment.Permissions), Certifications: slices.Clone(assignment.Certifications)}, nil
		}
	}
	return Principal{}, ErrForbidden
}

func (assignment Assignment) certificationAssertion(certificate, region string) (CertificateAssertion, bool) {
	for _, assertion := range assignment.CertificationAssertions {
		if assertion.Certificate == certificate && assertion.Region == region {
			return assertion, true
		}
	}
	return CertificateAssertion{}, false
}

func authorityLabel(value string) bool {
	return bounded(value, 128) && !strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r > '~' })
}

func assertionToken(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && !strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r > '~' })
}
