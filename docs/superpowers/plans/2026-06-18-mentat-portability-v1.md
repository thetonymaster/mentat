# Mentat Portability (microservices) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the same `Evidence`-only comparator suite works against distributed microservices by adding an `http` driver (baggage correlation), a portable `sequence(service)` comparator, the service-path Gherkin grammar, and `http` target config — then validate it L1→L3 against the existing `orderflow` SUT.

**Architecture:** Mentat is layered: Gherkin/godog → `engine.Drive` (composition root) → per-seam `driver`/`store`/`correlate` interfaces → `comparator` core that reads only `Evidence` (a `Trace` forest + driver `Output`). This plan adds one new driver adapter (`http`), generalizes one comparator (`sequence` gains a `Kind` selector), and adds four godog steps. Correlation flips from v1's `OTEL_RESOURCE_ATTRIBUTES` (shell) to **W3C baggage** (http) — the architecturally risky path this phase de-risks. The `orderflow` SUT and its golden fixtures already exist (Phase 2, plan 1); this plan consumes them.

**Tech Stack:** Go 1.x, `github.com/cucumber/godog` v0.15.1 (BDD), `go.opentelemetry.io/otel` (baggage encoding), `go.uber.org/mock` (gomock), `net/http` + `net/http/httptest` (driver + tests). Tempo 2.5 + OTel Collector for live E2E (`make harness-up`).

## Global Constraints

These apply to **every** task. Copied verbatim from `CLAUDE.md` and the design spec.

- **Comparators consume `Evidence` only** — never a `TraceStore` or `Driver`. (Architecture invariant 1.)
- **`Trace` is a forest** — never assume a single root. The orderflow run happens to have one root (`gateway`), but the model stays forest-shaped. (Invariant 2.)
- **Every seam is an interface wired at one composition root (`engine.Build`)** via per-seam registries. Manual DI; no `wire`/`fx`. (Invariant 3.)
- **No silent fallbacks.** A function that cannot do its job returns a wrapped `error` (`fmt.Errorf("doing X: %w", err)`) naming the concrete thing that failed and the value involved. Never a zero-value success or guessed result. **A non-2xx HTTP response is NOT a driver error — it is data the comparators judge.** Only transport-level failures (connection refused, timeout, malformed URL) are errors. (Invariant 4.)
- **Correlation is tag-first, baggage for http.** The `http` driver injects baggage `test.run.id=<uuid>,test.scenario=<s>` and an `X-Scenario: <s>` header. It does **NOT** inject `traceparent`. (Invariant 5 + spec §3.)
- **Service identity = the `service.name` resource attribute.** The trace store (Tempo and the fixture loader) merges resource attributes onto every span, so the comparator reads `service.name` like any other span attribute.
- **Go hygiene:** `gofmt -l .` clean and `go vet ./...` clean before any commit. Run `golangci-lint run` if `.golangci.yml` exists.
- **TDD, table-driven tests, uber gomock** for the `core` interfaces. **Coverage floor: 80% per package** (`make cover`). A PR dropping a package below 80% is blocked.
- **Conventional Commits** (`feat:`, `fix:`, `test:`, `docs:`). `git add .` is forbidden — add files individually. **No AI attribution** in commits or PRs.

---

## Known reality-vs-model gaps (read before starting)

These were discovered by inspecting the real golden fixtures and the live SUT. They shape the test design — do not "fix" them away.

1. **Golden fixtures carry no timestamps.** `internal/store/filestore.go::LoadFixture` produces spans with a zero `Start`. So `sequence(service)` MUST order services by a **stable** sort on `Start`: for fixtures (all-zero `Start`) the stable sort preserves the spans' array order, and the `tracelab` capture writes that array in start-time order. For live Tempo, `Start` is real and authoritative. Both paths therefore yield the correct service order. (Verified against `testdata/traces/orderflow/{happy,reorder}.json`.)

2. **The `slow` golden is structurally identical to `happy`** (same five services, all `201/200`, no error span). The injected 900 ms latency lives only in timestamps, which fixtures drop. Therefore the **latency violation is L3-only** (live Tempo). Do not write an L1 latency assertion for `slow` — it would be a no-op (`Envelope()==0`). (Verified against `testdata/traces/orderflow/slow.json`.)

3. **Live Tempo span-status string mapping is unverified.** The `budgets` comparator counts `s.Status == "Error"` (the OTel SDK string). The golden capture writes exactly that string, so `budgets(error)` is exercised at **L1** against `payment_decline.json` (which carries a `payment.declined` span with `"status": "Error"`). At **L3** we deliberately assert `payment_decline` via `result(status)` (reads the HTTP response status directly, no Tempo status-string dependency) to avoid coupling the meta-test to an unverified mapping.

4. **The orderflow gateway emits no CLIENT spans.** It calls downstreams via `propagatingClient()` (injects context/baggage without creating client spans). So live traces are one clean SERVER span per service — `sequence(service)` first-seen-per-service dedup is robust either way. (Verified: `newClient` is used only in `tracelab/orderflow/service_test.go`.)

---

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `internal/config/config.go` (modify) | `Target.HTTP` block + loader validation (url/method required when `adapter: http`). | 1 |
| `internal/config/config_test.go` (modify) | Table tests for http-target load + validation errors. | 1 |
| `internal/core/core.go` (modify) | `HTTPSpec` type + `RunSpec.HTTP` field (plain struct, no interface change → no mock regen). | 2 |
| `internal/driver/http.go` (create) | `http` driver adapter: baggage + `X-Scenario` injection, `Output` mapping, non-2xx-is-data. | 2 |
| `internal/driver/http_test.go` (create) | Hermetic `httptest` tests of the driver. | 2 |
| `internal/comparator/sequence.go` (modify) | `SequenceExpectation.Kind` selector; `tool` (default) + new `service` path. | 3 |
| `internal/comparator/sequence_test.go` (modify) | Table tests for the `service` path + backward-compat + error paths. | 3 |
| `internal/comparator/orderflow_fixtures_test.go` (create) | L1 golden tests: run `sequence(service)`/`result`/`budgets` against the 6 orderflow goldens. | 4 |
| `internal/engine/engine.go` (modify) | Map `config.Target.HTTP` → `core.RunSpec.HTTP` in `Drive`. | 5 |
| `internal/engine/build.go` (modify) | Register `driver.NewHTTP()` under `"http"`. | 5 |
| `internal/engine/engine_test.go` (modify) | Integration test: engine drives an `http` target through `httptest` + gomock store. | 5 |
| `internal/steps/steps.go` (modify) | Four service-path steps: services-in-order, service-never-called, response-status (exists), response-body-json-contains. | 6 |
| `mentat.yaml` (modify) | Add the `checkout` http target. | 6 |
| `features/checkout.feature` (create) | L2 happy scenario (all green). | 6 |
| `e2e/orderflow_test.go` (create) | L2 e2e: `mentat run features/checkout.feature` exits zero. | 6 |
| `features/meta/orderflow/*.feature` (create ×5) | L3 meta scenarios (each trips exactly one comparator). | 7 |
| `e2e/orderflow_meta_test.go` (create) | L3 e2e: each bad scenario makes mentat exit non-zero with the expected reason. | 7 |

