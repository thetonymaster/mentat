# tracelab Harness v1 (researchbot + deploy) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `researchbot` — a deterministic, OTel-instrumented agent SUT that emits known `gen_ai.*` traces on command — plus the local Tempo+Collector stack and golden-trace capture, so Mentat has a faithful target to develop against.

**Architecture:** A Go program replays a data-driven "scenario plan" (YAML) into an OpenTelemetry span tree (`invoke_agent` root + `chat`/`execute_tool` children), exports it via OTLP, and prints only its final answer to stdout. Correlation is honored via standard OTel resource attributes (`OTEL_RESOURCE_ATTRIBUTES`). A capture mode snapshots scenarios to normalized JSON fixtures for downstream unit tests.

**Tech Stack:** Go 1.23+, OpenTelemetry Go SDK (`go.opentelemetry.io/otel`, `sdk/trace`, `sdk/resource`, `exporters/otlp/otlptrace/otlptracehttp`, `sdk/trace/tracetest`), `gopkg.in/yaml.v3`, Docker Compose (Grafana Tempo + OpenTelemetry Collector), Make.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat` (single module at repo root).
- **Go version floor:** `go 1.23`.
- **Pinned GenAI attribute keys** (used identically by emitter, tests, and later by Mentat's comparators):
  `gen_ai.operation.name`, `gen_ai.agent.name`, `gen_ai.request.model`,
  `gen_ai.response.finish_reasons`, `gen_ai.tool.name`, `gen_ai.tool.call.arguments`,
  `gen_ai.tool.call.result`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`,
  `gen_ai.usage.cost_usd`.
- **Correlation key:** `test.run.id` (carried via `OTEL_RESOURCE_ATTRIBUTES` for spawned SUTs → resource attribute on every span).
- **Answer-extraction convention (project-wide):** a SUT writes **only its final result to stdout**; all diagnostics go to **stderr**. `Output.Answer = strings.TrimSpace(stdout)`.
- **Determinism:** no `time.Now()`/`rand` in emitted *content*; scenario selectors fully determine spans, attributes, and output. Tests assert on structure/attributes/output, never on absolute span durations.
- **Commits:** add files individually (no `git add .`); no AI authorship/attribution in commit messages.

## File Structure

```
go.mod                                      module + deps
.gitignore                                  Go + local artifacts
tracelab/researchbot/plan.go                Plan model + YAML loader + validation
tracelab/researchbot/plan_test.go
tracelab/researchbot/attrs.go               pinned attribute-key constants
tracelab/researchbot/otel.go                resource (env-aware) + TracerProvider builder
tracelab/researchbot/otel_test.go
tracelab/researchbot/emit.go                Plan -> span tree emitter
tracelab/researchbot/emit_test.go
tracelab/researchbot/scenarios.go           //go:embed of scenario plans + Scenario(name)
tracelab/researchbot/scenarios/*.yaml       the 5 scenario plans
tracelab/researchbot/scenarios_test.go      coverage table test
tracelab/researchbot/run.go                 Run core (emit + write answer)
tracelab/researchbot/run_test.go
tracelab/researchbot/cmd/researchbot/main.go CLI entrypoint (wires real OTLP exporter)
tracelab/researchbot/capture.go             scenario -> normalized JSON fixture
tracelab/researchbot/capture_test.go
tracelab/researchbot/cmd/capture/main.go    golden-fixture generator
testdata/traces/researchbot/*.json          generated golden fixtures
deploy/docker-compose.yml                   Tempo + OTel Collector
deploy/tempo.yaml                           Tempo config
deploy/otel-collector.yaml                  Collector config (OTLP in -> Tempo out)
deploy/smoke.sh                             integration smoke check
Makefile                                    harness-up/down, capture, smoke
```

---

### Task 1: Module bootstrap + scenario Plan model & loader

**Files:**
- Create: `go.mod`, `.gitignore`
- Create: `tracelab/researchbot/plan.go`
- Test: `tracelab/researchbot/plan_test.go`

**Interfaces:**
- Produces: `Plan{Scenario string; Output string; Tokens Tokens; CostUSD float64; Steps []Step}`,
  `Tokens{Input,Output int}`, `Step{Chat *ChatStep; Tool *ToolStep}`,
  `ChatStep{Model,Finish string}`, `ToolStep{Name,Args,Result string}`;
  `func LoadPlan(data []byte) (*Plan, error)`; `func (p *Plan) Validate() error`.

- [ ] **Step 1: Initialize the module and ignore file**

