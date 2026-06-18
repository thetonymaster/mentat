package orderflow

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestCallDownstreamPropagatesBaggageAndScenario(t *testing.T) {
	otel.SetTextMapPropagator(Propagator())
	exp := tracetest.NewInMemoryExporter()

	// A downstream "echo" service that records the scenario header it received.
	tp, err := NewTracerProvider(context.Background(), "echo", exp)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var gotScenario string
	h := instrument(tp, "POST /echo", func(w http.ResponseWriter, r *http.Request) {
		gotScenario = r.Header.Get(HeaderScenario)
		w.WriteHeader(http.StatusOK)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	topo := Topology{"echo": "http://" + ln.Addr().String()}
	bag, _ := baggage.Parse(BaggageRunID + "=run-xyz")
	ctx := baggage.ContextWithBaggage(context.Background(), bag)

	status, _, err := callDownstream(ctx, newClient(tp), topo, "echo", "happy")
	if err != nil {
		t.Fatalf("callDownstream: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if gotScenario != "happy" {
		t.Errorf("downstream X-Scenario = %q, want happy", gotScenario)
	}

	// The downstream SERVER span must carry the propagated test.run.id.
	stubs := waitForSpans(t, exp)
	if got := spanAttr(stubs, "echo", BaggageRunID); got != "run-xyz" {
		t.Errorf("downstream span %s = %q, want run-xyz", BaggageRunID, got)
	}
}

// waitForSpans polls until the exported span count stabilizes, then returns the
// snapshot. otelhttp server spans end on their own goroutine after the handler
// returns, so a fixed-count wait could return before late spans (e.g. the
// payment_decline error span) flush; waiting for stability is race-free here
// because all in-process calls are synchronous.
func waitForSpans(t *testing.T, exp *tracetest.InMemoryExporter) []sdktrace.ReadOnlySpan {
	t.Helper()
	last, stable := -1, 0
	for i := 0; i < 200; i++ { // up to ~2s
		n := len(exp.GetSpans())
		if n > 0 && n == last {
			if stable++; stable >= 3 {
				break
			}
		} else {
			stable = 0
		}
		last = n
		time.Sleep(10 * time.Millisecond)
	}
	return exp.GetSpans().Snapshots()
}

// spanAttr returns the attribute value for the first span whose service.name
// resource attribute equals service.
func spanAttr(spans []sdktrace.ReadOnlySpan, service, key string) string {
	for _, s := range spans {
		if svc := resourceServiceName(s.Resource()); svc == service {
			for _, kv := range s.Attributes() {
				if string(kv.Key) == key {
					return kv.Value.AsString()
				}
			}
		}
	}
	return ""
}

// resourceServiceName is a temporary helper (deleted in Task 6 once capture.go
// exports it) that extracts service.name from a resource.
func resourceServiceName(res *resource.Resource) string {
	for _, kv := range res.Attributes() {
		if kv.Key == semconv.ServiceNameKey {
			return kv.Value.AsString()
		}
	}
	return ""
}