---

### Task 1: `http` target config

Add an optional `http` block to `Target` and validate it. Pure framework, hermetic, no dependency on later tasks.

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.HTTP{ URL, Method string; Headers map[string]string }` and `config.Target.HTTP config.HTTP` (yaml key `http`). Loader rule: when `adapter == "http"`, `http.url` and `http.method` are required (missing → descriptive error); `headers` optional. The `http` default `max_concurrency` (8) is unchanged.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoadHTTPTarget(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErr     bool
		wantErrSub  string
		wantURL     string
		wantMethod  string
		wantConc    int
		wantHeaders map[string]string
	}{
		{
			name: "valid http target loads with default concurrency 8",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
      method: POST
      headers:
        Content-Type: application/json
`,
			wantURL:     "http://localhost:8080/orders",
			wantMethod:  "POST",
			wantConc:    8,
			wantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		{
			name: "http target without headers loads (headers optional)",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
      method: POST
`,
			wantURL:    "http://localhost:8080/orders",
			wantMethod: "POST",
			wantConc:   8,
		},
		{
			name: "http target missing url is a descriptive error",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      method: POST
`,
			wantErr:    true,
			wantErrSub: `target "checkout": http.url is required`,
		},
		{
			name: "http target missing method is a descriptive error",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
`,
			wantErr:    true,
			wantErrSub: `target "checkout": http.method is required`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := cfg.Targets["checkout"]
			if got.HTTP.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.HTTP.URL, tt.wantURL)
			}
			if got.HTTP.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", got.HTTP.Method, tt.wantMethod)
			}
			if got.MaxConcurrency != tt.wantConc {
				t.Errorf("MaxConcurrency = %d, want %d", got.MaxConcurrency, tt.wantConc)
			}
			for k, v := range tt.wantHeaders {
				if got.HTTP.Headers[k] != v {
					t.Errorf("Headers[%q] = %q, want %q", k, got.HTTP.Headers[k], v)
				}
			}
		})
	}
}
```

If `strings` is not already imported in `config_test.go`, add it to the import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadHTTPTarget -v`
Expected: FAIL — compile error `got.HTTP undefined (type Target has no field or method HTTP)`.

- [ ] **Step 3: Add the `HTTP` type, the `Target.HTTP` field, and loader validation**

In `internal/config/config.go`, add the `HTTP` struct and a field on `Target`:

```go
type Target struct {
	Adapter        string   `yaml:"adapter"`
	Command        []string `yaml:"command"`
	MaxConcurrency int      `yaml:"max_concurrency"`
	HTTP           HTTP     `yaml:"http"`
}

// HTTP is the per-target request config used when adapter is "http".
type HTTP struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
}
```

Then, in `Load`, inside the `for name, t := range c.Targets` loop, after the existing `MaxConcurrency` handling, add the http validation:

```go
		if t.Adapter == "http" {
			if t.HTTP.URL == "" {
				return Config{}, fmt.Errorf("target %q: http.url is required when adapter is http", name)
			}
			if t.HTTP.Method == "" {
				return Config{}, fmt.Errorf("target %q: http.method is required when adapter is http", name)
			}
		}
```

- [ ] **Step 4: Run the tests and make sure they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (new `TestLoadHTTPTarget` and all existing config tests).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/config/config.go internal/config/config_test.go
go vet ./internal/config/
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): http target block with url/method validation"
```

---

### Task 2: `http` driver adapter

Add the `core.HTTPSpec` boundary type, a `RunSpec.HTTP` field, and the `http` driver. The driver injects baggage + `X-Scenario`, sends `RunSpec.Input` as the body, and maps the response into `Output`. Non-2xx is data; only transport failures error.

**Files:**
- Modify: `internal/core/core.go`
- Create: `internal/driver/http.go`
- Test: `internal/driver/http_test.go`

**Interfaces:**
- Consumes: `core.RunSpec` (gains `HTTP core.HTTPSpec`), `core.Output` (existing `Status int`, `Body []byte`, `Answer string`).
- Produces:
  - `core.HTTPSpec{ URL, Method string; Headers map[string]string }` and `core.RunSpec.HTTP core.HTTPSpec` — a **plain struct field**; the `Driver` interface is unchanged, so **mocks do NOT need regenerating**.
  - `driver.NewHTTP() core.Driver` — registered later (Task 5) under `"http"`. On `Run`: builds the request from `spec.HTTP`, sets each `spec.HTTP.Headers` entry, derives the scenario from `--scenario <s>` in `spec.Command`, sets header `X-Scenario: <s>`, sets header `baggage: test.run.id=<id>,test.scenario=<s>` (W3C-encoded via `go.opentelemetry.io/otel/baggage`), sends `spec.Input` as body, and returns `core.Output{ Status, Body, Answer: string(body) }`. Empty URL/method or transport failure → wrapped error. Non-2xx → no error.

- [ ] **Step 1: Add `HTTPSpec` and `RunSpec.HTTP` to core**

In `internal/core/core.go`, add the `HTTP` field to `RunSpec` and define `HTTPSpec`:

```go
// RunSpec is the driver input. The adapter applies RunID/Tags via its transport.
type RunSpec struct {
	Target  string
	Adapter string
	Command []string // shell adapter argv; http adapter parses --scenario from it
	Env     map[string]string
	Input   string // prompt / request body
	HTTP    HTTPSpec
	RunID   string
	Tags    map[string]string // test.run.id, test.scenario, test.case
}

// HTTPSpec is the http adapter's per-target request config (mirrors config.HTTP,
// kept in core so the driver has no dependency on the config layer).
type HTTPSpec struct {
	URL     string
	Method  string
	Headers map[string]string
}
```

(Adding a struct field to `RunSpec` does not change the `Driver` interface; `go generate` / mock regen is not required.)

- [ ] **Step 2: Write the failing test**

Create `internal/driver/http_test.go`:

