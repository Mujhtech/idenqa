package telemetry

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

const domainMetricScope = "github.com/Mujhtech/idenqa/internal/platform/telemetry/domain"

var (
	// durationBuckets bound every domain duration histogram in seconds.
	durationBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900, 3600, 21600, 86400, 604800, 2592000}
	// callbackBuckets bound provider callback delay histograms in seconds.
	callbackBuckets = []float64{0.1, 0.5, 1, 2.5, 5, 15, 30, 60, 120, 300, 900, 3600}
	// backupBuckets bound backup-expiry age histograms in seconds.
	backupBuckets = []float64{3600, 21600, 86400, 172800, 604800, 1209600, 1814400, 2592000, 5184000}

	// modelHealthStates is the fixed gauge series set kept fresh per model.
	modelHealthStates = []observability.HealthState{
		observability.HealthReady, observability.HealthDegraded, observability.HealthNotReady,
	}
)

// DomainMetrics implements every owning-boundary metric receiver using bounded
// instruments and an explicit low-cardinality label allow-list. Unknown label
// values collapse to a constant instead of reaching the exporter.
type DomainMetrics struct {
	transitions         otelmetric.Int64Counter
	completions         otelmetric.Int64Counter
	workflowDuration    otelmetric.Float64Histogram
	timeToDecision      otelmetric.Float64Histogram
	failures            otelmetric.Int64Counter
	sessionStarts       otelmetric.Int64Counter
	recaptures          otelmetric.Int64Counter
	captureSteps        otelmetric.Int64Counter
	deliveryAttempts    otelmetric.Int64Counter
	deliveryDuration    otelmetric.Float64Histogram
	providerDispatches  otelmetric.Int64Counter
	callbackDelay       otelmetric.Float64Histogram
	modelDispatches     otelmetric.Int64Counter
	modelDuration       otelmetric.Float64Histogram
	modelHealth         otelmetric.Int64Gauge
	deletionTransitions otelmetric.Int64Counter
	deletionBacklog     otelmetric.Int64Gauge
	backupExpiryAge     otelmetric.Float64Histogram
	requestTransitions  otelmetric.Int64Counter
	requestAge          otelmetric.Float64Gauge
	reviewResolutions   otelmetric.Int64Counter
	reviewDuration      otelmetric.Float64Histogram
}

// NewDomainMetrics creates the bounded domain metric instruments.
func NewDomainMetrics(provider otelmetric.MeterProvider) (*DomainMetrics, error) {
	if provider == nil {
		return nil, errors.New("telemetry: domain metric meter provider is required")
	}
	meter := provider.Meter(domainMetricScope)
	metrics := &DomainMetrics{}
	var err error
	if metrics.transitions, err = meter.Int64Counter("idenqa.verification.transitions"); err != nil {
		return nil, err
	}
	if metrics.completions, err = meter.Int64Counter("idenqa.verification.completions"); err != nil {
		return nil, err
	}
	if metrics.workflowDuration, err = meter.Float64Histogram("idenqa.verification.workflow.duration", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		return nil, err
	}
	if metrics.timeToDecision, err = meter.Float64Histogram("idenqa.verification.time_to_decision", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		return nil, err
	}
	if metrics.failures, err = meter.Int64Counter("idenqa.verification.operational_failures"); err != nil {
		return nil, err
	}
	if metrics.sessionStarts, err = meter.Int64Counter("idenqa.verification.sessions.started"); err != nil {
		return nil, err
	}
	if metrics.recaptures, err = meter.Int64Counter("idenqa.verification.recaptures"); err != nil {
		return nil, err
	}
	if metrics.captureSteps, err = meter.Int64Counter("idenqa.capture.steps"); err != nil {
		return nil, err
	}
	if metrics.deliveryAttempts, err = meter.Int64Counter("idenqa.delivery.attempts"); err != nil {
		return nil, err
	}
	if metrics.deliveryDuration, err = meter.Float64Histogram("idenqa.delivery.attempt.duration", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		return nil, err
	}
	if metrics.providerDispatches, err = meter.Int64Counter("idenqa.provider.dispatches"); err != nil {
		return nil, err
	}
	if metrics.callbackDelay, err = meter.Float64Histogram("idenqa.provider.callback.delay", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(callbackBuckets...)); err != nil {
		return nil, err
	}
	if metrics.modelDispatches, err = meter.Int64Counter("idenqa.model.dispatches"); err != nil {
		return nil, err
	}
	if metrics.modelDuration, err = meter.Float64Histogram("idenqa.model.dispatch.duration", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		return nil, err
	}
	if metrics.modelHealth, err = meter.Int64Gauge("idenqa.model.health"); err != nil {
		return nil, err
	}
	if metrics.deletionTransitions, err = meter.Int64Counter("idenqa.privacy.deletion.transitions"); err != nil {
		return nil, err
	}
	if metrics.deletionBacklog, err = meter.Int64Gauge("idenqa.privacy.deletion.backlog"); err != nil {
		return nil, err
	}
	if metrics.backupExpiryAge, err = meter.Float64Histogram("idenqa.privacy.backup.expiry.age", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(backupBuckets...)); err != nil {
		return nil, err
	}
	if metrics.requestTransitions, err = meter.Int64Counter("idenqa.privacy.request.transitions"); err != nil {
		return nil, err
	}
	if metrics.requestAge, err = meter.Float64Gauge("idenqa.privacy.request.age", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.reviewResolutions, err = meter.Int64Counter("idenqa.review.resolutions"); err != nil {
		return nil, err
	}
	if metrics.reviewDuration, err = meter.Float64Histogram("idenqa.review.resolution.duration", otelmetric.WithUnit("s"), otelmetric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		return nil, err
	}
	return metrics, nil
}

// RecordVerificationTransition records one applied lifecycle transition.
func (metrics *DomainMetrics) RecordVerificationTransition(transition observability.Transition) {
	if metrics == nil {
		return
	}
	metrics.transitions.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("from", transition.From.Safe()),
		attribute.String("to", transition.To.Safe()),
		attribute.String("failure_class", transition.FailureClass.Safe()),
		attribute.String("region", transition.Region.Safe()),
	))
}

