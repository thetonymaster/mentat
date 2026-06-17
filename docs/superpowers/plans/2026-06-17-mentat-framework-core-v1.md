# Mentat Framework Core v1 (Plan 2a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the hermetic, unit-testable core of Mentat — the `Trace` forest model, core contracts, a file/in-memory `TraceStore`, the three v1 comparators (sequence, budgets, result), the `Correlator` with stable-poll, and the shell driver — all validated against Plan 1's golden fixtures with zero infrastructure.

**Architecture:** Comparators consume `Evidence` (a `Trace` forest + driver `Output`) and return a `Verdict`. Everything is an interface resolved later by the composition root (Plan 2b). This plan delivers the libraries those interfaces describe; Plan 2b wires them into godog + CLIs + live Tempo.

**Tech Stack:** Go 1.23+, standard library (`os/exec`, `encoding/json`, `regexp`, `sort`, `strconv`, `strings`, `time`). No third-party deps in core. Tests load Plan 1's `testdata/traces/researchbot/*.json` fixtures.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat` (already initialized in Plan 1).
- **Prerequisite:** Plan 1 is complete and `testdata/traces/researchbot/*.json` are committed (Task 3+ tests load them).
- **Pinned GenAI keys** live in `internal/genai` and are the single source of truth (same string values Plan 1's `researchbot` emits): `gen_ai.operation.name`, `gen_ai.tool.name`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.usage.cost_usd`, operation values `invoke_agent`/`chat`/`execute_tool`.
- **Answer-extraction:** `core.ExtractAnswer(stdout) = strings.TrimSpace(stdout)` (the SUT writes only its result to stdout).
- **Comparators never touch a store or driver** — only `Evidence`.
- **No silent fallbacks:** a comparator that cannot evaluate (missing required attribute, malformed expectation) returns an `error`, not a false pass.
- **Commits:** files added individually; no AI attribution.

## File Structure

```
internal/genai/keys.go              pinned gen_ai.* attribute keys + op values
internal/trace/trace.go             Span, Trace (forest), attr/op/envelope helpers
internal/trace/trace_test.go
internal/core/core.go               Output, Evidence, Verdict, Comparator, RunSpec,
                                    RunResult, Driver, TraceStore, Correlator, ExtractAnswer
internal/core/core_test.go
internal/store/filestore.go         load Plan 1 fixtures -> *trace.Trace; FileStore + InMemStore
internal/store/filestore_test.go
internal/comparator/sequence.go     ordered-subsequence + forbidden over tool spans
internal/comparator/sequence_test.go
internal/comparator/budgets.go      tokens / cost / latency / error thresholds
internal/comparator/budgets_test.go
internal/comparator/result.go       deterministic matchers (exact/contains/regex/json-subset/status)
internal/comparator/result_test.go
internal/correlate/correlate.go     TagCorrelator: inject test.run.id, resolve+merge+stable-poll
internal/correlate/correlate_test.go
internal/driver/shell.go            ShellDriver: exec SUT with OTEL env, capture Output
internal/driver/shell_test.go
```

---

### Task 1: GenAI keys + Trace forest model

**Files:**
- Create: `internal/genai/keys.go`
- Create: `internal/trace/trace.go`
- Test: `internal/trace/trace_test.go`

**Interfaces:**
- Produces: `genai` constants (`Op`, `ToolName`, `InTokens`, `OutTokens`, `CostUSD`, `OpInvokeAgent`, `OpChat`, `OpExecuteTool`).
- Produces: `trace.Span{ID,ParentID,Name,Kind,Status string; Start,End time.Time; Attrs map[string]string}`
  with `Attr(k) string`, `AttrInt(k)(int,bool)`, `AttrFloat(k)(float64,bool)`;
  `trace.Trace{RunID string; Roots,Spans []*Span}` with `ByOp(op string) []*Span` (stable-sorted by Start)
  and `Envelope() time.Duration`.

- [ ] **Step 1: Add the pinned keys**

Create `internal/genai/keys.go`:
```go
// Package genai holds the OTel GenAI attribute keys Mentat reads. Single source
// of truth; mirrors the values researchbot emits.
package genai

const (
	Op        = "gen_ai.operation.name"
	ToolName  = "gen_ai.tool.name"
	InTokens  = "gen_ai.usage.input_tokens"
	OutTokens = "gen_ai.usage.output_tokens"
	CostUSD   = "gen_ai.usage.cost_usd"

	OpInvokeAgent = "invoke_agent"
	OpChat        = "chat"
	OpExecuteTool = "execute_tool"
)
```

- [ ] **Step 2: Write the failing test**

Create `internal/trace/trace_test.go`:
```go
package trace

import (
	"testing"
	"time"
)

func TestByOpIsStableSortedAndEnvelopeSpansForest(t *testing.T) {
	t0 := time.Unix(0, 0)
	tr := &Trace{
		RunID: "r1",
		Spans: []*Span{
			{Name: "invoke_agent", Start: t0, End: t0.Add(3 * time.Second), Attrs: map[string]string{"gen_ai.operation.name": "invoke_agent"}},
			{Name: "execute_tool search", Start: t0.Add(1 * time.Second), End: t0.Add(2 * time.Second), Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"}},
			{Name: "execute_tool summarize", Start: t0.Add(2 * time.Second), End: t0.Add(2500 * time.Millisecond), Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "summarize"}},
		},
	}
	tools := tr.ByOp("execute_tool")
	if len(tools) != 2 || tools[0].Attr("gen_ai.tool.name") != "search" || tools[1].Attr("gen_ai.tool.name") != "summarize" {
		t.Fatalf("ByOp order wrong: %v", tools)
	}
	if tr.Envelope() != 3*time.Second {
		t.Fatalf("envelope = %v, want 3s", tr.Envelope())
	}
}

func TestAttrIntAndFloat(t *testing.T) {
	s := &Span{Attrs: map[string]string{"gen_ai.usage.input_tokens": "1200", "gen_ai.usage.cost_usd": "0.018"}}
	if n, ok := s.AttrInt("gen_ai.usage.input_tokens"); !ok || n != 1200 {
		t.Fatalf("AttrInt = %d %v", n, ok)
	}
	if f, ok := s.AttrFloat("gen_ai.usage.cost_usd"); !ok || f != 0.018 {
		t.Fatalf("AttrFloat = %f %v", f, ok)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/trace/ -v`
Expected: FAIL — `undefined: Trace` / `Span`.

- [ ] **Step 4: Write the implementation**

Create `internal/trace/trace.go`:
```go
package trace

import (
	"sort"
	"strconv"
	"time"
)

type Span struct {
	ID       string
	ParentID string
	Name     string
	Kind     string
	Status   string
	Start    time.Time
	End      time.Time
	Attrs    map[string]string
}

func (s *Span) Attr(k string) string { return s.Attrs[k] }

func (s *Span) AttrInt(k string) (int, bool) {
	v, ok := s.Attrs[k]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

func (s *Span) AttrFloat(k string) (float64, bool) {
	v, ok := s.Attrs[k]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// Trace is a forest: one or more root traces merged by run id (spec §5).
type Trace struct {
	RunID string
	Roots []*Span
	Spans []*Span
}

// ByOp returns spans whose gen_ai.operation.name == op, stable-sorted by start
// time. Stable sort keeps insertion order when timestamps are equal (e.g. for
// timestamp-free fixtures), preserving emit order.
func (t *Trace) ByOp(op string) []*Span {
	var out []*Span
	for _, s := range t.Spans {
		if s.Attrs["gen_ai.operation.name"] == op {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Envelope is the run's wall-clock span: max(end) - min(start) across all spans.
func (t *Trace) Envelope() time.Duration {
	if len(t.Spans) == 0 {
		return 0
	}
	min, max := t.Spans[0].Start, t.Spans[0].End
	for _, s := range t.Spans {
		if s.Start.Before(min) {
			min = s.Start
		}
		if s.End.After(max) {
			max = s.End
		}
	}
	return max.Sub(min)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/trace/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/genai/keys.go internal/trace/trace.go internal/trace/trace_test.go
git commit -m "feat(core): gen_ai keys and Trace forest model"
```

---

### Task 2: Core contracts (Output, Evidence, Verdict, RunSpec, interfaces)

**Files:**
- Create: `internal/core/core.go`
- Test: `internal/core/core_test.go`

**Interfaces:**
- Consumes: `trace.Trace` (Task 1).
- Produces: `core.Output`, `core.Evidence`, `core.Verdict`, `core.Expectation` (alias `any`),
  `core.Comparator`, `core.RunSpec`, `core.RunResult`, `core.Driver`, `core.TraceQuery`,
  `core.TraceRef`, `core.StoreCaps`, `core.TraceStore`, `core.Correlator`,
  `core.ExtractAnswer(string) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/core_test.go`:
```go
package core

import "testing"

func TestExtractAnswerTrimsWhitespace(t *testing.T) {
	if got := ExtractAnswer("  the answer\n"); got != "the answer" {
		t.Fatalf("ExtractAnswer = %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/ -v`
Expected: FAIL — `undefined: ExtractAnswer`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/core.go`:
```go
package core

import (
	"context"
	"strings"

	"github.com/thetonymaster/mentat/internal/trace"
)

// Output is the driver-captured boundary result of a run.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int    // shell adapters
	Status   int    // http adapters (HTTP status)
	Body     []byte // http adapters
	Answer   string // extracted result (see ExtractAnswer)
}