```go
package driver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestHTTPDriverHappyPath(t *testing.T) {
	var gotMethod, gotScenario, gotBaggage, gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotScenario = r.Header.Get("X-Scenario")
		gotBaggage = r.Header.Get("baggage")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"confirmed"}`))
	}))
	defer srv.Close()

	spec := core.RunSpec{
		Target:  "checkout",
		Adapter: "http",
		Command: []string{"--scenario", "happy"},
		Input:   "request-body",
		HTTP: core.HTTPSpec{
			URL:     srv.URL,
			Method:  http.MethodPost,
			Headers: map[string]string{"Content-Type": "application/json"},
		},
		RunID: "run-abc",
		Tags:  map[string]string{"test.run.id": "run-abc"},
	}

	res, err := NewHTTP().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotScenario != "happy" {
		t.Errorf("X-Scenario = %q, want happy", gotScenario)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody != "request-body" {
		t.Errorf("body = %q, want request-body", gotBody)
	}
	if !strings.Contains(gotBaggage, "test.run.id=run-abc") {
		t.Errorf("baggage %q missing test.run.id=run-abc", gotBaggage)
	}
	if !strings.Contains(gotBaggage, "test.scenario=happy") {
		t.Errorf("baggage %q missing test.scenario=happy", gotBaggage)
	}
	if res.Output.Status != http.StatusCreated {
		t.Errorf("Status = %d, want 201", res.Output.Status)
	}
	if res.Output.Answer != `{"status":"confirmed"}` {
		t.Errorf("Answer = %q", res.Output.Answer)
	}
	if string(res.Output.Body) != `{"status":"confirmed"}` {
		t.Errorf("Body = %q", res.Output.Body)
	}
	if res.RunID != "run-abc" {
		t.Errorf("RunID = %q, want run-abc", res.RunID)
	}
}

func TestHTTPDriverNon2xxIsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"status":"declined"}`))
	}))
	defer srv.Close()

	spec := core.RunSpec{
		Target:  "checkout",
		Adapter: "http",
		Command: []string{"--scenario", "payment_decline"},
		HTTP:    core.HTTPSpec{URL: srv.URL, Method: http.MethodPost},
		RunID:   "run-402",
		Tags:    map[string]string{"test.run.id": "run-402"},
	}

	res, err := NewHTTP().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("non-2xx must NOT be an error, got: %v", err)
	}
	if res.Output.Status != http.StatusPaymentRequired {
		t.Errorf("Status = %d, want 402", res.Output.Status)
	}
}

func TestHTTPDriverErrors(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close() // connection will be refused

	tests := []struct {
		name    string
		spec    core.RunSpec
		wantSub string
	}{
		{
			name:    "empty URL is an error",
			spec:    core.RunSpec{Target: "checkout", HTTP: core.HTTPSpec{Method: "POST"}},
			wantSub: "empty URL",
		},
		{
			name:    "empty method is an error",
			spec:    core.RunSpec{Target: "checkout", HTTP: core.HTTPSpec{URL: "http://x"}},
			wantSub: "empty method",
		},
		{
			name:    "transport failure is a wrapped error",
			spec:    core.RunSpec{Target: "checkout", RunID: "run-x", HTTP: core.HTTPSpec{URL: closedURL, Method: "POST"}},
			wantSub: "http:",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHTTP().Run(context.Background(), tt.spec)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/driver/ -run TestHTTP -v`
Expected: FAIL — compile error `undefined: NewHTTP`.

- [ ] **Step 4: Write the `http` driver**

Create `internal/driver/http.go`:

```go
package driver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/baggage"

	"github.com/thetonymaster/mentat/internal/core"
)

// httpClientTimeout bounds the request so a stalled SUT cannot hang a run.
const httpClientTimeout = 30 * time.Second

const (
	tagRunID       = "test.run.id"
	tagScenario    = "test.scenario"
	headerScenario = "X-Scenario"
	headerBaggage  = "baggage"
)

type httpDriver struct{ hc *http.Client }

// NewHTTP returns the http driver adapter. It is a plain, un-instrumented,
// non-exporting HTTP client: it injects correlation baggage only (no traceparent),
// so the SUT's first server span roots the trace (spec §3).
func NewHTTP() core.Driver {
	return httpDriver{hc: &http.Client{Timeout: httpClientTimeout}}
}

func (d httpDriver) Run(ctx context.Context, spec core.RunSpec) (core.RunResult, error) {
	if spec.HTTP.URL == "" {
		return core.RunResult{}, fmt.Errorf("http: empty URL for target %q", spec.Target)
	}
	if spec.HTTP.Method == "" {
		return core.RunResult{}, fmt.Errorf("http: empty method for target %q", spec.Target)
	}
	scenario := scenarioFromArgs(spec.Command)

	req, err := http.NewRequestWithContext(ctx, spec.HTTP.Method, spec.HTTP.URL, bytes.NewReader([]byte(spec.Input)))
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: build request %s %s: %w", spec.HTTP.Method, spec.HTTP.URL, err)
	}
	for k, v := range spec.HTTP.Headers {
		req.Header.Set(k, v)
	}
	if scenario != "" {
		req.Header.Set(headerScenario, scenario)
	}
	bag, err := buildBaggage(spec.Tags[tagRunID], scenario)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: build baggage for run %q: %w", spec.RunID, err)
	}
	if bag != "" {
		req.Header.Set(headerBaggage, bag)
	}

	resp, err := d.hc.Do(req)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: %s %s for run %q: %w", spec.HTTP.Method, spec.HTTP.URL, spec.RunID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: read response body for run %q: %w", spec.RunID, err)
	}

	// A non-2xx response is data the comparators judge, not a driver error.
	out := core.Output{
		Status: resp.StatusCode,
		Body:   body,
		Answer: string(body),
	}
	return core.RunResult{RunID: spec.RunID, Output: out}, nil
}

// scenarioFromArgs extracts the value of the --scenario flag from the driver
// args (the http adapter consumes args directly, the way the shell adapter hands
// them to a subprocess). Absent --scenario yields "" — a valid empty scenario,
// not an error.
func scenarioFromArgs(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--scenario" {
			return args[i+1]
		}
	}
	return ""
}

// buildBaggage renders the W3C baggage header value carrying the correlation tags.
// Empty runID and empty scenario both yield no member; an all-empty result returns
// "" so the caller omits the header.
func buildBaggage(runID, scenario string) (string, error) {
	var members []baggage.Member
	if runID != "" {
		m, err := baggage.NewMember(tagRunID, runID)
		if err != nil {
			return "", fmt.Errorf("baggage member %s=%q: %w", tagRunID, runID, err)
		}
		members = append(members, m)
	}
	if scenario != "" {
		m, err := baggage.NewMember(tagScenario, scenario)
		if err != nil {
			return "", fmt.Errorf("baggage member %s=%q: %w", tagScenario, scenario, err)
		}
		members = append(members, m)
	}
	if len(members) == 0 {
		return "", nil
	}
	b, err := baggage.New(members...)
	if err != nil {
		return "", fmt.Errorf("build baggage: %w", err)
	}
	return b.String(), nil
}
```

