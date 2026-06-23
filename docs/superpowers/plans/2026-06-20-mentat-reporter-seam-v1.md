# Reporter Seam (JSON + HTML) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class `Reporter` seam and two reporters — `json` (canonical data model) and `html` (a view over it) — that emit a structured per-run report alongside godog's unchanged pretty/junit output.

**Architecture:** A run-scoped `report.Collector` accumulates one `core.ScenarioResult` per scenario (derived from the run's `Evidence` forest + `Verdict`). After `suite.Run()`, `cmd/mentat` folds the `Collector` into a `core.RunReport` and invokes each `--report-*`-selected reporter, resolved from a new instance-based registry. Comparators are untouched except the cost-helper extraction; structured aggregate detail comes from the already-built `core.AggregateDetail` (Feature A).

**Tech Stack:** Go, `encoding/json`, `html/template`, godog, uber gomock (new `Reporter` mock).

**Spec:** `docs/superpowers/specs/2026-06-20-mentat-reporter-seam-design.md`

**Depends on:** `feat/mentat-cel-aggregate-scalar` (Feature A) merged first — this plan consumes `core.AggregateDetail` + `Verdict.Detail`.

## Global Constraints

- Module path: `github.com/thetonymaster/mentat`.
- `gofmt -l .` clean, `go vet ./...` clean before every commit.
- **Comparators consume `Evidence` only** (invariant #1). The reporter likewise derives from `Evidence` — no `TraceStore`/`Driver` access.
- **`Trace` is a forest** (invariant #2) — never assume a single root; the derivation uses forest-safe helpers (`ByOp`, `ServiceSequence`, `Envelope`).
- **No silent fallbacks** (invariant #4): a reporter that cannot write returns a wrapped `error`; `cmd/mentat` treats it as a fatal non-zero exit.
- **`report` imports `core` + `comparator`**; neither imports `report` (acyclic).
- Tables-driven tests; **uber gomock** for the new `Reporter` interface (`go generate ./...`).
- **Coverage floor 80%** for `internal/report` and every touched package.
- **L3 meta-test mandatory** — the report artifact must reflect failure, and an unwritable path must exit non-zero.
- Git: Conventional Commits; `git add .` forbidden; **no AI attribution**.

---

### Task 0: Branch setup

**Files:** none (git only).

- [ ] **Step 1: Branch off `main` after Feature A has merged**

```bash
git checkout main && git pull
# confirm Feature A is present:
grep -q "AggregateDetail" internal/core/core.go || { echo "Feature A not merged — stop"; exit 1; }
git checkout -b feat/mentat-reporter-seam-impl
git checkout feat/mentat-reporter-seam -- docs/superpowers/specs/2026-06-20-mentat-reporter-seam-design.md docs/superpowers/plans/2026-06-20-mentat-reporter-seam-v1.md
git add docs/superpowers/specs/2026-06-20-mentat-reporter-seam-design.md docs/superpowers/plans/2026-06-20-mentat-reporter-seam-v1.md
git commit -m "docs: reporter seam spec + plan"
```

- [ ] **Step 2: Baseline green**

Run: `go build ./... && go test ./...`
Expected: PASS.

---

### Task 1: Extract `deriveCost` + add `comparator.CostOrZero`

**Goal:** Give the reporter a cost function with absent→0 semantics, without changing `budgets`/`cel`/`aggregate_cel` behaviour. (Reporter spec §4.3.)

**Files:**
- Modify: `internal/comparator/budgets.go:125-185` (`costSum`)
- Test: `internal/comparator/budgets_test.go`

**Interfaces:**
- Produces:
  - `func CostOrZero(t *trace.Trace, pricing core.Pricing) (float64, error)` — `(0, nil)` when cost is absent (incl. nil trace); propagates malformed/ambiguous/out-of-range errors.
  - `costSum` keeps its existing signature and behaviour (absent → "cost not available" error).

- [ ] **Step 1: Write the failing test**

```go
func TestCostOrZero(t *testing.T) {
	tests := []struct {
		name    string
		trace   *trace.Trace
		pricing core.Pricing
		want    float64
		wantErr bool
	}{
		{"nil trace -> 0", nil, nil, 0, false},
		{"no cost, no pricing -> 0", traceWithSpans(spanNoCost()), nil, 0, false},
		{"emitted cost", traceWithSpans(spanCost("0.0030")), nil, 0.0030, false},
		{"malformed cost -> err", traceWithSpans(spanCost("abc")), nil, 0, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := CostOrZero(tt.trace, tt.pricing)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
```

> Implementer note: reuse the existing test helpers in `budgets_test.go` for building traces/spans (`traceWithSpans`, `spanCost`, `spanNoCost` are illustrative — match the actual helper names in that file; if none exist, build a `*trace.Trace` literal as the existing tests do).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/comparator/ -run TestCostOrZero -v`
Expected: FAIL — `undefined: CostOrZero`.

- [ ] **Step 3: Refactor `costSum` to expose `deriveCost`; add `CostOrZero`**

In `internal/comparator/budgets.go`, split the per-span loop out of `costSum`:

```go
// deriveCost walks the spans applying the §4.3 precedence and reports whether any
// cost signal was seen. seen=false means no span carried emitted cost or derivable
// tokens — the caller decides whether that is an error (budgets) or a 0 (reporter).
func deriveCost(t *trace.Trace, pricing core.Pricing) (cost float64, seen bool, err error) {
	// ... the existing body of costSum, verbatim, EXCEPT:
	//   - return (0, false, err) at each existing error return,
	//   - replace the final `if !seen { return 0, fmt.Errorf(...) }` with `return cost, seen, nil`.
}

// costSum preserves the existing contract: absent cost is a hard error.
func costSum(t *trace.Trace, pricing core.Pricing) (float64, error) {
	cost, seen, err := deriveCost(t, pricing)
	if err != nil {
		return 0, err
	}
	if !seen {
		return 0, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
	}
	return cost, nil
}

// CostOrZero is the reporter-facing cost: absent cost (incl. a nil trace) yields 0;
// malformed/ambiguous/out-of-range values still error (no silent fallback on corruption).
func CostOrZero(t *trace.Trace, pricing core.Pricing) (float64, error) {
	if t == nil {
		return 0, nil
	}
	cost, _, err := deriveCost(t, pricing)
	if err != nil {
		return 0, err
	}
	return cost, nil
}
```

Move the nil-trace guard out of `deriveCost` (callers handle nil: `budgets.Compare` already errors on nil at `budgets.go:40`; `CostOrZero` returns 0).

- [ ] **Step 4: Run to verify new + existing pass**

Run: `go test ./internal/comparator/...`
Expected: PASS — `TestCostOrZero` green AND all existing `budgets`/`cel`/`aggregate_cel` tests green (behaviour-preserving).

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/comparator/budgets.go internal/comparator/budgets_test.go
go vet ./internal/comparator/
git add internal/comparator/budgets.go internal/comparator/budgets_test.go
git commit -m "refactor(comparator): extract deriveCost; add CostOrZero for the reporter"
```

---

### Task 2: Export `comparator.ToolSequence`; add `Engine.Pricing()`

**Goal:** Two small accessors the reporter needs: a tool-call sequence and the run's pricing table.

**Files:**
- Modify: `internal/comparator/sequence.go:94` (export wrapper)
- Modify: `internal/engine/engine.go` (`Engine` struct + accessor), `internal/engine/build.go:38` (store pricing)
- Test: `internal/comparator/sequence_test.go`, `internal/engine/engine_test.go`

**Interfaces:**
- Produces: `func ToolSequence(t *trace.Trace) ([]string, error)`; `func (e *Engine) Pricing() core.Pricing`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/comparator/sequence_test.go
func TestToolSequence_Exported(t *testing.T) {
	tr := traceWithToolSpans("search", "fetch") // match existing helper in this file
	got, err := ToolSequence(tr)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := []string{"search", "fetch"}; !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

```go
// internal/engine/engine_test.go
func TestEngine_Pricing(t *testing.T) {
	cfg := config.Config{Pricing: config.Pricing{"m": {InputPerMTok: 1, OutputPerMTok: 2}}}
	eng, err := Build(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if eng.Pricing()["m"].InputPerMTok != 1 {
		t.Errorf("pricing not exposed: %+v", eng.Pricing())
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/comparator/ -run TestToolSequence_Exported ./internal/engine/ -run TestEngine_Pricing -v`
Expected: FAIL — `undefined: ToolSequence`, `eng.Pricing undefined`.

- [ ] **Step 3: Implement**

In `internal/comparator/sequence.go`, beside `ServiceSequence` (line 110):

```go
// ToolSequence returns the execute_tool tool names in start order (exported wrapper
// over toolSequence, mirroring ServiceSequence).
func ToolSequence(t *trace.Trace) ([]string, error) { return toolSequence(t) }
```

In `internal/engine/engine.go`, add a `pricing` field to `Engine` and an accessor:

```go
// (add to the Engine struct definition)
	pricing core.Pricing

// Pricing returns the per-model cost table wired at Build (may be nil).
func (e *Engine) Pricing() core.Pricing { return e.pricing }
```

In `internal/engine/build.go`, set it when constructing the Engine (line 38):

```go
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems, pricing: pricing}, nil
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/comparator/ ./internal/engine/`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/comparator/sequence.go internal/engine/engine.go internal/engine/build.go internal/comparator/sequence_test.go internal/engine/engine_test.go
go vet ./internal/comparator/ ./internal/engine/
git add internal/comparator/sequence.go internal/engine/engine.go internal/engine/build.go internal/comparator/sequence_test.go internal/engine/engine_test.go
git commit -m "feat(comparator,engine): export ToolSequence; expose Engine.Pricing"
```

---

### Task 3: Core types + `Reporter` interface + mock

**Goal:** Define the report data contract and the seam interface; regenerate the gomock mock.

**Files:**
- Modify: `internal/core/core.go`
- Regenerate: `internal/core/mocks/mock_core.go` (`go generate`)
- Test: compile-only here; behaviour covered downstream.

**Interfaces:**
- Produces: `core.RunReport`, `core.ScenarioResult`, `core.RunRecord`, `core.Reporter`.

- [ ] **Step 1: Add the types and interface**

In `internal/core/core.go` (after `Verdict`/`AggregateDetail` from Feature A), add:

```go
import "io" // add to the import block

// RunReport is the whole-run artifact a Reporter renders. Pure data.
type RunReport struct {
	Scenarios []ScenarioResult
	Total     int
	Passed    int
	Failed    int
	TotalCost float64
	StartedAt time.Time
	Duration  time.Duration
}

// ScenarioResult is one scenario's outcome, derived from its Evidence + Verdict.
type ScenarioResult struct {
	Name      string
	Tags      []string
	Pass      bool
	Reasons   []string
	Cost      float64
	Sequence  []string
	Runs      []RunRecord
	Aggregate *AggregateDetail
}

// RunRecord is one run within a scenario (one element per @runs iteration).
type RunRecord struct {
	RunID       string
	Passed      bool
	FailureKind string
	LatencyMS   int64
	Cost        float64
}

// Reporter renders a whole RunReport to a writer. Stateless; registered as an instance.
type Reporter interface {
	Report(rep RunReport, w io.Writer) error
}
```

(Add `"time"` to the import block if not already present.)

- [ ] **Step 2: Regenerate mocks**

Run: `go generate ./internal/core/... && go build ./...`
Expected: `internal/core/mocks/mock_core.go` now contains a `MockReporter`; build PASS.

- [ ] **Step 3: gofmt, vet, commit**

```bash
gofmt -w internal/core/core.go
go vet ./internal/core/...
git add internal/core/core.go internal/core/mocks/mock_core.go
git commit -m "feat(core): RunReport/ScenarioResult/RunRecord + Reporter interface (+mock)"
```

---

### Task 4: Reporter registry

**Goal:** Instance-based registry mirroring the matcher seam.

**Files:**
- Modify: `internal/registry/registry.go`
- Test: `internal/registry/registry_test.go`

**Interfaces:**
- Produces: `func RegisterReporter(name string, r core.Reporter)`, `func Reporter(name string) (core.Reporter, bool)`.

- [ ] **Step 1: Write the failing test**

```go
func TestReporterRegistry(t *testing.T) {
	RegisterReporter("fake", mocks.NewMockReporter(gomock.NewController(t)))
	if _, ok := Reporter("fake"); !ok {
		t.Fatal("registered reporter not found")
	}
	if _, ok := Reporter("nope"); ok {
		t.Fatal("unknown reporter unexpectedly found")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/registry/ -run TestReporterRegistry -v`
Expected: FAIL — `undefined: RegisterReporter`.

- [ ] **Step 3: Implement (mirror the matcher registry, `registry.go:60-64`)**

In `internal/registry/registry.go`, add to the `var (...)` block `reporters = map[string]core.Reporter{}`, then:

```go
// RegisterReporter registers a Reporter under the given name.
func RegisterReporter(name string, r core.Reporter) { reporters[name] = r }

// Reporter resolves a registered Reporter by name.
func Reporter(name string) (core.Reporter, bool) { r, ok := reporters[name]; return r, ok }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/registry/`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/registry/registry.go internal/registry/registry_test.go
go vet ./internal/registry/
git add internal/registry/registry.go internal/registry/registry_test.go
git commit -m "feat(registry): reporter seam (instance-based)"
```

---

### Task 5: `report.Derive` — Evidence → ScenarioResult

**Goal:** Project a scenario's `Verdict` + `[]Evidence` into a `core.ScenarioResult`, deriving cost/sequence/per-run rows.

**Files:**
- Create: `internal/report/derive.go`
- Test: `internal/report/derive_test.go`

**Interfaces:**
- Consumes: `comparator.CostOrZero`, `comparator.ToolSequence`, `comparator.ServiceSequence` (Tasks 1–2); `trace.Trace.Envelope`.
- Produces: `func Derive(name string, tags []string, v core.Verdict, evs []core.Evidence, pricing core.Pricing) (core.ScenarioResult, error)`.

- [ ] **Step 1: Write the failing test**

```go
package report

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestDerive(t *testing.T) {
	evs := []core.Evidence{
		{RunID: "a", Output: core.Output{Status: 200}},                // passed run, no trace -> cost 0, latency 0
		{RunID: "b", Failed: true, FailureKind: core.FailureKindResolve},
	}
	v := core.Verdict{Pass: false, Reasons: []string{"aggregate-cel failed: rate = 0.50, want >= 0.80"},
		Detail: &core.AggregateDetail{Macro: "rate", Op: ">=", Computed: 0.5, Expected: 0.8, PerRun: []float64{1, 0}}}

	sr, err := Derive("flaky scenario", []string{"@runs(2)"}, v, evs, nil)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if sr.Name != "flaky scenario" || sr.Pass {
		t.Errorf("name/pass = %q/%v", sr.Name, sr.Pass)
	}
	if len(sr.Runs) != 2 || sr.Runs[1].Passed || sr.Runs[1].FailureKind != core.FailureKindResolve {
		t.Errorf("runs = %+v", sr.Runs)
	}
	if sr.Aggregate == nil || sr.Aggregate.Computed != 0.5 {
		t.Errorf("aggregate = %+v", sr.Aggregate)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/report/ -run TestDerive -v`
Expected: FAIL — package/function undefined.

- [ ] **Step 3: Implement `Derive`**

```go
package report

import (
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Derive projects a scenario's Verdict + per-run Evidence into a ScenarioResult.
// Cost and sequence are derived from the Evidence forest (Evidence-only, invariant #1).
func Derive(name string, tags []string, v core.Verdict, evs []core.Evidence, pricing core.Pricing) (core.ScenarioResult, error) {
	sr := core.ScenarioResult{
		Name:      name,
		Tags:      tags,
		Pass:      v.Pass,
		Reasons:   v.Reasons,
		Aggregate: v.Detail,
	}
	for _, ev := range evs {
		cost, err := comparator.CostOrZero(ev.Trace, pricing)
		if err != nil {
			return core.ScenarioResult{}, err
		}
		rec := core.RunRecord{
			RunID:       ev.RunID,
			Passed:      !ev.Failed,
			FailureKind: ev.FailureKind,
			Cost:        cost,
		}
		if ev.Trace != nil {
			rec.LatencyMS = ev.Trace.Envelope().Milliseconds()
		}
		sr.Runs = append(sr.Runs, rec)
		sr.Cost += cost
	}
	if len(evs) > 0 && evs[0].Trace != nil {
		seq, err := sequence(evs[0].Trace)
		if err != nil {
			return core.ScenarioResult{}, err
		}
		sr.Sequence = seq
	}
	return sr, nil
}

// sequence returns the tool-call sequence (agents) or, if none, the service-hop
// sequence (microservices) for the representative run.
func sequence(tr *trace.Trace) ([]string, error) {
	tools, err := comparator.ToolSequence(tr)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		return tools, nil
	}
	return comparator.ServiceSequence(tr)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/report/ -run TestDerive -v`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/report/derive.go internal/report/derive_test.go
go vet ./internal/report/
git add internal/report/derive.go internal/report/derive_test.go
git commit -m "feat(report): Derive projects Evidence+Verdict into ScenarioResult"
```

---

### Task 6: `report.Collector`

**Goal:** A concurrency-safe run-scoped accumulator of `ScenarioResult`s, foldable into a `RunReport`.

**Files:**
- Create: `internal/report/collector.go`
- Test: `internal/report/collector_test.go`

**Interfaces:**
- Produces:
  - `type Collector struct { ... }`, `func NewCollector() *Collector`
  - `func (c *Collector) Append(sr core.ScenarioResult)`
  - `func (c *Collector) Report(started time.Time, dur time.Duration) core.RunReport`

- [ ] **Step 1: Write the failing test**

```go
func TestCollector(t *testing.T) {
	c := NewCollector()
	c.Append(core.ScenarioResult{Name: "a", Pass: true, Cost: 0.01})
	c.Append(core.ScenarioResult{Name: "b", Pass: false, Cost: 0.02})
	rep := c.Report(time.Unix(0, 0), 5*time.Second)
	if rep.Total != 2 || rep.Passed != 1 || rep.Failed != 1 {
		t.Errorf("totals = %+v", rep)
	}
	if rep.TotalCost != 0.03 {
		t.Errorf("total cost = %v", rep.TotalCost)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/report/ -run TestCollector -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
package report

import (
	"sync"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// Collector accumulates per-scenario results across a run. Append is safe for the
// concurrent scenarios godog may run (the -concurrency flag).
type Collector struct {
	mu        sync.Mutex
	scenarios []core.ScenarioResult
}

func NewCollector() *Collector { return &Collector{} }

func (c *Collector) Append(sr core.ScenarioResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scenarios = append(c.scenarios, sr)
}

// Report folds the accumulated scenarios into a RunReport with rollups.
func (c *Collector) Report(started time.Time, dur time.Duration) core.RunReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	rep := core.RunReport{StartedAt: started, Duration: dur, Total: len(c.scenarios)}
	rep.Scenarios = append(rep.Scenarios, c.scenarios...)
	for _, sr := range c.scenarios {
		if sr.Pass {
			rep.Passed++
		} else {
			rep.Failed++
		}
		rep.TotalCost += sr.Cost
	}
	return rep
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/report/ -run TestCollector -v`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/report/collector.go internal/report/collector_test.go
go vet ./internal/report/
git add internal/report/collector.go internal/report/collector_test.go
git commit -m "feat(report): concurrency-safe Collector + RunReport rollup"
```

---

### Task 7: JSON reporter

**Goal:** Marshal a `RunReport` to indented JSON.

**Files:**
- Create: `internal/report/json.go`
- Test: `internal/report/json_test.go`

**Interfaces:**
- Produces: `type jsonReporter struct{}` implementing `core.Reporter`; constructed via `report.RegisterBuiltins` (Task 9).

- [ ] **Step 1: Write the failing test**

```go
func TestJSONReporter(t *testing.T) {
	var buf bytes.Buffer
	rep := core.RunReport{Total: 1, Passed: 0, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "s", Pass: false, Reasons: []string{"rate = 0.50, want >= 0.80"}, Cost: 0.01},
	}}
	if err := (jsonReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("report: %v", err)
	}
	var round core.RunReport
	if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if round.Failed != 1 || round.Scenarios[0].Name != "s" {
		t.Errorf("round-trip lost data: %+v", round)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/report/ -run TestJSONReporter -v`
Expected: FAIL — `undefined: jsonReporter`.

- [ ] **Step 3: Implement**

```go
package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
)

type jsonReporter struct{}

func (jsonReporter) Report(rep core.RunReport, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("report: encoding json: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/report/ -run TestJSONReporter -v`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/report/json.go internal/report/json_test.go
go vet ./internal/report/
git add internal/report/json.go internal/report/json_test.go
git commit -m "feat(report): json reporter"
```

---

### Task 8: HTML reporter

**Goal:** Render a `RunReport` to a self-contained HTML page (summary, per-scenario rows, reasons, per-run table).

**Files:**
- Create: `internal/report/html.go`
- Test: `internal/report/html_test.go`

**Interfaces:**
- Produces: `type htmlReporter struct{}` implementing `core.Reporter`.

- [ ] **Step 1: Write the failing test**

```go
func TestHTMLReporter(t *testing.T) {
	var buf bytes.Buffer
	rep := core.RunReport{Total: 1, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "flaky", Pass: false, Cost: 0.0125,
			Reasons: []string{"rate = 0.50, want >= 0.80"},
			Runs:    []core.RunRecord{{RunID: "abc", Passed: true, LatencyMS: 120}},
			Aggregate: &core.AggregateDetail{Macro: "rate", Op: ">=", Computed: 0.5, Expected: 0.8}},
	}}
	if err := (htmlReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("report: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"<html", "flaky", "rate = 0.50, want >= 0.80", "abc", "0.0125"} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/report/ -run TestHTMLReporter -v`
Expected: FAIL — `undefined: htmlReporter`.

- [ ] **Step 3: Implement**

```go
package report

import (
	"fmt"
	"html/template"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
)

var htmlTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Mentat report</title>
<style>body{font-family:system-ui,sans-serif;margin:2rem}table{border-collapse:collapse}
td,th{border:1px solid #ccc;padding:.25rem .5rem}.fail{color:#b00}.pass{color:#080}</style>
</head><body>
<h1>Mentat run</h1>
<p>{{.Total}} scenarios — <span class="pass">{{.Passed}} passed</span>,
<span class="fail">{{.Failed}} failed</span> — total cost ${{printf "%.4f" .TotalCost}}</p>
{{range .Scenarios}}
<h2 class="{{if .Pass}}pass{{else}}fail{{end}}">{{.Name}}</h2>
<p>cost ${{printf "%.4f" .Cost}}{{if .Sequence}} — sequence: {{range .Sequence}}{{.}} {{end}}{{end}}</p>
{{if not .Pass}}<ul>{{range .Reasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Aggregate}}<p>{{.Aggregate.Macro}} = {{printf "%.2f" .Aggregate.Computed}}, want {{.Aggregate.Op}} {{printf "%.2f" .Aggregate.Expected}}</p>{{end}}
{{if .Runs}}<table><tr><th>run</th><th>passed</th><th>kind</th><th>latency ms</th><th>cost</th></tr>
{{range .Runs}}<tr><td>{{.RunID}}</td><td>{{.Passed}}</td><td>{{.FailureKind}}</td><td>{{.LatencyMS}}</td><td>{{printf "%.4f" .Cost}}</td></tr>{{end}}
</table>{{end}}
{{end}}
</body></html>`))

type htmlReporter struct{}

func (htmlReporter) Report(rep core.RunReport, w io.Writer) error {
	if err := htmlTmpl.Execute(w, rep); err != nil {
		return fmt.Errorf("report: executing html template: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/report/ -run TestHTMLReporter -v`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/report/html.go internal/report/html_test.go
go vet ./internal/report/
git add internal/report/html.go internal/report/html_test.go
git commit -m "feat(report): html reporter"
```

---

### Task 9: `RegisterBuiltins` + engine wiring

**Goal:** Register `json`/`html` at the composition root.

**Files:**
- Create: `internal/report/register.go`
- Modify: `internal/engine/build.go:28` (call site)
- Test: `internal/report/register_test.go`

**Interfaces:**
- Produces: `func RegisterBuiltins()` registering `"json"` and `"html"`.

- [ ] **Step 1: Write the failing test**

```go
func TestRegisterBuiltins(t *testing.T) {
	RegisterBuiltins()
	for _, name := range []string{"json", "html"} {
		if _, ok := registry.Reporter(name); !ok {
			t.Errorf("reporter %q not registered", name)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/report/ -run TestRegisterBuiltins -v`
Expected: FAIL — `undefined: RegisterBuiltins`.

- [ ] **Step 3: Implement + wire**

`internal/report/register.go`:

```go
package report

import "github.com/thetonymaster/mentat/internal/registry"

// RegisterBuiltins registers the built-in reporters at the composition root.
func RegisterBuiltins() {
	registry.RegisterReporter("json", jsonReporter{})
	registry.RegisterReporter("html", htmlReporter{})
}
```

In `internal/engine/build.go`, after `comparator.RegisterBuiltinMatchers()` (line 28):

```go
	report.RegisterBuiltins()
```

and add `"github.com/thetonymaster/mentat/internal/report"` to the import block.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/report/ ./internal/engine/`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/report/register.go internal/engine/build.go internal/report/register_test.go
go vet ./internal/report/ ./internal/engine/
git add internal/report/register.go internal/engine/build.go internal/report/register_test.go
git commit -m "feat(report,engine): register json+html reporters at composition root"
```

---

### Task 10: Steps wiring — capture results into the Collector

**Goal:** A Collector-aware initializer; the world captures the aggregate `Detail` and, on scenario end, derives a `ScenarioResult` and appends it.

**Files:**
- Modify: `internal/steps/steps.go` (`world`, `Initializer`, `checkRuns`, new After hook + helper)
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `report.Derive`, `report.Collector`, `core.Verdict`, `eng.Pricing()`.
- Produces: `func InitializerWithCollector(eng *engine.Engine, col *report.Collector) func(*godog.ScenarioContext)`. The existing `Initializer(eng)` delegates with a throwaway collector to preserve current callers/tests.

- [ ] **Step 1: Write the failing test**

```go
func TestInitializer_CollectsResults(t *testing.T) {
	// Drive a scenario through a stub engine that yields known evidence, then assert
	// the collector received one ScenarioResult. Reuse the existing steps_test harness
	// (mocked engine / inmem store) — match its setup helpers.
	col := report.NewCollector()
	// ... build eng with a stub target that returns one Evidence ...
	// ... run a minimal feature via godog.TestSuite with InitializerWithCollector(eng, col) ...
	rep := col.Report(time.Unix(0, 0), 0)
	if rep.Total != 1 {
		t.Fatalf("collector got %d scenarios, want 1", rep.Total)
	}
}
```

> Implementer note: `internal/steps` already has a test harness that runs godog programmatically (it must, to test the steps). Mirror it; if the existing tests call step methods directly rather than via a suite, add the smallest godog `TestSuite` run that exercises one passing scenario and asserts `col.Report().Total == 1`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/steps/ -run TestInitializer_CollectsResults -v`
Expected: FAIL — `undefined: InitializerWithCollector`.

- [ ] **Step 3: Implement the wiring**

Add a `lastDetail` field and a collector to `world`:

```go
type world struct {
	eng       *engine.Engine
	col       *report.Collector
	target    string
	ev        core.Evidence
	evs       []core.Evidence
	n         int
	parallel  bool
	lastDetail *core.AggregateDetail
}
```

Refactor `Initializer` to delegate, and add the collector-aware variant:

```go
// Initializer preserves the existing signature; results go to a discarded collector.
func Initializer(eng *engine.Engine) func(*godog.ScenarioContext) {
	return InitializerWithCollector(eng, report.NewCollector())
}

// InitializerWithCollector binds the grammar and records one ScenarioResult per
// scenario into col.
func InitializerWithCollector(eng *engine.Engine, col *report.Collector) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &world{eng: eng, col: col}
		// ... existing sc.Step(...) registrations unchanged ...
		// ... existing sc.Before(...) unchanged ...

		sc.After(func(ctx context.Context, scenario *godog.Scenario, stepErr error) (context.Context, error) {
			v := core.Verdict{Pass: stepErr == nil, Detail: w.lastDetail}
			if stepErr != nil {
				v.Reasons = []string{stepErr.Error()}
			}
			sr, err := report.Derive(scenario.Name, tagNames(scenario.Tags), v, w.evs, w.eng.Pricing())
			if err != nil {
				return ctx, err
			}
			w.col.Append(sr)
			return ctx, nil
		})
	}
}

func tagNames(tags []*messages.PickleTag) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Name)
	}
	return out
}
```

Capture the aggregate detail in `checkRuns` (it currently discards the verdict, `steps.go:237-244`):

```go
	v, err := c.Aggregate(context.Background(), w.evs, comparator.AggregateCELExpectation{Expr: expr})
	if err != nil {
		return fmt.Errorf("aggregate-cel: %w", err)
	}
	w.lastDetail = v.Detail
	if !v.Pass {
		return fmt.Errorf("aggregate-cel failed: %s", strings.Join(v.Reasons, "; "))
	}
	return nil
```

Add the `report` import to `steps.go`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/steps/...`
Expected: PASS — new test green; existing steps tests unaffected (they use `Initializer`, which now delegates).

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/steps/
git add internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): collect per-scenario ScenarioResult; capture aggregate detail"
```

---

### Task 11: `cmd/mentat` — flags + emit reports after the suite

**Goal:** Add `--report-json`/`--report-html`; after `suite.Run()`, fold the collector into a `RunReport` and invoke the selected reporters; a write failure is fatal.

**Files:**
- Modify: `cmd/mentat/main.go`
- Test: `cmd/mentat/main_test.go` (a focused helper test) — or fold into the L3 e2e (Task 12). Add a unit test for the emit helper.

**Interfaces:**
- Consumes: `report.NewCollector`, `steps.InitializerWithCollector`, `registry.Reporter`, `eng.Pricing()`.

- [ ] **Step 1: Write a failing unit test for the emit helper**

```go
// cmd/mentat/main_test.go
func TestEmitReports_UnknownReporter(t *testing.T) {
	err := emitReports(core.RunReport{}, map[string]string{"nope": "/dev/null"})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("want unknown-reporter error, got %v", err)
	}
}

func TestEmitReports_WriteAndReadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	registry.RegisterReporter("json", reporterStubJSON{}) // or rely on report.RegisterBuiltins() in init
	err := emitReports(core.RunReport{Total: 1}, map[string]string{"json": path})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("report file not written: %v", err)
	}
}
```

> Implementer note: call `report.RegisterBuiltins()` in the test (or an `init`) so `"json"` resolves; drop `reporterStubJSON` if using the real builtin.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/mentat/ -run TestEmitReports -v`
Expected: FAIL — `undefined: emitReports`.

- [ ] **Step 3: Implement flags, collector wiring, and `emitReports`**

In `main()`:

```go
	reportJSON := fs.String("report-json", "", "write a JSON run report to this file")
	reportHTML := fs.String("report-html", "", "write an HTML run report to this file")
```

Replace the suite construction/run to thread a collector:

```go
	col := report.NewCollector()
	suite := godog.TestSuite{ScenarioInitializer: steps.InitializerWithCollector(eng, col), Options: &opts}
	started := time.Now()
	code := suite.Run()
	if junitFile != nil {
		if err := junitFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "mentat: close junit file %q: %v\n", *junit, err)
			if code == 0 {
				code = 1
			}
		}
	}
	targets := map[string]string{}
	if *reportJSON != "" {
		targets["json"] = *reportJSON
	}
	if *reportHTML != "" {
		targets["html"] = *reportHTML
	}
	if len(targets) > 0 {
		if err := emitReports(col.Report(started, time.Since(started)), targets); err != nil {
			fmt.Fprintln(os.Stderr, "mentat:", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
```

Add the helper:

```go
// emitReports writes each selected report. A failure (unknown reporter, create/encode
// error) is returned — never swallowed (invariant #4). The caller turns it into a
// non-zero exit.
func emitReports(rep core.RunReport, targets map[string]string) error {
	for name, path := range targets {
		r, ok := registry.Reporter(name)
		if !ok {
			return fmt.Errorf("unknown reporter %q", name)
		}
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create %s report %q: %w", name, path, err)
		}
		if err := r.Report(rep, f); err != nil {
			f.Close()
			return fmt.Errorf("writing %s report %q: %w", name, path, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %s report %q: %w", name, path, err)
		}
	}
	return nil
}
```

Add imports: `core`, `registry`, `report`. (`engine.Build` already calls `report.RegisterBuiltins`, so the registry is populated before `emitReports` runs.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/mentat/...`
Expected: PASS.

- [ ] **Step 5: Manual smoke (optional but recommended)**

Run: `go run ./cmd/mentat run features --report-json /tmp/mentat.json --report-html /tmp/mentat.html` (against an existing feature dir + config)
Expected: both files written; `cat /tmp/mentat.json | jq .Total` prints a number.

- [ ] **Step 6: gofmt, vet, commit**

```bash
gofmt -w cmd/mentat/main.go cmd/mentat/main_test.go
go vet ./cmd/mentat/
git add cmd/mentat/main.go cmd/mentat/main_test.go
git commit -m "feat(cmd): --report-json/--report-html emit run reports after the suite"
```

---

### Task 12: L3 meta-test — the report reflects failure; bad path exits non-zero

**Goal:** Prove end-to-end that a failing run produces a JSON report with `Pass:false`, and that an unwritable path exits non-zero.

**Files:**
- Modify: `e2e/meta_test.go` (`//go:build e2e`)

- [ ] **Step 1: Inspect the existing L3 harness**

Run: `sed -n '1,60p' e2e/meta_test.go`
Expected: reuse its `go run ./cmd/mentat` invocation helper.

- [ ] **Step 2: Write the failing assertions**

```go
func TestL3_ReportReflectsFailure(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "r.json")
	runMentatWithArgs(t, "--report-json", jsonPath, badFeaturePath) // reuse/extend the existing helper
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	var rep core.RunReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if rep.Failed == 0 {
		t.Fatalf("expected at least one failed scenario in report: %+v", rep)
	}
}

func TestL3_UnwritableReportExitsNonZero(t *testing.T) {
	code := runMentatExitCode(t, "--report-json", "/this/dir/does/not/exist/r.json", anyFeaturePath)
	if code == 0 {
		t.Fatal("expected non-zero exit for unwritable report path")
	}
}
```

> Implementer note: the existing meta-test almost certainly already has a helper that runs the binary and returns output/exit code — extend it to pass extra args and to return the exit code. `badFeaturePath`/`anyFeaturePath` are existing fixtures used by the current L3 cases.

- [ ] **Step 3: Run the e2e suite**

Run: `make harness-up && go test -tags e2e ./e2e/ -run 'TestL3_Report|TestL3_Unwritable' -v`
Expected: PASS.

- [ ] **Step 4: Full e2e regression**

Run: `go test -tags e2e ./e2e/...`
Expected: PASS.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w e2e/meta_test.go
go vet -tags e2e ./e2e/
git add e2e/meta_test.go
git commit -m "test(e2e): L3 — report reflects failure; unwritable path exits non-zero"
```

---

### Task 13: Final gate + PR

**Files:** none.

- [ ] **Step 1: Whole-repo format/vet/test**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: clean; all hermetic tests PASS.

- [ ] **Step 2: Coverage floor**

Run: `go test ./internal/report/ ./internal/steps/ ./internal/comparator/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1`
Expected: each ≥ 80%. If `internal/report` is below, add table rows to `derive_test.go` (single-run with trace + cost; tools-vs-services selection).

- [ ] **Step 3: Open the PR**

```bash
git push -u origin feat/mentat-reporter-seam-impl
gh pr create --title "feat(report): Reporter seam — json + html run reports" --body "Implements docs/superpowers/specs/2026-06-20-mentat-reporter-seam-design.md. Consumes Feature A's AggregateDetail."
```

---

## Self-Review

- **Spec coverage:** §3.1 Reporter iface → Task 3; §3.2 registry → Task 4; §3.3 RegisterBuiltins → Task 9; §4 types → Task 3; §4.1 Derive → Task 5; §4.3 CostOrZero/deriveCost → Task 1; §5 reporters-after-suite + Collector → Tasks 6, 10, 11; §6 error handling (fatal write, empty run, nil-trace records) → Tasks 1 (nil trace→0), 5, 11; §7 tests (Derive, json, html, registry, cmd wiring via mock) → Tasks 5,7,8,4,11; L3 → Task 12; coverage → Task 13. Aggregate detail consumed (not produced) → Task 5/10 via Feature A. No gaps.
- **Type consistency:** `CostOrZero(t,pricing)`, `ToolSequence(t)`, `Engine.Pricing()`, `core.{RunReport,ScenarioResult,RunRecord,Reporter,AggregateDetail}`, `Derive(name,tags,v,evs,pricing)`, `Collector.{Append,Report}`, `RegisterReporter/Reporter`, `RegisterBuiltins`, `InitializerWithCollector`, `emitReports(rep,targets)` — consistent across tasks.
- **Spec refinements flagged (additive plumbing, not design changes):** `comparator.ToolSequence` exported (spec §4.1 implied "ctl/format helpers"); `Engine.Pricing()` accessor (the reporter needs the run's pricing); `Derive` returns `(ScenarioResult, error)` rather than the spec's error-free signature, because `CostOrZero` can error on corrupt cost — propagated, not swallowed.
- **Cross-feature dependency:** Tasks 5 and 10 reference `core.AggregateDetail`/`Verdict.Detail` from Feature A; Task 0 hard-checks Feature A is merged before starting.