// Evidence is everything a comparator may inspect about a single run.
type Evidence struct {
	RunID  string
	Trace  *trace.Trace
	Output Output
}

type Verdict struct {
	Pass    bool
	Reasons []string
}

// Expectation is comparator-specific config; each comparator type-asserts its own.
type Expectation = any

type Comparator interface {
	Name() string
	Compare(ctx context.Context, ev Evidence, e Expectation) (Verdict, error)
}

// RunSpec is the driver input. The adapter applies RunID/Tags via its transport.
type RunSpec struct {
	Target  string
	Adapter string
	Command []string // shell adapter argv
	Env     map[string]string
	Input   string // prompt / request body
	RunID   string
	Tags    map[string]string // test.run.id, test.scenario, test.case
}

type RunResult struct {
	RunID          string
	PrimaryTraceID string
	Output         Output
}

type Driver interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

type TraceQuery struct {
	Tag   string // e.g. "test.run.id"
	Value string
}

type TraceRef struct{ TraceID string }

type StoreCaps struct{ StructuralQuery bool }

type TraceStore interface {
	GetByID(ctx context.Context, id string) (*trace.Trace, error)
	Query(ctx context.Context, q TraceQuery) ([]TraceRef, error)
	Caps() StoreCaps
}

type Correlator interface {
	Inject(ctx context.Context, spec *RunSpec) (runID string)
	Resolve(ctx context.Context, store TraceStore, runID string) (*trace.Trace, error)
}

