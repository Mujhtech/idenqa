package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type recordedPoint struct {
	labels map[string]string
	value  float64
	count  uint64
}

func collectDomainMetrics(t *testing.T) (*DomainMetrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := NewDomainMetrics(provider)
	if err != nil {
		t.Fatalf("NewDomainMetrics() error = %v", err)
	}
	return metrics, reader
}

func flattenPoints(t *testing.T, reader *sdkmetric.ManualReader) map[string][]recordedPoint {
	t.Helper()
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &resource); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	points := make(map[string][]recordedPoint)
	for _, scope := range resource.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			switch data := instrument.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					points[instrument.Name] = append(points[instrument.Name], recordedPoint{labels: labels(point.Attributes), value: float64(point.Value)})
				}
			case metricdata.Gauge[int64]:
				for _, point := range data.DataPoints {
					points[instrument.Name] = append(points[instrument.Name], recordedPoint{labels: labels(point.Attributes), value: float64(point.Value)})
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					points[instrument.Name] = append(points[instrument.Name], recordedPoint{
						labels: labels(point.Attributes), value: point.Sum, count: point.Count,
					})
				}
			default:
				t.Fatalf("unexpected instrument type %T for %s", instrument.Data, instrument.Name)
			}
		}
	}
	return points
}

func labels(set attribute.Set) map[string]string {
	values := set.ToSlice()
	labels := make(map[string]string, len(values))
	for _, value := range values {
		labels[string(value.Key)] = value.Value.AsString()
	}
	return labels
}

