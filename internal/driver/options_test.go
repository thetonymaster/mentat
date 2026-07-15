package driver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestNewShellLoggerSeam pins the T002/T003 logger seam for the shell driver:
// NewShell accepts a WithLogger option, the zero-arg form still compiles, and
// the seam is SILENT by default — no narration is wired in this phase (that is
// T007), so a buffer-backed logger passed to a successful Run must emit ZERO
// bytes. This is the load-bearing SC-005 guarantee (happy-path output stays
// byte-identical).
func TestNewShellLoggerSeam(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		withLog bool
	}{
		{name: "default_no_option_builds"},
		{name: "with_logger_builds_and_is_silent", withLog: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			var d core.Driver
			if tt.withLog {
				d = NewShell(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			} else {
				d = NewShell()
			}
			spec := core.RunSpec{
				Command: []string{"sh", "-c", "printf 'hi\\n'"},
				RunID:   "run-log",
			}
			if _, err := d.Run(context.Background(), spec); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("silent seam violated: logger emitted %d bytes: %q", buf.Len(), buf.String())
			}
		})
	}
}

// TestNewHTTPLoggerSeam is the http-driver complement: NewHTTP accepts
// WithLogger, the zero-arg form still compiles, and a successful Run emits zero
// bytes through a buffer-backed logger (silent by default).
func TestNewHTTPLoggerSeam(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	tests := []struct {
		name    string
		withLog bool
	}{
		{name: "default_no_option_builds"},
		{name: "with_logger_builds_and_is_silent", withLog: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			var d core.Driver
			if tt.withLog {
				d = NewHTTP(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			} else {
				d = NewHTTP()
			}
			spec := core.RunSpec{
				HTTP:  core.HTTPSpec{URL: srv.URL, Method: http.MethodGet},
				RunID: "run-log",
			}
			if _, err := d.Run(context.Background(), spec); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("silent seam violated: logger emitted %d bytes: %q", buf.Len(), buf.String())
			}
		})
	}
}
