// Command audit-export writes one portable tenant audit export and its
// checkpoint public-key history for offline `idenqa audit verify`.
//
// Usage:
//
//	IDENQA_DATABASE_URL=postgres://idenqa:idenqa_dev@postgres:5432/idenqa?sslmode=disable \
//	audit-export --tenant ten_... --export-file /state/audit-export.json --keys-file /state/audit-keys.json
//
// Core does not yet own a configured audit-checkpoint signing key. This
// development helper signs one checkpoint for the current chain head with an
// ephemeral Ed25519 key generated in memory, registers only the public key,
// and writes the private key nowhere. It is a deployment-package fixture for
// the self-hosted smoke gate, not a production key-management path.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "audit-export: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var tenantText, databaseURL, exportFile, keysFile string
	flag.StringVar(&tenantText, "tenant", "", "owning tenant identifier")
	flag.StringVar(&databaseURL, "database-url", os.Getenv("IDENQA_DATABASE_URL"), "PostgreSQL runtime URL")
	flag.StringVar(&exportFile, "export-file", "", "destination portable audit export file")
	flag.StringVar(&keysFile, "keys-file", "", "destination checkpoint public-key history file")
	flag.Parse()
	if tenantText == "" || databaseURL == "" || exportFile == "" || keysFile == "" {
		return errors.New("--tenant, --export-file, --keys-file, and a database URL are required")
	}
	tenantID, err := id.ParseTenant(tenantText)
	if err != nil {
		return errors.New("tenant identifier is invalid")
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return errors.New("tenant scope is invalid")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{
		URL: databaseURL, MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge: time.Minute, MaxConnectionIdle: time.Minute,
		HealthCheckInterval: 10 * time.Second, ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		return errors.New("open database")
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		return errors.New("check application schema")
	}
	store, err := auditpostgres.New(pool)
	if err != nil {
		return errors.New("compose audit store")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("generate ephemeral checkpoint key")
	}
	keyID := "local-" + hex.EncodeToString(publicKey[:8])
	// PostgreSQL timestamptz preserves microseconds while the checkpoint
	// signature binds the exact creation instant. Stay on a whole-second
	// boundary so the persisted export reproduces the signed bytes.
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.RegisterKey(ctx, keyID, publicKey, now.Add(-time.Minute)); err != nil {
		return errors.New("register checkpoint public key")
	}
	if _, err := store.Checkpoint(ctx, scope, keyID, privateKey, now); err != nil {
		return errors.New("sign audit checkpoint (the tenant must already have audit records)")
	}
	exported, keys, err := store.Export(ctx, scope)
	if err != nil {
		return errors.New("export audit chain")
	}
	encodedExport, err := json.Marshal(exported)
	if err != nil {
		return errors.New("encode audit export")
	}
	encodedKeys := make(map[string]string, len(keys))
	for identifier, key := range keys {
		encodedKeys[identifier] = hex.EncodeToString(key)
	}
	encodedKeyHistory, err := json.Marshal(encodedKeys)
	if err != nil {
		return errors.New("encode checkpoint keys")
	}
	if err := os.WriteFile(exportFile, append(encodedExport, '\n'), 0o600); err != nil {
		return errors.New("write audit export")
	}
	if err := os.WriteFile(keysFile, append(encodedKeyHistory, '\n'), 0o600); err != nil {
		return errors.New("write checkpoint keys")
	}
	fmt.Printf("audit_export tenant=%s records=%d checkpoints=%d export_file=%s keys_file=%s\n",
		exported.TenantID, len(exported.Records), len(exported.Checkpoints), exportFile, keysFile)
	return nil
}
