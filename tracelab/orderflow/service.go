package orderflow

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Topology maps a service name to its base URL.
type Topology map[string]string

// Service is one orderflow microservice: a name, its own tracer provider, and an
// otelhttp-instrumented handler (which creates SERVER spans and extracts trace
// context + baggage from incoming requests via the global propagator).
type Service struct {
	Name    string
	TP      *sdktrace.TracerProvider
	Handler http.Handler
}

// newClient returns a client whose transport creates CLIENT spans and injects
// trace context + baggage into downstream requests (via the global propagator).
// The timeout bounds any single downstream call so a stalled socket cannot block
// the request path indefinitely.
func newClient(tp *sdktrace.TracerProvider) *http.Client {
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(tp)),
	}
}

// instrument wraps a handler func as a named otelhttp SERVER handler.
func instrument(tp *sdktrace.TracerProvider, op string, h http.HandlerFunc) http.Handler {
	return otelhttp.NewHandler(h, op, otelhttp.WithTracerProvider(tp))
}

// callDownstream issues a POST to topo[service] carrying the scenario header,
// using ctx so trace context + baggage propagate to the next hop.
func callDownstream(ctx context.Context, client *http.Client, topo Topology, service, scenario string) (int, []byte, error) {
	url, ok := topo[service]
	if !ok {
		return 0, nil, fmt.Errorf("orderflow: no topology address for %q", service)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: build request to %q: %w", service, err)
	}
	req.Header.Set(HeaderScenario, scenario)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: call %q: %w", service, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("orderflow: read %q response: %w", service, err)
	}
	return resp.StatusCode, body, nil
}
