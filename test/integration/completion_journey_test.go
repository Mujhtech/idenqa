//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	sdk "github.com/Mujhtech/idenqa/sdk/go"
)

type journeyReceiver struct {
	server   *httptest.Server
	mu       sync.Mutex
	bodies   [][]byte
	events   []string
	failures []error
	verifier *sdk.WebhookVerifier
	accept   bool
}

func newJourneyReceiver(t *testing.T, client *http.Client, baseURL, credential string) *journeyReceiver {
	t.Helper()
	receiver := &journeyReceiver{}
	receiver.server = httptest.NewTLSServer(http.HandlerFunc(receiver.receive))
	t.Cleanup(receiver.server.Close)
	var created publicWebhookMutation
	performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodPost, URL: baseURL + "/v1/webhook-endpoints", Bearer: credential, IdempotencyKey: "journey-webhook-create", Body: map[string]any{"url": "https://journey.example.test/webhook"}, WantStatus: 200, Result: &created})
	secret, err := base64.RawURLEncoding.DecodeString(created.SigningSecret)
	if err != nil || len(secret) != 32 {
		t.Fatal("public webhook creation omitted signing secret")
	}
	defer clear(secret)
	receiver.verifier, err = sdk.NewWebhookVerifier([][]byte{secret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return receiver
}

func (receiver *journeyReceiver) receive(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, sdk.MaximumWebhookBody+1))
	if err == nil {
		err = receiver.verifier.Verify(request.Header.Get(sdk.WebhookTimestampHeader), request.Header.Get(sdk.WebhookEventIDHeader), request.Header.Get(sdk.WebhookSignatureHeader), body)
	}
	if err == nil && receiver.verifier.Verify(request.Header.Get(sdk.WebhookTimestampHeader), request.Header.Get(sdk.WebhookEventIDHeader), request.Header.Get(sdk.WebhookSignatureHeader), append(bytes.Clone(body), ' ')) == nil {
		err = errors.New("SDK accepted tampered body")
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if err != nil {
		receiver.failures = append(receiver.failures, err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	receiver.bodies = append(receiver.bodies, body)
	receiver.events = append(receiver.events, request.Header.Get(sdk.WebhookEventIDHeader))
	if !receiver.accept {
		writer.Header().Set("Retry-After", "2")
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// This injected transport reaches only the test TLS receiver. Production still
// uses the public-DNS-pinned callback client; its SSRF restrictions are unchanged.
func (receiver *journeyReceiver) Send(ctx context.Context, target string, signature delivery.Signature, body []byte) (delivery.SafeDiagnostic, bool, bool, error) {
	if target != "https://journey.example.test/webhook" {
		return delivery.SafeDiagnostic{}, false, false, errors.New("unexpected callback target")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, receiver.server.URL, bytes.NewReader(body))
	if err != nil {
		return delivery.SafeDiagnostic{}, false, false, err
	}
	request.Header.Set(sdk.WebhookSignatureHeader, signature.Value)
	request.Header.Set(sdk.WebhookTimestampHeader, signature.Timestamp)
	request.Header.Set(sdk.WebhookEventIDHeader, signature.EventID)
	response, err := receiver.server.Client().Do(request)
	if err != nil {
		return delivery.SafeDiagnostic{}, false, true, err
	}
	if err := response.Body.Close(); err != nil {
		return delivery.SafeDiagnostic{}, false, true, err
	}
	success := response.StatusCode == http.StatusNoContent
	class := "delivered"
	retryAfter := time.Duration(0)
	if !success {
		class = "retryable_status"
		retryAfter = 2 * time.Second
	}
	diagnostic, err := delivery.NewSafeDiagnostic(response.StatusCode, class, retryAfter)
	return diagnostic, success, !success, err
}

type journeyKeys struct{ keys *localkms.Keyring }

func (keys journeyKeys) Shutdown(context.Context) error { return keys.keys.Close() }
func (receiver *journeyReceiver) infrastructure(t *testing.T, path string) bootstrapworker.DeliveryInfrastructure {
	t.Helper()
	keys, err := localkms.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	infrastructure, err := bootstrapworker.NewDeliveryInfrastructure(keys, keys, receiver, journeyKeys{keys})
	if err != nil {
		t.Fatal(err)
	}
	return infrastructure
}

func (receiver *journeyReceiver) waitForDelivery(t *testing.T, admin *pg.Pool, scope tenant.Scope, verificationID id.Verification) id.Decision {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var state string
		var decision *string
		var delivered, attempts int
		if err := admin.Native().QueryRow(t.Context(), `SELECT state,completed_decision_id,(SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND state='delivered'),(SELECT count(*) FROM idenqa.webhook_delivery_attempts WHERE tenant_id=$1) FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), verificationID.String()).Scan(&state, &decision, &delivered, &attempts); err != nil {
			t.Fatal(err)
		}
		if state == "completed" && decision != nil && delivered == 1 && attempts == 2 {
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.failures) != 0 {
				t.Fatalf("webhook verification: %v", receiver.failures)
			}
			if len(receiver.bodies) < 2 {
				t.Fatal("missing callback retry")
			}
			for index := 1; index < len(receiver.bodies); index++ {
				if receiver.events[0] != receiver.events[index] || !bytes.Equal(receiver.bodies[0], receiver.bodies[index]) {
					t.Fatal("retry changed signed event identity or body")
				}
			}
			var envelope struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Data struct {
					VerificationID string `json:"verification_id"`
					DecisionID     string `json:"decision_id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(receiver.bodies[0], &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.ID != receiver.events[0] || envelope.Type != "verification.completed" || envelope.Data.VerificationID != verificationID.String() || envelope.Data.DecisionID != *decision {
				t.Fatalf("completion envelope mismatch: %+v", envelope)
			}
			parsed, err := id.ParseDecision(*decision)
			if err != nil {
				t.Fatal(err)
			}
			return parsed
		}
		select {
		case <-deadline.C:
			t.Fatalf("completion delivery not finished: state=%s delivered=%d attempts=%d", state, delivered, attempts)
		case <-tick.C:
		}
	}
}

func (receiver *journeyReceiver) enableSuccess() {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	receiver.accept = true
}
