package researchbot

import (
	"context"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// NewResource builds the OTel resource, honoring OTEL_RESOURCE_ATTRIBUTES so the
// driver-injected test.run.id lands on every span (the correlation contract).
func NewResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(), // reads OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME
		resource.WithAttributes(semconv.ServiceName("researchbot")),
	)
}

// NewTracerProvider wires the given exporter (OTLP in prod, in-memory in tests)
// with a SimpleSpanProcessor for deterministic flushing.
func NewTracerProvider(ctx context.Context, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error) {
	res, err := NewResource(ctx)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	), nil
}
