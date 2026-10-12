package config_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestReviewCertificationFileRejectsAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	file := config.ReviewCertificationFile{Path: path}
	for _, replacement := range []string{`{"assignments":[]}`, `{"allow_all":true}`, `invalid`, string(make([]byte, 257<<10))} {
		if err := os.WriteFile(path, []byte(replacement), 0600); err != nil {
			t.Fatal(err)
		}
		if err := file.Validate(); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("invalid file: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := file.CertificationVerifier(); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("removed file: %v", err)
	}
}

func TestReviewCertificationFileExternalCertificationVerifier(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	key, _ := ids.NewAPIKey()
	scope, _ := tenant.NewScope(tenantID)
	payload, err := json.Marshal(struct {
		Issuer      string    `json:"issuer"`
		KeyID       string    `json:"key_id"`
		ReviewerID  string    `json:"reviewer_id"`
		Certificate string    `json:"certificate"`
		Region      string    `json:"region"`
		IssuedAt    time.Time `json:"issued_at"`
		ExpiresAt   time.Time `json:"expires_at"`
	}{"issuer.registry", "2026-09", "operator-1", "document.level2", "ng-1", now.Add(-time.Minute), now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, payload)
	token := "v1." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
	assignment := review.Assignment{
		TenantID: tenantID.String(), APIKeyID: key.String(), OperatorID: "operator-1",
		Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"},
		CertificationAssertions: []review.CertificateAssertion{{Certificate: "document.level2", Region: "ng-1", Token: token}},
		Regions:                 []string{"ng-1"}, NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	path := filepath.Join(t.TempDir(), "review.json")
	file := config.ReviewCertificationFile{Path: path}
	write := func(document any) {
		t.Helper()
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(map[string]any{
		"certification_issuers": map[string]map[string]string{"issuer.registry": {"2026-09": base64.StdEncoding.EncodeToString(public)}},
	})
	if err := file.Validate(); err != nil {
		t.Fatal(err)
	}
	resolve := func(value review.Assignment) (review.Principal, error) {
		verifier, err := file.CertificationVerifier()
		if err != nil {
			return review.Principal{}, err
		}
		registry, err := review.NewRegistry([]review.Assignment{value})
		if err != nil {
			return review.Principal{}, err
		}
		return registry.WithCertificationVerifier(verifier).ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now)
	}
	principal, err := resolve(assignment)
	if err != nil || principal.ID != "operator-1" {
		t.Fatalf("external assertion principal=%+v error=%v", principal, err)
	}
	if verifier, err := file.CertificationVerifier(); err != nil || verifier == nil {
		t.Fatalf("CertificationVerifier()=%v error=%v", verifier, err)
	}

	// A configured external issuer fails closed when the assignment has no assertion.
	unasserted := assignment
	unasserted.CertificationAssertions = nil
	write(map[string]any{
		"certification_issuers": map[string]map[string]string{"issuer.registry": {"2026-09": base64.StdEncoding.EncodeToString(public)}},
	})
	if _, err := resolve(unasserted); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("missing assertion error=%v", err)
	}

	// Without a configured issuer the tenant-attested behaviour is preserved.
	write(map[string]any{})
	if principal, err := resolve(assignment); err != nil || principal.ID != "operator-1" {
		t.Fatalf("tenant-attested principal=%+v error=%v", principal, err)
	}
	if verifier, err := file.CertificationVerifier(); err != nil || verifier != nil {
		t.Fatalf("unconfigured CertificationVerifier()=%v error=%v", verifier, err)
	}

	// Invalid issuer key material never loads.
	write(map[string]any{
		"certification_issuers": map[string]map[string]string{"issuer.registry": {"2026-09": "not-base64"}},
	})
	if err := file.Validate(); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("invalid issuer key error=%v", err)
	}

	// An absent path has no external verifier and preserves durable authority.
	absent := config.ReviewCertificationFile{}
	if verifier, err := absent.CertificationVerifier(); err != nil || verifier != nil {
		t.Fatalf("absent CertificationVerifier()=%v error=%v", verifier, err)
	}
}