Run:
```bash
cd /Users/antonio/personal/trace
go mod init github.com/thetonymaster/mentat
go get gopkg.in/yaml.v3@latest
printf '/bin\n*.out\n.env\n' > .gitignore
```

- [ ] **Step 2: Write the failing test**

Create `tracelab/researchbot/plan_test.go`:
```go
package researchbot

import "testing"

func TestLoadPlanParsesStepsInOrder(t *testing.T) {
	data := []byte(`
scenario: happy
output: "Q3 revenue grew 12%"
tokens: { input: 1200, output: 600 }
cost_usd: 0.018
steps:
  - chat: { model: claude-x, finish: tool_calls }
  - tool: { name: search, args: "q3", result: "doc-1" }
`)
	p, err := LoadPlan(data)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if p.Scenario != "happy" || p.Tokens.Input != 1200 || p.CostUSD != 0.018 {
		t.Fatalf("scalars wrong: %+v", p)
	}
	if len(p.Steps) != 2 || p.Steps[0].Chat == nil || p.Steps[1].Tool == nil {
		t.Fatalf("steps wrong: %+v", p.Steps)
	}
	if p.Steps[1].Tool.Name != "search" {
		t.Fatalf("tool name = %q", p.Steps[1].Tool.Name)
	}
}

func TestValidateRejectsStepWithBothChatAndTool(t *testing.T) {
	p := &Plan{Scenario: "x", Steps: []Step{{Chat: &ChatStep{}, Tool: &ToolStep{}}}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for step with both chat and tool")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./tracelab/researchbot/ -run TestLoadPlan -v`
Expected: FAIL — `undefined: LoadPlan` (build error).

- [ ] **Step 4: Write the implementation**

Create `tracelab/researchbot/plan.go`:
```go
package researchbot

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Plan struct {
	Scenario string  `yaml:"scenario"`
	Output   string  `yaml:"output"`
	Tokens   Tokens  `yaml:"tokens"`
	CostUSD  float64 `yaml:"cost_usd"`
	Steps    []Step  `yaml:"steps"`
}

type Tokens struct {
	Input  int `yaml:"input"`
	Output int `yaml:"output"`
}

type Step struct {
	Chat *ChatStep `yaml:"chat,omitempty"`
	Tool *ToolStep `yaml:"tool,omitempty"`
}

type ChatStep struct {
	Model  string `yaml:"model"`
	Finish string `yaml:"finish"`
}

type ToolStep struct {
	Name   string `yaml:"name"`
	Args   string `yaml:"args"`
	Result string `yaml:"result"`
}

func LoadPlan(data []byte) (*Plan, error) {
	var p Plan
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Plan) Validate() error {
	if p.Scenario == "" {
		return fmt.Errorf("plan: scenario is required")
	}
	for i, s := range p.Steps {
		switch {
		case s.Chat != nil && s.Tool != nil:
			return fmt.Errorf("plan: step %d has both chat and tool", i)
		case s.Chat == nil && s.Tool == nil:
			return fmt.Errorf("plan: step %d has neither chat nor tool", i)
		case s.Tool != nil && s.Tool.Name == "":
			return fmt.Errorf("plan: step %d tool missing name", i)
		}
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./tracelab/researchbot/ -run 'TestLoadPlan|TestValidate' -v`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum .gitignore tracelab/researchbot/plan.go tracelab/researchbot/plan_test.go
git commit -m "feat(researchbot): scenario plan model and YAML loader"
```

---

### Task 2: Pinned attribute keys + env-aware OTel setup

**Files:**
- Create: `tracelab/researchbot/attrs.go`
- Create: `tracelab/researchbot/otel.go`
- Test: `tracelab/researchbot/otel_test.go`

**Interfaces:**
- Consumes: nothing from prior tasks.
- Produces: attribute-key constants (`AttrOp`, `AttrAgentName`, `AttrModel`, `AttrFinish`,
  `AttrToolName`, `AttrToolArgs`, `AttrToolResult`, `AttrInTokens`, `AttrOutTokens`, `AttrCostUSD`);
  `func NewResource(ctx context.Context) (*resource.Resource, error)`;
  `func NewTracerProvider(ctx context.Context, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error)`.

- [ ] **Step 1: Add the attribute-key constants**

Create `tracelab/researchbot/attrs.go`:
```go
package researchbot

