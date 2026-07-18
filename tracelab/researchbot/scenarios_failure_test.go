package researchbot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// errShutdownFailed is the sentinel shutdownFailingExporter returns, so tests can
// assert the runner wraps (rather than swallows) the underlying cause.
var errShutdownFailed = errors.New("exporter shutdown failed")

// shutdownFailingExporter exports spans successfully but fails to shut down, so
// the emit phase of a scenario succeeds and only the flush-on-exit step fails.
// No call-count or argument verification is needed, so a value stub is enough.
type shutdownFailingExporter struct{}

func (shutdownFailingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}
func (shutdownFailingExporter) Shutdown(context.Context) error { return errShutdownFailed }

// TestScenarioRunnersPropagateFailures covers every non-happy exit of the two
// hand-rolled scenario runners (RunLateFlush and runSentinel). Both exist to prove
// a completeness contract, so a runner that fails *quietly* is the worst possible
// outcome: the harness would read an empty stdout plus exit 0 and score a truncated
// forest as a clean run. Each row therefore asserts a descriptive wrapped error AND
// that no final answer was written.
//
// Serial by construction: the provider-construction rows use t.Setenv, which
// mutates process-wide state and panics under t.Parallel().
func TestScenarioRunnersPropagateFailures(t *testing.T) {
	// lateFlush and sentinel adapt the two runners to one signature. The delay is
	// sub-millisecond because these rows never reach the barrier sleep.
	lateFlush := func(ctx context.Context, exp sdktrace.SpanExporter, stdout io.Writer) error {
		return RunLateFlush(ctx, exp, time.Millisecond, stdout, io.Discard)
	}
	sentinel := func(ctx context.Context, exp sdktrace.SpanExporter, stdout io.Writer) error {
		return runSentinel(ctx, exp, stdout, sentinelGoodDeclared)
	}

	// canceledCtx makes TracerProvider.ForceFlush return ctx.Err() before it
	// reaches any span processor, which is how the emit phase fails.
	canceledCtx := func(t *testing.T) context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	// badResourceEnv makes resource.WithFromEnv fail, which is the only way
	// NewTracerProvider can fail.
	badResourceEnv := func(t *testing.T) context.Context {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "not-a-key-value-pair")
		return context.Background()
	}
	background := func(t *testing.T) context.Context { return context.Background() }

	tests := []struct {
		name          string
		run           func(context.Context, sdktrace.SpanExporter, io.Writer) error
		ctx           func(t *testing.T) context.Context
		exp           sdktrace.SpanExporter
		wantErrSubstr string
		wantCause     error
	}{
		{
			name:          "late-flush: tracer provider cannot be built",
			run:           lateFlush,
			ctx:           badResourceEnv,
			exp:           tracetest.NewInMemoryExporter(),
			wantErrSubstr: "run late-flush: create tracer provider:",
		},
		{
			name:          "late-flush: decoy batch force-flush fails",
			run:           lateFlush,
			ctx:           canceledCtx,
			exp:           tracetest.NewInMemoryExporter(),
			wantErrSubstr: "run late-flush: emit scenario: emit late-flush: force-flush decoy batch:",
			wantCause:     context.Canceled,
		},
		{
			name:          "late-flush: flush-on-exit shutdown fails",
			run:           lateFlush,
			ctx:           background,
			exp:           shutdownFailingExporter{},
			wantErrSubstr: "run late-flush: shutdown tracer provider:",
			wantCause:     errShutdownFailed,
		},
		{
			name:          "sentinel: tracer provider cannot be built",
			run:           sentinel,
			ctx:           badResourceEnv,
			exp:           tracetest.NewInMemoryExporter(),
			wantErrSubstr: "run sentinel (declared [4]): create tracer provider:",
		},
		{
			name:          "sentinel: forest force-flush fails",
			run:           sentinel,
			ctx:           canceledCtx,
			exp:           tracetest.NewInMemoryExporter(),
			wantErrSubstr: "run sentinel (declared [4]): emit scenario: emit sentinel: force-flush forest:",
			wantCause:     context.Canceled,
		},
		{
			name:          "sentinel: flush-on-exit shutdown fails",
			run:           sentinel,
			ctx:           background,
			exp:           shutdownFailingExporter{},
			wantErrSubstr: "run sentinel (declared [4]): shutdown tracer provider:",
			wantCause:     errShutdownFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer

			err := tt.run(tt.ctx(t), tt.exp, &stdout)

			if err == nil {
				t.Fatalf("runner error = nil, want error containing %q", tt.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Errorf("runner error = %q, want it to contain %q", err, tt.wantErrSubstr)
			}
			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
				t.Errorf("runner error = %v, want it to wrap %v", err, tt.wantCause)
			}
			// A failed run must never emit the final answer: stdout is the
			// boundary Output the result comparator reads, so printing it here
			// would let a broken run be scored as a passing one.
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty on the failure path", stdout.String())
			}
		})
	}
}
