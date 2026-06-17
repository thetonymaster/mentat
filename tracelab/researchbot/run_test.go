package researchbot

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// failingWriter always fails on Write, simulating a broken stdout.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// recordingExporter wraps InMemoryExporter and counts spans received via
// ExportSpans before delegating to the inner exporter. This lets tests verify
// that spans were exported even after the SDK calls Shutdown (which resets the
// inner store). shutdownCalled is set atomically when Shutdown is invoked so
// tests can assert the provider was cleaned up on all return paths.
type recordingExporter struct {
	inner          *tracetest.InMemoryExporter
	received       atomic.Int64
	shutdownCalled atomic.Bool
}

func newRecordingExporter() *recordingExporter {
	return &recordingExporter{inner: tracetest.NewInMemoryExporter()}
}

func (r *recordingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.received.Add(int64(len(spans)))
	return r.inner.ExportSpans(ctx, spans)
}

func (r *recordingExporter) Shutdown(ctx context.Context) error {
	r.shutdownCalled.Store(true)
	return r.inner.Shutdown(ctx)
}

func TestRunWritesOnlyAnswerToStdout(t *testing.T) {
	p := &Plan{
		Scenario: "happy",
		Output:   "Q3 revenue grew 12%",
		Steps:    []Step{{Tool: &ToolStep{Name: "search"}}},
	}
	var out, errBuf bytes.Buffer
	rec := newRecordingExporter()
	if err := Run(context.Background(), p, rec, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "Q3 revenue grew 12%\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	// Plan emits a root span + one tool span = at least 2 spans exported.
	if got := rec.received.Load(); got < 2 {
		t.Fatalf("expected ≥2 spans (root+tool) to be exported before Shutdown, got %d", got)
	}
	// Run owns the provider lifecycle: Shutdown must run on the success path too.
	if !rec.shutdownCalled.Load() {
		t.Fatal("provider Shutdown was not called on the success path — resource leak")
	}
}

func TestRunPropagatesStdoutWriteError(t *testing.T) {
	p := &Plan{
		Scenario: "happy",
		Output:   "Q3 revenue grew 12%",
		Steps:    []Step{{Tool: &ToolStep{Name: "search"}}},
	}
	var errBuf bytes.Buffer
	rec := newRecordingExporter()
	if err := Run(context.Background(), p, rec, failingWriter{}, &errBuf); err == nil {
		t.Fatal("expected error when stdout write fails, got nil")
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name    string
		plan    *Plan
		wantErr bool
	}{
		{
			name:    "nil plan returns error from Emit",
			plan:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			rec := newRecordingExporter()
			err := Run(context.Background(), tt.plan, rec, &out, &errBuf)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr && !rec.shutdownCalled.Load() {
				t.Fatal("provider Shutdown was not called on the error path — resource leak")
			}
		})
	}
}