- [ ] **Step 5: Run the tests and make sure they pass**

Run: `go test ./internal/driver/ -v`
Expected: PASS (new http tests + existing shell tests).

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -w internal/core/core.go internal/driver/http.go internal/driver/http_test.go
go vet ./internal/core/ ./internal/driver/
git add internal/core/core.go internal/driver/http.go internal/driver/http_test.go
git commit -m "feat(driver): http adapter with baggage correlation and non-2xx-as-data"
```

---

### Task 3: `sequence(service)` comparator generalization

Add a `Kind` selector to `SequenceExpectation`. `""`/`"tool"` keep today's `gen_ai.tool.name` path (backward compatible). `"service"` groups spans by `service.name`, takes each service's first-seen span in stable `Start` order, and matches the ordered subsequence + forbidden set. The forbidden/order matching logic is shared; only the actual-sequence extraction and the failure-reason noun differ.

**Files:**
- Modify: `internal/comparator/sequence.go`
- Test: `internal/comparator/sequence_test.go`

**Interfaces:**
- Consumes: `core.Evidence` (the `Trace` forest), `trace.Span.Attr("service.name")`, `trace.Span.Start`.
- Produces: `comparator.SequenceExpectation{ Kind, Order, Forbidden }`. `Kind` is `"" | "tool" | "service"`; any other value → error. `service` path: a span missing `service.name` → hard error (mirrors the missing-`tool.name` error). Failure reasons read `expected service %q not found...` / `forbidden service %q was called` for the service kind.

- [ ] **Step 1: Write the failing tests**

Add to `internal/comparator/sequence_test.go`. First, a helper that builds a service trace with explicit `Start` times (so the test does not depend on map/stable-sort accidents), plus the new table cases:

```go
// svcTrace builds a *trace.Trace of SERVER spans, one per name, each carrying a
// service.name attr and a strictly increasing Start so first-seen order is the
// call order. A name may repeat (same service emitting multiple spans).
func svcTrace(names ...string) *trace.Trace {
	tr := &trace.Trace{}
	base := time.Unix(0, 0)
	for i, n := range names {
		tr.Spans = append(tr.Spans, &trace.Span{
			Name:  "POST",
			Start: base.Add(time.Duration(i) * time.Millisecond),
			Attrs: map[string]string{"service.name": n},
		})
	}
	return tr
}

func TestSequenceServiceKind(t *testing.T) {
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      core.Expectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "service ordered-subsequence passes (extra services allowed)",
			ev:       core.Evidence{Trace: svcTrace("gateway", "auth", "inventory", "payment", "notify")},
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment"}},
			wantPass: true,
		},
		{
			name:     "service wrong order fails (payment before inventory)",
			ev:       core.Evidence{Trace: svcTrace("gateway", "auth", "payment", "inventory", "notify")},
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: false,
		},
		{
			name:     "service forbidden fails (legacy-pricing called)",
			ev:       core.Evidence{Trace: svcTrace("gateway", "auth", "legacy-pricing", "inventory")},
			exp:      SequenceExpectation{Kind: "service", Forbidden: []string{"legacy-pricing"}},
			wantPass: false,
		},
		{
			name:     "service deduplicates repeated service spans",
			ev:       core.Evidence{Trace: svcTrace("gateway", "gateway", "auth")},
			exp:      SequenceExpectation{Kind: "service", Order: []string{"gateway", "auth"}},
			wantPass: true,
		},
		{
			name:    "service span missing service.name returns error",
			ev:      core.Evidence{Trace: &trace.Trace{Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{}}}}},
			exp:     SequenceExpectation{Kind: "service", Order: []string{"auth"}},
			wantErr: true,
		},
		{
			name:    "unknown Kind returns error",
			ev:      core.Evidence{Trace: svcTrace("gateway", "auth")},
			exp:     SequenceExpectation{Kind: "endpoint", Order: []string{"auth"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewSequence().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v; reasons = %v", got.Pass, tt.wantPass, got.Reasons)
			}
		})
	}
}
```

Add `"time"` to the test file's import block (the existing `sequence_test.go` imports `context`, `testing`, `core`, `genai`, `trace`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run TestSequenceServiceKind -v`
Expected: FAIL — compile error `unknown field 'Kind' in struct literal of type SequenceExpectation`.

- [ ] **Step 3: Generalize the comparator**

Replace the contents of `internal/comparator/sequence.go` with:

