package orderflow

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Propagator is the composite W3C tracecontext + baggage propagator. Set it once
// as the global propagator so otelhttp client/server inject and extract both.
func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// NewTracerProvider builds a provider whose resource carries service.name=service
// (plus anything in OTEL_RESOURCE_ATTRIBUTES) and that stamps correlation baggage
// onto every span. exp is OTLP in deployment, in-memory in capture and tests.
func NewTracerProvider(ctx context.Context, service string, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceName(service)),
	)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(NewBaggageSpanProcessor(BaggageRunID, BaggageScenario)),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	), nil
}