// ExtractAnswer applies the project-wide convention: stdout is the result.
func ExtractAnswer(stdout string) string { return strings.TrimSpace(stdout) }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/core/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/core.go internal/core/core_test.go
git commit -m "feat(core): Evidence/Verdict/Driver/TraceStore/Correlator contracts"
```

---

### Task 3: File/in-memory TraceStore (load Plan 1 fixtures)

**Files:**
- Create: `internal/store/filestore.go`
- Test: `internal/store/filestore_test.go`

**Interfaces:**
- Consumes: `trace.Trace`/`Span` (Task 1), `core.TraceStore`/`TraceQuery`/`TraceRef`/`StoreCaps` (Task 2), Plan 1 fixtures.
- Produces: `func LoadFixture(data []byte) (*trace.Trace, error)`;
  `func NewInMemStore(byRunID map[string]*trace.Trace) *InMemStore` implementing `core.TraceStore`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/filestore_test.go`:
```go
package store

import (
	"context"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestLoadFixtureBuildsForestFromPlan1Golden(t *testing.T) {
	data, err := os.ReadFile("../../testdata/traces/researchbot/happy.json")
	if err != nil {
		t.Fatalf("read fixture (run Plan 1 capture first): %v", err)
	}
	tr, err := LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(tr.Roots) != 1 || tr.Roots[0].Name != "invoke_agent researchbot" {
		t.Fatalf("root wrong: %+v", tr.Roots)
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if len(tools) < 3 {
		t.Fatalf("want >=3 tool spans, got %d", len(tools))
	}
}

func TestInMemStoreResolvesByRunID(t *testing.T) {
	data, _ := os.ReadFile("../../testdata/traces/researchbot/happy.json")
	tr, _ := LoadFixture(data)
	tr.RunID = "r1"
	st := NewInMemStore(map[string]*trace.Trace{"r1": tr})
	refs, err := st.Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "r1"})
	if err != nil || len(refs) != 1 {
		t.Fatalf("Query: refs=%v err=%v", refs, err)
	}
}
```
- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL — `undefined: LoadFixture`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/filestore.go`:
```go
package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// fixture mirrors Plan 1's tracelab capture format.
type fixture struct {
	RunScenario string `json:"runScenario"`
	Spans       []struct {
		Name        string            `json:"name"`
		Op          string            `json:"op"`
		ParentIndex int               `json:"parentIndex"`
		Attrs       map[string]string `json:"attrs"`
		Status      string            `json:"status"`
	} `json:"spans"`
}

