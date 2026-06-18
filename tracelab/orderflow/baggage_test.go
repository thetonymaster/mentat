package orderflow

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestBaggageSpanProcessorCopiesMembersOntoSpan(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(NewBaggageSpanProcessor(BaggageRunID, BaggageScenario)),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	bag, err := baggage.Parse(BaggageRunID + "=run-123," + BaggageScenario + "=happy")
	if err != nil {
		t.Fatalf("parse baggage: %v", err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), bag)

	_, span := tp.Tracer("test").Start(ctx, "op")
	span.End()

	stubs := exp.GetSpans()
	if len(stubs) != 1 {
		t.Fatalf("got %d spans, want 1", len(stubs))
	}
	attrs := map[string]string{}
	for _, kv := range stubs[0].Attributes {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if attrs[BaggageRunID] != "run-123" {
		t.Errorf("%s = %q, want run-123", BaggageRunID, attrs[BaggageRunID])
	}
	if attrs[BaggageScenario] != "happy" {
		t.Errorf("%s = %q, want happy", BaggageScenario, attrs[BaggageScenario])
	}
}

// TestBaggageSpanProcessorNoBaggage verifies that the processor is a no-op when
// there is no baggage in context (no attribute, no panic).
func TestBaggageSpanProcessorNoBaggage(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(NewBaggageSpanProcessor(BaggageRunID, BaggageScenario)),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, span := tp.Tracer("test").Start(context.Background(), "op")
	span.End()

	stubs := exp.GetSpans()
	if len(stubs) != 1 {
		t.Fatalf("got %d spans, want 1", len(stubs))
	}
	for _, kv := range stubs[0].Attributes {
		key := string(kv.Key)
		if key == BaggageRunID || key == BaggageScenario {
			t.Errorf("unexpected attribute %s=%q on span with no baggage", key, kv.Value.AsString())
		}
	}
}

// TestBaggageSpanProcessorLifecycleMethods calls OnEnd/ForceFlush/Shutdown to
// verify they are no-ops and return no errors.
func TestBaggageSpanProcessorLifecycleMethods(t *testing.T) {
	p := NewBaggageSpanProcessor(BaggageRunID)
	ctx := context.Background()

	// OnEnd is a documented no-op; call it via a started span.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(p),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })

	_, span := tp.Tracer("test").Start(ctx, "lifecycle")
	span.End() // triggers OnEnd on all processors

	if err := p.ForceFlush(ctx); err != nil {
		t.Errorf("ForceFlush: %v", err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestPropagatorReturnsComposite checks that Propagator() returns a non-nil
// composite that implements TextMapPropagator (W3C TraceContext + Baggage).
func TestPropagatorReturnsComposite(t *testing.T) {
	p := Propagator()
	if p == nil {
		t.Fatal("Propagator() returned nil")
	}
	// A composite propagator must report at least two field names.
	fields := p.Fields()
	if len(fields) < 2 {
		t.Errorf("Propagator().Fields() = %v, want at least 2", fields)
	}
	// Check known W3C fields are present.
	fieldSet := map[string]bool{}
	for _, f := range fields {
		fieldSet[f] = true
	}
	if !fieldSet["traceparent"] {
		t.Error("expected propagator to include 'traceparent' field")
	}
}

// TestPropagatorInjectExtractRoundtrip exercises the propagator end-to-end: inject
// a span context and baggage into a carrier, then extract and verify both survive.
func TestPropagatorInjectExtractRoundtrip(t *testing.T) {
	prop := Propagator()

	// Set up a trace context to inject.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	bag, err := baggage.Parse(BaggageRunID + "=rt-456")
	if err != nil {
		t.Fatalf("parse baggage: %v", err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), bag)
	ctx, span := tp.Tracer("test").Start(ctx, "inject")
	defer span.End()

	carrier := propagation.MapCarrier{}
	prop.Inject(ctx, carrier)

	if carrier["traceparent"] == "" {
		t.Error("inject: traceparent header missing")
	}
	if carrier["baggage"] == "" {
		t.Error("inject: baggage header missing")
	}

	// Extract back and verify baggage survives.
	ctx2 := prop.Extract(context.Background(), carrier)
	got := baggage.FromContext(ctx2).Member(BaggageRunID).Value()
	if got != "rt-456" {
		t.Errorf("extracted %s = %q, want rt-456", BaggageRunID, got)
	}
}

// TestNewTracerProviderHappyPath verifies that NewTracerProvider returns a
// working provider that emits spans with the baggage processor installed.
func TestNewTracerProviderHappyPath(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	ctx := context.Background()

	tp, err := NewTracerProvider(ctx, ServiceGateway, exp)
	if err != nil {
		t.Fatalf("NewTracerProvider: %v", err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })

	bag, err := baggage.Parse(BaggageRunID + "=prov-789")
	if err != nil {
		t.Fatalf("parse baggage: %v", err)
	}
	spanCtx := baggage.ContextWithBaggage(ctx, bag)
	_, span := tp.Tracer(ServiceGateway).Start(spanCtx, "checkout")
	span.End()

	stubs := exp.GetSpans()
	if len(stubs) != 1 {
		t.Fatalf("got %d spans, want 1", len(stubs))
	}
	attrs := map[string]string{}
	for _, kv := range stubs[0].Attributes {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if attrs[BaggageRunID] != "prov-789" {
		t.Errorf("%s = %q, want prov-789", BaggageRunID, attrs[BaggageRunID])
	}
}
