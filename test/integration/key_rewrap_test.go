//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	keycustodypostgres "github.com/Mujhtech/idenqa/internal/keycustody/postgres"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	keyrewrappostgres "github.com/Mujhtech/idenqa/internal/keyrewrap/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestKeyRewrapDestructionAndRecovery(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, _ := generator.NewTenant()
	otherID, _ := generator.NewTenant()
	if err := admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$3,$3),($2,'active',1,$3,$3)`, tenantID.String(), otherID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	keyring, err := localkms.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyring.Close() }()
	probePurpose, err := kms.NewPurpose("idenqa.test.rewrap-probe.v1")
	if err != nil {
		t.Fatal(err)
	}
	firstEpoch, err := keyring.Wrap(ctx, probePurpose, []byte("probe"), []byte("probe"))
	if err != nil {
		t.Fatal(err)
	}
	first := firstEpoch.Record()

	generation := byte(0)
	custodyStore, err := keycustodypostgres.New(runtime, keyring, keyring, nil, func() (string, []byte, error) {
		identifier, err := generator.New(keycustody.KeyIDPrefix)
		if err != nil {
			return "", nil, err
		}
		generation++
		material := make([]byte, keycustody.KeySize)
		for index := range material {
			material[index] = generation
		}
		return identifier.String(), material, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	custodyService, err := keycustody.NewService(custodyStore, generator, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := generator.NewAPIKey()
	if _, err := custodyService.ExecuteDirect(ctx, scope, actor, "itest-rewrap-create", keycustody.Command{
		Operation: "create", Domain: "identity.identifier.v1", Reason: "integration rewrap custody key",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := custodyService.ExecuteDirect(ctx, scope, actor, "itest-rewrap-rotate", keycustody.Command{
		Operation: "rotate", Domain: "identity.identifier.v1", ExpectedVersion: 1, Reason: "integration rewrap rotation",
	}); err != nil {
		t.Fatal(err)
	}

	// Seed one catalogue event body wrapped under the first material epoch.
	eventID, _ := generator.NewEvent()
	body := []byte(`{"event":"verification.created"}`)
	bodyWrapped, err := keyring.Wrap(ctx, delivery.BodyPurpose(), body, delivery.BodyContext(tenantID.String(), eventID.String()))
	if err != nil {
		t.Fatal(err)
	}
	bodyRecord := bodyWrapped.Record()
	if _, err := admin.Native().Exec(ctx, `INSERT INTO idenqa.webhook_events
		(tenant_id,id,event_type,schema_version,dedupe_key,body,body_digest,state,cursor,delivered_count,created_at,
		 body_provider,body_reference,body_key_version,body_algorithm,aggregate_ids,payload_expires_at,retain_until)
		VALUES($1,$2,'verification.created','1.0','integration-rewrap',$3::bytea,encode(sha256($3::bytea),'hex'),'pending','',0,$4::timestamptz,
		 $5,$6,$7,$8,$9::text[], $4::timestamptz + interval '7 days', $4::timestamptz + interval '365 days')`,
		tenantID.String(), eventID.String(), bodyRecord.Ciphertext, now,
		bodyRecord.Provider, bodyRecord.Reference, bodyRecord.Version, bodyRecord.Algorithm, []string{}); err != nil {
		t.Fatal(err)
	}

	if _, err := keyring.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	secondEpoch, err := keyring.Wrap(ctx, probePurpose, []byte("probe"), []byte("probe"))
	if err != nil {
		t.Fatal(err)
	}
	second := secondEpoch.Record()
	if first.Version == second.Version {
		t.Fatalf("keyring rotation did not change the material epoch: %q", first.Version)
	}

	repository, err := keyrewrappostgres.New(runtime, keyring, keyring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	hmacAdapter, err := keyrewrappostgres.NewHMACKeyAdapter(repository)
	if err != nil {
		t.Fatal(err)
	}
	webhookAdapter, err := keyrewrappostgres.NewWebhookEventAdapter(repository)
	if err != nil {
		t.Fatal(err)
	}
	rewrapService, err := keyrewrap.NewService(repository, repository, []keyrewrap.Adapter{hmacAdapter, webhookAdapter}, keyring, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	destructionService, err := keycustody.NewDestructionService(custodyStore, custodyStore, nil, generator, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	target := keycustody.DestructionTarget{Provider: first.Provider, Reference: first.Reference, Version: first.Version, Algorithm: first.Algorithm}
	blocked, err := destructionService.Verify(ctx, actor, target, "integration blocked destruction")
	if !errors.Is(err, keycustody.ErrDestructionBlocked) {
		t.Fatalf("Verify(live references) error = %v, want ErrDestructionBlocked (receipt %+v)", err, blocked)
	}
	if blocked.Total == 0 || blocked.Counts[keycustody.ReferenceHMACKey] == 0 {
		t.Fatalf("blocked receipt did not find live references: %+v", blocked)
	}

	hmacResult, err := rewrapService.SweepClass(ctx, keyrewrap.ClassHMACKey, 8)
	if err != nil || hmacResult.Rewrapped != 2 {
		t.Fatalf("hmac sweep = %+v, %v", hmacResult, err)
	}
	webhookResult, err := rewrapService.SweepClass(ctx, keyrewrap.ClassWebhookEvent, 8)
	if err != nil || webhookResult.Rewrapped != 1 {
		t.Fatalf("webhook sweep = %+v, %v", webhookResult, err)
	}
	// The HMAC version record now records its rewrap instant and the replacement
	// identity and is idempotent on a second pass.
	var rewrappedAt *time.Time
	var raw []byte
	if err := admin.Native().QueryRow(ctx, `SELECT rewrapped_at,wrapped_key FROM idenqa.hmac_keys WHERE tenant_id=$1 AND domain='identity.identifier.v1' AND version=1`, tenantID.String()).Scan(&rewrappedAt, &raw); err != nil {
		t.Fatal(err)
	}
	var stored kms.WrappedKeyRecord
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if rewrappedAt == nil || stored.Version != second.Version || stored.Provider != second.Provider {
		t.Fatalf("hmac key after rewrap = %+v rewrapped_at=%v", stored, rewrappedAt)
	}
	if result, err := rewrapService.SweepClass(ctx, keyrewrap.ClassHMACKey, 8); err != nil || !result.Completed || result.Rewrapped != 0 {
		t.Fatalf("hmac sweep(idempotent) = %+v, %v", result, err)
	}
	// The catalogue event body still releases the original plaintext.
	var storedBody []byte
	var provider, reference, keyVersion, algorithm string
	if err := admin.Native().QueryRow(ctx, `SELECT body,body_provider,body_reference,body_key_version,body_algorithm FROM idenqa.webhook_events WHERE tenant_id=$1 AND id=$2`, tenantID.String(), eventID.String()).Scan(&storedBody, &provider, &reference, &keyVersion, &algorithm); err != nil {
		t.Fatal(err)
	}
	storedWrapping, err := kms.NewWrappedKey(kms.WrappedKeyRecord{Provider: provider, Reference: reference, Version: keyVersion, Algorithm: algorithm, Ciphertext: storedBody})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := keyring.Unwrap(ctx, delivery.BodyPurpose(), storedWrapping, delivery.BodyContext(tenantID.String(), eventID.String()))
	if err != nil || string(plaintext) != string(body) {
		t.Fatalf("rewrapped event body = %q, %v", plaintext, err)
	}

	// Verified destruction: the retired first epoch is now unreferenced.
	receipt, err := destructionService.Verify(ctx, actor, target, "integration verified destruction")
	if err != nil || !receipt.Verified() || receipt.Total != 0 {
		t.Fatalf("Verify() = %+v, %v", receipt, err)
	}
	schedule, err := destructionService.Schedule(ctx, actor, receipt.ID, 7*24*time.Hour, true, "integration recorded destruction")
	if err != nil || schedule.Mode != "recorded" {
		t.Fatalf("Schedule() = %+v, %v", schedule, err)
	}

	// Dual-control epoch migration ceremony.
	recoveryService, err := keycustody.NewRecoveryService(custodyStore, rewrapService, rewrapService, generator, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	starter, _ := generator.NewAPIKey()
	approver, _ := generator.NewAPIKey()
	started, err := recoveryService.ExecuteDirect(ctx, starter, keycustody.RecoveryCommand{
		Operation: "start", Kind: keycustody.RecoveryKindMigrateEpoch, Class: "keycustody.hmac",
		Target: keycustody.DestructionTarget{Provider: second.Provider, Reference: second.Reference, Version: second.Version, Algorithm: second.Algorithm},
		Reason: "integration epoch migration",
	})
	if err != nil || started.Ceremony.State != keycustody.RecoveryStarted {
		t.Fatalf("recovery start = %+v, %v", started, err)
	}
	if _, err := recoveryService.ExecuteDirect(ctx, starter, keycustody.RecoveryCommand{
		Operation: "approve", Identifier: started.Ceremony.ID, ExpectedVersion: 1, Reason: "integration self approval",
	}); !errors.Is(err, keycustody.ErrRecoveryForbidden) {
		t.Fatalf("self approval error = %v, want ErrRecoveryForbidden", err)
	}
	approved, err := recoveryService.ExecuteDirect(ctx, approver, keycustody.RecoveryCommand{
		Operation: "approve", Identifier: started.Ceremony.ID, ExpectedVersion: 1, Reason: "integration distinct approval",
	})
	if err != nil || approved.Ceremony.State != keycustody.RecoveryApproved {
		t.Fatalf("recovery approve = %+v, %v", approved, err)
	}
	completed, err := recoveryService.ExecuteDirect(ctx, approver, keycustody.RecoveryCommand{
		Operation: "complete", Identifier: started.Ceremony.ID, ExpectedVersion: 2, Reason: "integration epoch completion",
	})
	if err != nil || completed.Ceremony.State != keycustody.RecoveryCompleted || completed.Ceremony.Receipt["generation"] == nil {
		t.Fatalf("recovery complete = %+v, %v", completed, err)
	}
}