```go
package comparator

import (
	"context"
	"fmt"
	"sort"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// serviceNameAttr is the OTel resource attribute identifying which service
// emitted a span. The trace store (Tempo and the fixture loader) merges resource
// attributes onto every span, so the comparator reads it like any span attr.
const serviceNameAttr = "service.name"

type SequenceExpectation struct {
	// Kind selects the identity strategy: "" or "tool" (default, gen_ai.tool.name)
	// or "service" (the service.name resource attribute).
	Kind      string
	Order     []string
	Forbidden []string
}

type sequence struct{}

func NewSequence() core.Comparator { return sequence{} }
func (sequence) Name() string      { return "sequence" }

func (sequence) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(SequenceExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("sequence: expectation must be SequenceExpectation, got %T", e)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("sequence: Evidence.Trace is nil")
	}

	var (
		actual []string
		noun   string
		err    error
	)
	switch exp.Kind {
	case "", "tool":
		noun = "tool"
		actual, err = toolSequence(ev.Trace)
	case "service":
		noun = "service"
		actual, err = serviceSequence(ev.Trace)
	default:
		return core.Verdict{}, fmt.Errorf("sequence: unknown Kind %q (want \"\", \"tool\", or \"service\")", exp.Kind)
	}
	if err != nil {
		return core.Verdict{}, err
	}

	v := core.Verdict{Pass: true}

	// forbidden
	forbidden := map[string]bool{}
	for _, f := range exp.Forbidden {
		forbidden[f] = true
	}
	for _, a := range actual {
		if forbidden[a] {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("forbidden %s %q was called", noun, a))
		}
	}

	// ordered subsequence: each Order item must appear, in order, in actual
	i := 0
	for _, want := range exp.Order {
		found := false
		for i < len(actual) {
			if actual[i] == want {
				found = true
				i++
				break
			}
			i++
		}
		if !found {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("expected %s %q not found in order; actual sequence = %v", noun, want, actual))
		}
	}
	return v, nil
}

// toolSequence returns the execute_tool names in start order (today's path).
func toolSequence(t *trace.Trace) ([]string, error) {
	var out []string
	for i, s := range t.ByOp(genai.OpExecuteTool) {
		name := s.Attr(genai.ToolName)
		if name == "" {
			return nil, fmt.Errorf("sequence: execute_tool span[%d] (%q) missing %s", i, s.Name, genai.ToolName)
		}
		out = append(out, name)
	}
	return out, nil
}

// serviceSequence returns the distinct services in first-seen order. Spans are
// stable-sorted by Start: live traces order by real start time; fixtures carry no
// timestamps, so the stable sort preserves the spans' array order, which the
// tracelab capture wrote in start-time order. A span missing service.name is a
// hard error, mirroring the missing-tool-name path.
func serviceSequence(t *trace.Trace) ([]string, error) {
	spans := make([]*trace.Span, len(t.Spans))
	copy(spans, t.Spans)
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })

	seen := map[string]bool{}
	var out []string
	for i, s := range spans {
		svc := s.Attr(serviceNameAttr)
		if svc == "" {
			return nil, fmt.Errorf("sequence: span[%d] (%q) missing %s", i, s.Name, serviceNameAttr)
		}
		if !seen[svc] {
			seen[svc] = true
			out = append(out, svc)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests and make sure they pass**

Run: `go test ./internal/comparator/ -v`
Expected: PASS — new `TestSequenceServiceKind` passes; all existing sequence/budgets/result/fixtures tests still pass (backward compat preserved by the `""`/`"tool"` default).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/comparator/sequence.go internal/comparator/sequence_test.go
go vet ./internal/comparator/
git add internal/comparator/sequence.go internal/comparator/sequence_test.go
git commit -m "feat(comparator): sequence Kind selector with portable service path"
```

---

### Task 4: L1 golden comparator tests

Run the real orderflow goldens (captured by plan 1) through `sequence(service)`, `result`, and `budgets`, hermetically via `store.LoadFixture`. This is the L1 deliverable from spec §9. Mirrors the existing `internal/comparator/fixtures_test.go` pattern (which already reads goldens from `../../testdata/traces/...`).

**Files:**
- Create: `internal/comparator/orderflow_fixtures_test.go`

**Interfaces:**
- Consumes: `store.LoadFixture` (parses a golden into a `*trace.Trace`), the goldens at `testdata/traces/orderflow/<scenario>.json`, and the comparators from Task 3 (`sequence`) plus existing `result`/`budgets`.
- Note: the result comparator reads `ev.Output`, which goldens do not carry — the test supplies the scenario's known `(status, body)` inline (the same convention `fixtures_test.go::TestFixtureResult` uses for answer strings). Status/body literals are the orderflow gateway's defined responses (`tracelab/orderflow/handlers.go::planFor`).

- [ ] **Step 1: Write the test (will fail to compile until the file exists — that is the red state)**

Create `internal/comparator/orderflow_fixtures_test.go`:

