package orderflow

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// failingExporter is a SpanExporter whose Shutdown always fails, so a
// TracerProvider built around it surfaces a shutdown error. No call-count or
// argument verification is needed, so a value stub is enough.
type failingExporter struct{ err error }

func (failingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (e failingExporter) Shutdown(context.Context) error                           { return e.err }

// closedPortURL binds an ephemeral port, releases it, and returns its URL so a
// dial against it is refused deterministically (no network, no fixed port).
func closedPortURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "http://" + addr
}

// truncatingServer answers with a Content-Length far larger than the bytes it
// writes and then aborts the connection, so io.ReadAll on the response body
// fails with an unexpected EOF.
func truncatingServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestDriveReturnsWrappedErrors pins Constitution IV (no silent fallbacks) on the
// driver seam: every way Drive can fail must surface a wrapped, descriptive error
// and a zero status — never a zero-value success.
func TestDriveReturnsWrappedErrors(t *testing.T) {
	t.Parallel()

	// oversize is long enough that two members blow the 8192-byte baggage-string
	// limit while each member on its own stays valid.
	oversize := strings.Repeat("a", 4090)

	tests := []struct {
		name          string
		setup         func(t *testing.T) (Topology, string, string) // topo, runID, scenario
		wantErrSubstr string
	}{
		{
			name: "topology missing gateway",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceAuth: "http://127.0.0.1:1"}, "run-1", "happy"
			},
			wantErrSubstr: "topology missing gateway",
		},
		{
			name: "gateway url unparseable",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceGateway: "://not-a-url"}, "run-1", "happy"
			},
			wantErrSubstr: "build gateway request",
		},
		{
			name: "run id not a legal baggage value",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceGateway: "http://127.0.0.1:1"}, "run id with spaces", "happy"
			},
			wantErrSubstr: `baggage member test.run.id="run id with spaces"`,
		},
		{
			name: "scenario not a legal baggage value",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceGateway: "http://127.0.0.1:1"}, "run-1", "happy scenario"
			},
			wantErrSubstr: `baggage member test.scenario="happy scenario"`,
		},
		{
			name: "baggage string exceeds size limit",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceGateway: "http://127.0.0.1:1"}, oversize, oversize
			},
			wantErrSubstr: "build baggage",
		},
		{
			name: "gateway refuses connection",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceGateway: closedPortURL(t)}, "run-1", "happy"
			},
			wantErrSubstr: "drive gateway",
		},
		{
			name: "gateway response body truncated",
			setup: func(t *testing.T) (Topology, string, string) {
				return Topology{ServiceGateway: truncatingServer(t)}, "run-1", "happy"
			},
			wantErrSubstr: "read gateway response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			topo, runID, scenario := tt.setup(t)

			status, body, err := (&System{}).Drive(context.Background(), topo, runID, scenario)

			if err == nil {
				t.Fatalf("Drive() error = nil, want error containing %q", tt.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Errorf("Drive() error = %q, want it to contain %q", err, tt.wantErrSubstr)
			}
			if !strings.HasPrefix(err.Error(), "orderflow: ") {
				t.Errorf("Drive() error = %q, want an %q-prefixed error", err, "orderflow: ")
			}
			if status != 0 {
				t.Errorf("Drive() status = %d, want 0 on error", status)
			}
			if body != nil {
				t.Errorf("Drive() body = %q, want nil on error", body)
			}
		})
	}
}

// errExporterShutdown is the sentinel a failingExporter returns, so the test can
// assert Shutdown wraps (rather than swallows) the underlying cause.
var errExporterShutdown = errors.New("exporter shutdown boom")

// blockedServer starts an HTTP server holding one request open, so Shutdown
// cannot drain it and must honour its context deadline instead.
func blockedServer(t *testing.T) *http.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})}
	go func() { _ = srv.Serve(ln) }()

	go func() {
		resp, perr := http.Get("http://" + ln.Addr().String())
		if perr == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never received the request")
	}
	t.Cleanup(func() {
		close(release)
		_ = srv.Close()
	})
	return srv
}

