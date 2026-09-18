//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type publicWebhookMutation struct {
	delivery.ManagementResult
	SigningSecret string `json:"signing_secret"`
}
type failingWebhookQueue struct{ adapter *taskheadgate.Adapter }

func (queue failingWebhookQueue) EnqueueTx(ctx context.Context, tx pg.Transaction, intents ...task.Intent) error {
	if err := queue.adapter.EnqueueTx(ctx, tx, intents...); err != nil {
		return err
	}
	return errors.New("injected queue failure")
}

func assertPublicWebhookManagement(t *testing.T, client *http.Client, base, credential, keyringFile string, admin, runtime *pg.Pool, scope tenant.Scope, peppers *access.PepperSet) {
	t.Helper()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, key string, body, result any, status int) {
		t.Helper()
		performPublicJSONRequest(t, client, publicJSONRequest{Method: method, URL: base + path, Bearer: credential, IdempotencyKey: key, Body: body, Result: result, WantStatus: status})
	}
	var created, retried, second, rotated publicWebhookMutation
	input := map[string]any{"url": "https://hooks.example.com/public"}
	call("POST", "/v1/webhook-endpoints", "webhook-create", input, &created, 200)
	if created.Endpoint == nil || len(created.SigningSecret) != 43 || created.Replayed {
		t.Fatal("missing display-once result")
	}
	secret, err := base64.RawURLEncoding.DecodeString(created.SigningSecret)
	if err != nil || len(secret) != 32 {
		t.Fatal("invalid secret")
	}
	defer clear(secret)
	endpointPath := "/v1/webhook-endpoints/" + created.Endpoint.ID
	call("POST", "/v1/webhook-endpoints", "webhook-create", input, &retried, 200)
	if !retried.Replayed || retried.SigningSecret != "" || retried.Endpoint.ID != created.Endpoint.ID {
		t.Fatal("create retry disclosed or regenerated secret")
	}
	call("POST", "/v1/webhook-endpoints", "webhook-create", map[string]any{"url": "https://hooks.example.com/changed"}, nil, 409)
	call("POST", "/v1/webhook-endpoints", "webhook-create-second", input, &second, 200)
	var page struct {
		Data []delivery.EndpointView `json:"data"`
		Page struct {
			HasMore bool   `json:"has_more"`
			Next    string `json:"next_cursor"`
		} `json:"page"`
	}
	call("GET", "/v1/webhook-endpoints?limit=1", "", nil, &page, 200)
	if len(page.Data) != 1 || !page.Page.HasMore || page.Page.Next == "" {
		t.Fatal("missing endpoint pagination")
	}
	cursor := page.Page.Next
	firstID := page.Data[0].ID
	call("GET", "/v1/webhook-endpoints?limit=1&cursor="+cursor, "", nil, &page, 200)
	if len(page.Data) != 1 || page.Data[0].ID == firstID {
		t.Fatal("unstable endpoint pagination")
	}
	call("GET", "/v1/webhook-endpoints?limit=2&cursor="+cursor, "", nil, nil, 400)
	call("GET", endpointPath+"/deliveries?limit=1&cursor="+cursor, "", nil, nil, 400)
	rotate := map[string]any{"expected_version": 1, "overlap_seconds": 3600}
	call("POST", endpointPath+"/rotate", "webhook-rotate", rotate, &rotated, 200)
	if rotated.SigningSecret == created.SigningSecret || rotated.Endpoint.SecretVersion != 2 || rotated.Endpoint.PreviousValidUntil == nil {
		t.Fatal("invalid rotation")
	}
	retried = publicWebhookMutation{}
	call("POST", endpointPath+"/rotate", "webhook-rotate", rotate, &retried, 200)
	if !retried.Replayed || retried.SigningSecret != "" || retried.Endpoint.Version != 2 {
		t.Fatal("rotation retry revealed secret")
	}
	call("POST", endpointPath+"/rotate", "webhook-rotate-again", map[string]any{"expected_version": 2, "overlap_seconds": 3600}, nil, 409)
	call("POST", endpointPath+"/disable", "webhook-disable-stale", map[string]any{"expected_version": 1, "reason": "tenant_requested"}, nil, 409)
	var metadata map[string]json.RawMessage
	call("GET", endpointPath, "", nil, &metadata, 200)
	for _, field := range []string{"signing_secret", "wrapped", "active", "previous"} {
		if _, ok := metadata[field]; ok {
			t.Fatalf("metadata leaked %s", field)
		}
	}

	keys, err := localkms.Open(keyringFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := keys.Close(); err != nil {
			t.Error(err)
		}
	}()
	store, err := deliverypostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := delivery.NewManager(store, ids, keys, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	endpointID, err := id.ParseWebhookEndpoint(created.Endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	event, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"event_id":"` + event.String() + `","type":"verification.completed"}`)
	original, err := manager.CreateDelivery(t.Context(), scope, endpointID, event, "verification.completed", body)
	if err != nil {
		t.Fatal(err)
	}
	deliveryPath := "/v1/webhook-deliveries/" + original.ID.String()
	call("POST", deliveryPath+"/replay", "webhook-pending-replay", map[string]any{"reason": "receiver_recovered"}, nil, 409)
	now := time.Now().UTC().Truncate(time.Microsecond)
	diagnostic, err := delivery.NewSafeDiagnostic(400, "http_terminal", 0)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic = diagnostic.WithResponse([]byte(`{"error":"payload rejected","detail":"` + strings.Repeat("x", delivery.MaximumResponseExcerptBytes) + `"}`))
	if err := store.RecordAttempt(t.Context(), scope, original.ID, delivery.Attempt{Number: 1, SecretVersion: 2, SignatureTimestamp: now.Unix(), Diagnostic: diagnostic, CompletedAt: now}, false, false, now); err != nil {
		t.Fatal(err)
	}
	var attempts struct {
		Data []delivery.AttemptView `json:"data"`
	}
	call("GET", deliveryPath+"/attempts", "", nil, &attempts, 200)
	if len(attempts.Data) != 1 || attempts.Data[0].StatusCode != 400 {
		t.Fatal("attempt diagnostics missing")
	}
	responseBody := attempts.Data[0].ResponseBody
	if responseBody == nil || len(*responseBody) != delivery.MaximumResponseExcerptBytes || !attempts.Data[0].ResponseTruncated || !strings.HasPrefix(*responseBody, `{"error":"payload rejected"`) {
		t.Fatalf("attempt response excerpt = %v truncated=%t", responseBody, attempts.Data[0].ResponseTruncated)
	}
	call("GET", deliveryPath, "", nil, &metadata, 200)
	for _, field := range []string{"body", "payload", "body_digest", "signature", "signature_timestamp"} {
		if _, ok := metadata[field]; ok {
			t.Fatalf("inspection leaked %s", field)
		}
	}

	accessStore, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Authenticate(t.Context(), credential)
	if err != nil {
		t.Fatal(err)
	}
	// Queue failure must roll back delivery, outbox, audit and idempotency receipt.
	adapter, err := taskheadgate.NewPostgres(runtime.Native(), taskheadgate.DefaultConfig("idenqa-test"))
	if err != nil {
		t.Fatal(err)
	}
	failingStore, err := deliverypostgres.NewManagementStore(runtime, failingWebhookQueue{adapter}, ids, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	failing, err := delivery.NewManagement(failingStore, ids, keys, time.Now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	count := func() string {
		t.Helper()
		var value string
		err := admin.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
			return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM idenqa.outbox_events WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM idenqa.idempotency_records WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM headgate.headgate_job)::text`, scope.ID().String()).Scan(&value)
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	// Concurrent identical rotations commit once and reveal secret material once.
	type outcome struct {
		result delivery.ManagementResult
		secret []byte
		err    error
	}
	outcomes := make(chan outcome, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			result, secret, err := failing.Execute(t.Context(), actor, "concurrent-rotation", delivery.ManagementCommand{Operation: "rotate", EndpointID: second.Endpoint.ID, ExpectedVersion: 1, OverlapSeconds: 60})
			outcomes <- outcome{result, secret, err}
		}()
	}
	close(start)
	fresh, duplicates := 0, 0
	completedOutcomes := []outcome{<-outcomes, <-outcomes}
	for _, completed := range completedOutcomes {
		if errors.Is(completed.err, idempotency.ErrInProgress) {
			completed.result, completed.secret, completed.err = failing.Execute(t.Context(), actor, "concurrent-rotation", delivery.ManagementCommand{Operation: "rotate", EndpointID: second.Endpoint.ID, ExpectedVersion: 1, OverlapSeconds: 60})
		}
		if completed.err != nil {
			t.Fatal(completed.err)
		}
		if completed.result.Endpoint.Version != 2 {
			t.Fatal("concurrent rotation lost version")
		}
		if completed.result.Replayed {
			duplicates++
			if len(completed.secret) != 0 {
				t.Fatal("duplicate rotation revealed secret")
			}
		} else {
			fresh++
			if len(completed.secret) != 32 {
				t.Fatal("first rotation omitted secret")
			}
		}
		clear(completed.secret)
	}
	if fresh != 1 || duplicates != 1 {
		t.Fatal("concurrent rotation executed more than once")
	}
	before := count()
	if _, _, err := failing.Execute(t.Context(), actor, "queue-rollback", delivery.ManagementCommand{Operation: "replay", DeliveryID: original.ID.String(), Reason: "receiver_recovered"}); err == nil {
		t.Fatal("queue failure was ignored")
	}
	if count() != before {
		t.Fatal("queue failure retained partial effects")
	}
	var replay publicWebhookMutation
	call("POST", deliveryPath+"/replay", "queue-rollback", map[string]any{"reason": "receiver_recovered"}, &replay, 200)
	if replay.Delivery == nil || replay.Delivery.ID == original.ID.String() || replay.Delivery.EventID != event.String() || replay.Delivery.ReplayOf != original.ID.String() {
		t.Fatal("replay lineage or event identity changed")
	}
	replayID, err := id.ParseDelivery(replay.Delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.FindDelivery(t.Context(), scope, replayID)
	if err != nil || !bytes.Equal(persisted.Body, body) {
		t.Fatal("replay changed exact event body")
	}
	before = count()
	retried = publicWebhookMutation{}
	call("POST", deliveryPath+"/replay", "queue-rollback", map[string]any{"reason": "receiver_recovered"}, &retried, 200)
	if !retried.Replayed || retried.Delivery.ID != replay.Delivery.ID || count() != before {
		t.Fatal("replay retry duplicated effects")
	}
	var deliveries struct {
		Data []delivery.View `json:"data"`
	}
	call("GET", endpointPath+"/deliveries", "", nil, &deliveries, 200)
	if len(deliveries.Data) != 2 {
		t.Fatal("delivery listing omitted lineage")
	}
	// Immutable read-only grant does not confer configuration or replay permission.
	readKey, presented := newFullScopeIntegrationCredential(t, ids, scope.ID(), time.Now().UTC(), peppers)
	record := access.KeyRecord{ID: readKey.ID(), TenantID: readKey.TenantID(), Label: readKey.Label(), Digest: readKey.Digest(), PepperVersion: readKey.PepperVersion(), Version: 1, CreatedAt: readKey.CreatedAt(), UpdatedAt: readKey.UpdatedAt()}
	pattern, _ := access.ParsePattern("webhooks:read")
	record.Grant, err = access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatal(err)
	}
	readKey, err = access.RestoreKey(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := accessStore.Create(t.Context(), scope, readKey); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{endpointPath + "/disable", deliveryPath + "/replay"} {
		performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + path, Bearer: presented.Reveal(), IdempotencyKey: "unauthorized", Body: map[string]any{"reason": "tenant_requested"}, WantStatus: 403})
	}
	reader, err := auth.Authenticate(t.Context(), presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := failing.Execute(t.Context(), reader, "direct-denied", delivery.ManagementCommand{Operation: "replay", DeliveryID: original.ID.String(), Reason: "receiver_recovered"}); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatal("application permission bypass")
	}
	// Cross-tenant identifiers and cursors cannot reveal metadata.
	otherID, err := ids.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	err = admin.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants(id,state,version,created_at,updated_at) VALUES ($1,'active',1,$2,$2)`, otherID.String(), now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	otherScope, _ := tenant.NewScope(otherID)
	otherKey, otherPresented := newFullScopeIntegrationCredential(t, ids, otherID, now, peppers)
	if err := accessStore.Create(t.Context(), otherScope, otherKey); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{endpointPath, endpointPath + "/deliveries", deliveryPath, deliveryPath + "/attempts"} {
		performPublicJSONRequest(t, client, publicJSONRequest{Method: "GET", URL: base + path, Bearer: otherPresented.Reveal(), WantStatus: 404})
	}
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "GET", URL: base + "/v1/webhook-endpoints?limit=1&cursor=" + cursor, Bearer: otherPresented.Reveal(), WantStatus: 400})
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + deliveryPath + "/replay", Bearer: otherPresented.Reveal(), IdempotencyKey: "cross-tenant-replay", Body: map[string]any{"reason": "receiver_recovered"}, WantStatus: 404})
	for _, value := range []struct {
		id      string
		version int64
	}{{created.Endpoint.ID, 2}, {second.Endpoint.ID, 2}} {
		call("POST", "/v1/webhook-endpoints/"+value.id+"/disable", "disable-"+value.id, map[string]any{"expected_version": value.version, "reason": "tenant_requested"}, nil, 200)
	}
	call("POST", deliveryPath+"/replay", "disabled-replay", map[string]any{"reason": "receiver_recovered"}, nil, 409)
	// Administrative outbox payloads contain neither callback URL nor signing material.
	err = admin.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		var payload string
		err := tx.QueryRow(ctx, `SELECT COALESCE(string_agg(payload::text,''),'') FROM idenqa.outbox_events WHERE tenant_id=$1 AND event_type LIKE 'webhook.%'`, scope.ID().String()).Scan(&payload)
		if err == nil && (strings.Contains(payload, created.SigningSecret) || strings.Contains(payload, "https://")) {
			return errors.New("administrative outbox leaked secret or URL")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
