//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/go-chi/chi/v5"
)

// TestWebhookEventStreamReadResumeAndAuthorization proves the read-only tenant
// event feed decrypts canonical envelopes, resumes from Last-Event-ID, wakes
// promptly on emission, and enforces webhooks:read at both boundaries.
func TestWebhookEventStreamReadResumeAndAuthorization(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := platformpostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := platformpostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$2,$2)`, tenantID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := platformpostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := localkms.Create(t.TempDir() + "/keyring.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyring.Close() }()
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x5a}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	fullKey, presented := newFullScopeIntegrationCredential(t, generator, scope.ID(), now, peppers)
	if err := accessStore.Create(ctx, scope, fullKey); err != nil {
		t.Fatal(err)
	}
	configureKey, configurePresented := newIntegrationCredential(t, generator, scope.ID(), now, peppers, "webhooks:configure")
	if err := accessStore.Create(ctx, scope, configureKey); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	middleware, err := httpapi.NewAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	queue := &bodyProofQueue{}
	managementStore, err := deliverypostgres.NewManagementStore(runtime, queue, generator, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	management, err := delivery.NewManagement(managementStore, generator, keyring, func() time.Time { return now }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := delivery.NewStream(managementStore, keyring)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := runtime.OpenNotificationListener(ctx, deliverypostgres.WebhookEventChannel)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := deliverypostgres.NewWakeupHub(listener)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	cursorKeyring, err := cursor.NewKeyring(1, map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{0x33}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := cursor.New(cursorKeyring, clock.System{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpapi.NewWebhookRoutes(middleware, management, stream, hub, codec, logger)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route("/v1", routes.Register)
	server := httptest.NewServer(router)
	defer server.Close()
	emit := func(seed, verificationID string) bool {
		t.Helper()
		inserted := false
		if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()); err != nil {
				return err
			}
			data, err := delivery.EventData(map[string]any{
				"verification_id": verificationID,
				"subject_id":      "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV",
				"decision_id":     "dec_01ARZ3NDEKTSV4RRFFQ69G5FAV",
				"verification":    map[string]any{"id": verificationID, "type": "verification.session", "status": "completed"},
			})
			if err != nil {
				return err
			}
			event, err := webhookv1.NewEvent(webhookv1.DeterministicEventID(seed), scope.ID().String(), "ng-lagos", webhookv1.VerificationCompleted, webhookv1.SchemaVersion, time.Now().UTC().Truncate(time.Microsecond), data)
			if err != nil {
				return err
			}
			inserted, err = deliverypostgres.EmitEventWithin(ctx, tx, keyring, event, seed)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return inserted
	}
	if !emit("verification.completed:stream-a", "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Fatal("first event emission reported a duplicate")
	}
	if emit("verification.completed:stream-a", "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Fatal("duplicate event emission reported an insertion")
	}
	emit("verification.completed:stream-b", "ver_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	var listing struct {
		Data []json.RawMessage `json:"data"`
		Page struct {
			HasMore    bool    `json:"has_more"`
			NextCursor *string `json:"next_cursor"`
		} `json:"page"`
	}
	performPublicJSONRequest(t, server.Client(), publicJSONRequest{Method: http.MethodGet, URL: server.URL + "/v1/webhook-events?limit=1", Bearer: presented.Reveal(), WantStatus: http.StatusOK, Result: &listing})
	if len(listing.Data) != 1 || !listing.Page.HasMore || listing.Page.NextCursor == nil {
		t.Fatalf("event page=%d more=%t", len(listing.Data), listing.Page.HasMore)
	}
	var first struct {
		ID            string `json:"id"`
		Type          string `json:"type"`
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(listing.Data[0], &first); err != nil || first.Type != "verification.completed" || first.SchemaVersion != "1.0" {
		t.Fatalf("first event=%s err=%v", listing.Data[0], err)
	}
	var second struct {
		Data []json.RawMessage `json:"data"`
		Page struct {
			HasMore bool `json:"has_more"`
		} `json:"page"`
	}
	performPublicJSONRequest(t, server.Client(), publicJSONRequest{Method: http.MethodGet, URL: server.URL + "/v1/webhook-events?limit=1&cursor=" + *listing.Page.NextCursor, Bearer: presented.Reveal(), WantStatus: http.StatusOK, Result: &second})
	if len(second.Data) != 1 || second.Page.HasMore {
		t.Fatalf("second page=%d more=%t", len(second.Data), second.Page.HasMore)
	}
	var secondEvent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(second.Data[0], &secondEvent); err != nil || secondEvent.ID == first.ID {
		t.Fatalf("second event=%s err=%v", second.Data[0], err)
	}
	performPublicJSONRequest(t, server.Client(), publicJSONRequest{Method: http.MethodGet, URL: server.URL + "/v1/webhook-events", Bearer: configurePresented.Reveal(), WantStatus: http.StatusForbidden})
	performPublicJSONRequest(t, server.Client(), publicJSONRequest{Method: http.MethodGet, URL: server.URL + "/v1/webhook-events", WantStatus: http.StatusUnauthorized})

	streamContext, cancelStream := context.WithTimeout(ctx, 15*time.Second)
	defer cancelStream()
	request, err := http.NewRequestWithContext(streamContext, http.MethodGet, server.URL+"/v1/webhook-events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+presented.Reveal())
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status=%d type=%s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "retry: 1000\n" {
		t.Fatalf("stream preamble=%q err=%v", line, err)
	}
	liveID := webhookv1.DeterministicEventID("verification.completed:stream-c")
	emit("verification.completed:stream-c", "ver_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	liveSequence, liveBody := readStreamFrame(t, reader, 5*time.Second)
	if envelopeID(liveBody) != liveID {
		t.Fatalf("live envelope=%s want=%s", envelopeID(liveBody), liveID)
	}
	cancelStream()
	_ = response.Body.Close()

	resumeContext, cancelResume := context.WithTimeout(ctx, 10*time.Second)
	defer cancelResume()
	resumed, err := http.NewRequestWithContext(resumeContext, http.MethodGet, server.URL+"/v1/webhook-events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Header.Set("Accept", "text/event-stream")
	resumed.Header.Set("Authorization", "Bearer "+presented.Reveal())
	resumed.Header.Set("Last-Event-ID", liveSequence)
	resumedResponse, err := server.Client().Do(resumed)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumedResponse.Body.Close() }()
	if resumedResponse.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resumedResponse.Body, 1024))
		t.Fatalf("resume status=%d body=%s", resumedResponse.StatusCode, raw)
	}
	resumedReader := bufio.NewReader(resumedResponse.Body)
	if line, err := resumedReader.ReadString('\n'); err != nil || line != "retry: 1000\n" {
		t.Fatalf("resume preamble=%q err=%v", line, err)
	}
	resumeID := webhookv1.DeterministicEventID("verification.completed:stream-d")
	emit("verification.completed:stream-d", "ver_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	if _, body := readStreamFrame(t, resumedReader, 5*time.Second); envelopeID(body) != resumeID {
		t.Fatalf("resumed envelope=%s want=%s", envelopeID(body), resumeID)
	} else if !bytes.Contains(body, []byte("ver_01ARZ3NDEKTSV4RRFFQ69G5FAY")) {
		t.Fatal("resumed frame lost its canonical payload")
	}
	cancelResume()
	_ = resumedResponse.Body.Close()
	holdID, err := generator.NewLegalHold()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.legal_holds
			(tenant_id,id,aggregate_id,authority,reason,starts_at,review_at,created_at)
			VALUES ($1,$2,$3,'court-order','retention integration',$4,$5,$4)`,
			scope.ID().String(), holdID.String(), "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", now, now.Add(400*24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	retentionStore, err := deliverypostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	result, err := retentionStore.ExpireWebhookData(ctx, now.Add(8*24*time.Hour), 500)
	if err != nil || result.PayloadsExpired < 3 {
		t.Fatalf("retention with hold = %+v, %v", result, err)
	}
	streamActor, err := authenticator.Authenticate(ctx, presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	held, err := stream.Events(ctx, streamActor, 0, nil, 10)
	if err != nil || len(held) != 1 || !bytes.Contains(held[0].Body, []byte("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")) {
		t.Fatalf("held webhook events = %d, %v", len(held), err)
	}
	if err := runtime.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE idenqa.legal_holds SET released_at=$3 WHERE tenant_id=$1 AND id=$2`,
			scope.ID().String(), holdID.String(), now.Add(9*24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := retentionStore.ExpireWebhookData(ctx, now.Add(10*24*time.Hour), 500); err != nil {
		t.Fatal(err)
	}
	if expired, err := stream.Events(ctx, streamActor, 0, nil, 10); err != nil || len(expired) != 0 {
		t.Fatalf("expired webhook event stream = %d, %v", len(expired), err)
	}
}

func readStreamFrame(t *testing.T, reader *bufio.Reader, timeout time.Duration) (string, []byte) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var frameID string
	var data []byte
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event stream: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			if len(data) == 0 {
				continue
			}
			return frameID, data
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "id: "):
			frameID = line[len("id: "):]
		case strings.HasPrefix(line, "data: "):
			data = append(data, line[len("data: "):]...)
		}
	}
	t.Fatal("event stream frame timed out")
	return "", nil
}

func envelopeID(body []byte) string {
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return envelope.ID
}

func newIntegrationCredential(t *testing.T, generator *id.Generator, tenantID id.Tenant, createdAt time.Time, peppers *access.PepperSet, patternValue string) (access.Key, access.PresentedKey) {
	t.Helper()
	identifier, err := generator.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	secretGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x42}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := secretGenerator.Generate(tenantID, identifier)
	if err != nil {
		t.Fatal(err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatal(err)
	}
	pattern, err := access.ParsePattern(patternValue)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatal(err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: identifier, TenantID: tenantID, Label: "event stream integration",
		Digest: digest, PepperVersion: version, Grant: grant, Version: 1,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return key, presented
}