// LoadFixture parses a captured fixture into a Trace forest. Parentage is by index;
// captured spans carry no timestamps, so ByOp relies on stable order (Task 1).
func LoadFixture(data []byte) (*trace.Trace, error) {
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	tr := &trace.Trace{}
	spans := make([]*trace.Span, len(fx.Spans))
	for i, fs := range fx.Spans {
		spans[i] = &trace.Span{Name: fs.Name, Status: fs.Status, Attrs: fs.Attrs}
	}
	for i, fs := range fx.Spans {
		if fs.ParentIndex >= 0 && fs.ParentIndex < len(spans) {
			spans[i].ParentID = spans[fs.ParentIndex].Name // synthetic id; names are unique within a fixture
		} else {
			tr.Roots = append(tr.Roots, spans[i])
		}
	}
	tr.Spans = spans
	return tr, nil
}

// InMemStore serves preloaded traces by run id; for L1 unit tests, zero infra.
type InMemStore struct{ byRunID map[string]*trace.Trace }

func NewInMemStore(byRunID map[string]*trace.Trace) *InMemStore {
	return &InMemStore{byRunID: byRunID}
}

func (s *InMemStore) GetByID(_ context.Context, id string) (*trace.Trace, error) {
	if tr, ok := s.byRunID[id]; ok {
		return tr, nil
	}
	return nil, fmt.Errorf("inmem store: no trace %q", id)
}

func (s *InMemStore) Query(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	if q.Tag != "test.run.id" {
		return nil, fmt.Errorf("inmem store: only test.run.id queries supported, got %q", q.Tag)
	}
	if _, ok := s.byRunID[q.Value]; ok {
		return []core.TraceRef{{TraceID: q.Value}}, nil
	}
	return nil, nil
}

func (s *InMemStore) Caps() core.StoreCaps { return core.StoreCaps{StructuralQuery: false} }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS (requires Plan 1 fixtures committed at `testdata/traces/researchbot/`).

- [ ] **Step 5: Commit**

```bash
git add internal/store/filestore.go internal/store/filestore_test.go
git commit -m "feat(store): file/in-memory TraceStore loading Plan 1 fixtures"
```

---

### Task 4: Sequence comparator

**Files:**
- Create: `internal/comparator/sequence.go`
- Test: `internal/comparator/sequence_test.go`

**Interfaces:**
- Consumes: `core.Evidence`/`Verdict`/`Comparator` (Task 2), `trace.ByOp` (Task 1), `genai` (Task 1).
- Produces: `type SequenceExpectation struct{ Order, Forbidden []string }`;
  `func NewSequence() core.Comparator` (Name `"sequence"`).

- [ ] **Step 1: Write the failing test**

Create `internal/comparator/sequence_test.go`:
```go
package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func toolTrace(names ...string) *trace.Trace {
	tr := &trace.Trace{}
	for _, n := range names {
		tr.Spans = append(tr.Spans, &trace.Span{
			Name:  "execute_tool " + n,
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: n},
		})
	}
	return tr
}

func TestSequencePassesOnOrderedSubsequence(t *testing.T) {
	ev := core.Evidence{Trace: toolTrace("search", "fetch_doc", "summarize")}
	v, err := NewSequence().Compare(context.Background(), ev, SequenceExpectation{Order: []string{"search", "summarize"}})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
}

func TestSequenceFailsOnWrongOrder(t *testing.T) {
	ev := core.Evidence{Trace: toolTrace("summarize", "search")}
	v, _ := NewSequence().Compare(context.Background(), ev, SequenceExpectation{Order: []string{"search", "summarize"}})
	if v.Pass {
		t.Fatal("want fail on reversed order")
	}
}

func TestSequenceFailsOnForbiddenTool(t *testing.T) {
	ev := core.Evidence{Trace: toolTrace("search", "delete_record", "summarize")}
	v, _ := NewSequence().Compare(context.Background(), ev, SequenceExpectation{Forbidden: []string{"delete_record"}})
	if v.Pass {
		t.Fatal("want fail on forbidden tool")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/comparator/ -run TestSequence -v`
Expected: FAIL — `undefined: NewSequence`.

- [ ] **Step 3: Write the implementation**