```go
package comparator

import (
	"context"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/store"
)

func loadOrderflowGolden(t *testing.T, scenario string) *core.Evidence {
	t.Helper()
	data, err := os.ReadFile("../../testdata/traces/orderflow/" + scenario + ".json")
	if err != nil {
		t.Fatalf("read golden %q: %v", scenario, err)
	}
	tr, err := store.LoadFixture(data)
	if err != nil {
		t.Fatalf("load golden %q: %v", scenario, err)
	}
	return &core.Evidence{Trace: tr}
}

// TestOrderflowSequenceService asserts the portable service-sequence comparator
// against the real captured goldens (spec §9 L1).
func TestOrderflowSequenceService(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		exp      SequenceExpectation
		wantPass bool
	}{
		{
			name:     "happy: services in expected order",
			scenario: "happy",
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: true,
		},
		{
			name:     "happy: legacy-pricing never called",
			scenario: "happy",
			exp:      SequenceExpectation{Kind: "service", Forbidden: []string{"legacy-pricing"}},
			wantPass: true,
		},
		{
			name:     "reorder: payment-before-inventory trips order",
			scenario: "reorder",
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: false,
		},
		{
			name:     "legacy_path: forbidden legacy-pricing trips",
			scenario: "legacy_path",
			exp:      SequenceExpectation{Kind: "service", Forbidden: []string{"legacy-pricing"}},
			wantPass: false,
		},
		{
			name:     "inventory_out: payment/notify skipped trips order",
			scenario: "inventory_out",
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := loadOrderflowGolden(t, tt.scenario)
			v, err := NewSequence().Compare(context.Background(), *ev, tt.exp)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

// TestOrderflowBudgetsError asserts the error-span budget against the goldens.
// payment_decline carries a payment.declined span with status "Error"; happy has
// none. (Latency is NOT testable here — goldens carry no timestamps; see the
// plan's reality-vs-model note. Latency is asserted at L3.)
func TestOrderflowBudgetsError(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		wantPass bool
	}{
		{"happy: zero error spans", "happy", true},
		{"payment_decline: one error span trips", "payment_decline", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := loadOrderflowGolden(t, tt.scenario)
			zero := 0
			v, err := NewBudgets().Compare(context.Background(), *ev, BudgetExpectation{MaxErrors: &zero})
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

// TestOrderflowResult asserts the result comparator against each scenario's
// defined gateway response (tracelab/orderflow/handlers.go::planFor).
func TestOrderflowResult(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		exp      ResultExpectation
		wantPass bool
	}{
		{
			name:     "happy: status 201",
			status:   201,
			body:     `{"status":"confirmed"}`,
			exp:      ResultExpectation{Matcher: "status", Want: "201"},
			wantPass: true,
		},
		{
			name:     "happy: body json-contains confirmed",
			status:   201,
			body:     `{"status":"confirmed"}`,
			exp:      ResultExpectation{Matcher: "json-subset", Want: `{"status":"confirmed"}`},
			wantPass: true,
		},
		{
			name:     "payment_decline: status 402 not 201",
			status:   402,
			body:     `{"status":"declined"}`,
			exp:      ResultExpectation{Matcher: "status", Want: "201"},
			wantPass: false,
		},
		{
			name:     "inventory_out: status 409 not 201",
			status:   409,
			body:     `{"status":"out_of_stock"}`,
			exp:      ResultExpectation{Matcher: "status", Want: "201"},
			wantPass: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Status: tt.status, Body: []byte(tt.body)}}
			v, err := NewResult().Compare(context.Background(), ev, tt.exp)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests and confirm green**

Run: `go test ./internal/comparator/ -run TestOrderflow -v`
Expected: PASS for all three new test functions.

If `TestOrderflowSequenceService` shows an unexpected order, STOP — re-read the relevant golden (`testdata/traces/orderflow/<scenario>.json`); the span array order IS the asserted call order (the goldens are timestamp-free). Do not edit goldens to make the test pass.

- [ ] **Step 3: Verify the comparator package still meets the coverage floor**

Run: `go test ./internal/comparator/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1`
Expected: total ≥ 80.0%.

- [ ] **Step 4: Commit**

```bash
gofmt -w internal/comparator/orderflow_fixtures_test.go
git add internal/comparator/orderflow_fixtures_test.go
git commit -m "test(comparator): L1 golden coverage for orderflow service path"
```

---

### Task 5: Engine wiring + Build registration

Register the `http` driver at the composition root and map `config.Target.HTTP` into `core.RunSpec.HTTP` so `engine.Drive` can run http targets. Test the full engine→driver→store path with `httptest` + a gomock store.

**Files:**
- Modify: `internal/engine/build.go`
- Modify: `internal/engine/engine.go`
- Test: `internal/engine/engine_test.go`

**Interfaces:**
- Consumes: `driver.NewHTTP()` (Task 2), `config.Target.HTTP` (Task 1), `core.RunSpec.HTTP` (Task 2).
- Produces: an engine that resolves adapter `"http"` to the http driver and threads the target's URL/method/headers into the `RunSpec`. No public signature change to `Build`/`Drive`.

- [ ] **Step 1: Write the failing integration test**

Add to `internal/engine/engine_test.go`. It builds an engine with an `http` target pointing at an `httptest` server and a gomock store that returns a trace for the injected run id; it asserts the response is mapped into `Evidence.Output` and that the SUT received the correlation headers.

```go
func TestDriveHTTPTarget(t *testing.T) {
	var gotScenario, gotBaggage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotScenario = r.Header.Get("X-Scenario")
		gotBaggage = r.Header.Get("baggage")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"confirmed"}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"checkout": {
				Adapter:        "http",
				MaxConcurrency: 8,
				HTTP:           config.HTTP{URL: srv.URL, Method: http.MethodPost},
			},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{"service.name": "gateway"}}}}

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "run-http"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()

	cor := correlate.New(func() string { return "run-http" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ev, err := eng.Drive(context.Background(), "checkout", []string{"--scenario", "happy"})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if ev.Output.Status != http.StatusCreated {
		t.Errorf("Status = %d, want 201", ev.Output.Status)
	}
	if ev.Output.Answer != `{"status":"confirmed"}` {
		t.Errorf("Answer = %q", ev.Output.Answer)
	}
	if gotScenario != "happy" {
		t.Errorf("SUT saw X-Scenario = %q, want happy", gotScenario)
	}
	if !strings.Contains(gotBaggage, "test.run.id=run-http") {
		t.Errorf("SUT saw baggage %q, missing test.run.id=run-http", gotBaggage)
	}
}
```

Add `"net/http"` and `"net/http/httptest"` to the `engine_test.go` import block (it already imports `context`, `errors`, `strings`, `testing`, `time`, `gomock`, `config`, `core`, `mocks`, `correlate`, `trace`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestDriveHTTPTarget -v`
Expected: FAIL — `Drive` returns `engine: no driver for adapter "http"` (Build does not register it yet), so the assertions never run.

- [ ] **Step 3: Register the http driver in Build**

In `internal/engine/build.go`, add the import and the registration line. Update the import block:

```go
import (
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/driver"
	"github.com/thetonymaster/mentat/internal/registry"
)
```

And add, alongside the existing `RegisterDriver("shell", ...)` call:

```go
	registry.RegisterDriver("shell", driver.NewShell())
	registry.RegisterDriver("http", driver.NewHTTP())
```

- [ ] **Step 4: Map the target's HTTP block into the RunSpec**

In `internal/engine/engine.go::Drive`, extend the `spec := core.RunSpec{...}` literal to carry the http config:

```go
	spec := core.RunSpec{
		Target:  target,
		Adapter: t.Adapter,
		Command: append(append([]string{}, t.Command...), args...),
		HTTP: core.HTTPSpec{
			URL:     t.HTTP.URL,
			Method:  t.HTTP.Method,
			Headers: t.HTTP.Headers,
		},
		Env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": e.cfg.OTLPEndpoint},
	}
```

- [ ] **Step 5: Run the tests and make sure they pass**

Run: `go test ./internal/engine/ -v`
Expected: PASS — `TestDriveHTTPTarget` and all existing engine tests.

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -w internal/engine/build.go internal/engine/engine.go internal/engine/engine_test.go
go vet ./internal/engine/
git add internal/engine/build.go internal/engine/engine.go internal/engine/engine_test.go
git commit -m "feat(engine): register http driver and thread http target config into RunSpec"
```

---

### Task 6: Service-path grammar + `checkout` target + L2 happy E2E

Add the four service-path godog steps, the `checkout` http target in `mentat.yaml`, the L2 happy feature, and the L2 e2e test. Per the established repo pattern, godog steps are behaviorally verified by e2e (there is no `steps` unit test for the existing grammar either); the L2 test is this task's teeth. **L2 requires the live stack (`make harness-up`).**

**Files:**
- Modify: `internal/steps/steps.go`
- Modify: `mentat.yaml`
- Create: `features/checkout.feature`
- Create: `e2e/orderflow_test.go`

**Interfaces:**
- Consumes: `comparator.SequenceExpectation{Kind:"service", ...}` (Task 3), `comparator.ResultExpectation{Matcher:"json-subset"|"status", ...}` (existing), the wired `http` engine (Task 5).
- Produces these Gherkin steps:
  - `Then the services are called in order:` (table) → `sequence{Kind:"service", Order}`
  - `Then the service "<x>" is never called` → `sequence{Kind:"service", Forbidden:[x]}`
  - `Then the response status is <code>` → `result{Matcher:"status", Want:code}` (already exists; reused)
  - `Then the response body json-contains:` (docstring) → `result{Matcher:"json-subset", Want:docstring}`

- [ ] **Step 1: Add the new step handlers and registrations**

In `internal/steps/steps.go`, register the three new steps inside `Initializer` (the `responseStatus` step already exists). Add to the `sc.Step(...)` block:

```go
		sc.Step(`^the services are called in order:$`, w.servicesInOrder)
		sc.Step(`^the service "([^"]+)" is never called$`, w.serviceNeverCalled)
		sc.Step(`^the response body json-contains:$`, w.responseBodyJSONContains)
```

Then add the handler methods (next to the existing `toolsInOrder` / `toolNeverCalled`):

```go
func (w *world) servicesInOrder(tbl *godog.Table) error {
	var order []string
	for i, row := range tbl.Rows {
		if len(row.Cells) == 0 {
			return fmt.Errorf("services-in-order: table row %d has no cells", i)
		}
		svc := strings.TrimSpace(row.Cells[0].Value)
		if svc == "" {
			return fmt.Errorf("services-in-order: table row %d has empty service name", i)
		}
		order = append(order, svc)
	}
	if len(order) == 0 {
		return fmt.Errorf("services-in-order: at least one service is required")
	}
	return w.check("sequence", comparator.SequenceExpectation{Kind: "service", Order: order})
}

func (w *world) serviceNeverCalled(name string) error {
	return w.check("sequence", comparator.SequenceExpectation{Kind: "service", Forbidden: []string{name}})
}

func (w *world) responseBodyJSONContains(doc *godog.DocString) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "json-subset", Want: doc.Content})
}
```

(`*godog.DocString` is `messages.PickleDocString`; its `.Content` field holds the docstring text. godog binds a trailing `*godog.DocString` parameter to the step's attached docstring automatically.)

- [ ] **Step 2: Add the `checkout` target to `mentat.yaml`**

Append to the `targets:` block in `mentat.yaml` (the gateway has no router, so `/orders` reaches the same handler the smoke script hits on `/`):

```yaml
  checkout:
    adapter: http
    max_concurrency: 8
    http:
      url: "http://localhost:8080/orders"
      method: POST
      headers:
        Content-Type: application/json
