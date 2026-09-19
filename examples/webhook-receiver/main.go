// Command webhook-receiver is the self-hosted deployment's example tenant
// backend. It verifies Idenqa webhook signatures with the public Go SDK and
// records each verified event as one NDJSON line for the smoke gate.
//
// Usage (inside the deploy/self-hosted compose stack):
//
//	IDENQA_WEBHOOK_LISTEN_ADDRESS=:443
//	IDENQA_WEBHOOK_CERTIFICATE_FILE=/run/idenqa/tls/webhook-receiver.crt
//	IDENQA_WEBHOOK_PRIVATE_KEY_FILE=/run/idenqa/tls/webhook-receiver.key
//	IDENQA_WEBHOOK_SECRET_FILE=/state/webhook-secret
//	IDENQA_WEBHOOK_RECORD_FILE=/state/verified-events.ndjson
//	IDENQA_WEBHOOK_TOLERANCE=5m
//	webhook-receiver
//
// The signing secret file is the display-once Base64URL secret written by
// `idenqa webhook create --secret-out`; it is read on every request so a newly
// created endpoint can be verified without restarting the receiver. Delivery is
// at-least-once, so verified event identifiers are deduplicated before the
// record is appended. This receiver is a development fixture, not a production
// tenant backend: it has no authentication, no TLS client policy, and no
// durable queue.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	sdk "github.com/Mujhtech/idenqa/sdk/go"
)

const (
	defaultListenAddress = ":8080"
	defaultTolerance     = 5 * time.Minute
	defaultRecordFile    = "/state/verified-events.ndjson"
	defaultSecretFile    = "/state/webhook-secret" //nolint:gosec // dev mount path, not a credential
	maximumSeenEvents    = 4096
)

type receiver struct {
	secretFile   string
	recordFile   string
	tolerance    time.Duration
	seen         map[string]struct{}
	seenOrder    []string
	seenNext     int
	secrets      [][]byte
	secretDigest string
	mutex        sync.Mutex
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("webhook-receiver: %v", err)
	}
}

func run() error {
	address := environment("IDENQA_WEBHOOK_LISTEN_ADDRESS", defaultListenAddress)
	certificateFile := os.Getenv("IDENQA_WEBHOOK_CERTIFICATE_FILE")
	privateKeyFile := os.Getenv("IDENQA_WEBHOOK_PRIVATE_KEY_FILE")
	recordFile := environment("IDENQA_WEBHOOK_RECORD_FILE", defaultRecordFile)
	secretFile := environment("IDENQA_WEBHOOK_SECRET_FILE", defaultSecretFile)
	tolerance, err := time.ParseDuration(environment("IDENQA_WEBHOOK_TOLERANCE", defaultTolerance.String()))
	if err != nil || tolerance <= 0 || tolerance > 24*time.Hour {
		return errors.New("IDENQA_WEBHOOK_TOLERANCE must be a positive duration of at most 24h")
	}
	if certificateFile == "" || privateKeyFile == "" {
		return errors.New("IDENQA_WEBHOOK_CERTIFICATE_FILE and IDENQA_WEBHOOK_PRIVATE_KEY_FILE are required")
	}

	handler := &receiver{
		secretFile: secretFile,
		recordFile: recordFile,
		tolerance:  tolerance,
		seen:       make(map[string]struct{}, maximumSeenEvents),
		seenOrder:  make([]string, 0, maximumSeenEvents),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handler.health)
	mux.HandleFunc("/webhooks/idenqa", handler.webhook)

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServeTLS(certificateFile, privateKeyFile)
	}()
	log.Printf("webhook-receiver listening on %s (record=%s)", address, recordFile)

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func (handler *receiver) health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, "ok\n")
}

func (handler *receiver) webhook(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, int64(sdk.MaximumWebhookBody)+1))
	if err != nil || len(body) > sdk.MaximumWebhookBody {
		http.Error(writer, "invalid body", http.StatusBadRequest)
		return
	}
	timestamp := request.Header.Get(sdk.WebhookTimestampHeader)
	eventID := request.Header.Get(sdk.WebhookEventIDHeader)
	signature := request.Header.Get(sdk.WebhookSignatureHeader)

	verifier, err := handler.verifier()
	if err != nil {
		log.Printf("webhook rejected: signing secret unavailable: %v", err)
		http.Error(writer, "receiver not ready", http.StatusServiceUnavailable)
		return
	}
	if err := verifier.Verify(timestamp, eventID, signature, body); err != nil {
		log.Printf("webhook rejected: %v", err)
		http.Error(writer, "invalid signature", http.StatusUnauthorized)
		return
	}
	if !handler.markSeen(eventID) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	eventType, err := eventTypeOf(body)
	if err != nil {
		http.Error(writer, "invalid envelope", http.StatusBadRequest)
		return
	}
	record := map[string]any{
		"verified":    true,
		"event_id":    eventID,
		"type":        eventType,
		"received_at": time.Now().UTC().Format(time.RFC3339Nano),
		"body_sha256": "sha256:" + hex.EncodeToString(sum(body)),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		http.Error(writer, "record failure", http.StatusInternalServerError)
		return
	}
	if err := appendRecord(handler.recordFile, encoded); err != nil {
		log.Printf("webhook record failed: %v", err)
		http.Error(writer, "record failure", http.StatusInternalServerError)
		return
	}
	log.Printf("verified webhook event")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *receiver) verifier() (*sdk.WebhookVerifier, error) {
	raw, err := os.ReadFile(handler.secretFile)
	if err != nil {
		return nil, err
	}
	encoded := strings.TrimSpace(string(raw))
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return nil, errors.New("signing secret is not unpadded Base64URL")
	}
	// Cache only the most recently observed secret so the per-request read
	// stays cheap while endpoint rotation is still picked up.
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	if handler.secretDigest != encoded {
		handler.secrets = [][]byte{decoded}
		handler.secretDigest = encoded
	}
	return sdk.NewWebhookVerifier(handler.secrets, handler.tolerance)
}

func (handler *receiver) markSeen(eventID string) bool {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	if _, exists := handler.seen[eventID]; exists {
		return false
	}
	if len(handler.seenOrder) < maximumSeenEvents {
		handler.seenOrder = append(handler.seenOrder, eventID)
	} else {
		delete(handler.seen, handler.seenOrder[handler.seenNext])
		handler.seenOrder[handler.seenNext] = eventID
		handler.seenNext = (handler.seenNext + 1) % maximumSeenEvents
	}
	handler.seen[eventID] = struct{}{}
	return true
}

func appendRecord(path string, encoded []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644) //nolint:gosec // operator-mounted record path
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func eventTypeOf(body []byte) (string, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Type == "" {
		return "", errors.New("webhook envelope type is missing")
	}
	return envelope.Type, nil
}

func sum(body []byte) []byte {
	digest := sha256.Sum256(body)
	return digest[:]
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
