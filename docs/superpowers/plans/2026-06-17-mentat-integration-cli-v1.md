# Mentat Integration & CLI v1 (Plan 2b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Plan 2a core into a runnable BDD tool — a Tempo HTTP `TraceStore`, `mentat.yaml` config, DI registries + composition root, the engine (drive + per-target concurrency), the godog step grammar, the `mentat run` CLI — and prove it with an L2 hermetic E2E and the L3 meta-test (red-on-bad).

**Architecture:** `mentat run` embeds godog. Each scenario gets a fresh `world`; its `When` step calls `engine.Drive` (inject tag → run SUT via the shell driver → resolve+merge trace from Tempo → `Evidence`), and each `Then` step invokes a Plan 2a comparator against the stashed `Evidence`, failing the step with the verdict reasons. The engine resolves all dependencies from registries via a single composition root.

**Tech Stack:** Go 1.23+, Plan 2a packages, `github.com/cucumber/godog`, `gopkg.in/yaml.v3`, `net/http` + `net/http/httptest` (Tempo client + its tests).

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat`.
- **Prerequisites:** Plan 1 (researchbot binary + fixtures) and Plan 2a (`internal/{trace,core,store,comparator,correlate,driver}`) complete.
- **Correlation tag:** `test.run.id`. Tempo TraceQL resolves it unscoped: `{ .test.run.id = "<id>" }`.
- **Concurrency defaults:** per-target `max_concurrency` defaults by adapter kind — `shell`/`mcp` → 1, `http`/`grpc` → 8 (spec §4.1).
- **No silent fallbacks:** a `Then` step whose comparator returns `Pass=false` fails the godog step with the verdict reasons; a comparator/engine error fails the step with the error (never a false pass).
- **Commits:** files added individually; no AI attribution.

## File Structure

```
internal/config/config.go           mentat.yaml schema + loader + per-adapter defaults
internal/config/config_test.go
internal/store/tempo.go             Tempo HTTP TraceStore (OTLP-JSON trace + search)
internal/store/tempo_test.go
internal/registry/registry.go       per-seam registries + built-in registration
internal/registry/registry_test.go
internal/engine/engine.go           Engine: Drive (inject->run->resolve) + per-target sem
internal/engine/build.go            composition root: engine.Build(cfg)
internal/engine/engine_test.go
internal/steps/steps.go             godog step grammar + world; the v1 step set
internal/steps/steps_test.go
cmd/mentat/main.go                  `mentat run` CLI (embeds godog)
features/research_agent.feature     L2 happy-path feature
features/meta/*.feature             L3 bad-scenario features (expect failure)
mentat.yaml                         sample config (targets -> researchbot)
e2e/e2e_test.go                     L2 hermetic E2E (build tag: e2e)
e2e/meta_test.go                    L3 meta-test (build tag: e2e)
```

---

### Task 1: `mentat.yaml` config (schema + loader + defaults)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{Tempo Endpoint; OTLPEndpoint string; Poll PollSpec; Targets map[string]Target}`,
  `Target{Adapter string; Command []string; MaxConcurrency int}`, `PollSpec{Interval,Timeout string; StableFor int}`;
  `func Load(data []byte) (Config, error)` applying per-adapter `MaxConcurrency` defaults.
  Target binding: a Gherkin `Given ... target "<name>"` looks up `Targets[name]`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import "testing"

func TestLoadAppliesPerAdapterConcurrencyDefaults(t *testing.T) {
	data := []byte(`
tempo: { endpoint: "http://localhost:3200" }
otlpEndpoint: "http://localhost:4318"
poll: { interval: "200ms", stableFor: 3, timeout: "30s" }
targets:
  research-agent:
    adapter: shell
    command: ["go", "run", "./tracelab/researchbot/cmd/researchbot"]
  checkout:
    adapter: http
    command: ["POST", "http://localhost:8080/orders"]
`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Targets["research-agent"].MaxConcurrency != 1 {
		t.Fatalf("shell default concurrency = %d, want 1", cfg.Targets["research-agent"].MaxConcurrency)
	}
	if cfg.Targets["checkout"].MaxConcurrency != 8 {
		t.Fatalf("http default concurrency = %d, want 8", cfg.Targets["checkout"].MaxConcurrency)
	}
	if cfg.OTLPEndpoint != "http://localhost:4318" {
		t.Fatalf("otlp endpoint = %q", cfg.OTLPEndpoint)
	}
}

func TestLoadRejectsUnknownAdapter(t *testing.T) {
	_, err := Load([]byte(`targets: { x: { adapter: telepathy } }`))
	if err == nil {
		t.Fatal("expected error for unknown adapter")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the implementation**

Create `internal/config/config.go`:
```go
package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Tempo        Endpoint          `yaml:"tempo"`
	OTLPEndpoint string            `yaml:"otlpEndpoint"`
	Poll         PollSpec          `yaml:"poll"`
	Targets      map[string]Target `yaml:"targets"`
}

type Endpoint struct {
	Endpoint string `yaml:"endpoint"`
}

type PollSpec struct {
	Interval  string `yaml:"interval"`
	Timeout   string `yaml:"timeout"`
	StableFor int    `yaml:"stableFor"`
}

type Target struct {
	Adapter        string   `yaml:"adapter"`
	Command        []string `yaml:"command"`
	MaxConcurrency int      `yaml:"max_concurrency"`
}

var defaultConcurrency = map[string]int{"shell": 1, "mcp": 1, "http": 8, "grpc": 8}

func Load(data []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	for name, t := range c.Targets {
		def, ok := defaultConcurrency[t.Adapter]
		if !ok {
			return Config{}, fmt.Errorf("target %q: unknown adapter %q", name, t.Adapter)
		}
		if t.MaxConcurrency == 0 {
			t.MaxConcurrency = def
			c.Targets[name] = t
		}
	}
	return c, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): mentat.yaml schema with per-adapter concurrency defaults"
```

---

### Task 2: Tempo HTTP TraceStore

**Files:**
- Create: `internal/store/tempo.go`
- Test: `internal/store/tempo_test.go`

**Interfaces:**
- Consumes: `core.TraceStore`/`TraceQuery`/`TraceRef`/`StoreCaps` (2a), `trace.Trace`/`Span` (2a).
- Produces: `func NewTempo(endpoint string, hc *http.Client) *Tempo` implementing `core.TraceStore`.
  `GetByID` GETs `/api/traces/{id}` (OTLP JSON) and parses `resourceSpans` into a `trace.Trace`
  (resource attrs merged onto each span); `Query` GETs `/api/search?q={ .<tag> = "<v>" }` and returns
  `TraceRef`s; `Caps().StructuralQuery == true`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/tempo_test.go`:
```go
package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
)

const otlpTrace = `{
  "resourceSpans": [{
    "resource": { "attributes": [
      { "key": "test.run.id", "value": { "stringValue": "abc123" } }
    ]},
    "scopeSpans": [{ "spans": [
      {
        "traceId": "aa", "spanId": "01", "name": "invoke_agent researchbot",
        "startTimeUnixNano": "1000", "endTimeUnixNano": "4000",
        "attributes": [
          { "key": "gen_ai.operation.name", "value": { "stringValue": "invoke_agent" } },
          { "key": "gen_ai.usage.input_tokens", "value": { "intValue": "1200" } }
        ]
      },
      {
        "traceId": "aa", "spanId": "02", "parentSpanId": "01", "name": "execute_tool search",
        "startTimeUnixNano": "2000", "endTimeUnixNano": "2500",
        "attributes": [
          { "key": "gen_ai.operation.name", "value": { "stringValue": "execute_tool" } },
          { "key": "gen_ai.tool.name", "value": { "stringValue": "search" } }
        ]
      }
    ]}]
  }]
}`

func TestTempoGetByIDParsesForestAndMergesResourceAttrs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(otlpTrace))
	}))
	defer srv.Close()

	tr, err := NewTempo(srv.URL, srv.Client()).GetByID(context.Background(), "aa")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(tr.Spans) != 2 || len(tr.Roots) != 1 {
		t.Fatalf("forest wrong: spans=%d roots=%d", len(tr.Spans), len(tr.Roots))
	}
	if tr.Roots[0].Attr("test.run.id") != "abc123" {
		t.Fatalf("resource attr not merged onto span: %v", tr.Roots[0].Attrs)
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if len(tools) != 1 || tools[0].Attr(genai.ToolName) != "search" {
		t.Fatalf("tool span wrong: %v", tools)
	}
	if n, _ := tr.Roots[0].AttrInt(genai.InTokens); n != 1200 {
		t.Fatalf("input tokens = %d", n)
	}
}

func TestTempoQueryBuildsTraceQLAndReturnsRefs(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Write([]byte(`{"traces":[{"traceID":"aa"},{"traceID":"bb"}]}`))
	}))
	defer srv.Close()

	refs, err := NewTempo(srv.URL, srv.Client()).Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "abc123"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(refs) != 2 || refs[0].TraceID != "aa" {
		t.Fatalf("refs = %v", refs)
	}
	if !strings.Contains(gotQuery, `.test.run.id = "abc123"`) {
		t.Fatalf("traceql = %q", gotQuery)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestTempo -v`
Expected: FAIL — `undefined: NewTempo`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/tempo.go`:
```go
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

type Tempo struct {
	endpoint string
	hc       *http.Client
}

func NewTempo(endpoint string, hc *http.Client) *Tempo {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Tempo{endpoint: strings.TrimRight(endpoint, "/"), hc: hc}
}

func (t *Tempo) Caps() core.StoreCaps { return core.StoreCaps{StructuralQuery: true} }

// --- OTLP JSON shapes (minimal subset) ---

type otlpValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"` // proto JSON encodes int64 as string
	DoubleValue *float64 `json:"doubleValue"`
	BoolValue   *bool    `json:"boolValue"`
}

type otlpKV struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpSpan struct {
	TraceID           string   `json:"traceId"`
	SpanID            string   `json:"spanId"`
	ParentSpanID      string   `json:"parentSpanId"`
	Name              string   `json:"name"`
	StartTimeUnixNano string   `json:"startTimeUnixNano"`
	EndTimeUnixNano   string   `json:"endTimeUnixNano"`
	Attributes        []otlpKV `json:"attributes"`
	Status            struct {
		Code string `json:"code"`
	} `json:"status"`
}

type otlpTrace struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []otlpKV `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []otlpSpan `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func valStr(v otlpValue) string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64)
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

func nanos(s string) time.Time {
	n, _ := strconv.ParseInt(s, 10, 64)
	return time.Unix(0, n)
}

func (t *Tempo) GetByID(ctx context.Context, id string) (*trace.Trace, error) {
	body, err := t.get(ctx, t.endpoint+"/api/traces/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	var ot otlpTrace
	if err := json.Unmarshal(body, &ot); err != nil {
		return nil, fmt.Errorf("tempo: parse trace %s: %w", id, err)
	}
	tr := &trace.Trace{}
	byID := map[string]*trace.Span{}
	for _, rs := range ot.ResourceSpans {
		resAttrs := map[string]string{}
		for _, kv := range rs.Resource.Attributes {
			resAttrs[kv.Key] = valStr(kv.Value)
		}
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				attrs := map[string]string{}
				for k, v := range resAttrs { // merge resource attrs onto span
					attrs[k] = v
				}
				for _, kv := range s.Attributes {
					attrs[kv.Key] = valStr(kv.Value)
				}
				sp := &trace.Span{
					ID:       s.SpanID,
					ParentID: s.ParentSpanID,
					Name:     s.Name,
					Start:    nanos(s.StartTimeUnixNano),
					End:      nanos(s.EndTimeUnixNano),
					Status:   s.Status.Code,
					Attrs:    attrs,
				}
				tr.Spans = append(tr.Spans, sp)
				byID[sp.ID] = sp
			}
		}
	}
	for _, sp := range tr.Spans {
		if sp.ParentID == "" || byID[sp.ParentID] == nil {
			tr.Roots = append(tr.Roots, sp)
		}
	}
	return tr, nil
}

func (t *Tempo) Query(ctx context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	traceql := fmt.Sprintf(`{ .%s = "%s" }`, q.Tag, q.Value)
	u := t.endpoint + "/api/search?q=" + url.QueryEscape(traceql)
	body, err := t.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var res struct {
		Traces []struct {
			TraceID string `json:"traceID"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("tempo: parse search: %w", err)
	}
	refs := make([]core.TraceRef, 0, len(res.Traces))
	for _, tr := range res.Traces {
		refs = append(refs, core.TraceRef{TraceID: tr.TraceID})
	}
	return refs, nil
}

func (t *Tempo) get(ctx context.Context, u string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tempo: GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tempo: GET %s: status %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run TestTempo -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/store/tempo.go internal/store/tempo_test.go
git commit -m "feat(store): Tempo HTTP TraceStore (OTLP-JSON parse + TraceQL search)"
```

---

### Task 3: DI registries

**Files:**
- Create: `internal/registry/registry.go`
- Test: `internal/registry/registry_test.go`

**Interfaces:**
- Consumes: `core` interfaces (2a).
- Produces: `RegisterComparator(name, core.Comparator)`, `Comparator(name) (core.Comparator, bool)`;
  `RegisterDriver(scheme string, core.Driver)`, `Driver(scheme) (core.Driver, bool)`;
  plus `Comparators()` listing. (Stores/correlators are constructed directly by the composition
  root in v1; the registry covers the by-name seams the steps resolve.)

- [ ] **Step 1: Write the failing test**

Create `internal/registry/registry_test.go`:
```go
package registry

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

type fakeCmp struct{}

func (fakeCmp) Name() string { return "fake" }
func (fakeCmp) Compare(context.Context, core.Evidence, core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

func TestRegisterAndResolveComparator(t *testing.T) {
	RegisterComparator("fake", fakeCmp{})
	c, ok := Comparator("fake")
	if !ok || c.Name() != "fake" {
		t.Fatalf("resolve failed: %v %v", c, ok)
	}
	if _, ok := Comparator("missing"); ok {
		t.Fatal("missing comparator should not resolve")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/registry/ -v`
Expected: FAIL — `undefined: RegisterComparator`.

- [ ] **Step 3: Write the implementation**

Create `internal/registry/registry.go`:
```go
package registry

import "github.com/thetonymaster/mentat/internal/core"

var (
	comparators = map[string]core.Comparator{}
	drivers     = map[string]core.Driver{}
)

func RegisterComparator(name string, c core.Comparator) { comparators[name] = c }
func Comparator(name string) (core.Comparator, bool)    { c, ok := comparators[name]; return c, ok }

func RegisterDriver(scheme string, d core.Driver) { drivers[scheme] = d }
func Driver(scheme string) (core.Driver, bool)    { d, ok := drivers[scheme]; return d, ok }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/registry/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/registry/registry_test.go
git commit -m "feat(registry): per-seam comparator/driver registries"
```

---

### Task 4: Engine (Drive + per-target concurrency) + composition root

**Files:**
- Create: `internal/engine/engine.go`
- Create: `internal/engine/build.go`
- Test: `internal/engine/engine_test.go`

**Interfaces:**
- Consumes: `config.Config` (Task 1), `core` (2a), `registry` (Task 3), `correlate` (2a), `store` (Task 2).
- Produces: `type Engine struct{...}`;
  `func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error)`;
  `func (e *Engine) Comparator(name string) (core.Comparator, bool)`;
  `func Build(cfg config.Config, st core.TraceStore, cor core.Correlator) (*Engine, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/engine/engine_test.go`:
```go
package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/store"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestDriveProducesEvidenceFromFakeStore(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	// A store that returns a fixed one-span trace for the injected run id.
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	st := store.NewInMemStore(map[string]*trace.Trace{}) // overridden below via fixedStore
	_ = st
	fixed := &fixedStore{tr: tr}
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	eng, err := Build(cfg, fixed, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ev, err := eng.Drive(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if ev.RunID != "run-1" || ev.Output.Answer != "hi" || len(ev.Trace.Spans) != 1 {
		t.Fatalf("evidence wrong: %+v", ev)
	}
}

type fixedStore struct{ tr *trace.Trace }

func (s *fixedStore) GetByID(context.Context, string) (*trace.Trace, error) { return s.tr, nil }
func (s *fixedStore) Query(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	return []core.TraceRef{{TraceID: q.Value}}, nil
}
func (s *fixedStore) Caps() core.StoreCaps { return core.StoreCaps{} }
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/engine/ -v`
Expected: FAIL — `undefined: Build`.

- [ ] **Step 3: Write the engine**

Create `internal/engine/engine.go`:
```go
package engine

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

type Engine struct {
	cfg  config.Config
	cor  core.Correlator
	st   core.TraceStore
	sems map[string]chan struct{} // per-target concurrency gate
}

func (e *Engine) Comparator(name string) (core.Comparator, bool) { return registry.Comparator(name) }

// Drive injects the run tag, runs the SUT via its adapter, then resolves+merges
// the run's trace. The per-target semaphore enforces max_concurrency.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	t, ok := e.cfg.Targets[target]
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: unknown target %q", target)
	}
	drv, ok := registry.Driver(t.Adapter)
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: no driver for adapter %q", t.Adapter)
	}

	spec := core.RunSpec{
		Target:  target,
		Adapter: t.Adapter,
		Command: append(append([]string{}, t.Command...), args...),
		Env:     map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": e.cfg.OTLPEndpoint},
	}
	runID := e.cor.Inject(ctx, &spec)

	sem := e.sems[target]
	sem <- struct{}{}
	defer func() { <-sem }()

	res, err := drv.Run(ctx, spec)
	if err != nil {
		return core.Evidence{}, fmt.Errorf("engine: drive %q: %w", target, err)
	}
	tr, err := e.cor.Resolve(ctx, e.st, runID)
	if err != nil {
		return core.Evidence{}, err
	}
	return core.Evidence{RunID: runID, Trace: tr, Output: res.Output}, nil
}
```

- [ ] **Step 4: Write the composition root**

Create `internal/engine/build.go`:
```go
package engine

import (
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/driver"
	"github.com/thetonymaster/mentat/internal/registry"
)

// Build is the single composition root: register built-ins, then wire the engine.
func Build(cfg config.Config, st core.TraceStore, cor core.Correlator) (*Engine, error) {
	registry.RegisterDriver("shell", driver.NewShell())
	registry.RegisterComparator("sequence", comparator.NewSequence())
	registry.RegisterComparator("budgets", comparator.NewBudgets())
	registry.RegisterComparator("result", comparator.NewResult())

	sems := map[string]chan struct{}{}
	for name, t := range cfg.Targets {
		n := t.MaxConcurrency
		if n < 1 {
			n = 1
		}
		sems[name] = make(chan struct{}, n)
	}
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems}, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/engine/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/engine.go internal/engine/build.go internal/engine/engine_test.go
git commit -m "feat(engine): Drive lifecycle + per-target concurrency + composition root"
```

---

### Task 5: godog step grammar (the v1 step set)

**Files:**
- Create: `internal/steps/steps.go`
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `engine.Engine` (Task 4), comparators' Expectation types (2a), godog.
- Produces: `func Initializer(eng *engine.Engine) func(*godog.ScenarioContext)`.
- **v1 step grammar (pinned):**
  - `^the (?:agent|service) target "([^"]+)"$`
  - `^I run scenario "([^"]+)"$` (drives `--scenario <name>`)
  - `^I run the agent with prompt "([^"]*)"$` (drives `--prompt <p>`)
  - `^the agent calls tools in order:$` (DataTable, one tool per row) → sequence Order
  - `^the tool "([^"]+)" is never called$` → sequence Forbidden
  - `^total tokens are under (\d+)$` → budgets MaxTokens
  - `^total cost is under ([0-9.]+) USD$` → budgets MaxCostUSD
  - `^total latency is under (\d+) ms$` → budgets MaxLatency
  - `^no span has status "ERROR"$` → budgets MaxErrors=0
  - `^the result contains "([^"]*)"$` → result contains
  - `^the result equals "([^"]*)"$` → result exact
  - `^the response status is (\d+)$` → result status

- [ ] **Step 1: Add godog**

Run: `go get github.com/cucumber/godog@latest`

- [ ] **Step 2: Write the failing test**

Create `internal/steps/steps_test.go`:
```go
package steps

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// happyTrace: tools search->summarize, 1800 tokens.
func happyTrace() *trace.Trace {
	mk := func(op, tool string) *trace.Span {
		return &trace.Span{Name: op + " " + tool, Attrs: map[string]string{genai.Op: op, genai.ToolName: tool}}
	}
	root := &trace.Span{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "1200", genai.OutTokens: "600"}}
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: []*trace.Span{root, mk(genai.OpExecuteTool, "search"), mk(genai.OpExecuteTool, "summarize")}}
}

func TestFeatureExercisesGrammarAgainstFakeEngine(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, _ := engine.Build(cfg, &stubStore{tr: happyTrace()}, cor)

	feature := `Feature: grammar
  Scenario: happy
    Given the agent target "bot"
    When I run scenario "happy"
    Then the agent calls tools in order:
      | search    |
      | summarize |
    And the tool "delete_record" is never called
    And total tokens are under 5000
    And the result contains "hi"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:   "pretty",
			Output:   &out,
			FeatureContents: []godog.Feature{{Name: "grammar", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}
}

type stubStore struct{ tr *trace.Trace }

func (s *stubStore) GetByID(context.Context, string) (*trace.Trace, error) { return s.tr, nil }
func (s *stubStore) Query(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	return []core.TraceRef{{TraceID: q.Value}}, nil
}
func (s *stubStore) Caps() core.StoreCaps { return core.StoreCaps{} }
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/steps/ -v`
Expected: FAIL — `undefined: Initializer`.

- [ ] **Step 4: Write the step grammar**

Create `internal/steps/steps.go`:
```go
package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
)

type world struct {
	eng    *engine.Engine
	target string
	ev     core.Evidence
}

// Initializer binds the v1 grammar; a fresh world is created per scenario.
func Initializer(eng *engine.Engine) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &world{eng: eng}

		sc.Step(`^the (?:agent|service) target "([^"]+)"$`, w.target_)
		sc.Step(`^I run scenario "([^"]+)"$`, w.runScenario)
		sc.Step(`^I run the agent with prompt "([^"]*)"$`, w.runPrompt)
		sc.Step(`^the agent calls tools in order:$`, w.toolsInOrder)
		sc.Step(`^the tool "([^"]+)" is never called$`, w.toolNeverCalled)
		sc.Step(`^total tokens are under (\d+)$`, w.tokensUnder)
		sc.Step(`^total cost is under ([0-9.]+) USD$`, w.costUnder)
		sc.Step(`^total latency is under (\d+) ms$`, w.latencyUnder)
		sc.Step(`^no span has status "ERROR"$`, w.noErrorSpans)
		sc.Step(`^the result contains "([^"]*)"$`, w.resultContains)
		sc.Step(`^the result equals "([^"]*)"$`, w.resultEquals)
		sc.Step(`^the response status is (\d+)$`, w.responseStatus)
	}
}

func (w *world) target_(name string) error { w.target = name; return nil }

func (w *world) runScenario(name string) error { return w.drive([]string{"--scenario", name}) }
func (w *world) runPrompt(p string) error      { return w.drive([]string{"--prompt", p}) }

func (w *world) drive(args []string) error {
	if w.target == "" {
		return fmt.Errorf("no target set; use a Given ... target step first")
	}
	ev, err := w.eng.Drive(context.Background(), w.target, args)
	if err != nil {
		return err
	}
	w.ev = ev
	return nil
}

func (w *world) check(name string, exp core.Expectation) error {
	c, ok := w.eng.Comparator(name)
	if !ok {
		return fmt.Errorf("no comparator %q", name)
	}
	v, err := c.Compare(context.Background(), w.ev, exp)
	if err != nil {
		return err
	}
	if !v.Pass {
		return fmt.Errorf("%s failed: %s", name, strings.Join(v.Reasons, "; "))
	}
	return nil
}

func (w *world) toolsInOrder(tbl *godog.Table) error {
	var order []string
	for _, row := range tbl.Rows {
		order = append(order, strings.TrimSpace(row.Cells[0].Value))
	}
	return w.check("sequence", comparator.SequenceExpectation{Order: order})
}

func (w *world) toolNeverCalled(name string) error {
	return w.check("sequence", comparator.SequenceExpectation{Forbidden: []string{name}})
}

func (w *world) tokensUnder(n int) error {
	return w.check("budgets", comparator.BudgetExpectation{MaxTokens: &n})
}

func (w *world) costUnder(c float64) error {
	return w.check("budgets", comparator.BudgetExpectation{MaxCostUSD: &c})
}

func (w *world) latencyUnder(ms int) error {
	d := time.Duration(ms) * time.Millisecond
	return w.check("budgets", comparator.BudgetExpectation{MaxLatency: &d})
}

func (w *world) noErrorSpans() error {
	zero := 0
	return w.check("budgets", comparator.BudgetExpectation{MaxErrors: &zero})
}

func (w *world) resultContains(s string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "contains", Want: s})
}

func (w *world) resultEquals(s string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "exact", Want: s})
}

func (w *world) responseStatus(code int) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "status", Want: fmt.Sprintf("%d", code)})
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/steps/ -v`
Expected: PASS — the feature passes against the fake engine (the shell driver execs
`sh -c "echo hi"`; the stub store returns `happyTrace()`).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): v1 godog step grammar mapping to comparators"
```

---

### Task 6: `mentat run` CLI

**Files:**
- Create: `cmd/mentat/main.go`
- Create: `mentat.yaml`

**Interfaces:**
- Consumes: `config` (Task 1), `store.NewTempo` (Task 2), `correlate.New` (2a), `engine.Build` (Task 4), `steps.Initializer` (Task 5), godog.
- Produces: the `mentat` binary: `mentat run [paths...] --config mentat.yaml --concurrency N --tags X --junit FILE --fail-fast`.

- [ ] **Step 1: Write the sample config**

Create `mentat.yaml`:
```yaml
tempo: { endpoint: "http://localhost:3200" }
otlpEndpoint: "http://localhost:4318"
poll: { interval: "200ms", stableFor: 3, timeout: "30s" }
targets:
  research-agent:
    adapter: shell
    command: ["go", "run", "./tracelab/researchbot/cmd/researchbot"]
```

- [ ] **Step 2: Write the CLI**

Create `cmd/mentat/main.go`:
```go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cucumber/godog"
	"github.com/google/uuid"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/steps"
	"github.com/thetonymaster/mentat/internal/store"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: mentat run [paths...] [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "mentat.yaml", "config file")
	concurrency := fs.Int("concurrency", 1, "scenario scheduler width")
	tags := fs.String("tags", "", "godog tag expression")
	junit := fs.String("junit", "", "write JUnit XML to this file")
	failFast := fs.Bool("fail-fast", false, "stop on first failure")
	_ = fs.Parse(os.Args[2:])
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"features"}
	}

	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}
	cfg, err := config.Load(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}

	pc := correlate.PollConfig{
		Interval:  mustDur(cfg.Poll.Interval, 200*time.Millisecond),
		Timeout:   mustDur(cfg.Poll.Timeout, 30*time.Second),
		StableFor: orDefault(cfg.Poll.StableFor, 3),
	}
	cor := correlate.New(func() string { return uuid.NewString() }, pc)
	st := store.NewTempo(cfg.Tempo.Endpoint, nil)
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}

	opts := godog.Options{
		Format:      "pretty",
		Paths:       paths,
		Tags:        *tags,
		Concurrency: *concurrency,
		Output:      os.Stdout,
		StopOnFailure: *failFast,
	}
	if *junit != "" {
		opts.Format = "junit"
		f, _ := os.Create(*junit)
		defer f.Close()
		opts.Output = f
	}

	suite := godog.TestSuite{ScenarioInitializer: steps.Initializer(eng), Options: &opts}
	os.Exit(suite.Run())
}

func mustDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

func orDefault(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
```

- [ ] **Step 3: Build the binary**

Run: `go get github.com/google/uuid@latest && go build ./cmd/mentat`
Expected: builds `mentat`.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum cmd/mentat/main.go mentat.yaml
git commit -m "feat(cmd): mentat run CLI embedding godog"
```

---

### Task 7: L2 hermetic E2E

**Files:**
- Create: `features/research_agent.feature`
- Create: `e2e/e2e_test.go`

**Interfaces:**
- Consumes: the `mentat` binary (Task 6), the harness stack (Plan 1 `make harness-up`), researchbot.

- [ ] **Step 1: Write the happy-path feature**

Create `features/research_agent.feature`:
```gherkin
Feature: Research agent behaviour
  Scenario: summarizes Q3 revenue within budget
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then the agent calls tools in order:
      | search    |
      | fetch_doc |
      | summarize |
    And the tool "delete_record" is never called
    And total tokens are under 5000
    And the result contains "Q3 revenue"
```

- [ ] **Step 2: Write the E2E test (build-tagged)**

Create `e2e/e2e_test.go`:
```go
//go:build e2e

package e2e

import (
	"os/exec"
	"testing"
)

func TestHappyScenarioPasses(t *testing.T) {
	// Requires: make harness-up (Tempo + Collector running).
	cmd := exec.Command("go", "run", "./cmd/mentat", "run", "features/research_agent.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mentat run failed (want pass):\n%s", out)
	}
}
```

- [ ] **Step 3: Run the E2E (manual / CI, Docker required)**

Run:
```bash
make harness-up
sleep 5
go test -tags e2e ./e2e/ -run TestHappyScenario -v
make harness-down
```
Expected: PASS — the happy scenario goes green through the full pipeline.

- [ ] **Step 4: Commit**

```bash
git add features/research_agent.feature e2e/e2e_test.go
git commit -m "test(e2e): L2 hermetic happy-path through full pipeline"
```

---

### Task 8: L3 meta-test (red-on-bad)

**Files:**
- Create: `features/meta/wrong_order.feature`, `features/meta/over_budget.feature`, `features/meta/forbidden.feature`, `features/meta/bad_answer.feature`
- Create: `e2e/meta_test.go`

**Interfaces:**
- Consumes: `mentat` binary + harness. Each meta feature drives a **bad** researchbot scenario but
  asserts **good** behaviour, so a correct Mentat must FAIL the run with a specific reason.

- [ ] **Step 1: Write the meta features**

Create `features/meta/wrong_order.feature`:
```gherkin
Feature: meta - sequence must fail on wrong order
  Scenario: wrong_order trips the sequence comparator
    Given the agent target "research-agent"
    When I run scenario "wrong_order"
    Then the agent calls tools in order:
      | search    |
      | summarize |
```

Create `features/meta/over_budget.feature`:
```gherkin
Feature: meta - budgets must fail when over
  Scenario: over_budget trips the budgets comparator
    Given the agent target "research-agent"
    When I run scenario "over_budget"
    Then total tokens are under 5000
```

Create `features/meta/forbidden.feature`:
```gherkin
Feature: meta - forbidden tool must fail
  Scenario: extra_tool calls a forbidden tool
    Given the agent target "research-agent"
    When I run scenario "extra_tool"
    Then the tool "delete_record" is never called
```

Create `features/meta/bad_answer.feature`:
```gherkin
Feature: meta - result must fail on bad answer
  Scenario: bad_answer trips the result comparator
    Given the agent target "research-agent"
    When I run scenario "bad_answer"
    Then the result contains "Q3 revenue"
```

- [ ] **Step 2: Write the meta-test (build-tagged)**

Create `e2e/meta_test.go`:
```go
//go:build e2e

package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// Each bad scenario must make mentat exit non-zero, with a recognizable reason.
func TestBadScenariosAreCaught(t *testing.T) {
	cases := []struct {
		feature string
		reason  string // substring expected in output
	}{
		{"features/meta/wrong_order.feature", "sequence failed"},
		{"features/meta/over_budget.feature", "tokens"},
		{"features/meta/forbidden.feature", "forbidden tool"},
		{"features/meta/bad_answer.feature", "result contains"},
	}
	for _, c := range cases {
		t.Run(c.feature, func(t *testing.T) {
			cmd := exec.Command("go", "run", "./cmd/mentat", "run", c.feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
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

- [ ] **Step 3: Run the meta-test (manual / CI, Docker required)**

Run:
```bash
make harness-up
sleep 5
go test -tags e2e ./e2e/ -run TestBadScenarios -v
make harness-down
```
Expected: PASS — every bad scenario is caught (mentat exits non-zero with the expected reason). This is the proof that Mentat goes red on bad behaviour.

- [ ] **Step 4: Commit**

```bash
git add features/meta e2e/meta_test.go
git commit -m "test(e2e): L3 meta-test proving red-on-bad for all comparators"
```

---

## Done criteria for Plan 2b

- `go test ./internal/...` passes (config, store incl. Tempo, registry, engine, steps).
- `go build ./cmd/mentat` builds.
- With `make harness-up`: `go test -tags e2e ./e2e/` passes — the happy scenario is green (L2) and every bad scenario is caught with its reason (L3).
- Mentat now runs Gherkin behaviour specs against researchbot end-to-end and is proven to fail correctly.

**Plan 2c (next, optional for v1 polish):** `mentatctl` manual CLI — `agent run/trace/tools/replay/diff` + `--target/--last/--save/--json` conveniences (spec §15). These are additive developer ergonomics over the same engine/driver/store/correlate libraries; the BDD product path (`mentat run`) does not depend on them.
