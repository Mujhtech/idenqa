package config_test

import (
	"context"
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
