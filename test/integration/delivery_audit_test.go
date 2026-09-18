//go:build integration

package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/audit"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type integrationProtector struct{}

func (integrationProtector) Wrap(_ context.Context, _ kms.Purpose, plaintext, _ []byte) (kms.WrappedKey, error) {
	return kms.NewWrappedKey(kms.WrappedKeyRecord{Provider: "integration", Reference: "keyring", Version: "v1", Algorithm: "TEST", Ciphertext: append([]byte(nil), plaintext...)})
}
func (integrationProtector) Unwrap(_ context.Context, _ kms.Purpose, wrapped kms.WrappedKey, _ []byte) ([]byte, error) {
	return wrapped.Record().Ciphertext, nil
}

var _ platformcrypto.KeyWrapper = integrationProtector{}

func TestDeliveryAndAuditDurabilityIsolationAndTamperEvidence(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err = migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	firstTenant, _ := generator.NewTenant()
	secondTenant, _ := generator.NewTenant()
	if err := admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$3,$3),($2,'active',1,$3,$3)`, firstTenant.String(), secondTenant.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	firstScope, _ := tenant.NewScope(firstTenant)
	secondScope, _ := tenant.NewScope(secondTenant)
	deliveryStore, _ := deliverypostgres.New(runtime)
	manager, _ := delivery.NewManager(deliveryStore, generator, integrationProtector{}, func() time.Time { return now })
	endpoint, secret, err := manager.CreateEndpoint(ctx, firstScope, "https://hooks.example.com/idenqa")
	if err != nil || len(secret) != 32 {
		t.Fatalf("endpoint: %v", err)
	}
	clear(secret)
	if _, err := deliveryStore.FindEndpoint(ctx, secondScope, endpoint.ID); !errors.Is(err, delivery.ErrNotFound) {
		t.Fatalf("cross tenant endpoint = %v", err)
	}
	eventID, _ := generator.NewEvent()
	intent, err := manager.CreateDelivery(ctx, firstScope, endpoint.ID, eventID, "verification.decision.v1", []byte(`{"outcome":"verified"}`))
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, _ := delivery.NewSafeDiagnostic(204, "delivered", 0)
	if err := deliveryStore.RecordAttempt(ctx, firstScope, intent.ID, delivery.Attempt{Number: 1, SecretVersion: 1, SignatureTimestamp: now.Unix(), Diagnostic: diagnostic, CompletedAt: now}, true, false, now); err != nil {
		t.Fatal(err)
	}
	stored, err := deliveryStore.FindDelivery(ctx, firstScope, intent.ID)
	if err != nil || stored.State != delivery.StateDelivered {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	testDeliveryRuntimeRollback(t, runtime, deliveryStore, firstScope, generator, manager, endpoint.ID, now)
	auditStore, _ := auditpostgres.New(runtime)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := auditStore.RegisterKey(ctx, "audit_key_v1", public, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	firstEvent, _ := generator.NewEvent()
	if _, err := auditStore.Append(ctx, firstScope, auditpostgres.Event{EventID: firstEvent.String(), EventType: "verification.decision.v1", AggregateID: intent.ID.String(), ActorID: "worker.policy", EventDigest: strings.Repeat("a", 64), OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	secondEvent, _ := generator.NewEvent()
	if _, err := auditStore.Append(ctx, firstScope, auditpostgres.Event{EventID: secondEvent.String(), EventType: "webhook.delivered.v1", AggregateID: intent.ID.String(), ActorID: "worker.delivery", EventDigest: strings.Repeat("b", 64), OccurredAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := auditStore.Checkpoint(ctx, firstScope, "audit_key_v1", private, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	exported, keys, err := auditStore.Export(ctx, firstScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Verify(exported, keys); err != nil {
		t.Fatal(err)
	}
	otherExport, _, err := auditStore.Export(ctx, secondScope)
	if err != nil || len(otherExport.Records) != 0 {
		t.Fatalf("cross tenant export records=%d err=%v", len(otherExport.Records), err)
	}
	err = runtime.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		var ignored string
		if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, firstTenant.String()).Scan(&ignored); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM idenqa.audit_records WHERE tenant_id=$1 AND sequence=1`, firstTenant.String())
		return err
	})
	if err == nil {
		t.Fatal("append-only audit deletion succeeded")
	}
}
