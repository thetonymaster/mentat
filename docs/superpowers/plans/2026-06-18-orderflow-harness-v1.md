# orderflow Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `tracelab/orderflow` — a dual-mode (in-process + container) microservices SUT that emits real `otelhttp` traces correlated by W3C baggage, plus golden-trace capture, so Mentat Phase 2 has a faithful microservice target.

**Architecture:** Six HTTP services (`gateway → auth → inventory → payment → notify` + forbidden `legacy-pricing`), each with its own `TracerProvider`/`service.name`, instrumented with `otelhttp` and a composite tracecontext+baggage propagator. A `BaggageSpanProcessor` copies `test.run.id`/`test.scenario` onto every span. The scenario is selected by an `X-Scenario` request header; the gateway holds all per-scenario orchestration so leaf services stay trivial. Golden capture drives each scenario through an ephemeral-port in-process system into an in-memory exporter, then normalizes to the same fixture schema the Mentat store already loads.

**Tech Stack:** Go 1.25, OpenTelemetry Go SDK v1.44.0, `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`, `net/http` (stdlib).

## Global Constraints

- Module path: `github.com/thetonymaster/mentat`; Go `1.25.0`.
- This plan touches ONLY `tracelab/orderflow/**`, `deploy/**`, `testdata/traces/orderflow/**`, `go.mod`/`go.sum`, and (optionally) `Makefile`. It makes **no changes** to `internal/**` or `cmd/mentat*` — those are the Phase 2 framework plan.
- `gofmt -l .` clean and `go vet ./...` clean before every commit; `golangci-lint run` clean (a `.golangci.yml` exists).
- Errors wrap with `fmt.Errorf("doing X: %w", err)` and name the concrete thing + value that failed. **No silent fallbacks** — a function that cannot do its job returns an `error`.
- Table-driven tests are the default shape. Hermetic by default: no network beyond localhost, no Docker in `go test`.
- Coverage floor **80% per package** (`cmd/*` is exempt per `.claude/skills/coverage/coverage.sh`).
- Conventional Commits (`feat:`, `test:`, `chore:`, `docs:`). Add files individually — `git add .` is forbidden. **No AI attribution** in commits.
- Pinned OTel GenAI keys and the fixture schema must match the existing harness exactly: model `tracelab/researchbot/capture.go` and the store's loader `internal/store/filestore.go`.

---

### Task 1: Dependency + package foundation

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `tracelab/orderflow/attrs.go`
- Test: `tracelab/orderflow/attrs_test.go`

**Interfaces:**
- Produces: `ServiceGateway, ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify, ServiceLegacy string` consts; `HeaderScenario, BaggageRunID, BaggageScenario string` consts; `func Scenarios() []string`.

- [ ] **Step 1: Add the otelhttp dependency**

Run:
```bash
go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@latest
go mod tidy
```
Expected: `go.mod` gains the `otelhttp` require line. If `go mod tidy` upgrades `go.opentelemetry.io/otel` beyond `v1.44.0`, that is acceptable **only if** `go build ./...` and `go test ./...` stay green; otherwise pin the otelhttp release that targets otel `v1.44.0` and re-run `go mod tidy`.

- [ ] **Step 2: Write the failing test**

```go
package orderflow

import (
	"reflect"
	"testing"
)

func TestScenariosAreTheSixKnownNames(t *testing.T) {
	want := []string{"happy", "payment_decline", "inventory_out", "slow", "legacy_path", "reorder"}
	if got := Scenarios(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Scenarios() = %v, want %v", got, want)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./tracelab/orderflow/ -run TestScenarios -v`
Expected: FAIL — `undefined: Scenarios` (package does not compile).

- [ ] **Step 4: Write minimal implementation**

```go
// Package orderflow is a dual-mode microservices SUT for Mentat Phase 2.
package orderflow

// Service names. Each runs with its own TracerProvider / service.name.
const (
	ServiceGateway   = "gateway"
	ServiceAuth      = "auth"
	ServiceInventory = "inventory"
	ServicePayment   = "payment"
	ServiceNotify    = "notify"
	ServiceLegacy    = "legacy-pricing"
)

// HeaderScenario selects scenario behaviour per request.
const HeaderScenario = "X-Scenario"

// Correlation baggage keys copied onto every span (see BaggageSpanProcessor).
const (
	BaggageRunID    = "test.run.id"
	BaggageScenario = "test.scenario"
)

// Scenarios lists the supported scenarios in deterministic order.
func Scenarios() []string {
	return []string{"happy", "payment_decline", "inventory_out", "slow", "legacy_path", "reorder"}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./tracelab/orderflow/ -run TestScenarios -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum tracelab/orderflow/attrs.go tracelab/orderflow/attrs_test.go
git commit -m "feat(orderflow): package foundation + otelhttp dependency"
```

---

### Task 2: OTel wiring — propagator, per-service provider, BaggageSpanProcessor

**Files:**
- Create: `tracelab/orderflow/otel.go`, `tracelab/orderflow/baggage.go`
- Test: `tracelab/orderflow/baggage_test.go`