Create `internal/comparator/sequence.go`:
```go
package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
)

type SequenceExpectation struct {
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
	var actual []string
	for _, s := range ev.Trace.ByOp(genai.OpExecuteTool) {
		actual = append(actual, s.Attr(genai.ToolName))
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
			v.Reasons = append(v.Reasons, fmt.Sprintf("forbidden tool %q was called", a))
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
			v.Reasons = append(v.Reasons, fmt.Sprintf("expected tool %q not found in order; actual sequence = %v", want, actual))
		}
	}
	return v, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/comparator/ -run TestSequence -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/comparator/sequence.go internal/comparator/sequence_test.go
git commit -m "feat(comparator): sequence (ordered subsequence + forbidden)"
```

---

### Task 5: Budgets comparator

**Files:**
- Create: `internal/comparator/budgets.go`
- Test: `internal/comparator/budgets_test.go`

**Interfaces:**
- Consumes: `core.Evidence`/`Comparator` (Task 2), `trace.Envelope`/`AttrInt`/`AttrFloat` (Task 1), `genai` (Task 1).
- Produces: `type BudgetExpectation struct{ MaxTokens *int; MaxCostUSD *float64; MaxLatency *time.Duration; MaxErrors *int }`;
  `func NewBudgets() core.Comparator` (Name `"budgets"`); helper `func IntPtr(int) *int`.

- [ ] **Step 1: Write the failing test**

Create `internal/comparator/budgets_test.go`:
```go
package comparator

import (
	"context"
	"strconv"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func tokenTrace(in, out int) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "invoke_agent",
		Attrs: map[string]string{
			genai.Op:        genai.OpInvokeAgent,
			genai.InTokens:  strconv.Itoa(in),
			genai.OutTokens: strconv.Itoa(out),
		},
	}}}
}

func TestBudgetsPassesUnderTokenCap(t *testing.T) {
	ev := core.Evidence{Trace: tokenTrace(1200, 600)}
	v, err := NewBudgets().Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(5000)})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
}

func TestBudgetsFailsOverTokenCap(t *testing.T) {
	ev := core.Evidence{Trace: tokenTrace(9000, 4000)}
	v, _ := NewBudgets().Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(5000)})
	if v.Pass {
		t.Fatal("want fail over token cap")
	}
}
```
- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/comparator/ -run TestBudgets -v`
Expected: FAIL — `undefined: NewBudgets`.

- [ ] **Step 3: Write the implementation**

Create `internal/comparator/budgets.go`:
```go
package comparator

import (
	"context"
	"fmt"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
)

type BudgetExpectation struct {
	MaxTokens  *int
	MaxCostUSD *float64
	MaxLatency *time.Duration
	MaxErrors  *int
}

func IntPtr(i int) *int { return &i }

type budgets struct{}

func NewBudgets() core.Comparator { return budgets{} }
func (budgets) Name() string      { return "budgets" }

func (budgets) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(BudgetExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("budgets: expectation must be BudgetExpectation, got %T", e)
	}
	v := core.Verdict{Pass: true}

	if exp.MaxTokens != nil {
		total := 0
		for _, s := range ev.Trace.Spans {
			if n, ok := s.AttrInt(genai.InTokens); ok {
				total += n
			}
			if n, ok := s.AttrInt(genai.OutTokens); ok {
				total += n
			}
		}
		if total > *exp.MaxTokens {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total tokens %d exceed budget %d", total, *exp.MaxTokens))
		}
	}

	if exp.MaxCostUSD != nil {
		cost := 0.0
		seen := false
		for _, s := range ev.Trace.Spans {
			if c, ok := s.AttrFloat(genai.CostUSD); ok {
				cost += c
				seen = true
			}
		}
		if !seen {
			return core.Verdict{}, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
		}
		if cost > *exp.MaxCostUSD {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total cost $%.4f exceeds budget $%.4f", cost, *exp.MaxCostUSD))
		}
	}

	if exp.MaxLatency != nil {
		if env := ev.Trace.Envelope(); env > *exp.MaxLatency {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("run latency %v exceeds budget %v", env, *exp.MaxLatency))
		}
	}

	if exp.MaxErrors != nil {
		errs := 0
		for _, s := range ev.Trace.Spans {
			if s.Status == "Error" {
				errs++
			}
		}
		if errs > *exp.MaxErrors {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%d error spans exceed budget %d", errs, *exp.MaxErrors))
		}
	}
	return v, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/comparator/ -run TestBudgets -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/comparator/budgets.go internal/comparator/budgets_test.go