// Pinned OTel GenAI semantic-convention keys. One place so the emitter, tests,
// and downstream Mentat comparators agree exactly.
const (
	AttrOp         = "gen_ai.operation.name"
	AttrAgentName  = "gen_ai.agent.name"
	AttrModel      = "gen_ai.request.model"
	AttrFinish     = "gen_ai.response.finish_reasons"
	AttrToolName   = "gen_ai.tool.name"
	AttrToolArgs   = "gen_ai.tool.call.arguments"
	AttrToolResult = "gen_ai.tool.call.result"
	AttrInTokens   = "gen_ai.usage.input_tokens"
	AttrOutTokens  = "gen_ai.usage.output_tokens"
	AttrCostUSD    = "gen_ai.usage.cost_usd"

	OpInvokeAgent = "invoke_agent"
	OpChat        = "chat"
	OpExecuteTool = "execute_tool"
)
```

- [ ] **Step 2: Write the failing test**

Create `tracelab/researchbot/otel_test.go`:
```go
package researchbot

import (
	"context"
	"testing"
)

func TestNewResourceReadsRunIDFromEnv(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "test.run.id=abc123,test.scenario=happy")
	res, err := NewResource(context.Background())
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	var got string
	for _, kv := range res.Attributes() {
		if string(kv.Key) == "test.run.id" {
			got = kv.Value.AsString()
		}
	}
	if got != "abc123" {
		t.Fatalf("test.run.id = %q, want abc123", got)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./tracelab/researchbot/ -run TestNewResource -v`
Expected: FAIL — `undefined: NewResource`.

- [ ] **Step 4: Write the implementation**

Run: `go get go.opentelemetry.io/otel/sdk@latest go.opentelemetry.io/otel@latest`

Create `tracelab/researchbot/otel.go`:
```go
package researchbot

import (
	"context"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// NewResource builds the OTel resource, honoring OTEL_RESOURCE_ATTRIBUTES so the
// driver-injected test.run.id lands on every span (the correlation contract).
func NewResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(), // reads OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME
		resource.WithAttributes(semconv.ServiceName("researchbot")),
	)
}

// NewTracerProvider wires the given exporter (OTLP in prod, in-memory in tests)
// with a SimpleSpanProcessor for deterministic flushing.
func NewTracerProvider(ctx context.Context, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error) {
	res, err := NewResource(ctx)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	), nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./tracelab/researchbot/ -run TestNewResource -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum tracelab/researchbot/attrs.go tracelab/researchbot/otel.go tracelab/researchbot/otel_test.go
git commit -m "feat(researchbot): env-aware OTel resource and tracer provider"
```

---

### Task 3: Plan → span tree emitter

**Files:**
- Create: `tracelab/researchbot/emit.go`
- Test: `tracelab/researchbot/emit_test.go`

**Interfaces:**
- Consumes: `Plan`/`Step` (Task 1), attribute constants (Task 2), `NewTracerProvider` (Task 2).
- Produces: `func Emit(ctx context.Context, tr trace.Tracer, p *Plan) error` — emits an
  `invoke_agent researchbot` root span (with usage attrs) and one child span per step
  (`chat <model>` / `execute_tool <name>`), children parented to the root.

- [ ] **Step 1: Write the failing test**

Create `tracelab/researchbot/emit_test.go`:
```go
package researchbot

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEmitProducesParentedToolAndChatSpans(t *testing.T) {
	ctx := context.Background()
	exp := tracetest.NewInMemoryExporter()
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	p := &Plan{
		Scenario: "happy",
		Tokens:   Tokens{Input: 1200, Output: 600},
		CostUSD:  0.018,
		Steps: []Step{
			{Chat: &ChatStep{Model: "claude-x", Finish: "tool_calls"}},
			{Tool: &ToolStep{Name: "search", Args: "q3", Result: "doc-1"}},
		},
	}
	if err := Emit(ctx, tp.Tracer("test"), p); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	spans := exp.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(spans))
	}
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	root, ok := byName["invoke_agent researchbot"]
	if !ok {
		t.Fatal("missing root span")
	}
	tool, ok := byName["execute_tool search"]
	if !ok {
		t.Fatal("missing tool span")
	}
	if tool.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("tool span not parented to root")
	}
	if attr(tool, AttrToolName) != "search" {
		t.Fatalf("tool name attr = %q", attr(tool, AttrToolName))
	}
	if attrInt(root, AttrInTokens) != 1200 {
		t.Fatalf("input tokens = %d", attrInt(root, AttrInTokens))
	}
}

