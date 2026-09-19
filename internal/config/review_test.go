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

func TestReviewAssignmentReloadFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	key, _ := ids.NewAPIKey()
	scope, _ := tenant.NewScope(tenantID)
	assignment := review.Assignment{TenantID: tenantID.String(), APIKeyID: key.String(), OperatorID: "operator-1", Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"}, Regions: []string{"ng-1"}, NotBefore: now, ExpiresAt: now.Add(time.Hour)}
	data, err := json.Marshal(map[string]any{"assignments": []review.Assignment{assignment}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "review.json")
	file := config.ReviewAuthorityFile{Path: path}
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(data)
	if err := file.Validate(); err != nil {
		t.Fatal(err)
	}
	principal, err := file.ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now)
	if err != nil || principal.ID != "operator-1" {
		t.Fatalf("principal=%+v error=%v", principal, err)
	}
	for _, replacement := range []string{`{"assignments":[]}`, `{"assignments":[],"allow_all":true}`, `invalid`, string(make([]byte, 257<<10))} {
		write([]byte(replacement))
		if _, err := file.ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now); !errors.Is(err, review.ErrForbidden) {
			t.Fatalf("replacement error=%v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := file.ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("removed file error=%v", err)
	}
}

func TestReviewAuthorityFileExternalCertificationVerifier(t *testing.T) {
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
	file := config.ReviewAuthorityFile{Path: path}
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
		"assignments":           []review.Assignment{assignment},
		"certification_issuers": map[string]map[string]string{"issuer.registry": {"2026-09": base64.StdEncoding.EncodeToString(public)}},
	})
	if err := file.Validate(); err != nil {
		t.Fatal(err)
	}
	principal, err := file.ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now)
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
		"assignments":           []review.Assignment{unasserted},
		"certification_issuers": map[string]map[string]string{"issuer.registry": {"2026-09": base64.StdEncoding.EncodeToString(public)}},
	})
	if _, err := file.ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("missing assertion error=%v", err)
	}

	// Without a configured issuer the tenant-attested behaviour is preserved.
	write(map[string]any{"assignments": []review.Assignment{assignment}})
	if principal, err := file.ResolveReviewer(context.Background(), scope, review.Actor{ID: key.String()}, "ng-1", now); err != nil || principal.ID != "operator-1" {
		t.Fatalf("tenant-attested principal=%+v error=%v", principal, err)
	}
	if verifier, err := file.CertificationVerifier(); err != nil || verifier != nil {
		t.Fatalf("unconfigured CertificationVerifier()=%v error=%v", verifier, err)
	}

	// Invalid issuer key material never loads.
	write(map[string]any{
		"assignments":           []review.Assignment{assignment},
		"certification_issuers": map[string]map[string]string{"issuer.registry": {"2026-09": "not-base64"}},
	})
	if err := file.Validate(); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("invalid issuer key error=%v", err)
	}

	// An absent path has no external verifier and preserves durable authority.
	absent := config.ReviewAuthorityFile{}
	if verifier, err := absent.CertificationVerifier(); err != nil || verifier != nil {
		t.Fatalf("absent CertificationVerifier()=%v error=%v", verifier, err)
	}
}