// TestSystemShutdownReportsFirstFailure proves Shutdown does not silently swallow
// a server-drain timeout or a tracer-provider flush failure (Constitution IV).
func TestSystemShutdownReportsFirstFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setup         func(t *testing.T) (*System, context.Context)
		wantErrSubstr string
		wantCause     error
	}{
		{
			name: "nothing to shut down succeeds",
			setup: func(t *testing.T) (*System, context.Context) {
				return &System{}, context.Background()
			},
		},
		{
			name: "server drain exceeds context",
			setup: func(t *testing.T) (*System, context.Context) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // already expired: the in-flight request can never drain
				return &System{servers: []*http.Server{blockedServer(t)}}, ctx
			},
			wantErrSubstr: "orderflow: server shutdown",
			wantCause:     context.Canceled,
		},
		{
			name: "tracer provider flush fails",
			setup: func(t *testing.T) (*System, context.Context) {
				tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(
					sdktrace.NewSimpleSpanProcessor(failingExporter{err: errExporterShutdown})))
				return &System{services: []*Service{{Name: ServiceAuth, TP: tp}}}, context.Background()
			},
			wantErrSubstr: `orderflow: provider "auth" shutdown`,
			wantCause:     errExporterShutdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sys, ctx := tt.setup(t)

			err := sys.Shutdown(ctx)

			if tt.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("Shutdown() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Shutdown() error = nil, want error containing %q", tt.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Errorf("Shutdown() error = %q, want it to contain %q", err, tt.wantErrSubstr)
			}
			if !errors.Is(err, tt.wantCause) {
				t.Errorf("Shutdown() error = %v, want it to wrap %v", err, tt.wantCause)
			}
		})
	}
}

// TestTracerProviderFailurePropagatesToEveryEntryPoint drives the one fault that
// can break provider construction — a malformed OTEL_RESOURCE_ATTRIBUTES, which
// makes resource.WithFromEnv fail — through every entry point that builds a
// provider. None may degrade to an un-traced system: telemetry that silently
// stops describing the run would make every downstream comparator lie.
//
// Serial by construction: t.Setenv mutates process-wide state and panics under
// t.Parallel().
func TestTracerProviderFailurePropagatesToEveryEntryPoint(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "not-a-key-value-pair")

	tests := []struct {
		name          string
		call          func(t *testing.T) error
		wantErrSubstr string
	}{
		{
			name: "NewTracerProvider",
			call: func(t *testing.T) error {
				_, err := NewTracerProvider(context.Background(), ServiceGateway, tracetest.NewInMemoryExporter())
				return err
			},
			wantErrSubstr: `orderflow: build resource for service "gateway"`,
		},
		{
			name: "StartInProcess",
			call: func(t *testing.T) error {
				sys, topo, err := StartInProcess(context.Background(), tracetest.NewInMemoryExporter())
				// Bound listeners must be released and no half-built System
				// handed back, or the next capture would inherit stray ports.
				if sys != nil || topo != nil {
					t.Errorf("StartInProcess() = (%v, %v), want (nil, nil) on error", sys, topo)
				}
				return err
			},
			wantErrSubstr: `orderflow: provider "gateway"`,
		},
		{
			name: "RunService",
			call: func(t *testing.T) error {
				return RunService(context.Background(), ServiceAuth, "127.0.0.1:0", Topology{}, tracetest.NewInMemoryExporter())
			},
			wantErrSubstr: `orderflow: provider "auth"`,
		},
		{
			name: "Capture",
			call: func(t *testing.T) error {
				_, err := Capture(context.Background(), "happy")
				return err
			},
			wantErrSubstr: `capture "happy": start:`,
		},
		{
			name: "WriteFixtures",
			call: func(t *testing.T) error {
				return WriteFixtures(t.TempDir())
			},
			wantErrSubstr: `write fixtures: capture "happy"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(t)

			if err == nil {
				t.Fatalf("error = nil, want error containing %q", tt.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErrSubstr)
			}
			// Every entry point must keep the root cause reachable through %w.
			if !strings.Contains(err.Error(), "not-a-key-value-pair") {
				t.Errorf("error = %q, want it to name the offending resource attribute", err)
			}
		})
	}
}

// TestRunServiceReportsServeAndFlushFailures covers the two ways the
// container-mode entrypoint can fail after provider construction succeeds.
// Serial: RunService installs the global OTel propagator.
func TestRunServiceReportsServeAndFlushFailures(t *testing.T) {
	t.Run("port already bound", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		addr := ln.Addr().String()

		err = RunService(context.Background(), ServiceAuth, addr, Topology{}, tracetest.NewInMemoryExporter())

		if err == nil {
			t.Fatal("RunService() error = nil, want a bind failure")
		}
		want := `orderflow: serve "auth" on "` + addr + `"`
		if !strings.Contains(err.Error(), want) {
			t.Errorf("RunService() error = %q, want it to contain %q", err, want)
		}
	})

	t.Run("provider flush fails on exit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- RunService(ctx, ServiceAuth, "127.0.0.1:0", Topology{}, failingExporter{err: errExporterShutdown})
		}()
		cancel() // graceful drain, then the provider flush fails

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("RunService() error = nil, want a provider shutdown failure")
			}
			if !strings.Contains(err.Error(), `orderflow: provider "auth" shutdown`) {
				t.Errorf("RunService() error = %q, want a provider shutdown failure", err)
			}
			if !errors.Is(err, errExporterShutdown) {
				t.Errorf("RunService() error = %v, want it to wrap %v", err, errExporterShutdown)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("RunService did not return after ctx cancel")
		}
	})
}