func findPoint(t *testing.T, points []recordedPoint, expected map[string]string) recordedPoint {
	t.Helper()
	for _, point := range points {
		matches := true
		for key, value := range expected {
			if point.labels[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return point
		}
	}
	t.Fatalf("no point matched %v among %v", expected, points)
	return recordedPoint{}
}

func TestDomainMetricsEmitEveryBoundedFamily(t *testing.T) {
	t.Parallel()
	metrics, reader := collectDomainMetrics(t)

	metrics.RecordVerificationTransition(observability.Transition{
		From: observability.StateCollecting, To: observability.StateProcessing,
		FailureClass: observability.FailureNone, Region: "sa-riyadh-1",
	})
	metrics.RecordVerificationCompletion(observability.WorkflowCompletion{
		Outcome: observability.OutcomeVerified, Duration: 90 * time.Second,
		TimeToDecision: 80 * time.Second, Region: "sa-riyadh-1",
	})
	metrics.RecordVerificationOperationalFailure(observability.OperationalFailure{
		FailureClass: observability.FailureProvider, Region: "sa-riyadh-1",
	})
	metrics.RecordVerificationSessionStart(observability.SessionStart{Region: "sa-riyadh-1"})
	metrics.RecordVerificationRecapture(observability.Recapture{
		Reason: observability.RecaptureRequested, Region: "sa-riyadh-1",
	})
	metrics.RecordCaptureStep(observability.CaptureEvent{
		Step: observability.StepDocumentFront, Outcome: observability.CaptureAccepted,
	})
	metrics.RecordDeliveryAttempt(observability.DeliveryAttempt{
		Outcome: observability.Delivered, Attempt: 2, Duration: 1500 * time.Millisecond,
	})
	metrics.RecordProviderDispatch(observability.ProviderDispatch{
		Provider: "smileid", Outcome: observability.DispatchCompleted, FailureClass: observability.FailureNone,
	})
	metrics.RecordProviderCallbackDelay(observability.ProviderCallbackDelay{
		Provider: "smileid", Delay: 30 * time.Second,
	})
	metrics.RecordProviderHealth(observability.ProviderHealth{
		Provider: "smileid", State: observability.HealthNotReady, Region: "sa-riyadh-1",
	})
	metrics.RecordModelDispatch(observability.ModelDispatch{
		Model: "idenqa.model.liveness", Outcome: observability.DispatchFailed,
		FailureClass: observability.FailureUnavailable, Duration: 2 * time.Second,
	})
	metrics.RecordModelHealth(observability.ModelHealth{
		Model: "idenqa.model.liveness", State: observability.HealthDegraded,
	})
	metrics.RecordDeletionTransition(observability.DeletionTransition{
		From: observability.DeletionRequested, To: observability.DeletionInProgress,
		Kind: observability.DataRawEvidence, Region: "sa-riyadh-1",
	})
	metrics.RecordDeletionBacklog(observability.DeletionBacklog{State: observability.DeletionAwaiting, Count: 3})
	metrics.RecordBackupExpiry(observability.BackupExpiry{Age: 24 * time.Hour, Region: "sa-riyadh-1"})
	metrics.RecordReviewResolution(observability.ReviewResolution{
		Outcome: observability.ReviewSatisfied, Oversight: observability.OversightDual,
		Duration: 10 * time.Minute, Region: "sa-riyadh-1",
	})

	points := flattenPoints(t, reader)
	tests := []struct {
		name    string
		labels  map[string]string
		minimum int
	}{
		{name: "idenqa.verification.transitions", labels: map[string]string{"from": "collecting", "to": "processing", "failure_class": "none", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.verification.completions", labels: map[string]string{"outcome": "verified", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.verification.workflow.duration", labels: map[string]string{"outcome": "verified", "region": "sa-riyadh-1"}, minimum: 0},
		{name: "idenqa.verification.time_to_decision", labels: map[string]string{"outcome": "verified", "region": "sa-riyadh-1"}, minimum: 0},
		{name: "idenqa.verification.operational_failures", labels: map[string]string{"failure_class": "provider", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.verification.sessions.started", labels: map[string]string{"region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.verification.recaptures", labels: map[string]string{"reason": "requested", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.capture.steps", labels: map[string]string{"step": "document_front", "outcome": "accepted"}, minimum: 1},
		{name: "idenqa.delivery.attempts", labels: map[string]string{"outcome": "delivered", "attempt": "2"}, minimum: 1},
		{name: "idenqa.delivery.attempt.duration", labels: map[string]string{"outcome": "delivered", "attempt": "2"}, minimum: 0},
		{name: "idenqa.provider.dispatches", labels: map[string]string{"provider": "smileid", "outcome": "completed", "failure_class": "none"}, minimum: 1},
		{name: "idenqa.provider.callback.delay", labels: map[string]string{"provider": "smileid"}, minimum: 0},
		{name: "idenqa.provider.health", labels: map[string]string{"provider": "smileid", "state": "not_ready", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.model.dispatches", labels: map[string]string{"model": "idenqa.model.liveness", "outcome": "failed", "failure_class": "unavailable"}, minimum: 1},
		{name: "idenqa.model.dispatch.duration", labels: map[string]string{"model": "idenqa.model.liveness", "outcome": "failed", "failure_class": "unavailable"}, minimum: 0},
		{name: "idenqa.model.health", labels: map[string]string{"model": "idenqa.model.liveness", "state": "degraded"}, minimum: 1},
		{name: "idenqa.privacy.deletion.transitions", labels: map[string]string{"from": "requested", "to": "in_progress", "kind": "raw_evidence", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.privacy.deletion.backlog", labels: map[string]string{"state": "awaiting_backup_expiry"}, minimum: 1},
		{name: "idenqa.privacy.backup.expiry.age", labels: map[string]string{"region": "sa-riyadh-1"}, minimum: 0},
		{name: "idenqa.review.resolutions", labels: map[string]string{"outcome": "satisfied", "oversight": "dual", "region": "sa-riyadh-1"}, minimum: 1},
		{name: "idenqa.review.resolution.duration", labels: map[string]string{"outcome": "satisfied", "oversight": "dual", "region": "sa-riyadh-1"}, minimum: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			series, exists := points[test.name]
			if !exists {
				t.Fatalf("metric %s was not emitted", test.name)
			}
			findPoint(t, series, test.labels)
		})
	}
}

func TestDomainMetricsCollapseForbiddenIdentifiers(t *testing.T) {
	t.Parallel()
	metrics, reader := collectDomainMetrics(t)

	const (
		subjectID  = "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"
		evidenceID = "evd_01ARZ3NDEKTSV4RRFFQ69G5FAV"
		tenantID   = "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	)
	metrics.RecordVerificationTransition(observability.Transition{
		From: observability.State(subjectID), To: observability.State(evidenceID),
		FailureClass: observability.FailureClass(tenantID), Region: observability.Region(subjectID),
	})
	metrics.RecordVerificationSessionStart(observability.SessionStart{Region: observability.Region(tenantID)})
	metrics.RecordProviderDispatch(observability.ProviderDispatch{
		Provider: observability.Provider(subjectID), Outcome: observability.DispatchOutcome(evidenceID),
		FailureClass: observability.FailureClass(tenantID),
	})
	metrics.RecordModelDispatch(observability.ModelDispatch{
		Model: observability.Model(evidenceID), Outcome: observability.DispatchOutcome(subjectID),
		FailureClass: observability.FailureClass(tenantID),
	})

	points := flattenPoints(t, reader)
	for name, series := range points {
		for _, point := range series {
			for key, value := range point.labels {
				for _, forbidden := range []string{subjectID, evidenceID, tenantID, "sub_", "evd_", "ten_"} {
					if strings.Contains(value, forbidden) {
						t.Fatalf("metric %s label %s leaked %q", name, key, value)
					}
				}
			}
		}
	}
	transition := findPoint(t, points["idenqa.verification.transitions"], map[string]string{"from": "other", "to": "other", "failure_class": "other"})
	if transition.labels["region"] != "unknown" {
		t.Fatalf("region label = %q, want unknown", transition.labels["region"])
	}
	providerPoint := findPoint(t, points["idenqa.provider.dispatches"], map[string]string{"provider": "other"})
	if providerPoint.labels["failure_class"] != "other" {
		t.Fatalf("failure class = %q, want other", providerPoint.labels["failure_class"])
	}
}

func TestDomainMetricsHistogramBucketsAreBounded(t *testing.T) {
	t.Parallel()
	_, reader := collectDomainMetrics(t)
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &resource); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	// Bounds are registered at instrument construction even without samples.
	if len(durationBuckets) == 0 || len(durationBuckets) > 32 {
		t.Fatalf("duration bucket count = %d, want 1..32", len(durationBuckets))
	}
	if len(callbackBuckets) == 0 || len(callbackBuckets) > 32 {
		t.Fatalf("callback bucket count = %d, want 1..32", len(callbackBuckets))
	}
	if len(backupBuckets) == 0 || len(backupBuckets) > 32 {
		t.Fatalf("backup bucket count = %d, want 1..32", len(backupBuckets))
	}
	for index := 1; index < len(durationBuckets); index++ {
		if durationBuckets[index] <= durationBuckets[index-1] {
			t.Fatalf("duration buckets are not increasing at %d", index)
		}
	}
}

func TestDisabledTelemetryRemainsLocalAndConfiguredExportSelectsOTLP(t *testing.T) {
	t.Parallel()
	providers, err := NewConfiguredProviders(context.Background(), "idenqa-test", "test", Config{})
	if err != nil {
		t.Fatalf("disabled NewConfiguredProviders() error = %v", err)
	}
	metrics, err := NewDomainMetrics(providers.MeterProvider())
	if err != nil {
		t.Fatalf("NewDomainMetrics() error = %v", err)
	}
	metrics.RecordVerificationSessionStart(observability.SessionStart{Region: "sa-riyadh-1"})
	if err := providers.Shutdown(context.Background()); err != nil {
		t.Fatalf("disabled providers Shutdown() error = %v", err)
	}
	if _, err := NewConfiguredProviders(context.Background(), "idenqa-test", "test", Config{
		Endpoint: "127.0.0.1:4317", Insecure: true,
	}); err == nil {
		t.Fatal("disabled telemetry accepted an exporter endpoint")
	}

	collectorEndpoint, _, metricExports := startHTTPCollector(t)
	exporting, err := NewConfiguredProviders(context.Background(), "idenqa-test", "test", Config{
		Protocol:           ProtocolHTTPProtobuf,
		Endpoint:           collectorEndpoint,
		Insecure:           true,
		Headers:            map[string]string{"authorization": "Bearer test"},
		TraceSamplingRatio: 1,
		MetricInterval:     10 * time.Second,
		ExportTimeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("OTLP NewConfiguredProviders() error = %v", err)
	}
	exported, err := NewDomainMetrics(exporting.MeterProvider())
	if err != nil {
		t.Fatalf("NewDomainMetrics() error = %v", err)
	}
	exported.RecordVerificationSessionStart(observability.SessionStart{Region: "sa-riyadh-1"})
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exporting.Shutdown(shutdownContext); err != nil {
		t.Fatalf("OTLP providers Shutdown() error = %v", err)
	}
	if metricExports.Load() == 0 {
		t.Fatal("OTLP configuration exported no domain metrics")
	}
}
