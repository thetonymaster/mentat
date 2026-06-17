package researchbot

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewResourceReadsRunIDFromEnv(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "test.run.id=abc123,test.scenario=happy")
	res, err := NewResource(context.Background())
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	var got string
	for _, kv := range res.Attributes() {
		if string(kv.Key) == "test.run.id" {
			got = kv.Value.AsString()
		}
	}
	if got != "abc123" {
		t.Fatalf("test.run.id = %q, want abc123", got)
	}
}

func TestNewTracerProvider(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp, err := NewTracerProvider(context.Background(), exp)
	if err != nil {
		t.Fatalf("NewTracerProvider: %v", err)
	}
	if tp == nil {
		t.Fatal("expected non-nil TracerProvider, got nil")
	}
	tracer := tp.Tracer("researchbot-test")
	if tracer == nil {
		t.Fatal("Tracer() returned nil")
	}
	// Verify spans are emitted to the in-memory exporter.
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()
	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span in exporter, got none")
	}
}