```

- [ ] **Step 3: Create the L2 happy feature**

Create `features/checkout.feature`:

```gherkin
Feature: Checkout service behaviour
  Scenario: confirms an order, services in order, within SLO
    Given the service target "checkout"
    When I run scenario "happy"
    Then the services are called in order:
      | auth      |
      | inventory |
      | payment   |
      | notify    |
    And the service "legacy-pricing" is never called
    And the response status is 201
    And no span has status "ERROR"
    And the response body json-contains:
      """
      {"status": "confirmed"}
      """
```

- [ ] **Step 4: Verify the framework builds and steps compile**

Run: `go build ./... && go vet ./internal/steps/`
Expected: clean. (The feature is exercised live in the next steps.)

- [ ] **Step 5: Write the L2 e2e test**

Create `e2e/orderflow_test.go`:

```go
//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestOrderflowHappyPasses drives the happy checkout scenario end-to-end over the
// http/baggage path and asserts mentat exits zero (every comparator passes).
// Requires: make harness-up (Tempo + Collector + orderflow containers running).
func TestOrderflowHappyPasses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/mentat", "run", "features/checkout.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("mentat run timed out:\n%s", out)
	}
	if err != nil {
		t.Fatalf("mentat run failed (want pass):\n%s", out)
	}
}
```

- [ ] **Step 6: Run the L2 e2e against the live stack**

```bash
make harness-up
# Give the orderflow containers a moment to come up, then:
go test -tags e2e ./e2e/ -run TestOrderflowHappyPasses -v
```

Expected: PASS — mentat exits zero; the run's spans correlate by `test.run.id` baggage and every comparator (service-sequence, forbidden, status, error-budget, json-subset) is green.

DOING: run the L2 test. EXPECT: PASS. IF NO: capture the full `mentat run` output. The most likely failures and their meaning:
- `no trace for run ... (0 spans seen)` → the http driver's baggage did not reach Tempo (check the gateway is on `:8080`, `make harness-up` finished, baggage header present). This is the baggage path — the core thing this phase proves; debug it, do not stub it.
- `sequence failed` on happy → the live service order differs from the expected subsequence; print the actual order and reconcile with `planFor("happy")`.

(Leave the stack up for Task 7, or `make harness-down` when done.)

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/steps/steps.go e2e/orderflow_test.go
go vet ./internal/steps/
git add internal/steps/steps.go mentat.yaml features/checkout.feature e2e/orderflow_test.go
git commit -m "feat(steps,e2e): service-path grammar + checkout target + L2 happy e2e"
```

---

### Task 7: L3 meta-tests (prove Mentat goes red)

The mandatory L3 meta layer: drive the five bad scenarios, each asserting a happy-path expectation that the scenario violates, and prove mentat exits non-zero with the expected reason. Each feature trips **exactly one** comparator for a clean reason assertion. Coverage across the five: service-order (×2), service-forbidden, result-status, budgets-latency (live). **L3 requires the live stack (`make harness-up`).**

