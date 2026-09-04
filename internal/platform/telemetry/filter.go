package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type filteringTracerProvider struct {
	oteltrace.TracerProvider
	allowed map[attribute.Key]struct{}
}

// NewAttributeFilteringTracerProvider wraps provider so only explicitly
// allowed span and event attributes reach it.
func NewAttributeFilteringTracerProvider(
	provider oteltrace.TracerProvider,
	allowed ...attribute.Key,
) oteltrace.TracerProvider {
	allowList := make(map[attribute.Key]struct{}, len(allowed))
	for _, key := range allowed {
		allowList[key] = struct{}{}
	}

	return &filteringTracerProvider{TracerProvider: provider, allowed: allowList}
}

func (provider *filteringTracerProvider) Tracer(
	name string,
	options ...oteltrace.TracerOption,
) oteltrace.Tracer {
	return &filteringTracer{
		Tracer:   provider.TracerProvider.Tracer(name, options...),
		provider: provider,
		allowed:  provider.allowed,
	}
}

type filteringTracer struct {
	oteltrace.Tracer
	provider oteltrace.TracerProvider
	allowed  map[attribute.Key]struct{}
}

func (tracer *filteringTracer) Start(
	ctx context.Context,
	name string,
	options ...oteltrace.SpanStartOption,
) (context.Context, oteltrace.Span) {
	configuration := oteltrace.NewSpanStartConfig(options...)
	safeOptions := spanStartOptions(configuration, tracer.allowed)
	spanContext, span := tracer.Tracer.Start(ctx, name, safeOptions...)
	wrapped := &filteringSpan{
		Span:     span,
		provider: tracer.provider,
		allowed:  tracer.allowed,
	}

	return oteltrace.ContextWithSpan(spanContext, wrapped), wrapped
}

type filteringSpan struct {
	oteltrace.Span
	provider oteltrace.TracerProvider
	allowed  map[attribute.Key]struct{}
}

func (span *filteringSpan) SetAttributes(attributes ...attribute.KeyValue) {
	span.Span.SetAttributes(filterAttributes(attributes, span.allowed)...)
}

func (span *filteringSpan) AddEvent(name string, options ...oteltrace.EventOption) {
	span.Span.AddEvent(name, eventOptions(oteltrace.NewEventConfig(options...), span.allowed)...)
}

func (span *filteringSpan) AddLink(link oteltrace.Link) {
	link.Attributes = filterAttributes(link.Attributes, span.allowed)
	span.Span.AddLink(link)
}

func (span *filteringSpan) RecordError(_ error, options ...oteltrace.EventOption) {
	span.Span.AddEvent("exception", eventOptions(oteltrace.NewEventConfig(options...), span.allowed)...)
}

func (span *filteringSpan) SetStatus(code codes.Code, _ string) {
	span.Span.SetStatus(code, "")
}

func (span *filteringSpan) TracerProvider() oteltrace.TracerProvider {
	return span.provider
}

func spanStartOptions(
	configuration oteltrace.SpanConfig,
	allowed map[attribute.Key]struct{},
) []oteltrace.SpanStartOption {
	options := make([]oteltrace.SpanStartOption, 0, 6)
	if attributes := filterAttributes(configuration.Attributes(), allowed); len(attributes) > 0 {
		options = append(options, oteltrace.WithAttributes(attributes...))
	}
	if timestamp := configuration.Timestamp(); !timestamp.IsZero() {
		options = append(options, oteltrace.WithTimestamp(timestamp))
	}
	if links := filterLinks(configuration.Links(), allowed); len(links) > 0 {
		options = append(options, oteltrace.WithLinks(links...))
	}
	if configuration.NewRoot() {
		options = append(options, oteltrace.WithNewRoot())
	}
	if kind := configuration.SpanKind(); kind != oteltrace.SpanKindUnspecified {
		options = append(options, oteltrace.WithSpanKind(kind))
	}

	return options
}

func eventOptions(
	configuration oteltrace.EventConfig,
	allowed map[attribute.Key]struct{},
) []oteltrace.EventOption {
	options := make([]oteltrace.EventOption, 0, 3)
	if attributes := filterAttributes(configuration.Attributes(), allowed); len(attributes) > 0 {
		options = append(options, oteltrace.WithAttributes(attributes...))
	}
	if timestamp := configuration.Timestamp(); !timestamp.IsZero() {
		options = append(options, oteltrace.WithTimestamp(timestamp))
	}
	if configuration.StackTrace() {
		options = append(options, oteltrace.WithStackTrace(true))
	}

	return options
}

func filterLinks(links []oteltrace.Link, allowed map[attribute.Key]struct{}) []oteltrace.Link {
	filtered := make([]oteltrace.Link, len(links))
	for index, link := range links {
		filtered[index] = link
		filtered[index].Attributes = filterAttributes(link.Attributes, allowed)
	}

	return filtered
}

func filterAttributes(
	attributes []attribute.KeyValue,
	allowed map[attribute.Key]struct{},
) []attribute.KeyValue {
	filtered := make([]attribute.KeyValue, 0, len(attributes))
	for _, keyValue := range attributes {
		if _, ok := allowed[keyValue.Key]; ok && keyValue.Valid() {
			filtered = append(filtered, keyValue)
		}
	}

	return filtered
}