// RecordVerificationCompletion records one terminal workflow observation.
func (metrics *DomainMetrics) RecordVerificationCompletion(completion observability.WorkflowCompletion) {
	if metrics == nil {
		return
	}
	attributes := metricAttributes(
		attribute.String("outcome", completion.Outcome.Safe()),
		attribute.String("region", completion.Region.Safe()),
	)
	metrics.completions.Add(context.Background(), 1, otelmetric.WithAttributes(attributes...))
	metrics.workflowDuration.Record(context.Background(), completion.Duration.Seconds(), otelmetric.WithAttributes(attributes...))
	if completion.TimeToDecision > 0 {
		metrics.timeToDecision.Record(context.Background(), completion.TimeToDecision.Seconds(), otelmetric.WithAttributes(attributes...))
	}
}

// RecordVerificationOperationalFailure records one non-decision failure.
func (metrics *DomainMetrics) RecordVerificationOperationalFailure(failure observability.OperationalFailure) {
	if metrics == nil {
		return
	}
	metrics.failures.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("failure_class", failure.FailureClass.Safe()),
		attribute.String("region", failure.Region.Safe()),
	))
}

// RecordVerificationSessionStart records one accepted session creation.
func (metrics *DomainMetrics) RecordVerificationSessionStart(start observability.SessionStart) {
	if metrics == nil {
		return
	}
	metrics.sessionStarts.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("region", start.Region.Safe()),
	))
}

// RecordVerificationRecapture records one accepted recapture creation.
func (metrics *DomainMetrics) RecordVerificationRecapture(recapture observability.Recapture) {
	if metrics == nil {
		return
	}
	metrics.recaptures.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("reason", recapture.Reason.Safe()),
		attribute.String("region", recapture.Region.Safe()),
	))
}

// RecordCaptureStep records one accepted capture artefact completion.
func (metrics *DomainMetrics) RecordCaptureStep(event observability.CaptureEvent) {
	if metrics == nil {
		return
	}
	metrics.captureSteps.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("step", event.Step.Safe()),
		attribute.String("outcome", event.Outcome.Safe()),
	))
}

// RecordDeliveryAttempt records one committed webhook attempt and its latency.
func (metrics *DomainMetrics) RecordDeliveryAttempt(attempt observability.DeliveryAttempt) {
	if metrics == nil {
		return
	}
	attributes := metricAttributes(
		attribute.String("outcome", attempt.Outcome.Safe()),
		attribute.String("attempt", attemptBucket(attempt.Attempt)),
	)
	metrics.deliveryAttempts.Add(context.Background(), 1, otelmetric.WithAttributes(attributes...))
	if attempt.Duration > 0 {
		metrics.deliveryDuration.Record(context.Background(), attempt.Duration.Seconds(), otelmetric.WithAttributes(attributes...))
	}
}

// RecordProviderDispatch records one provider dispatch outcome.
func (metrics *DomainMetrics) RecordProviderDispatch(dispatch observability.ProviderDispatch) {
	if metrics == nil {
		return
	}
	metrics.providerDispatches.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("provider", dispatch.Provider.Safe()),
		attribute.String("outcome", dispatch.Outcome.Safe()),
		attribute.String("failure_class", dispatch.FailureClass.Safe()),
	))
}

// RecordProviderCallbackDelay records one adopted provider callback delay.
func (metrics *DomainMetrics) RecordProviderCallbackDelay(delay observability.ProviderCallbackDelay) {
	if metrics == nil || delay.Delay <= 0 {
		return
	}
	metrics.callbackDelay.Record(context.Background(), delay.Delay.Seconds(), otelmetric.WithAttributes(
		attribute.String("provider", delay.Provider.Safe()),
	))
}