**Interfaces:**
- Consumes: `BaggageRunID`, `BaggageScenario` (Task 1).
- Produces:
  - `func Propagator() propagation.TextMapPropagator`
  - `func NewBaggageSpanProcessor(keys ...string) sdktrace.SpanProcessor`
  - `func NewTracerProvider(ctx context.Context, service string, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error)`

- [ ] **Step 1: Write the failing test**

```go
package orderflow

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestBaggageSpanProcessorCopiesMembersOntoSpan(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(NewBaggageSpanProcessor(BaggageRunID, BaggageScenario)),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	bag, err := baggage.Parse(BaggageRunID + "=run-123," + BaggageScenario + "=happy")
	if err != nil {
		t.Fatalf("parse baggage: %v", err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), bag)

	_, span := tp.Tracer("test").Start(ctx, "op")
	span.End()

	stubs := exp.GetSpans()
	if len(stubs) != 1 {
		t.Fatalf("got %d spans, want 1", len(stubs))
	}
	attrs := map[string]string{}
	for _, kv := range stubs[0].Attributes {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if attrs[BaggageRunID] != "run-123" {
		t.Errorf("%s = %q, want run-123", BaggageRunID, attrs[BaggageRunID])
	}
	if attrs[BaggageScenario] != "happy" {
		t.Errorf("%s = %q, want happy", BaggageScenario, attrs[BaggageScenario])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tracelab/orderflow/ -run TestBaggageSpanProcessor -v`
Expected: FAIL — `undefined: NewBaggageSpanProcessor`.

- [ ] **Step 3: Write minimal implementation**

`tracelab/orderflow/baggage.go`:
```go
package orderflow

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// baggageSpanProcessor copies selected baggage members onto every span as it
// starts. The SDK does not auto-stamp baggage onto spans, so this processor is
// what makes test.run.id queryable per span — the Phase 2 correlation contract.
type baggageSpanProcessor struct{ keys []string }

// NewBaggageSpanProcessor returns a SpanProcessor copying the named baggage
// members onto each span's attributes at start.
func NewBaggageSpanProcessor(keys ...string) sdktrace.SpanProcessor {
	return baggageSpanProcessor{keys: keys}
}

func (p baggageSpanProcessor) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	b := baggage.FromContext(ctx)
	for _, k := range p.keys {
		if v := b.Member(k).Value(); v != "" {
			s.SetAttributes(attribute.String(k, v))
		}
	}
}

func (baggageSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (baggageSpanProcessor) Shutdown(context.Context) error  { return nil }
func (baggageSpanProcessor) ForceFlush(context.Context) error { return nil }
```

`tracelab/orderflow/otel.go`:
```go
package orderflow

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Propagator is the composite W3C tracecontext + baggage propagator. Set it once
// as the global propagator so otelhttp client/server inject and extract both.
func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// NewTracerProvider builds a provider whose resource carries service.name=service
// (plus anything in OTEL_RESOURCE_ATTRIBUTES) and that stamps correlation baggage
// onto every span. exp is OTLP in deployment, in-memory in capture and tests.
func NewTracerProvider(ctx context.Context, service string, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceName(service)),
	)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(NewBaggageSpanProcessor(BaggageRunID, BaggageScenario)),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./tracelab/orderflow/ -run TestBaggageSpanProcessor -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracelab/orderflow/otel.go tracelab/orderflow/baggage.go tracelab/orderflow/baggage_test.go
git commit -m "feat(orderflow): composite propagator + per-service provider + BaggageSpanProcessor"
```

---

### Task 3: Instrumented service plumbing + baggage propagation across a hop

**Files:**
- Create: `tracelab/orderflow/service.go`
- Test: `tracelab/orderflow/service_test.go`

**Interfaces:**
- Consumes: `Propagator`, `NewTracerProvider` (Task 2); `HeaderScenario` (Task 1).
- Produces:
  - `type Topology map[string]string` (service name → base URL)
  - `type Service struct { Name string; TP *sdktrace.TracerProvider; Handler http.Handler }`
  - `func newClient(tp *sdktrace.TracerProvider) *http.Client`
  - `func callDownstream(ctx context.Context, client *http.Client, topo Topology, service, scenario string) (int, []byte, error)`
  - `func instrument(tp *sdktrace.TracerProvider, op string, h http.HandlerFunc) http.Handler`
  - `func currentSpan(ctx context.Context) oteltrace.Span`

- [ ] **Step 1: Write the failing test**

This test proves the riskiest integration: an `otelhttp` client calling an `otelhttp` server propagates context **and** baggage, and the `BaggageSpanProcessor` stamps `test.run.id` on the downstream server span.

```go
package orderflow

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
```

Note: `resourceServiceName` is introduced in Task 6 (`capture.go`). For this task, add a temporary copy at the bottom of `service_test.go` so the test compiles; delete it in Task 6 once the exported helper exists. Temporary test helper:
```go
import (
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func resourceServiceName(res *resource.Resource) string {
	for _, kv := range res.Attributes() {
		if kv.Key == semconv.ServiceNameKey {
			return kv.Value.AsString()
		}
	}
	return ""
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tracelab/orderflow/ -run TestCallDownstream -v`
Expected: FAIL — `undefined: instrument` / `newClient` / `callDownstream`.

