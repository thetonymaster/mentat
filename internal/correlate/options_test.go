package correlate

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestNewLoggerAndEndpointSeam pins the T002/T003 logger seam for the
// correlator: New accepts WithLogger and WithEndpoint, the existing
// New(idFn, poll) form still compiles, and the seam is SILENT — no narration is
// wired in this phase (that is T007), so a buffer-backed logger passed to New
// must emit ZERO bytes even after exercising Inject. This is the load-bearing
// SC-005 guarantee for the correlate seam.
func TestNewLoggerAndEndpointSeam(t *testing.T) {
	t.Parallel()
	pc := PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second}

	tests := []struct {
		name string
		opts func(buf *bytes.Buffer) []Option
	}{
		{
			name: "default_no_options",
			opts: func(*bytes.Buffer) []Option { return nil },
		},
		{
			name: "with_logger_only",
			opts: func(buf *bytes.Buffer) []Option {
				return []Option{WithLogger(slog.New(slog.NewTextHandler(buf, nil)))}
			},
		},
		{
			name: "with_logger_and_endpoint",
			opts: func(buf *bytes.Buffer) []Option {
				return []Option{
					WithLogger(slog.New(slog.NewTextHandler(buf, nil))),
					WithEndpoint("tempo:4317"),
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			c := New(func() string { return "fixed-id" }, pc, tt.opts(&buf)...)
			// Exercise a real code path; the seam must stay silent.
			spec := &core.RunSpec{}
			if id := c.Inject(context.Background(), spec); id != "fixed-id" {
				t.Fatalf("Inject id = %q, want fixed-id", id)
			}
			if buf.Len() != 0 {
				t.Fatalf("silent seam violated: logger emitted %d bytes: %q", buf.Len(), buf.String())
			}
		})
	}
}