git commit -m "feat(comparator): budgets (tokens/cost/latency/errors)"
```

---

### Task 6: Result comparator (deterministic matchers)

**Files:**
- Create: `internal/comparator/result.go`
- Test: `internal/comparator/result_test.go`

**Interfaces:**
- Consumes: `core.Evidence`/`Comparator` (Task 2).
- Produces: `type ResultExpectation struct{ Matcher, Want, Target string }`
  (Target `"answer"` default, or `"status"`); `func NewResult() core.Comparator` (Name `"result"`).
  Matchers: `exact`, `contains`, `regex`, `json-subset`, `status`.

- [ ] **Step 1: Write the failing test**

Create `internal/comparator/result_test.go`:
```go
package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestResultContainsPassesAndFails(t *testing.T) {
	pass := core.Evidence{Output: core.Output{Answer: "Q3 revenue grew 12%"}}
	v, err := NewResult().Compare(context.Background(), pass, ResultExpectation{Matcher: "contains", Want: "Q3 revenue"})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
	fail := core.Evidence{Output: core.Output{Answer: "I could not find any information."}}
	v, _ = NewResult().Compare(context.Background(), fail, ResultExpectation{Matcher: "contains", Want: "Q3 revenue"})
	if v.Pass {
		t.Fatal("want fail when substring absent")
	}
}

func TestResultStatusMatcher(t *testing.T) {
	ev := core.Evidence{Output: core.Output{Status: 201}}
	v, err := NewResult().Compare(context.Background(), ev, ResultExpectation{Matcher: "status", Want: "201"})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
}

func TestResultUnknownMatcherErrors(t *testing.T) {
	_, err := NewResult().Compare(context.Background(), core.Evidence{}, ResultExpectation{Matcher: "telepathy", Want: "x"})
	if err == nil {
		t.Fatal("want error for unknown matcher")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/comparator/ -run TestResult -v`
Expected: FAIL — `undefined: NewResult`.

- [ ] **Step 3: Write the implementation**

Create `internal/comparator/result.go`:
```go
package comparator

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
)

type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status
	Want    string
	Target  string // "answer" (default) or "status"
}

type result struct{}

func NewResult() core.Comparator { return result{} }
func (result) Name() string      { return "result" }

func (result) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ResultExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: expectation must be ResultExpectation, got %T", e)
	}
	pass := false
	got := ev.Output.Answer
	switch exp.Matcher {
	case "exact":
		pass = got == exp.Want
	case "contains":
		pass = strings.Contains(got, exp.Want)
	case "regex":
		re, err := regexp.Compile(exp.Want)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("result: bad regex %q: %w", exp.Want, err)
		}
		pass = re.MatchString(got)
	case "json-subset":
		ok, err := jsonSubset([]byte(exp.Want), ev.Output.Body)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("result: json-subset: %w", err)
		}
		pass = ok
	case "status":
		want, err := strconv.Atoi(exp.Want)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("result: status want must be int, got %q", exp.Want)
		}
		pass = ev.Output.Status == want
		got = strconv.Itoa(ev.Output.Status)
	default:
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	if pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result %s: want %q, got %q", exp.Matcher, exp.Want, got),
	}}, nil
}

// jsonSubset reports whether want's keys/values all appear in got (recursive).
func jsonSubset(want, got []byte) (bool, error) {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return false, fmt.Errorf("want: %w", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return false, fmt.Errorf("got: %w", err)
	}
	return subset(w, g), nil
}

