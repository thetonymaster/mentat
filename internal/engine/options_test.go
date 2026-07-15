package engine

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
)

// TestBuildLoggerSeam pins the T002/T003 logger seam at the composition root:
// Build accepts a WithLogger option, the existing Build(cfg, st, cor) form still
// compiles, and Build itself narrates nothing in this phase (that is T007) — a
// buffer-backed logger passed via WithLogger must emit ZERO bytes after Build.
// This is the load-bearing SC-005 guarantee: wiring the seam does not change
// happy-path output.
//
// The seam is verified structurally (Build compiles and succeeds with the
// option) rather than by asserting the drivers received the logger — no
// exported getter is added just to test, and narration that would make the
// hand-off observable arrives in T007.
//
// No t.Parallel(): Build mutates the registry's package-global maps; running the
// rows concurrently would data-race those writes.
func TestBuildLoggerSeam(t *testing.T) {
	tests := []struct {
		name    string
		withLog bool
	}{
		{name: "default_no_option_builds_silent"},
		{name: "with_logger_builds_silent", withLog: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{OTLPEndpoint: "x"}
			var buf bytes.Buffer
			var eng *Engine
			var err error
			if tt.withLog {
				eng, err = Build(cfg, nil, nil, WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			} else {
				eng, err = Build(cfg, nil, nil)
			}
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if eng == nil {
				t.Fatal("Build returned nil engine")
			}
			if buf.Len() != 0 {
				t.Fatalf("silent seam violated: Build emitted %d bytes: %q", buf.Len(), buf.String())
			}
		})
	}
}

// TestWithLoggerNilIsSilentDefault proves WithLogger(nil) is treated as the
// silent default rather than installing a nil logger that would panic on first
// use — callers may pass an unconditionally-resolved logger without a nil check.
func TestWithLoggerNilIsSilentDefault(t *testing.T) {
	// The contract lives in resolveOptions: a nil WithLogger must leave the
	// discard-handler default in place, never a nil *slog.Logger. Assert the
	// resolved logger is non-nil and actually usable — invoking it exercises the
	// exact "panic on first use" failure mode the silent default exists to prevent,
	// a path Build itself never reaches (it holds the logger but does not log here).
	got := resolveOptions([]Option{WithLogger(nil)})
	if got.logger == nil {
		t.Fatal("resolveOptions(WithLogger(nil)) left a nil logger; want the discard default")
	}
	got.logger.Info("must not panic on first use", "k", "v") // discard handler: no output, no panic

	// And the full composition root still succeeds end-to-end with the nil option.
	eng, err := Build(config.Config{OTLPEndpoint: "x"}, nil, nil, WithLogger(nil))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if eng == nil {
		t.Fatal("Build returned nil engine")
	}
}