// RecordModelDispatch records one durable model execution result.
func (metrics *DomainMetrics) RecordModelDispatch(dispatch observability.ModelDispatch) {
	if metrics == nil {
		return
	}
	attributes := metricAttributes(
		attribute.String("model", dispatch.Model.Safe()),
		attribute.String("outcome", dispatch.Outcome.Safe()),
		attribute.String("failure_class", dispatch.FailureClass.Safe()),
	)
	metrics.modelDispatches.Add(context.Background(), 1, otelmetric.WithAttributes(attributes...))
	if dispatch.Duration > 0 {
		metrics.modelDuration.Record(context.Background(), dispatch.Duration.Seconds(), otelmetric.WithAttributes(attributes...))
	}
}

// RecordModelHealth records one supervised readiness probe result.
func (metrics *DomainMetrics) RecordModelHealth(health observability.ModelHealth) {
	if metrics == nil {
		return
	}
	observed := health.State.Safe()
	for _, state := range modelHealthStates {
		value := int64(0)
		if string(state) == observed {
			value = 1
		}
		metrics.modelHealth.Record(context.Background(), value, otelmetric.WithAttributes(
			attribute.String("model", health.Model.Safe()),
			attribute.String("state", string(state)),
		))
	}
}

// RecordDeletionTransition records one privacy deletion state change.
func (metrics *DomainMetrics) RecordDeletionTransition(transition observability.DeletionTransition) {
	if metrics == nil {
		return
	}
	metrics.deletionTransitions.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("from", transition.From.Safe()),
		attribute.String("to", transition.To.Safe()),
		attribute.String("kind", transition.Kind.Safe()),
		attribute.String("region", transition.Region.Safe()),
	))
}

// RecordDeletionBacklog records one bounded due-work count sample.
func (metrics *DomainMetrics) RecordDeletionBacklog(backlog observability.DeletionBacklog) {
	if metrics == nil || backlog.Count < 0 {
		return
	}
	metrics.deletionBacklog.Record(context.Background(), backlog.Count, otelmetric.WithAttributes(
		attribute.String("state", backlog.State.Safe()),
	))
}

// RecordBackupExpiry records one backup-expiry age observation.
func (metrics *DomainMetrics) RecordBackupExpiry(expiry observability.BackupExpiry) {
	if metrics == nil || expiry.Age < 0 {
		return
	}
	metrics.backupExpiryAge.Record(context.Background(), expiry.Age.Seconds(), otelmetric.WithAttributes(
		attribute.String("region", expiry.Region.Safe()),
	))
}

// RecordPrivacyRequestTransition records one privacy-request state change.
func (metrics *DomainMetrics) RecordPrivacyRequestTransition(transition observability.PrivacyRequestTransition) {
	if metrics == nil {
		return
	}
	metrics.requestTransitions.Add(context.Background(), 1, otelmetric.WithAttributes(
		attribute.String("type", transition.Type.Safe()),
		attribute.String("from", transition.From.Safe()),
		attribute.String("to", transition.To.Safe()),
		attribute.String("region", transition.Region.Safe()),
	))
}

// RecordPrivacyRequestAge records the current age of one open request.
func (metrics *DomainMetrics) RecordPrivacyRequestAge(age observability.PrivacyRequestAge) {
	if metrics == nil || age.Age < 0 {
		return
	}
	overdue := "false"
	if age.Overdue {
		overdue = "true"
	}
	metrics.requestAge.Record(context.Background(), age.Age.Seconds(), otelmetric.WithAttributes(
		attribute.String("type", age.Type.Safe()),
		attribute.String("state", age.State.Safe()),
		attribute.String("overdue", overdue),
	))
}

// RecordReviewResolution records one reviewer case resolution.
func (metrics *DomainMetrics) RecordReviewResolution(resolution observability.ReviewResolution) {
	if metrics == nil {
		return
	}
	attributes := metricAttributes(
		attribute.String("outcome", resolution.Outcome.Safe()),
		attribute.String("oversight", resolution.Oversight.Safe()),
		attribute.String("region", resolution.Region.Safe()),
	)
	metrics.reviewResolutions.Add(context.Background(), 1, otelmetric.WithAttributes(attributes...))
	if resolution.Duration > 0 {
		metrics.reviewDuration.Record(context.Background(), resolution.Duration.Seconds(), otelmetric.WithAttributes(attributes...))
	}
}

func metricAttributes(attributes ...attribute.KeyValue) []attribute.KeyValue {
	return attributes
}

func attemptBucket(attempt int32) string {
	switch {
	case attempt <= 0:
		return "unknown"
	case attempt == 1:
		return "1"
	case attempt == 2:
		return "2"
	case attempt <= 5:
		return "3-5"
	case attempt <= 10:
		return "6-10"
	default:
		return "10+"
	}
}