> Per the reality-vs-model note (#3), `payment_decline` is asserted via `result(status)` (reads the HTTP response directly), not `budgets(error)` — `budgets(error)` is already covered hermetically at L1 (Task 4), and the live Tempo error-status string mapping is out of scope for this phase.

**Files:**
- Create: `features/meta/orderflow/reorder.feature`
- Create: `features/meta/orderflow/legacy_path.feature`
- Create: `features/meta/orderflow/inventory_out.feature`
- Create: `features/meta/orderflow/payment_decline.feature`
- Create: `features/meta/orderflow/slow.feature`
- Create: `e2e/orderflow_meta_test.go`

**Interfaces:**
- Consumes: the grammar + `checkout` target (Task 6).
- Produces: five features that each make `mentat run` exit non-zero; the e2e asserts the wrapped reason substring (`sequence failed` / `result status` / `run latency`).

- [ ] **Step 1: Create the five meta features**

`features/meta/orderflow/reorder.feature` — service ORDER trips (payment before inventory):

```gherkin
Feature: meta - service sequence must fail on reordered services
  Scenario: reorder trips the service-sequence comparator
    Given the service target "checkout"
    When I run scenario "reorder"
    Then the services are called in order:
      | auth      |
      | inventory |
      | payment   |
      | notify    |
```

`features/meta/orderflow/legacy_path.feature` — service FORBIDDEN trips (legacy-pricing is called):

```gherkin
Feature: meta - forbidden service must fail when called
  Scenario: legacy_path trips the forbidden-service check
    Given the service target "checkout"
    When I run scenario "legacy_path"
    Then the service "legacy-pricing" is never called
```

`features/meta/orderflow/inventory_out.feature` — service ORDER trips (payment/notify skipped):

```gherkin
Feature: meta - service sequence must fail on a short-circuited flow
  Scenario: inventory_out trips the service-sequence comparator
    Given the service target "checkout"
    When I run scenario "inventory_out"
    Then the services are called in order:
      | auth      |
      | inventory |
      | payment   |
      | notify    |
```

`features/meta/orderflow/payment_decline.feature` — result STATUS trips (402, not 201):

```gherkin
Feature: meta - result status must fail on a declined payment
  Scenario: payment_decline trips the result-status comparator
    Given the service target "checkout"
    When I run scenario "payment_decline"
    Then the response status is 201
```

`features/meta/orderflow/slow.feature` — budgets LATENCY trips (live; 900 ms injected > 500 ms budget):

```gherkin
Feature: meta - latency budget must fail on an over-SLO run
  Scenario: slow trips the latency budget
    Given the service target "checkout"
    When I run scenario "slow"
    Then total latency is under 500 ms
```

- [ ] **Step 2: Write the L3 e2e test**

Create `e2e/orderflow_meta_test.go`:

```go
//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestOrderflowBadScenariosAreCaught proves Mentat goes red over the http/baggage
// microservice path. Each feature drives a scenario that violates exactly one
// happy-path assertion, so the corresponding comparator must trip and mentat must
// exit non-zero. Requires: make harness-up.
func TestOrderflowBadScenariosAreCaught(t *testing.T) {
	cases := []struct {
		feature string
		reason  string // substring expected in combined output
	}{
		{"features/meta/orderflow/reorder.feature", "sequence failed"},
		{"features/meta/orderflow/legacy_path.feature", "sequence failed"},
		{"features/meta/orderflow/inventory_out.feature", "sequence failed"},
		{"features/meta/orderflow/payment_decline.feature", "result status"},
		{"features/meta/orderflow/slow.feature", "run latency"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.feature, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", "./cmd/mentat", "run", c.feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("expected FAILURE for %s, but run timed out:\n%s", c.feature, out)
			}
			if err == nil {
				t.Fatalf("expected FAILURE for %s, but mentat passed:\n%s", c.feature, out)
			}
			if !strings.Contains(string(out), c.reason) {
				t.Fatalf("expected reason %q in output for %s:\n%s", c.reason, c.feature, out)
			}
		})
	}
}
```

- [ ] **Step 3: Run the L3 meta-tests against the live stack**

```bash
make harness-up   # if not already up from Task 6
go test -tags e2e ./e2e/ -run TestOrderflowBadScenariosAreCaught -v
```

Expected: PASS — all five subtests confirm mentat exits non-zero with the expected reason.

DOING: run the L3 meta suite. EXPECT: 5/5 subtests pass (each bad scenario produces a non-zero exit + reason). IF NO on `slow` (`run latency`): the live envelope did not exceed 500 ms — print the actual latency from the output; the gateway's 900 ms sleep must be inside its server span (`tracelab/orderflow/handlers.go::gatewayHandler`). IF NO on a `sequence failed` case: print the actual service order from the output and reconcile with `planFor(<scenario>)` — do not relax the feature to make it pass; a meta-test that cannot go red is the one failure mode this layer exists to prevent.

- [ ] **Step 4: Full hermetic suite + coverage gate**

```bash
make harness-down
go test ./... -race
make cover
```

Expected: `go test ./...` green (hermetic; e2e tests are skipped without the `e2e` tag); every changed package (`config`, `core`, `driver`, `comparator`, `engine`, `steps`) ≥ 80%.

- [ ] **Step 5: Commit**

```bash
git add features/meta/orderflow/reorder.feature features/meta/orderflow/legacy_path.feature features/meta/orderflow/inventory_out.feature features/meta/orderflow/payment_decline.feature features/meta/orderflow/slow.feature e2e/orderflow_meta_test.go
git commit -m "test(e2e): L3 meta-tests prove Mentat goes red over the microservice path"
```

---

## Self-Review (performed against the spec)

**Spec coverage** (spec §2 in-scope items → task):
- `http` driver adapter (`internal/driver/http.go`) → **Task 2** ✓
- `sequence` comparator generalized with `Kind` (`tool` | `service`) → **Task 3** ✓
- godog grammar additions for the service path (§6) → **Task 6** ✓ (services-in-order, service-never-called, response-body-json-contains; response-status reused)
- `http` target config in `mentat.yaml` (§7) → **Task 1** (loader) + **Task 6** (the `checkout` target) ✓
- L1 goldens (§9) → **Task 4** ✓ (sequence(service)/result/budgets against the 6 goldens via `inmem`/`otlp-file` `LoadFixture`)
- L2 hermetic E2E (§9) → **Task 6** ✓ (`make harness-up` + `mentat run features/checkout.feature` green on `happy`)
- L3 meta-test (§9, mandatory) → **Task 7** ✓ (five bad scenarios; mentat exits non-zero with expected reasons)
- Correlation contract (§3): baggage-only, no `traceparent`, `X-Scenario` header, resolve by `test.run.id` → **Task 2** driver + reused `Tempo`/`correlate` ✓
- Coverage ≥ 80% per new package → gated in **Task 4** and **Task 7** ✓

**Out-of-scope honored:** no `shape` comparator, no CEL, no `grpc`/`mcp`/`tracetest`, no `traceparent` injection, no `semantic`/`@runs(N)`. ✓

**Reality-vs-model gaps surfaced** (not silently worked around): timestamp-free goldens drive the stable-sort design (#1); `slow` latency is L3-only (#2); live Tempo error-status mapping avoided at L3, covered at L1 (#3); gateway emits no CLIENT spans (#4). All four are documented at the top and referenced in the affected tasks.

**Type consistency check:**
- `config.HTTP{URL, Method string; Headers map[string]string}` (Task 1) ↔ `core.HTTPSpec{URL, Method string; Headers map[string]string}` (Task 2), mapped field-for-field in `engine.Drive` (Task 5). ✓
- `SequenceExpectation{Kind, Order, Forbidden}` defined in Task 3; constructed with `Kind:"service"` in steps (Task 6) and L1 tests (Task 4); existing constructions (`{Order: ...}` / `{Forbidden: ...}`) default `Kind=""` → tool path, unchanged. ✓
- `driver.NewHTTP() core.Driver` defined Task 2, registered Task 5 under `"http"`, resolved by `engine.Drive` via the driver registry. ✓
- `core.Output{Status, Body, Answer}` fields already exist; the http driver populates all three (Task 2); `result{Matcher:"status"}` reads `Status`, `{Matcher:"json-subset"}` reads `Body`. ✓
- godog step→expectation names match between `steps.go` (Task 6) and the comparators (Tasks 3/existing). ✓

**Placeholder scan:** no `TBD`/`TODO`/"add error handling"/"similar to Task N"; every code step shows complete code; every run step states the exact command and expected outcome. ✓