func subset(w, g any) bool {
	switch wt := w.(type) {
	case map[string]any:
		gt, ok := g.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range wt {
			gv, ok := gt[k]
			if !ok || !subset(wv, gv) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(w, g)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/comparator/ -run TestResult -v`
Expected: PASS.

- [ ] **Step 5: Run the full comparator package**

Run: `go test ./internal/comparator/ -v`
Expected: PASS (sequence + budgets + result).

- [ ] **Step 6: Commit**

```bash
git add internal/comparator/result.go internal/comparator/result_test.go
git commit -m "feat(comparator): result (exact/contains/regex/json-subset/status)"
```

---

### Task 7: Correlator (inject + resolve/merge + stable-poll)

**Files:**
- Create: `internal/correlate/correlate.go`
- Test: `internal/correlate/correlate_test.go`

**Interfaces:**
- Consumes: `core.Correlator`/`RunSpec`/`TraceStore`/`TraceQuery`/`TraceRef` (Task 2), `trace.Trace` (Task 1).
- Produces: `func New(idFn func() string, poll PollConfig) core.Correlator`;
  `type PollConfig struct{ Interval time.Duration; StableFor int; Timeout time.Duration }`.
  `Inject` sets `spec.RunID` + `spec.Tags["test.run.id"]`; `Resolve` queries by tag, fetches +
  **merges** all matching traces into one forest, polling until span count is stable.

- [ ] **Step 1: Write the failing test**

Create `internal/correlate/correlate_test.go`:
```go
package correlate

import (
	"context"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// growStore returns a trace whose span count grows for the first 2 calls, then stabilizes.
type growStore struct {
	calls int
}

func (s *growStore) GetByID(_ context.Context, id string) (*trace.Trace, error) {
	s.calls++
	n := s.calls
	if n > 3 {
		n = 3 // stabilize at 3 spans
	}
	tr := &trace.Trace{RunID: id}
	for i := 0; i < n; i++ {
		tr.Spans = append(tr.Spans, &trace.Span{Name: "span"})
	}
	return tr, nil
}
func (s *growStore) Query(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	return []core.TraceRef{{TraceID: q.Value}}, nil
}
func (s *growStore) Caps() core.StoreCaps { return core.StoreCaps{} }

func TestInjectSetsRunIDAndTag(t *testing.T) {
	c := New(func() string { return "fixed-id" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	spec := &core.RunSpec{}
	id := c.Inject(context.Background(), spec)
	if id != "fixed-id" || spec.RunID != "fixed-id" || spec.Tags["test.run.id"] != "fixed-id" {
		t.Fatalf("inject wrong: id=%q spec=%+v", id, spec)
	}
}

func TestResolveStablePollsUntilCountStable(t *testing.T) {
	c := New(func() string { return "x" }, PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: time.Second})
	tr, err := c.Resolve(context.Background(), &growStore{}, "x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tr.Spans) != 3 {
		t.Fatalf("want 3 stable spans, got %d", len(tr.Spans))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/correlate/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `internal/correlate/correlate.go`:
```go
package correlate

import (
	"context"
	"fmt"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

type PollConfig struct {
	Interval  time.Duration
	StableFor int // consecutive stable iterations required
	Timeout   time.Duration
}

type correlator struct {
	idFn func() string
	poll PollConfig
}

func New(idFn func() string, poll PollConfig) core.Correlator {
	return &correlator{idFn: idFn, poll: poll}
}

func (c *correlator) Inject(_ context.Context, spec *core.RunSpec) string {
	id := c.idFn()
	spec.RunID = id
	if spec.Tags == nil {
		spec.Tags = map[string]string{}
	}
	spec.Tags["test.run.id"] = id
	return id
}

// Resolve queries the store for all traces tagged runID, fetches + merges them into
// one forest, and polls until the merged span count is stable for StableFor
// iterations. Zero traces within Timeout is a hard error (spec §5).
func (c *correlator) Resolve(ctx context.Context, store core.TraceStore, runID string) (*trace.Trace, error) {
	deadline := time.Now().Add(c.poll.Timeout)
	lastCount, stable := -1, 0
	var merged *trace.Trace

	for {
		refs, err := store.Query(ctx, core.TraceQuery{Tag: "test.run.id", Value: runID})
		if err != nil {
			return nil, fmt.Errorf("correlate: query: %w", err)
		}
		m := &trace.Trace{RunID: runID}
		for _, ref := range refs {
			tr, err := store.GetByID(ctx, ref.TraceID)
			if err != nil {
				return nil, fmt.Errorf("correlate: get %s: %w", ref.TraceID, err)
			}
			m.Roots = append(m.Roots, tr.Roots...)
			m.Spans = append(m.Spans, tr.Spans...)
		}
		merged = m

		if len(m.Spans) > 0 && len(m.Spans) == lastCount {
			stable++
			if stable >= c.poll.StableFor {
				return merged, nil
			}
		} else {
			stable = 0
		}
		lastCount = len(m.Spans)

		if time.Now().After(deadline) {
			if len(m.Spans) == 0 {
				return nil, fmt.Errorf("correlate: no trace for run %q within %v (0 spans seen)", runID, c.poll.Timeout)
			}
			return merged, nil // timed out but we have spans; return what we have
		}
		time.Sleep(c.poll.Interval)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/correlate/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/correlate/correlate.go internal/correlate/correlate_test.go
git commit -m "feat(correlate): tag inject + resolve/merge forest + stable-poll"
```

---

### Task 8: Shell driver adapter

**Files:**
- Create: `internal/driver/shell.go`
- Test: `internal/driver/shell_test.go`

**Interfaces:**
- Consumes: `core.Driver`/`RunSpec`/`RunResult`/`Output` (Task 2), `core.ExtractAnswer` (Task 2).
- Produces: `func NewShell() core.Driver` (Name conceptually `"shell"`). `Run` execs `spec.Command`
  with `spec.Env` plus injected `OTEL_RESOURCE_ATTRIBUTES` (from `spec.Tags`) and any inherited
  env; captures stdout/stderr/exit into `Output` with `Answer = ExtractAnswer(stdout)`.

- [ ] **Step 1: Write the failing test**

Create `internal/driver/shell_test.go`:
```go
package driver

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestShellCapturesAnswerAndInjectsRunIDEnv(t *testing.T) {
	// The script echoes OTEL_RESOURCE_ATTRIBUTES so we can assert injection,
	// then prints the "answer" on its own line.
	spec := core.RunSpec{
		Command: []string{"sh", "-c", `printf '%s\n' "$OTEL_RESOURCE_ATTRIBUTES"; printf 'the answer\n'`},
		Tags:    map[string]string{"test.run.id": "abc123"},
		RunID:   "abc123",
	}
	res, err := NewShell().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RunID != "abc123" {
		t.Fatalf("RunID = %q", res.RunID)
	}
	if !strings.Contains(res.Output.Stdout, "test.run.id=abc123") {
		t.Fatalf("OTEL_RESOURCE_ATTRIBUTES not injected; stdout=%q", res.Output.Stdout)
	}
	if res.Output.Answer != "test.run.id=abc123\nthe answer" {
		t.Fatalf("Answer = %q", res.Output.Answer)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/driver/ -v`
Expected: FAIL — `undefined: NewShell`.

- [ ] **Step 3: Write the implementation**

Create `internal/driver/shell.go`:
```go
package driver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
)

type shell struct{}

func NewShell() core.Driver { return shell{} }

func (shell) Run(ctx context.Context, spec core.RunSpec) (core.RunResult, error) {
	if len(spec.Command) == 0 {
		return core.RunResult{}, fmt.Errorf("shell: empty command for target %q", spec.Target)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)

	// Base env = inherited, plus explicit spec.Env, plus injected correlation.
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	if ra := resourceAttrs(spec.Tags); ra != "" {
		env = append(env, "OTEL_RESOURCE_ATTRIBUTES="+ra)
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	exit := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if runErr != nil {
		return core.RunResult{}, fmt.Errorf("shell: exec %v: %w (stderr: %s)", spec.Command, runErr, stderr.String())
	}

	out := core.Output{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
		Answer:   core.ExtractAnswer(stdout.String()),
	}
	return core.RunResult{RunID: spec.RunID, Output: out}, nil
}

// resourceAttrs renders spec.Tags as the OTEL_RESOURCE_ATTRIBUTES value
// (k=v,k=v) with sorted keys for determinism.
func resourceAttrs(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+tags[k])
	}
	return strings.Join(parts, ",")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/driver/ -v`
Expected: PASS.

- [ ] **Step 5: Run the whole core suite**

Run: `go test ./internal/...`
Expected: PASS across `trace`, `core`, `store`, `comparator`, `correlate`, `driver`.

- [ ] **Step 6: Commit**

```bash
git add internal/driver/shell.go internal/driver/shell_test.go
git commit -m "feat(driver): shell adapter with OTEL_RESOURCE_ATTRIBUTES injection"
```

---

## Done criteria for Plan 2a

- `go test ./internal/...` passes (all six packages).
- Comparators validate Plan 1's golden fixtures: `happy` passes sequence; `wrong_order`/`extra_tool` fail sequence; `over_budget` fails budgets; `bad_answer` fails result. (Add these fixture-driven assertions as additional cases in the comparator tests if not already covered.)
- No package in `internal/` imports godog, a CLI, or a live Tempo client — the core is hermetic.

**Plan 2b (next)** wires these into: the Tempo HTTP `TraceStore`, registries + `engine.Build` composition root, the engine lifecycle + per-target concurrency, the godog step grammar, the `mentat` runner CLI, `mentatctl` (agent run/trace/tools/replay/diff/--save), and the L2 hermetic + L3 meta tests.