- [ ] **Step 3: Write minimal implementation**

```go
package orderflow

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
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
func newClient(tp *sdktrace.TracerProvider) *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(tp)),
	}
}

// instrument wraps a handler func as a named otelhttp SERVER handler.
func instrument(tp *sdktrace.TracerProvider, op string, h http.HandlerFunc) http.Handler {
	return otelhttp.NewHandler(h, op, otelhttp.WithTracerProvider(tp))
}

// currentSpan returns the active span in ctx (the otelhttp server span).
func currentSpan(ctx context.Context) oteltrace.Span { return oteltrace.SpanFromContext(ctx) }

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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./tracelab/orderflow/ -run TestCallDownstream -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracelab/orderflow/service.go tracelab/orderflow/service_test.go
git commit -m "feat(orderflow): instrumented server/client plumbing + baggage hop test"
```

---

### Task 4: Service handlers + per-scenario orchestration

**Files:**
- Create: `tracelab/orderflow/handlers.go`
- Test: `tracelab/orderflow/handlers_test.go`

**Interfaces:**
- Consumes: service-name consts, `HeaderScenario` (Task 1); `Topology`, `currentSpan`, `callDownstream` (Task 3).
- Produces:
  - `func planFor(scenario string) gatewayPlan` (unexported)
  - `func ExpectedResult(scenario string) (int, []byte)`
  - `func gatewayHandler(client *http.Client, topo Topology) http.HandlerFunc`
  - `func leafHandler(service string, tr oteltrace.Tracer) http.HandlerFunc`

- [ ] **Step 1: Write the failing test**

`planFor`/`ExpectedResult` are pure, so they unit-test without servers.

```go
package orderflow

import (
	"reflect"
	"testing"
)

func TestPlanForEncodesScenarioCallOrderAndStatus(t *testing.T) {
	tests := []struct {
		scenario   string
		wantCalls  []string
		wantStatus int
	}{
		{"happy", []string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, 201},
		{"slow", []string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, 201},
		{"payment_decline", []string{ServiceAuth, ServiceInventory, ServicePayment}, 402},
		{"inventory_out", []string{ServiceAuth, ServiceInventory}, 409},
		{"legacy_path", []string{ServiceAuth, ServiceLegacy, ServiceInventory, ServicePayment, ServiceNotify}, 201},
		{"reorder", []string{ServiceAuth, ServicePayment, ServiceInventory, ServiceNotify}, 201},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.scenario, func(t *testing.T) {
			p := planFor(tt.scenario)
			if !reflect.DeepEqual(p.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", p.calls, tt.wantCalls)
			}
			if p.status != tt.wantStatus {
				t.Errorf("status = %d, want %d", p.status, tt.wantStatus)
			}
		})
	}
}

func TestExpectedResultIsDeterministicJSON(t *testing.T) {
	status, body := ExpectedResult("happy")
	if status != 201 {
		t.Errorf("status = %d, want 201", status)
	}
	if string(body) != `{"status":"confirmed"}` {
		t.Errorf("body = %s, want confirmed JSON", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tracelab/orderflow/ -run "TestPlanFor|TestExpectedResult" -v`
Expected: FAIL — `undefined: planFor` / `ExpectedResult`.

- [ ] **Step 3: Write minimal implementation**

```go
package orderflow

import (
	"encoding/json"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// slowDelay is the fixed latency the `slow` scenario injects, chosen to breach a
// sub-second SLO budget deterministically.
const slowDelay = 900 * time.Millisecond

// gatewayPlan is the ordered downstream services the gateway calls for a
// scenario, plus the gateway's own response.
type gatewayPlan struct {
	calls  []string
	status int
	body   map[string]string
}

func planFor(scenario string) gatewayPlan {
	switch scenario {
	case "happy", "slow":
		return gatewayPlan{[]string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, http.StatusCreated, map[string]string{"status": "confirmed"}}
	case "payment_decline":
		return gatewayPlan{[]string{ServiceAuth, ServiceInventory, ServicePayment}, http.StatusPaymentRequired, map[string]string{"status": "declined"}}
	case "inventory_out":
		return gatewayPlan{[]string{ServiceAuth, ServiceInventory}, http.StatusConflict, map[string]string{"status": "out_of_stock"}}
	case "legacy_path":
		return gatewayPlan{[]string{ServiceAuth, ServiceLegacy, ServiceInventory, ServicePayment, ServiceNotify}, http.StatusCreated, map[string]string{"status": "confirmed"}}
	case "reorder":
		return gatewayPlan{[]string{ServiceAuth, ServicePayment, ServiceInventory, ServiceNotify}, http.StatusCreated, map[string]string{"status": "confirmed"}}
	default:
		return gatewayPlan{nil, http.StatusBadRequest, map[string]string{"status": "unknown_scenario"}}
	}
}

// ExpectedResult is the gateway's deterministic response per scenario — the
// boundary Output the framework's result comparator will assert against.
func ExpectedResult(scenario string) (int, []byte) {
	p := planFor(scenario)
	body, _ := json.Marshal(p.body)
	return p.status, body
}

func gatewayHandler(client *http.Client, topo Topology) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scenario := r.Header.Get(HeaderScenario)
		p := planFor(scenario)
		if scenario == "slow" {
			time.Sleep(slowDelay)
		}
		for _, svc := range p.calls {
			status, _, err := callDownstream(r.Context(), client, topo, svc, scenario)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			if status >= http.StatusBadRequest {
				break // short-circuit: a failed downstream stops the flow
			}
		}
		writeJSON(w, p.status, p.body)
	}
}

func leafHandler(service string, tr oteltrace.Tracer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scenario := r.Header.Get(HeaderScenario)
		switch {
		case service == ServicePayment && scenario == "payment_decline":
			// Explicit child error span so the error is independent of how
			// otelhttp maps the 402 status onto the server span.
			_, span := tr.Start(r.Context(), "payment.declined")
			span.SetStatus(codes.Error, "payment declined")
			span.End()
			writeJSON(w, http.StatusPaymentRequired, map[string]string{service: "declined"})
		case service == ServiceInventory && scenario == "inventory_out":
			writeJSON(w, http.StatusConflict, map[string]string{service: "out"})
		default:
			writeJSON(w, http.StatusOK, map[string]string{service: "ok"})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

Note on `ExpectedResult` JSON: a single-key map marshals deterministically to `{"status":"confirmed"}`. Keep every gateway `body` map to one key so the bytes are stable (the test asserts exact bytes).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./tracelab/orderflow/ -run "TestPlanFor|TestExpectedResult" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracelab/orderflow/handlers.go tracelab/orderflow/handlers_test.go
git commit -m "feat(orderflow): gateway orchestration + leaf handlers per scenario"
```

