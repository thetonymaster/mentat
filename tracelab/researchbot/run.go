package researchbot

import (
	"context"
	"fmt"
	"io"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Run emits the plan via exp, shuts down the tracer provider, and writes the
// final answer to stdout. Because Run creates the TracerProvider internally it
// also owns its lifecycle: Shutdown flushes any pending spans and releases the
// exporter before the answer is printed. Callers (e.g. main) have no handle to
// the provider and must not call Shutdown themselves.
func Run(ctx context.Context, p *Plan, exp sdktrace.SpanExporter, stdout, stderr io.Writer) error {
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		return err
	}
	if err := Emit(ctx, tp.Tracer("researchbot"), p); err != nil {
		_ = tp.Shutdown(ctx) // best-effort cleanup; the Emit error is the primary failure
		return err
	}
	if err := tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown tracer provider: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, p.Output); err != nil {
		return fmt.Errorf("write answer to stdout: %w", err)
	}
	return nil
}
