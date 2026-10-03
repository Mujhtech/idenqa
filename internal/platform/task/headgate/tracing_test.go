package headgate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	libheadgate "github.com/mujhtech/headgate/go"
	"github.com/riandyrn/otelchi"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type traceStore struct {
	libheadgate.TransactionalStore
	envelopes []libheadgate.Envelope
}

func (store *traceStore) Enqueue(_ context.Context, batch []libheadgate.Envelope) error {
	store.envelopes = batch
	return nil
}
func (store *traceStore) EnqueueTx(ctx context.Context, _ libheadgate.Tx, batch []libheadgate.Envelope) error {
	return store.Enqueue(ctx, batch)
}

type traceTransaction struct{ pgx.Tx }

type traceUploads struct {
	adapter *Adapter
	intent  task.Intent
}

func (uploads traceUploads) ListAcceptedUploads(ctx context.Context, _ tenant.Scope, _ id.CaptureToken, _ id.Verification, _ int32) ([]evidence.Upload, error) {
	return nil, uploads.adapter.Enqueue(ctx, uploads.intent)
}

func TestHTTPApplicationEnqueueAndWorkerShareTrace(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	operationTracer := telemetry.NewOperationTracer(provider)
	store := &traceStore{}
	adapter, err := New(store, DefaultConfig("idenqa-test"))
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTracerProvider(provider)
	intent := testIntent(t)
	reader, err := evidence.NewProgressReader(traceUploads{adapter: adapter, intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	reader.WithTracer(operationTracer)
	scope, err := tenant.NewScope(intent.TenantID())
	if err != nil {
		t.Fatal(err)
	}
	captureID, err := id.ParseCaptureToken("ctk_01K00000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	verificationID, err := id.ParseVerification("ver_01K00000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(otelchi.Middleware("api", otelchi.WithTracerProvider(provider), otelchi.WithPropagators(propagation.TraceContext{})))
	router.Get("/capture/progress", func(w http.ResponseWriter, r *http.Request) {
		_, err := reader.Find(r.Context(), evidence.UploadPrincipal{Scope: scope, CaptureTokenID: captureID, VerificationID: verificationID})
		if err != nil {
			t.Errorf("Find: %v", err)
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/capture/progress", nil)
	request.Header.Set("traceparent", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	router.ServeHTTP(httptest.NewRecorder(), request)
	if len(store.envelopes) != 1 {
		t.Fatalf("enqueued %d jobs", len(store.envelopes))
	}
	registry := task.NewRegistry()
	var consumer trace.SpanContext
	if err := registry.Register(intent.Key(), task.HandlerFunc(func(ctx context.Context, _ task.Delivery) task.Result {
		consumer = trace.SpanContextFromContext(ctx)
		_, complete := operationTracer.Start(ctx, "provider.execute")
		complete(nil)
		return task.Complete()
	})); err != nil {
		t.Fatal(err)
	}
	envelope := store.envelopes[0]
	args := verificationCarrier{carrierPayload{payload: envelope.Payload}}
	job := &libheadgate.Job[verificationCarrier]{ID: envelope.ID, Args: args, Queue: envelope.Queue, PartitionKey: envelope.PartitionKey, Deadline: intent.Deadline(), MaxAttempts: envelope.MaxAttempts}
	for range 2 {
		if err := executeJob(t.Context(), registry, job, libheadgate.Metadata{Headers: envelope.Headers, SchemaVersion: envelope.SchemaVersion}, adapter.tracer); err != nil {
			t.Fatal(err)
		}
	}
	if !consumer.IsValid() {
		t.Fatal("handler has no live span")
	}
	spans := recorder.Ended()
	byName := map[string]sdktrace.ReadOnlySpan{}
	attempts := []sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		byName[span.Name()] = span
		if span.Name() == "task.execute" {
			attempts = append(attempts, span)
		}
	}
	serviceSpan := byName["evidence.ProgressReader.Find"]
	producer := byName["task.enqueue"]
	if serviceSpan == nil || producer == nil || len(attempts) != 2 {
		t.Fatalf("missing spans: %v", byName)
	}
	traceID, _ := trace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for _, span := range spans {
		if span.SpanContext().TraceID() != traceID {
			t.Errorf("%s broke trace continuity", span.Name())
		}
	}
	if producer.Parent().SpanID() != serviceSpan.SpanContext().SpanID() {
		t.Fatal("enqueue is not a child of application service")
	}
	for _, attempt := range attempts {
		if attempt.Parent().SpanID() != producer.SpanContext().SpanID() || attempt.SpanKind() != trace.SpanKindConsumer {
			t.Fatal("worker attempt has wrong parent or kind")
		}
	}
	if attempts[0].SpanContext().SpanID() == attempts[1].SpanContext().SpanID() {
		t.Fatal("retry reused span ID")
	}
	if byName["provider.execute"].Parent().SpanID() != consumer.SpanID() {
		t.Fatal("downstream span is not a child of worker")
	}
}

func TestEnqueueTxPropagatesOnlyActiveTraceContext(t *testing.T) {
	t.Parallel()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, parent := provider.Tracer("test").Start(t.Context(), "request")
	defer parent.End()
	member, err := baggage.NewMember("secret", "must-not-travel")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)
	store := &traceStore{}
	adapter, err := New(store, DefaultConfig("idenqa-test"))
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTracerProvider(provider)
	intent := testIntent(t)
	if err := adapter.EnqueueTx(ctx, traceTransaction{}, intent); err != nil {
		t.Fatal(err)
	}
	headers := store.envelopes[0].Headers
	extracted := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier(headers))
	if trace.SpanContextFromContext(extracted).TraceID() != parent.SpanContext().TraceID() {
		t.Fatal("transactional enqueue lost trace")
	}
	if headers["baggage"] != "" || headers["secret"] != "" {
		t.Fatal("baggage was propagated")
	}
	if intent.Traceparent() == headers["traceparent"] {
		t.Fatal("stale intent parent superseded live producer")
	}
}

func TestWorkerFailureIsRecordedWithoutSensitiveCause(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-test")
	if err != nil {
		t.Fatal(err)
	}
	registry := task.NewRegistry()
	cause := errors.New("credential=private-evidence")
	if err := registry.Register(intent.Key(), task.HandlerFunc(func(context.Context, task.Delivery) task.Result { return task.Retry(task.RetryClassTransient, cause) })); err != nil {
		t.Fatal(err)
	}
	if err := execute(t.Context(), registry, libheadgate.Claim{Envelope: envelope}, provider.Tracer("test")); !errors.Is(err, cause) {
		t.Fatalf("handler cause changed: %v", err)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Status().Code != codes.Error {
		t.Fatal("failed attempt missing error status")
	}
	for _, event := range spans[0].Events() {
		for _, value := range event.Attributes {
			if value.Key == "exception.message" && value.Value.AsString() != "task operation failed" {
				t.Fatal("sensitive cause exported")
			}
		}
	}
}

type tracePreparingHandler struct {
	observed trace.SpanContext
	result   task.Result
}

func (handler *tracePreparingHandler) Handle(context.Context, task.Delivery) task.Result {
	return task.Quarantine(errors.New("transaction required"))
}
func (handler *tracePreparingHandler) Prepare(ctx context.Context, _ task.Delivery) (task.TransactionWork, task.Result) {
	handler.observed = trace.SpanContextFromContext(ctx)
	return nil, handler.result
}

func TestTransactionalPreparationUsesLiveConsumerSpan(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-test")
	if err != nil {
		t.Fatal(err)
	}
	handler := &tracePreparingHandler{result: task.Retry(task.RetryClassTransient, errors.New("try again"))}
	registry := task.NewRegistry()
	if err := registry.Register(intent.Key(), handler); err != nil {
		t.Fatal(err)
	}
	job := &libheadgate.Job[verificationCarrier]{ID: envelope.ID, Args: verificationCarrier{carrierPayload{payload: envelope.Payload}}, Queue: envelope.Queue, PartitionKey: envelope.PartitionKey, Deadline: intent.Deadline(), MaxAttempts: envelope.MaxAttempts}
	err = executeJob(t.Context(), registry, job, libheadgate.Metadata{Headers: envelope.Headers, SchemaVersion: envelope.SchemaVersion}, provider.Tracer("test"))
	var retry *retryError
	if !errors.As(err, &retry) {
		t.Fatalf("expected preparation retry: %v", err)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].SpanContext().TraceID() != handler.observed.TraceID() ||
		spans[0].SpanContext().SpanID() != handler.observed.SpanID() || spans[0].Status().Code != codes.Error {
		t.Fatal("preparation not inside live attempt span")
	}
}

func TestWorkerPanicEndsSpanAndPreservesRecoveryValue(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-test")
	if err != nil {
		t.Fatal(err)
	}
	registry := task.NewRegistry()
	if err := registry.Register(intent.Key(), task.HandlerFunc(func(context.Context, task.Delivery) task.Result { panic("private panic cause") })); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if got := recover(); got != "private panic cause" {
				t.Fatalf("recovery value changed: %v", got)
			}
		}()
		_ = execute(t.Context(), registry, libheadgate.Claim{Envelope: envelope}, provider.Tracer("test"))
	}()
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Status().Code != codes.Error {
		t.Fatal("panic did not close failed span")
	}
}
