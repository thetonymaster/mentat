package orderflow

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// baggageSpanProcessor copies selected baggage members onto every span as it
// starts. The SDK does not auto-stamp baggage onto spans, so this processor is
// what makes test.run.id queryable per span — the Phase 2 correlation contract.
type baggageSpanProcessor struct{ keys []string }

// NewBaggageSpanProcessor returns a SpanProcessor copying the named baggage
// members onto each span's attributes at start.
func NewBaggageSpanProcessor(keys ...string) sdktrace.SpanProcessor {
	return baggageSpanProcessor{keys: keys}
}

func (p baggageSpanProcessor) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	b := baggage.FromContext(ctx)
	for _, k := range p.keys {
		if v := b.Member(k).Value(); v != "" {
			s.SetAttributes(attribute.String(k, v))
		}
	}
}

func (baggageSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (baggageSpanProcessor) Shutdown(context.Context) error   { return nil }
func (baggageSpanProcessor) ForceFlush(context.Context) error { return nil }
