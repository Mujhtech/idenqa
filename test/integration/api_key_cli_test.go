//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	bootstrapidenqa "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

func TestAPIKeyCLILifecycleAndAudit(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	pepperMaterial := bytes.Repeat([]byte{0x42}, 32)
	t.Setenv("IDENQA_ENVIRONMENT", "test")
	t.Setenv("IDENQA_DATABASE_URL", database.url)
	t.Setenv("IDENQA_DATABASE_ADMIN_URL", database.url)
	t.Setenv("IDENQA_DATABASE_ROLE", "")
	t.Setenv("IDENQA_API_KEY_ACTIVE_PEPPER_VERSION", "1")
	t.Setenv("IDENQA_API_KEY_PEPPERS", "1="+base64.RawURLEncoding.EncodeToString(pepperMaterial))
	t.Setenv("IDENQA_API_KEY_ALLOW_NO_EXPIRY", "true")
	t.Setenv("IDENQA_API_KEY_MAXIMUM_ROTATION_OVERLAP", "30m")

	tenantOutput := runCLI(t,
		"tenant", "create", "--actor", "integration-operator", "--reason", "create CLI tenant",
	)
	tenantID := cliOutputValue(t, tenantOutput, "id")
	createOutput := runCLI(t,
		"api-key", "create", "--tenant", tenantID,
		"--actor", "integration-operator", "--reason", "create backend key",
		"--label", "integration-backend", "--scope", "tenant:read", "--scope", "verification_sessions:*",
		"--no-expiry",
	)
	firstKeyID := cliOutputValue(t, createOutput, "id")
	firstCredential := cliOutputValue(t, createOutput, "credential")
	if strings.Count(createOutput, firstCredential) != 1 || !strings.HasPrefix(firstCredential, "idq_v1_") {
		t.Fatal("create did not reveal exactly one display-once credential")
	}

	listOutput := runCLI(t,
		"api-key", "list", "--tenant", tenantID,
		"--actor", "integration-operator", "--reason", "inspect backend keys",
	)
	if !strings.Contains(listOutput, firstKeyID) || strings.Contains(listOutput, "idq_v1_") ||
		strings.Contains(listOutput, "credential=") {
		t.Fatalf("list output disclosed or omitted key metadata: %s", listOutput)
	}

	rotateOutput := runCLI(t,
		"api-key", "rotate", "--tenant", tenantID, "--id", firstKeyID,
		"--actor", "integration-operator", "--reason", "rotate backend key",
		"--overlap", "5m", "--no-expiry", "--confirm",
	)
	secondKeyID := cliOutputValue(t, rotateOutput, "id")
	secondCredential := cliOutputValue(t, rotateOutput, "credential")
	if secondKeyID == firstKeyID || secondCredential == firstCredential ||
		strings.Count(rotateOutput, secondCredential) != 1 {
		t.Fatal("rotation did not reveal exactly one distinct successor credential")
	}

	secondListOutput := runCLI(t,
		"api-key", "list", "--tenant", tenantID,
		"--actor", "integration-operator", "--reason", "inspect rotated keys",
	)
	if !strings.Contains(secondListOutput, firstKeyID) || !strings.Contains(secondListOutput, secondKeyID) ||
		strings.Contains(secondListOutput, firstCredential) || strings.Contains(secondListOutput, secondCredential) {
		t.Fatal("rotated key listing is incomplete or disclosed credential material")
	}

	revokeOutput := runCLI(t,
		"api-key", "revoke", "--tenant", tenantID, "--id", secondKeyID, "--version", "1",
		"--actor", "integration-operator", "--reason", "revoke successor key", "--confirm",
	)
	if !strings.Contains(revokeOutput, "state=revoked") || !strings.Contains(revokeOutput, "version=2") ||
		strings.Contains(revokeOutput, "credential=") {
		t.Fatalf("revoke output is incorrect or disclosed credential material: %s", revokeOutput)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(
		ctx context.Context,
		tx idenqapostgres.Transaction,
	) error {
		rows, err := tx.Query(ctx, `SELECT action, count(*) FROM idenqa.api_key_admin_audit GROUP BY action`)
		if err != nil {
			return err
		}
		defer rows.Close()
		counts := map[string]int64{}
		for rows.Next() {
			var action string
			var count int64
			if err := rows.Scan(&action, &count); err != nil {
				return err
			}
			counts[action] = count
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if counts["issue"] != 1 || counts["list"] != 2 || counts["rotate"] != 1 || counts["revoke"] != 1 {
			t.Fatalf("API-key admin audit counts = %#v", counts)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("inspect API-key admin audit: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	store, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: pepperMaterial})
	if err != nil {
		t.Fatalf("new pepper set: %v", err)
	}
	authenticator, err := access.NewAuthenticator(store, peppers, clock.System{})
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	if _, err := authenticator.Authenticate(ctx, firstCredential); err != nil {
		t.Fatalf("predecessor should authenticate during overlap: %v", err)
	}
	if _, err := authenticator.Authenticate(ctx, secondCredential); !errors.Is(err, access.ErrInvalidCredential) {
		t.Fatalf("revoked successor authentication error = %v, want ErrInvalidCredential", err)
	}
}

func runCLI(t *testing.T, args ...string) string {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := bootstrapidenqa.Run(args, &stdout, &stderr, buildinfo.Info{}); code != 0 {
		t.Fatalf("idenqa %s exit code = %d; stderr=%s", strings.Join(args, " "), code, stderr.String())
	}

	return stdout.String()
}

func cliOutputValue(t *testing.T, output, name string) string {
	t.Helper()

	prefix := name + "="
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix)
		}
	}
	t.Fatalf("CLI output %q does not contain %s", output, prefix)

	return ""
}
