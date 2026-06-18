package orderflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// outboundHTTPTimeout bounds gateway→leaf and driver→gateway calls so a stalled
// downstream cannot block an in-process run or its shutdown indefinitely.
const outboundHTTPTimeout = 5 * time.Second

// allServices is the build/serve order. StartInProcess binds every listener
// (fully populating the topology) before it builds any handler, so order within
// this slice does not affect wiring correctness — the gateway closes over the
// complete topology regardless of its position.
var allServices = []string{ServiceGateway, ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify, ServiceLegacy}

// System holds the running in-process servers and their tracer providers.
type System struct {
	services []*Service
	servers  []*http.Server
}

// StartInProcess binds every service on an ephemeral localhost port (no fixed
// ports → no conflicts across sequential captures/tests), wires the gateway with
// the resulting topology, and serves each. Returns the actual topology.
func StartInProcess(ctx context.Context, exp sdktrace.SpanExporter) (*System, Topology, error) {
	otel.SetTextMapPropagator(Propagator())

	// 1. Bind listeners first so the gateway can be wired with real addresses.
	lns := make(map[string]net.Listener, len(allServices))
	topo := Topology{}
	for _, name := range allServices {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, l := range lns {
				_ = l.Close()
			}
			return nil, nil, fmt.Errorf("orderflow: listen %q: %w", name, err)
		}
		lns[name] = ln
		topo[name] = "http://" + ln.Addr().String()
	}

	// 2. Build providers + handlers, then serve each on its bound listener.
	sys := &System{}
	for _, name := range allServices {
		tp, err := NewTracerProvider(ctx, name, exp)
		if err != nil {
			_ = sys.Shutdown(ctx)
			for _, l := range lns {
				_ = l.Close()
			}
			return nil, nil, fmt.Errorf("orderflow: provider %q: %w", name, err)
		}
		h := handlerFor(name, tp, topo)
		sys.services = append(sys.services, &Service{Name: name, TP: tp, Handler: h})
		srv := &http.Server{Handler: h}
		sys.servers = append(sys.servers, srv)
		go func(s *http.Server, l net.Listener) { _ = s.Serve(l) }(srv, lns[name])
	}
	return sys, topo, nil
}

// handlerFor builds the otelhttp handler for one service.
// The gateway uses a propagatingClient so that trace context flows to leaf
// services without creating gateway-side CLIENT spans; those CLIENT spans would
// be marked Error for any 4xx leaf response (per the OTel HTTP semconv) and
// would skew the integration test's error-span counts.
func handlerFor(name string, tp *sdktrace.TracerProvider, topo Topology) http.Handler {
	if name == ServiceGateway {
		return instrument(tp, "POST /orders", gatewayHandler(propagatingClient(), topo))
	}
	return instrument(tp, "POST /"+name, leafHandler(name, tp.Tracer(name)))
}

// propagatingTransport injects W3C trace context + baggage into outbound
// requests using the global propagator, without creating its own CLIENT spans.
type propagatingTransport struct{ base http.RoundTripper }

func (t *propagatingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	otel.GetTextMapPropagator().Inject(r.Context(), propagation.HeaderCarrier(r.Header))
	return t.base.RoundTrip(r)
}

// propagatingClient returns an http.Client that propagates trace context and
// baggage into outbound requests without emitting CLIENT spans.
func propagatingClient() *http.Client {
	return &http.Client{
		Timeout:   outboundHTTPTimeout,
		Transport: &propagatingTransport{base: http.DefaultTransport},
	}
}

// Drive sends a plain (un-instrumented) request to the gateway with correlation
// baggage — faithful to the framework's http driver (baggage-only, no traceparent;
// the gateway server span roots the trace).
func (s *System) Drive(ctx context.Context, topo Topology, runID, scenario string) (int, []byte, error) {
	url, ok := topo[ServiceGateway]
	if !ok {
		return 0, nil, fmt.Errorf("orderflow: topology missing gateway")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: build gateway request: %w", err)
	}
	req.Header.Set(HeaderScenario, scenario)
	runMember, err := baggage.NewMember(BaggageRunID, runID)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: baggage member %s=%q: %w", BaggageRunID, runID, err)
	}
	scenarioMember, err := baggage.NewMember(BaggageScenario, scenario)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: baggage member %s=%q: %w", BaggageScenario, scenario, err)
	}
	bag, err := baggage.New(runMember, scenarioMember)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: build baggage: %w", err)
	}
	req.Header.Set("baggage", bag.String())
	client := &http.Client{Timeout: outboundHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: drive gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readAll(resp)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// Shutdown gracefully stops all HTTP servers and flushes all tracer providers.
func (s *System) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, srv := range s.servers {
		if err := srv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("orderflow: server shutdown: %w", err)
		}
	}
	for _, svc := range s.services {
		if err := svc.TP.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("orderflow: provider %q shutdown: %w", svc.Name, err)
		}
	}
	return firstErr
}

// RunService runs ONE service on a fixed address (container mode). It blocks
// until ctx is canceled (e.g. by a caller's signal handler), then gracefully
// drains the server and flushes the tracer provider so buffered spans are not
// dropped on exit.
func RunService(ctx context.Context, name, addr string, topo Topology, exp sdktrace.SpanExporter) error {
	otel.SetTextMapPropagator(Propagator())
	tp, err := NewTracerProvider(ctx, name, exp)
	if err != nil {
		return fmt.Errorf("orderflow: provider %q: %w", name, err)
	}
	srv := &http.Server{Addr: addr, Handler: handlerFor(name, tp, topo)}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), outboundHTTPTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("orderflow: serve %q on %q: %w", name, addr, err)
	}
	if err := tp.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("orderflow: provider %q shutdown: %w", name, err)
	}
	return nil
}

func readAll(resp *http.Response) ([]byte, error) {
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("orderflow: read gateway response: %w", err)
	}
	return b, nil
}