---

### Task 5: In-process system + full-scenario integration test

**Files:**
- Create: `tracelab/orderflow/system.go`
- Test: `tracelab/orderflow/system_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–4.
- Produces:
  - `type System struct { ... }`
  - `func StartInProcess(ctx context.Context, exp sdktrace.SpanExporter) (*System, Topology, error)` — binds every service on an ephemeral localhost port, returns the actual topology.
  - `func (s *System) Drive(ctx context.Context, topo Topology, runID, scenario string) (int, []byte, error)` — sends a **plain** (un-instrumented) HTTP request to the gateway with `test.run.id`/`test.scenario` in the `baggage` header (faithful to the framework's future http driver: baggage-only, no traceparent).
  - `func (s *System) Shutdown(ctx context.Context) error`
  - `func RunService(ctx context.Context, name, addr string, topo Topology, exp sdktrace.SpanExporter) error` — runs ONE service on a fixed addr (container mode); blocks.

- [ ] **Step 1: Write the failing test**

```go
package orderflow

import (
	"context"
	"sort"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestScenariosProduceExpectedBehaviour(t *testing.T) {
	tests := []struct {
		scenario      string
		wantStatus    int
		wantSubseq    []string // expected service order (ordered subsequence)
		forbidden     string   // service that must NOT appear ("" = none)
		wantErrSpans  int
	}{
		{"happy", 201, []string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, ServiceLegacy, 0},
		{"payment_decline", 402, []string{ServiceAuth, ServiceInventory, ServicePayment}, ServiceLegacy, 1},
		{"inventory_out", 409, []string{ServiceAuth, ServiceInventory}, ServicePayment, 0},
		{"legacy_path", 201, []string{ServiceAuth, ServiceLegacy, ServiceInventory, ServicePayment}, "", 0},
		{"reorder", 201, []string{ServiceAuth, ServicePayment, ServiceInventory}, ServiceLegacy, 0},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.scenario, func(t *testing.T) {
			ctx := context.Background()
			exp := tracetest.NewInMemoryExporter()
			sys, topo, err := StartInProcess(ctx, exp)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			defer func() { _ = sys.Shutdown(ctx) }()

			status, _, err := sys.Drive(ctx, topo, "run-"+tt.scenario, tt.scenario)
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}

			spans := waitForSpans(t, exp)
			order := serviceOrder(spans)
			if !isSubsequence(tt.wantSubseq, order) {
				t.Errorf("service order = %v, want subsequence %v", order, tt.wantSubseq)
			}
			if tt.forbidden != "" && contains(order, tt.forbidden) {
				t.Errorf("forbidden service %q appeared in %v", tt.forbidden, order)
			}
			if got := errorSpanCount(spans); got != tt.wantErrSpans {
				t.Errorf("error spans = %d, want %d", got, tt.wantErrSpans)
			}
		})
	}
}

// serviceOrder returns the first-seen service.name per service, in start order.
func serviceOrder(spans []sdktrace.ReadOnlySpan) []string {
	sorted := append([]sdktrace.ReadOnlySpan(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartTime().Before(sorted[j].StartTime()) })
	var order []string
	seen := map[string]bool{}
	for _, s := range sorted {
		svc := resourceServiceName(s.Resource())
		if svc != "" && !seen[svc] {
			seen[svc] = true
			order = append(order, svc)
		}
	}
	return order
}

func errorSpanCount(spans []sdktrace.ReadOnlySpan) int {
	n := 0
	for _, s := range spans {
		if s.Status().Code.String() == "Error" {
			n++
		}
	}
	return n
}