func attr(s tracetest.SpanStub, key string) string {
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

func attrInt(s tracetest.SpanStub, key string) int64 {
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsInt64()
		}
	}
	return -1
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./tracelab/researchbot/ -run TestEmit -v`
Expected: FAIL — `undefined: Emit`.

- [ ] **Step 3: Write the implementation**

Create `tracelab/researchbot/emit.go`:
```go
package researchbot

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Emit replays a plan into a span tree. Children are started from the root's
// context so they are parented to it; sequential start order makes tool/chat
// ordering deterministic.
func Emit(ctx context.Context, tr trace.Tracer, p *Plan) error {
	ctx, root := tr.Start(ctx, "invoke_agent researchbot", trace.WithAttributes(
		attribute.String(AttrOp, OpInvokeAgent),
		attribute.String(AttrAgentName, "researchbot"),
		attribute.Int(AttrInTokens, p.Tokens.Input),
		attribute.Int(AttrOutTokens, p.Tokens.Output),
		attribute.Float64(AttrCostUSD, p.CostUSD),
	))
	defer root.End()

	for _, s := range p.Steps {
		switch {
		case s.Chat != nil:
			_, sp := tr.Start(ctx, "chat "+s.Chat.Model, trace.WithAttributes(
				attribute.String(AttrOp, OpChat),
				attribute.String(AttrModel, s.Chat.Model),
				attribute.StringSlice(AttrFinish, []string{s.Chat.Finish}),
			))
			sp.End()
		case s.Tool != nil:
			_, sp := tr.Start(ctx, "execute_tool "+s.Tool.Name, trace.WithAttributes(
				attribute.String(AttrOp, OpExecuteTool),
				attribute.String(AttrToolName, s.Tool.Name),
				attribute.String(AttrToolArgs, s.Tool.Args),
				attribute.String(AttrToolResult, s.Tool.Result),
			))
			sp.End()
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./tracelab/researchbot/ -run TestEmit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracelab/researchbot/emit.go tracelab/researchbot/emit_test.go
git commit -m "feat(researchbot): emit plan into gen_ai span tree"
```

---

### Task 4: `run` core + CLI entrypoint

**Files:**
- Create: `tracelab/researchbot/run.go`
- Create: `tracelab/researchbot/main.go`
- Test: `tracelab/researchbot/run_test.go`

**Interfaces:**
- Consumes: `Plan` (Task 1), `NewTracerProvider`/`Emit` (Tasks 2–3).
- Produces: `func Run(ctx context.Context, p *Plan, exp sdktrace.SpanExporter, stdout, stderr io.Writer) error`
  — emits the plan's spans via `exp` and writes `p.Output` (only) to `stdout`.

- [ ] **Step 1: Write the failing test**

Create `tracelab/researchbot/run_test.go`:
```go
package researchbot

import (
	"bytes"
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRunWritesOnlyAnswerToStdout(t *testing.T) {
	p := &Plan{
		Scenario: "happy",
		Output:   "Q3 revenue grew 12%",
		Steps:    []Step{{Tool: &ToolStep{Name: "search"}}},
	}
	var out, errBuf bytes.Buffer
	exp := tracetest.NewInMemoryExporter()
	if err := Run(context.Background(), p, exp, &out, &errBuf); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "Q3 revenue grew 12%\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(exp.GetSpans()) == 0 {
		t.Fatal("expected spans to be exported")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./tracelab/researchbot/ -run TestRun -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the `run` core**

Create `tracelab/researchbot/run.go`:
```go
package researchbot

import (
	"context"
	"fmt"
	"io"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Run emits the plan via exp and writes only the final answer to stdout
// (diagnostics belong on stderr). It flushes and shuts the provider down so all
// spans are exported before returning.
func Run(ctx context.Context, p *Plan, exp sdktrace.SpanExporter, stdout, stderr io.Writer) error {
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		return err
	}
	if err := Emit(ctx, tp.Tracer("researchbot"), p); err != nil {
		return err
	}
	if err := tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown tracer provider: %w", err)
	}
	fmt.Fprintln(stdout, p.Output)
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./tracelab/researchbot/ -run TestRun -v`
Expected: PASS.

- [ ] **Step 5: Write the CLI entrypoint**

Run: `go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@latest`

Create `tracelab/researchbot/cmd/researchbot/main.go` (a `package main` command;
the library code stays in `package researchbot` so it is unit-testable):
```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	rb "github.com/thetonymaster/mentat/tracelab/researchbot"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

func main() {
	scenario := flag.String("scenario", "", "scenario name (embedded)")
	prompt := flag.String("prompt", "", "prompt (recorded on stderr; output is scenario-driven)")
	flag.Parse()

	ctx := context.Background()
	p, err := rb.Scenario(*scenario)
	if err != nil {
		fmt.Fprintln(os.Stderr, "researchbot:", err)
		os.Exit(2)
	}
	if *prompt != "" {
		fmt.Fprintln(os.Stderr, "researchbot: prompt:", *prompt)
	}
	exp, err := otlptracehttp.New(ctx) // honors OTEL_EXPORTER_OTLP_ENDPOINT
	if err != nil {
		fmt.Fprintln(os.Stderr, "researchbot: exporter:", err)
		os.Exit(1)
	}
	if err := rb.Run(ctx, p, exp, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "researchbot:", err)
		os.Exit(1)
	}
}
```
> Note: `rb.Scenario` is delivered in Task 5; until then the entrypoint will not compile. Build the binary in Task 5's verification, not here.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum tracelab/researchbot/run.go tracelab/researchbot/run_test.go tracelab/researchbot/cmd/researchbot/main.go
git commit -m "feat(researchbot): run core + CLI entrypoint"
```

---

### Task 5: Scenario plans + coverage table test

**Files:**
- Create: `tracelab/researchbot/scenarios/{happy,extra_tool,wrong_order,over_budget,bad_answer}.yaml`
- Create: `tracelab/researchbot/scenarios.go`
- Test: `tracelab/researchbot/scenarios_test.go`

**Interfaces:**
- Consumes: `LoadPlan`/`Plan` (Task 1).
- Produces: `func Scenario(name string) (*Plan, error)`; `func ScenarioNames() []string`.

- [ ] **Step 1: Create the five scenario plans**

Create `tracelab/researchbot/scenarios/happy.yaml`:
```yaml
scenario: happy
output: "Q3 revenue grew 12% to $4.2M, driven by strong enterprise demand."
tokens: { input: 1200, output: 600 }
cost_usd: 0.018
steps:
  - chat: { model: claude-x, finish: tool_calls }
  - tool: { name: search,    args: "q3 revenue", result: "doc-1, doc-2" }
  - tool: { name: fetch_doc, args: "doc-1",      result: "revenue table" }
  - chat: { model: claude-x, finish: tool_calls }
  - tool: { name: summarize, args: "revenue table", result: "summary" }
  - chat: { model: claude-x, finish: stop }
```

Create `tracelab/researchbot/scenarios/extra_tool.yaml`:
```yaml
scenario: extra_tool
output: "Q3 revenue grew 12% to $4.2M."
tokens: { input: 1300, output: 650 }
cost_usd: 0.020
steps:
  - tool: { name: search,        args: "q3 revenue", result: "doc-1" }
  - tool: { name: fetch_doc,     args: "doc-1",      result: "table" }
  - tool: { name: delete_record, args: "doc-1",      result: "deleted" }
  - tool: { name: summarize,     args: "table",      result: "summary" }
```

Create `tracelab/researchbot/scenarios/wrong_order.yaml`:
```yaml
scenario: wrong_order
output: "Q3 revenue grew 12%."
tokens: { input: 1100, output: 500 }
cost_usd: 0.016
steps:
  - tool: { name: summarize, args: "nothing", result: "empty summary" }
  - tool: { name: search,    args: "q3",      result: "doc-1" }
  - tool: { name: fetch_doc, args: "doc-1",   result: "table" }
```

Create `tracelab/researchbot/scenarios/over_budget.yaml`:
```yaml
scenario: over_budget
output: "Q3 revenue grew 12% (verbose)."
tokens: { input: 9000, output: 4000 }
cost_usd: 0.45
steps:
  - tool: { name: search,    args: "q3", result: "doc-1" }
  - tool: { name: fetch_doc, args: "doc-1", result: "table" }
  - tool: { name: summarize, args: "table", result: "summary" }
```

Create `tracelab/researchbot/scenarios/bad_answer.yaml`:
```yaml
scenario: bad_answer
output: "I could not find any information."
tokens: { input: 1200, output: 200 }
cost_usd: 0.010
steps:
  - tool: { name: search,    args: "q3", result: "doc-1" }
  - tool: { name: fetch_doc, args: "doc-1", result: "table" }
  - tool: { name: summarize, args: "table", result: "summary" }
```

- [ ] **Step 2: Write the failing test**

Create `tracelab/researchbot/scenarios_test.go`:
```go
package researchbot

import (
	"strings"
	"testing"
)

func toolNames(p *Plan) []string {
	var n []string
	for _, s := range p.Steps {
		if s.Tool != nil {
			n = append(n, s.Tool.Name)
		}
	}
	return n
}

func TestScenariosCoverPassAndFailPaths(t *testing.T) {
	all := ScenarioNames()
	if len(all) != 5 {
		t.Fatalf("want 5 scenarios, got %v", all)
	}

	happy, err := Scenario("happy")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got := toolNames(happy); strings.Join(got, ",") != "search,fetch_doc,summarize" {
		t.Fatalf("happy tools = %v", got)
	}
	if happy.Tokens.Input+happy.Tokens.Output >= 5000 {
		t.Fatal("happy should be under budget")
	}
	if !strings.Contains(happy.Output, "Q3 revenue") {
		t.Fatalf("happy output = %q", happy.Output)
	}

	extra, _ := Scenario("extra_tool")
	if !contains(toolNames(extra), "delete_record") {
		t.Fatal("extra_tool must call delete_record")
	}

	wrong, _ := Scenario("wrong_order")
	tn := toolNames(wrong)
	if indexOf(tn, "summarize") > indexOf(tn, "search") {
		t.Fatal("wrong_order must summarize before search")
	}

	over, _ := Scenario("over_budget")
	if over.Tokens.Input+over.Tokens.Output < 5000 {
		t.Fatal("over_budget must exceed 5000 tokens")
	}

	bad, _ := Scenario("bad_answer")
	if strings.Contains(bad.Output, "Q3 revenue") {
		t.Fatal("bad_answer output must not contain the good answer")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./tracelab/researchbot/ -run TestScenarios -v`
Expected: FAIL — `undefined: ScenarioNames` / `Scenario`.

- [ ] **Step 4: Write the embed + accessors**

Create `tracelab/researchbot/scenarios.go`:
```go
package researchbot

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed scenarios/*.yaml
var scenarioFS embed.FS

func Scenario(name string) (*Plan, error) {
	if name == "" {
		return nil, fmt.Errorf("scenario name required; one of %v", ScenarioNames())
	}
	data, err := scenarioFS.ReadFile("scenarios/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unknown scenario %q; one of %v", name, ScenarioNames())
	}
	return LoadPlan(data)
}

func ScenarioNames() []string {
	var names []string
	_ = fs.WalkDir(scenarioFS, "scenarios", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".yaml") {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(p, "scenarios/"), ".yaml"))
		}
		return nil
	})
	sort.Strings(names)
	return names
}
```

- [ ] **Step 5: Run the test + build the binary to verify both pass**

Run: `go test ./tracelab/researchbot/ -run TestScenarios -v && go build ./tracelab/researchbot/cmd/researchbot`
Expected: PASS, and the `researchbot` binary builds (Task 4's entrypoint now resolves `rb.Scenario`).

- [ ] **Step 6: Commit**

```bash
git add tracelab/researchbot/scenarios tracelab/researchbot/scenarios.go tracelab/researchbot/scenarios_test.go
git commit -m "feat(researchbot): five deterministic scenarios with pass/fail coverage"
```

---

### Task 6: Local stack (Tempo + Collector) + Makefile + smoke

**Files:**
- Create: `deploy/docker-compose.yml`, `deploy/tempo.yaml`, `deploy/otel-collector.yaml`, `deploy/smoke.sh`
- Create: `Makefile`

**Interfaces:**
- Consumes: the `researchbot` binary (Task 5).
- Produces: `make harness-up` / `make harness-down`; a smoke check that a researchbot run lands a queryable trace in Tempo.

- [ ] **Step 1: Write the Tempo config**

Create `deploy/tempo.yaml`:
```yaml
server:
  http_listen_port: 3200
distributor:
  receivers:
    otlp:
      protocols:
        http:
          endpoint: 0.0.0.0:4318
        grpc:
          endpoint: 0.0.0.0:4317
storage:
  trace:
    backend: local
    local:
      path: /var/tempo/blocks
    wal:
      path: /var/tempo/wal
```

- [ ] **Step 2: Write the Collector config**

Create `deploy/otel-collector.yaml`:
```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318
exporters:
  otlp:
    endpoint: tempo:4317
    tls:
      insecure: true
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [otlp]
```

- [ ] **Step 3: Write the compose file**

Create `deploy/docker-compose.yml`:
```yaml
services:
  tempo:
    image: grafana/tempo:2.5.0
    command: ["-config.file=/etc/tempo.yaml"]
    volumes:
      - ./tempo.yaml:/etc/tempo.yaml
    ports:
      - "3200:3200"   # Tempo query API
      - "4317:4317"   # OTLP gRPC (collector -> tempo)
  collector:
    image: otel/opentelemetry-collector:0.105.0
    command: ["--config=/etc/otel-collector.yaml"]
    volumes:
      - ./otel-collector.yaml:/etc/otel-collector.yaml
    ports:
      - "4318:4318"   # OTLP HTTP (researchbot -> collector)
    depends_on:
      - tempo
```

- [ ] **Step 4: Write the Makefile**

Create `Makefile`:
```make
.PHONY: harness-up harness-down smoke

harness-up:
	docker compose -f deploy/docker-compose.yml up -d

harness-down:
	docker compose -f deploy/docker-compose.yml down -v

smoke:
	bash deploy/smoke.sh
```

- [ ] **Step 5: Write the smoke check**

Create `deploy/smoke.sh`:
```bash
#!/usr/bin/env bash
# Integration smoke: run a researchbot scenario, then query Tempo by test.run.id.
set -euo pipefail

RUN_ID="smoke-$$"
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
export OTEL_RESOURCE_ATTRIBUTES="test.run.id=${RUN_ID},test.scenario=happy"

go run ./tracelab/researchbot/cmd/researchbot --scenario happy >/dev/null

echo "waiting for trace to land in Tempo..."
for i in $(seq 1 20); do
  n=$(curl -s "http://localhost:3200/api/search?q=%7B%20.test.run.id%20%3D%20%22${RUN_ID}%22%20%7D" \
        | grep -o '"traceID"' | wc -l | tr -d ' ')
  if [ "$n" != "0" ]; then
    echo "OK: found trace(s) for ${RUN_ID}"
    exit 0
  fi
  sleep 1
done
echo "FAIL: no trace for ${RUN_ID} after 20s" >&2
exit 1
```

- [ ] **Step 6: Run the integration smoke (manual / CI)**

Run:
```bash
chmod +x deploy/smoke.sh
make harness-up
sleep 5
make smoke
make harness-down
```
Expected: `OK: found trace(s) for smoke-<pid>`. (Requires Docker; this is the L2 wiring proof — not a unit test.)

- [ ] **Step 7: Commit**

```bash
git add deploy/docker-compose.yml deploy/tempo.yaml deploy/otel-collector.yaml deploy/smoke.sh Makefile
git commit -m "feat(deploy): local Tempo + OTel Collector stack with smoke check"
```

---

### Task 7: Golden-trace capture → JSON fixtures

**Files:**
- Create: `tracelab/researchbot/capture.go`
- Test: `tracelab/researchbot/capture_test.go`
- Create (generated): `testdata/traces/researchbot/*.json`

**Interfaces:**
- Consumes: `Plan`/`Scenario` (Tasks 1,5), `Emit`/`NewTracerProvider` (Tasks 2–3), attribute keys (Task 2).
- Produces: `func CaptureFixture(ctx context.Context, p *Plan) ([]byte, error)` — emits via an
  in-memory exporter and marshals a **normalized** span-tree JSON (no volatile IDs/timestamps);
  `func WriteFixtures(dir string) error` (writes every scenario).
  Fixture schema: `{"runScenario":string,"spans":[{"name","op","parentIndex","attrs":{k:v},"status"}]}`.

- [ ] **Step 1: Write the failing test**

Create `tracelab/researchbot/capture_test.go`:
```go
package researchbot

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCaptureFixtureIsNormalizedAndDeterministic(t *testing.T) {
	p, err := Scenario("happy")
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	a, err := CaptureFixture(context.Background(), p)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	b, _ := CaptureFixture(context.Background(), p)
	if string(a) != string(b) {
		t.Fatal("capture is not deterministic")
	}

	var fx struct {
		Spans []struct {
			Name        string            `json:"name"`
			Op          string            `json:"op"`
			ParentIndex int               `json:"parentIndex"`
			Attrs       map[string]string `json:"attrs"`
		} `json:"spans"`
	}
	if err := json.Unmarshal(a, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fx.Spans[0].Name != "invoke_agent researchbot" || fx.Spans[0].ParentIndex != -1 {
		t.Fatalf("root span wrong: %+v", fx.Spans[0])
	}
	found := false
	for _, s := range fx.Spans {
		if s.Op == OpExecuteTool && s.Attrs[AttrToolName] == "search" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an execute_tool=search span")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./tracelab/researchbot/ -run TestCapture -v`
Expected: FAIL — `undefined: CaptureFixture`.

- [ ] **Step 3: Write the implementation**

Create `tracelab/researchbot/capture.go`:
```go
package researchbot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fixtureSpan struct {
	Name        string            `json:"name"`
	Op          string            `json:"op"`
	ParentIndex int               `json:"parentIndex"`
	Attrs       map[string]string `json:"attrs"`
	Status      string            `json:"status"`
}

type fixture struct {
	RunScenario string        `json:"runScenario"`
	Spans       []fixtureSpan `json:"spans"`
}

// CaptureFixture emits the plan to an in-memory exporter and renders a normalized,
// deterministic span-tree JSON: volatile IDs/timestamps are dropped; parentage is
// expressed by index; spans are ordered root-first then by start order.
func CaptureFixture(ctx context.Context, p *Plan) ([]byte, error) {
	exp := tracetest.NewInMemoryExporter()
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		return nil, err
	}
	if err := Emit(ctx, tp.Tracer("researchbot"), p); err != nil {
		return nil, err
	}
	if err := tp.Shutdown(ctx); err != nil {
		return nil, err
	}

	stubs := exp.GetSpans()
	// Index by span-id STRING (trace.SpanID is a named [8]byte; string keys avoid
	// the named-type map-key mismatch and are stable across runs for parentage).
	idxBySpanID := map[string]int{}
	for i, s := range stubs {
		idxBySpanID[s.SpanContext.SpanID().String()] = i
	}

	out := fixture{RunScenario: p.Scenario}
	for _, s := range stubs {
		attrs := map[string]string{}
		for _, kv := range s.Attributes {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
		parent := -1
		if s.Parent.IsValid() {
			if pi, ok := idxBySpanID[s.Parent.SpanID().String()]; ok {
				parent = pi
			}
		}
		out.Spans = append(out.Spans, fixtureSpan{
			Name:        s.Name,
			Op:          attrs[AttrOp],
			ParentIndex: parent,
			Attrs:       attrs,
			Status:      s.Status.Code.String(),
		})
	}
	// Stable order: root (parentIndex<0) first, then preserve export order.
	for i := range out.Spans {
		if out.Spans[i].ParentIndex < 0 && i != 0 {
			out.Spans[0], out.Spans[i] = out.Spans[i], out.Spans[0]
			break
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// WriteFixtures captures every scenario into dir/<scenario>.json.
func WriteFixtures(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range ScenarioNames() {
		p, err := Scenario(name)
		if err != nil {
			return err
		}
		data, err := CaptureFixture(context.Background(), p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}
```

> Note on parentage ordering: because the root is emitted first and `tracetest`
> preserves export order, `ParentIndex` is stable. The swap loop guarantees the
> root is element 0 even if the exporter reorders.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./tracelab/researchbot/ -run TestCapture -v`
Expected: PASS.

- [ ] **Step 5: Generate the golden fixtures**

Add a tiny generator and run it. Create `tracelab/researchbot/cmd/capture/main.go`:
```go
package main

import (
	"fmt"
	"os"

	rb "github.com/thetonymaster/mentat/tracelab/researchbot"
)

func main() {
	if err := rb.WriteFixtures("testdata/traces/researchbot"); err != nil {
		fmt.Fprintln(os.Stderr, "capture:", err)
		os.Exit(1)
	}
}
```
Run: `go run ./tracelab/researchbot/cmd/capture && ls testdata/traces/researchbot`
Expected: five files `happy.json … bad_answer.json`.

- [ ] **Step 6: Run the full package test suite**

Run: `go test ./tracelab/researchbot/ -v`
Expected: PASS (all tests, all tasks).

- [ ] **Step 7: Commit**

```bash
git add tracelab/researchbot/capture.go tracelab/researchbot/capture_test.go tracelab/researchbot/cmd/capture/main.go testdata/traces/researchbot
git commit -m "feat(researchbot): deterministic golden-trace capture + fixtures"
```

---

## Done criteria for Plan 1

- `go test ./tracelab/researchbot/...` passes.
- `go build ./tracelab/researchbot/cmd/...` builds both binaries.
- `make harness-up && make smoke` finds a trace in Tempo by `test.run.id` (Docker required).
- `testdata/traces/researchbot/*.json` exist for all five scenarios (feed Plan 2's L1 unit tests).

This harness is the target Plan 2 (Mentat Phase-1 framework) develops against.