func isSubsequence(want, have []string) bool {
	i := 0
	for _, h := range have {
		if i < len(want) && h == want[i] {
			i++
		}
	}
	return i == len(want)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

(`resourceServiceName` and `waitForSpans` already exist from Task 3's test file in this same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tracelab/orderflow/ -run TestScenariosProduceExpectedBehaviour -v`
Expected: FAIL — `undefined: StartInProcess`.

- [ ] **Step 3: Write minimal implementation**

```go
package orderflow

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// serviceOrderList is the build/wiring order; gateway must be built last so it
// can close over the full topology, but it is bound first (it is the entrypoint).
var allServices = []string{ServiceGateway, ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify, ServiceLegacy}

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
func handlerFor(name string, tp *sdktrace.TracerProvider, topo Topology) http.Handler {
	if name == ServiceGateway {
		return instrument(tp, "POST /orders", gatewayHandler(newClient(tp), topo))
	}
	return instrument(tp, "POST /"+name, leafHandler(name, tp.Tracer(name)))
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
	req.Header.Set("baggage", fmt.Sprintf("%s=%s,%s=%s", BaggageRunID, runID, BaggageScenario, scenario))
	resp, err := http.DefaultClient.Do(req)
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
// until the server stops; callers wire signal handling.
func RunService(ctx context.Context, name, addr string, topo Topology, exp sdktrace.SpanExporter) error {
	otel.SetTextMapPropagator(Propagator())
	tp, err := NewTracerProvider(ctx, name, exp)
	if err != nil {
		return fmt.Errorf("orderflow: provider %q: %w", name, err)
	}
	srv := &http.Server{Addr: addr, Handler: handlerFor(name, tp, topo)}
	return srv.ListenAndServe()
}

func readAll(resp *http.Response) ([]byte, error) {
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("orderflow: read gateway response: %w", err)
	}
	return b, nil
}

// hostPort strips the http:// scheme from a base URL (container topology helper).
func hostPort(baseURL string) string { return strings.TrimPrefix(baseURL, "http://") }
```

Add the missing import `"io"` to the import block. (`hostPort` is used by the container CLI in Task 7.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./tracelab/orderflow/ -run TestScenariosProduceExpectedBehaviour -v`
Expected: PASS for all five subtests. (This proves the harness goes the right colour on each scenario — the harness analogue of L2/L3.)

- [ ] **Step 5: Run the full package with the race detector**

Run: `go test ./tracelab/orderflow/ -race`
Expected: PASS, no race warnings. (The `gotScenario` capture in Task 3 is written on a server goroutine and read after `waitForSpans`; if `-race` flags it, guard the field read/write with a `sync.Mutex` in that test.)

- [ ] **Step 6: Commit**

```bash
git add tracelab/orderflow/system.go tracelab/orderflow/system_test.go
git commit -m "feat(orderflow): in-process system + full-scenario integration test"
```

---

### Task 6: Golden-trace capture

**Files:**
- Create: `tracelab/orderflow/capture.go`
- Test: `tracelab/orderflow/capture_test.go`
- Modify: `tracelab/orderflow/service_test.go` (delete the temporary `resourceServiceName` helper; the exported one now lives in `capture.go`)

**Interfaces:**
- Consumes: `StartInProcess`, `Drive`, `Shutdown` (Task 5).
- Produces:
  - `func resourceServiceName(res *resource.Resource) string`
  - `func Capture(ctx context.Context, scenario string) ([]byte, error)`
  - `func WriteFixtures(dir string) error`

- [ ] **Step 1: Write the failing test**

```go
package orderflow

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCaptureIsDeterministicAndCarriesServiceName(t *testing.T) {
	ctx := context.Background()
	a, err := Capture(ctx, "happy")
	if err != nil {
		t.Fatalf("capture 1: %v", err)
	}
	b, err := Capture(ctx, "happy")
	if err != nil {
		t.Fatalf("capture 2: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("capture not deterministic:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}

	var fx struct {
		RunScenario string `json:"runScenario"`
		Spans       []struct {
			Attrs map[string]string `json:"attrs"`
		} `json:"spans"`
	}
	if err := json.Unmarshal(a, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fx.RunScenario != "happy" {
		t.Errorf("runScenario = %q, want happy", fx.RunScenario)
	}
	if len(fx.Spans) == 0 {
		t.Fatal("no spans captured")
	}
	// Every span must carry service.name (merged from its resource).
	for i, s := range fx.Spans {
		if s.Attrs["service.name"] == "" {
			t.Errorf("span[%d] missing service.name attr: %v", i, s.Attrs)
		}
	}
	// The first span (start-ordered) is the gateway server span.
	if got := fx.Spans[0].Attrs["service.name"]; got != ServiceGateway {
		t.Errorf("first span service.name = %q, want gateway", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tracelab/orderflow/ -run TestCaptureIsDeterministic -v`
Expected: FAIL — `undefined: Capture`.

- [ ] **Step 3: Write minimal implementation**

```go
package orderflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// fixtureSpan / fixture mirror the schema the Mentat store loads
// (internal/store/filestore.go). Volatile IDs and timestamps are dropped;
// parentage is by final index; service.name (a resource attr) is merged into
// attrs so sequence(service)/budgets can read it.
type fixtureSpan struct {
	Name        string            `json:"name"`
	ParentIndex int               `json:"parentIndex"`
	Attrs       map[string]string `json:"attrs"`
	Status      string            `json:"status"`
}

type fixture struct {
	RunScenario string        `json:"runScenario"`
	Spans       []fixtureSpan `json:"spans"`
}

// resourceServiceName extracts service.name from a span's resource.
func resourceServiceName(res *resource.Resource) string {
	for _, kv := range res.Attributes() {
		if kv.Key == semconv.ServiceNameKey {
			return kv.Value.AsString()
		}
	}
	return ""
}

// Capture drives one scenario through an ephemeral in-process system into an
// in-memory exporter and renders a normalized, deterministic span-forest JSON.
// Spans are ordered by start time so the fixture order encodes service-call
// order — sequence(service) relies on this because fixtures carry no timestamps.
func Capture(ctx context.Context, scenario string) ([]byte, error) {
	exp := tracetest.NewInMemoryExporter()
	sys, topo, err := StartInProcess(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("capture %q: start: %w", scenario, err)
	}
	if _, _, err := sys.Drive(ctx, topo, "capture-"+scenario, scenario); err != nil {
		_ = sys.Shutdown(ctx)
		return nil, fmt.Errorf("capture %q: drive: %w", scenario, err)
	}
	stubs := stableSnapshots(exp)
	if err := sys.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("capture %q: shutdown: %w", scenario, err)
	}
	if len(stubs) == 0 {
		return nil, fmt.Errorf("capture %q: no spans exported", scenario)
	}

	sort.SliceStable(stubs, func(i, j int) bool { return stubs[i].StartTime.Before(stubs[j].StartTime) })

	idx := make(map[string]int, len(stubs))
	for i, s := range stubs {
		idx[s.SpanContext.SpanID().String()] = i
	}

	out := fixture{RunScenario: scenario, Spans: make([]fixtureSpan, 0, len(stubs))}
	for _, s := range stubs {
		attrs := make(map[string]string, len(s.Attributes)+1)
		for _, kv := range s.Attributes {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
		if sn := resourceServiceName(s.Resource); sn != "" {
			attrs["service.name"] = sn
		}
		parent := -1
		if s.Parent.IsValid() {
			if pi, ok := idx[s.Parent.SpanID().String()]; ok {
				parent = pi
			}
		}
		out.Spans = append(out.Spans, fixtureSpan{
			Name:        s.Name,
			ParentIndex: parent,
			Attrs:       attrs,
			Status:      s.Status.Code.String(),
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// stableSnapshots polls until the exported span count is stable, since otelhttp
// server spans end on their own goroutine after the handler returns.
func stableSnapshots(exp *tracetest.InMemoryExporter) []tracetest.SpanStub {
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
	return exp.GetSpans()
}

// WriteFixtures captures every scenario into dir/<scenario>.json with a trailing
// newline for clean diffs.
func WriteFixtures(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write fixtures: mkdir %q: %w", dir, err)
	}
	for _, name := range Scenarios() {
		data, err := Capture(context.Background(), name)
		if err != nil {
			return fmt.Errorf("write fixtures: capture %q: %w", name, err)
		}
		dest := filepath.Join(dir, name+".json")
		if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("write fixtures: write %q: %w", dest, err)
		}
	}
	return nil
}
```

Then delete the temporary `resourceServiceName` definition (and its `resource`/`semconv` imports if now unused) from `service_test.go` — the exported one in `capture.go` is in the same package.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./tracelab/orderflow/ -run TestCaptureIsDeterministic -v`
Expected: PASS.

- [ ] **Step 5: Run the whole package + coverage**

Run: `go test ./tracelab/orderflow/ -race && bash .claude/skills/coverage/coverage.sh ./tracelab/orderflow/`
Expected: tests PASS; `tracelab/orderflow` coverage ≥ 80%. If under, add cases for the `default` (unknown-scenario) branch in `planFor` and an error path in `callDownstream`/`Drive`.

- [ ] **Step 6: Commit**

```bash
git add tracelab/orderflow/capture.go tracelab/orderflow/capture_test.go tracelab/orderflow/service_test.go
git commit -m "feat(orderflow): deterministic golden-trace capture with service.name merge"
```

---

### Task 7: CLI entrypoints (serve + capture)

**Files:**
- Create: `tracelab/orderflow/cmd/orderflow/main.go`, `tracelab/orderflow/cmd/capture/main.go`

**Interfaces:**
- Consumes: `RunService`, `Topology`, `WriteFixtures` (Tasks 5–6).
- Produces: two `main` binaries. `cmd/*` is coverage-exempt, so these are build-verified, not unit-tested.

- [ ] **Step 1: Write the serve CLI (container mode)**

`tracelab/orderflow/cmd/orderflow/main.go`:
```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	of "github.com/thetonymaster/mentat/tracelab/orderflow"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

// Topology is supplied as ORDERFLOW_TOPOLOGY="gateway=http://gateway:8080,auth=http://auth:8081,...".
func parseTopology(env string) (of.Topology, error) {
	topo := of.Topology{}
	for _, pair := range strings.Split(env, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("orderflow: bad topology entry %q (want name=url)", pair)
		}
		topo[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(topo) == 0 {
		return nil, fmt.Errorf("orderflow: empty ORDERFLOW_TOPOLOGY")
	}
	return topo, nil
}

func main() {
	service := flag.String("service", "", "service name to run (one of orderflow's services)")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	if *service == "" {
		fmt.Fprintln(os.Stderr, "orderflow: -service is required")
		os.Exit(2)
	}
	topo, err := parseTopology(os.Getenv("ORDERFLOW_TOPOLOGY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "orderflow:", err)
		os.Exit(2)
	}

	ctx := context.Background()
	exp, err := otlptracehttp.New(ctx) // honors OTEL_EXPORTER_OTLP_ENDPOINT
	if err != nil {
		fmt.Fprintln(os.Stderr, "orderflow: exporter:", err)
		os.Exit(1)
	}
	if err := of.RunService(ctx, *service, *addr, topo, exp); err != nil {
		fmt.Fprintln(os.Stderr, "orderflow:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Write the capture CLI**

`tracelab/orderflow/cmd/capture/main.go`:
```go
package main

import (
	"fmt"
	"os"

	of "github.com/thetonymaster/mentat/tracelab/orderflow"
)

func main() {
	if err := of.WriteFixtures("testdata/traces/orderflow"); err != nil {
		fmt.Fprintln(os.Stderr, "capture:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Verify both binaries build**

Run: `go build ./tracelab/orderflow/cmd/...`
Expected: builds with no output. Then `go vet ./tracelab/orderflow/...` and `gofmt -l tracelab/orderflow` → both clean.

- [ ] **Step 4: Commit**

```bash
git add tracelab/orderflow/cmd/orderflow/main.go tracelab/orderflow/cmd/capture/main.go
git commit -m "feat(orderflow): serve (container) + capture CLIs"
```

---

### Task 8: Generate goldens + container deploy + smoke

**Files:**
- Create: `testdata/traces/orderflow/{happy,payment_decline,inventory_out,slow,legacy_path,reorder}.json` (generated)
- Modify: `deploy/docker-compose.yml`
- Create: `deploy/orderflow.Dockerfile`, `deploy/orderflow-smoke.sh`

**Interfaces:**
- Consumes: the capture CLI (Task 7).

- [ ] **Step 1: Generate the goldens**

Run: `go run ./tracelab/orderflow/cmd/capture`
Expected: six files appear under `testdata/traces/orderflow/`. Inspect `happy.json`: spans are start-ordered (gateway first), every span has a `service.name` attr, the forbidden `legacy-pricing` service is absent, and `payment_decline.json` contains one span with `"status": "Error"`.

- [ ] **Step 2: Verify a golden loads through the Mentat store**

Run:
```bash
go test ./internal/store/ -run TestLoadFixture -v
```
Expected: PASS (existing test). Then sanity-load an orderflow golden in a scratch check — the comparators that Phase 2's framework plan will run (`sequence`, `budgets`, `result`) read the flat span list + attrs, **not** parentage, so the depth-limited synthetic-ParentID note in `filestore.go` does not affect them. Document this in the commit body.

> **Known limitation (by design, documented):** Goldens drop timestamps, so the `slow` scenario's latency violation is **not** assertable at L1 — `slow.json` is structurally ~identical to `happy.json`. Latency budgets are validated at L2 (live Tempo) in the framework plan. This is intentional, not a capture bug.

- [ ] **Step 3: Commit the goldens**

```bash
git add testdata/traces/orderflow/happy.json testdata/traces/orderflow/payment_decline.json testdata/traces/orderflow/inventory_out.json testdata/traces/orderflow/slow.json testdata/traces/orderflow/legacy_path.json testdata/traces/orderflow/reorder.json
git commit -m "feat(orderflow): captured golden traces for the six scenarios"
```

- [ ] **Step 4: Add the container build + compose services**

`deploy/orderflow.Dockerfile`:
```dockerfile
FROM golang:1.25 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/orderflow ./tracelab/orderflow/cmd/orderflow

FROM gcr.io/distroless/static-debian12
COPY --from=build /bin/orderflow /bin/orderflow
ENTRYPOINT ["/bin/orderflow"]
```

Append the six services to `deploy/docker-compose.yml` (each its own container — authentic cross-process baggage propagation). All share one image, one topology env, and export OTLP to the existing `collector`:
```yaml
  orderflow-gateway:
    build: { context: .., dockerfile: deploy/orderflow.Dockerfile }
    command: ["-service=gateway", "-addr=:8080"]
    environment: &orderflow_env
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://collector:4318"
      ORDERFLOW_TOPOLOGY: "gateway=http://orderflow-gateway:8080,auth=http://orderflow-auth:8081,inventory=http://orderflow-inventory:8082,payment=http://orderflow-payment:8083,notify=http://orderflow-notify:8084,legacy-pricing=http://orderflow-legacy:8085"
    ports: ["8080:8080"]
    depends_on: [collector]
  orderflow-auth:
    build: { context: .., dockerfile: deploy/orderflow.Dockerfile }
    command: ["-service=auth", "-addr=:8081"]
    environment: *orderflow_env
    depends_on: [collector]
  orderflow-inventory:
    build: { context: .., dockerfile: deploy/orderflow.Dockerfile }
    command: ["-service=inventory", "-addr=:8082"]
    environment: *orderflow_env
    depends_on: [collector]
  orderflow-payment:
    build: { context: .., dockerfile: deploy/orderflow.Dockerfile }
    command: ["-service=payment", "-addr=:8083"]
    environment: *orderflow_env
    depends_on: [collector]
  orderflow-notify:
    build: { context: .., dockerfile: deploy/orderflow.Dockerfile }
    command: ["-service=notify", "-addr=:8084"]
    environment: *orderflow_env
    depends_on: [collector]
  orderflow-legacy:
    build: { context: .., dockerfile: deploy/orderflow.Dockerfile }
    command: ["-service=legacy-pricing", "-addr=:8085"]
    environment: *orderflow_env
    depends_on: [collector]
```

- [ ] **Step 5: Add a container smoke check**

`deploy/orderflow-smoke.sh`:
```bash
#!/usr/bin/env bash
# Drives the happy scenario against the containerized gateway and asserts the
# run's spans land in Tempo, correlated by test.run.id baggage.
set -euo pipefail
RUN_ID="smoke-$$"
curl -fsS -X POST http://localhost:8080/ \
  -H "X-Scenario: happy" \
  -H "baggage: test.run.id=${RUN_ID},test.scenario=happy" >/dev/null
echo "drove happy as ${RUN_ID}; waiting for Tempo..."
sleep 10
curl -fsS "http://localhost:3200/api/search?tags=test.run.id%3D${RUN_ID}" | grep -q "${RUN_ID}" \
  && echo "OK: trace for ${RUN_ID} found in Tempo" \
  || { echo "FAIL: no trace for ${RUN_ID}"; exit 1; }
```
Run: `chmod +x deploy/orderflow-smoke.sh`.

- [ ] **Step 6: Verify YAML parses and full build/test/lint are green**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('deploy/docker-compose.yml'))" && echo OK
go build ./... && go test ./tracelab/orderflow/ -race && gofmt -l tracelab deploy/orderflow-smoke.sh 2>/dev/null; golangci-lint run ./tracelab/...
```
Expected: `OK`; build + tests PASS; lint clean. (Container build is exercised on demand via `make harness-up`, not in `go test`.)

- [ ] **Step 7: Commit**

```bash
git add deploy/orderflow.Dockerfile deploy/docker-compose.yml deploy/orderflow-smoke.sh
git commit -m "feat(orderflow): container deploy (6 services) + cross-process smoke check"
```

---

## Self-Review

**1. Spec coverage** (`2026-06-18-mentat-phase2-portability-design.md`):
- §8 topology `gateway→auth→inventory→payment→notify` + forbidden `legacy-pricing` → Task 4 `planFor` + Task 5 integration test. ✔
- §8 dual-mode (in-process + container, config-driven topology) → Task 5 `StartInProcess`/`RunService`, Task 8 compose. ✔
- §8 instrumentation: `otelhttp` + composite propagator + `BaggageSpanProcessor` → Tasks 2–3. ✔
- §8 six `X-Scenario` scenarios + deterministic `slow` sleep → Tasks 1, 4. ✔
- §3 baggage-only correlation, gateway-rooted trace, `service.name` per service → Tasks 2, 5 (`Drive` plain client + baggage header). ✔
- §9 L1 goldens to `testdata/traces/orderflow/*.json`, reviewed-on-change → Tasks 6, 8. ✔
- §9 `inventory_out` captured now, asserted via sequence/result (shape deferred) → Task 5 (`forbidden: payment`, status 409) + golden. ✔
- §10 plan 1 scope: orderflow + capture, **no Mentat changes** → Global Constraints + every task path. ✔
- L2/L3 *through Mentat* and the http driver/grammar are explicitly **plan 2**, not here. ✔

**2. Placeholder scan:** No "TBD"/"handle edge cases"/"similar to". The one cross-task helper (`resourceServiceName`) is given concrete temporary code in Task 3 and promoted in Task 6 with an explicit delete step.

**3. Type consistency:** `Topology`, `Service`, `StartInProcess`, `Drive`, `Capture`, `WriteFixtures`, `RunService`, `ExpectedResult`, `planFor`, `gatewayPlan{calls,status,body}`, `resourceServiceName` are referenced with identical signatures across tasks. `waitForSpans`/`resourceServiceName` are defined once in the package's test files and reused. Fixture JSON keys (`name`,`parentIndex`,`attrs`,`status`,`runScenario`) match `internal/store/filestore.go` exactly.

**Documented reality-checks:** (a) goldens carry no timestamps → `slow` latency is L2-only, stated in Task 8; (b) `filestore.go` synthetic-ParentID is depth-limited but the in-scope comparators don't read parentage, stated in Task 8; (c) `otelhttp` 402-status handling is bypassed via an explicit child error span in Task 4 so the error signal is deterministic.
