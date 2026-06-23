# Result Span-Attribute Source Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the `result` comparator a second value source — a per-span attribute (e.g. `gen_ai.tool.call.result`) selected by tool name or a span selector, with ordinal (`first`/`last`/`Nth`) and quantifier (`every`/`any`) addressing — so intermediate / per-tool results can be asserted, not just the driver boundary output.

**Architecture:** Approach 1 of the spec. `ResultExpectation` gains an optional `Source *SpanSource`; `nil` keeps today's boundary path byte-for-byte. When set, `result.Compare` delegates to a new `internal/comparator/result_span.go` that walks the trace forest by a reused shape `Selector`, start-orders matches, applies the `Quant`, extracts the attribute, synthesizes a derived `Evidence` (its `Output.Answer`/`Output.Body` carry the value), and dispatches to the **unchanged** `core.Matcher`. Quantifiers combine per-span verdicts (AND / OR). No change to the `core.Matcher` interface, the matcher registry, the composition root, or any generated mock.

**Tech Stack:** Go 1.25, `github.com/cucumber/godog` (BDD), `go.uber.org/mock` (TraceStore mock in step tests), `github.com/santhosh-tekuri/jsonschema/v6` (existing schema matcher). Design spec: `docs/superpowers/specs/2026-06-23-mentat-result-span-attribute-source-design.md`.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat`; Go 1.25.
- **Format/vet/lint clean:** `gofmt -l .` prints nothing; `go vet ./...` clean; `golangci-lint run` clean (a `.golangci.yml` exists).
- **Comparators consume `Evidence` only** (invariant #1). The span source reads `ev.Trace` + synthesizes from span attributes; it never touches a `TraceStore` or `Driver`.
- **`Trace` is a forest** (invariant #2) — `matchingSpans` already walks every span across all roots; never assume a single root.
- **No silent fallbacks** (invariant #4). A function that cannot do its job returns a wrapped `error` (`fmt.Errorf("...: %w", err)`), never a zero-value success. Behavioural mismatch (value present, didn't match) → `core.Verdict{Pass:false, Reasons:[...]}`; author/trace defect (nil trace, zero match, ambiguous bare, Nth out of range, missing result attribute, `status`+span source) → hard `error`.
- **Two missing-value semantics** (the one subtle invariant): the **selector** filters (missing predicate attr → non-match, via `matchSpan`); the **result attribute** extracts (missing attr → hard error). Do not conflate them.
- **Errors name the concrete thing + value:** `fmt.Errorf("result: selector %s matched %d spans; use first/last/Nth, or every/any", sel, n)`, not `"invalid input"`.
- **Tests:** table-driven default; hermetic (no network) except the `//go:build e2e` cases; ≥80% coverage for every touched package (`internal/comparator`, `internal/steps`). `t.Parallel()` is a soft default for new table-driven tests sharing no mutable state.
- **`status` matcher stays boundary-only:** it is a hard error in combination with a span source.
- **Git:** Conventional Commits; `git add .` is forbidden (add files individually); **no AI attribution** in commits (no `Co-Authored-By`, no "Generated with…").

## File Structure

- **Modify** `internal/genai/keys.go` — add `ToolResult = "gen_ai.tool.call.result"`. Responsibility: OTel GenAI key constants (single source of truth).
- **Modify** `internal/comparator/result.go` — add `Source *SpanSource` to `ResultExpectation`; `Compare` branches to `resolveSpanSource` when `Source != nil`. Responsibility: result comparator entry + boundary path.
- **Create** `internal/comparator/result_span.go` — `SpanSource`, `Quant` (+ consts), `resolveSpanSource`, and the `selectSpans`/`extract`/`evaluate`/`reason` helpers. Responsibility: "span attribute → matched value", the entire second-source concern, isolated from `result.go`.
- **Create** `internal/comparator/result_span_test.go` — unit tests + the `resultTrace`/`toolSpan` fixtures.
- **Modify** `internal/steps/steps.go` — add the `genai` import, four `sc.Step` bindings, the `resultTool*`/`resultAttr*` handlers, and the shared `parseSpanSpec` + `verbToMatcher` helpers. Responsibility: Gherkin grammar binding.
- **Modify** `internal/steps/steps_test.go` — step-mapping tests, the span-result trace helper, and a hermetic L3 red feature.
- **Modify** `features/research_agent.feature` — add one green span-result step (exercised by the existing happy e2e).
- **Create** `features/meta/bad_result_span.feature` — a scenario that violates exactly one span-result assertion (for the e2e L3 sweep).
- **Modify** `e2e/meta_test.go` — add the `bad_result_span` case to the red sweep.

Tasks are ordered so each builds on the last and ends with an independently testable, committable deliverable. Tasks 1–3 build the comparator core (single span → ordinals → quantifiers); Tasks 4–5 add the Gherkin grammar (tool form → selector form); Task 6 proves red-on-bad-behaviour (hermetic L3 + e2e).

---

### Task 1: Core resolution — single-span (`QuantOne`) span source

**Files:**
- Modify: `internal/genai/keys.go`
- Modify: `internal/comparator/result.go`
- Create: `internal/comparator/result_span.go`
- Create: `internal/comparator/result_span_test.go`

**Interfaces:**
- Produces (used by Tasks 2–6):
  - `genai.ToolResult string` = `"gen_ai.tool.call.result"`.
  - `comparator.Quant int` with consts `QuantOne, QuantFirst, QuantLast, QuantNth, QuantEvery, QuantAny` (declared in full now so later tasks don't redeclare).
  - `comparator.SpanSource struct { Selector Selector; Attr string; Quant Quant; Index int }`.
  - `comparator.ResultExpectation` gains field `Source *SpanSource` (nil ⇒ boundary path).
  - `resolveSpanSource(ctx, ev, exp) (core.Verdict, error)` (unexported; the dispatch target).

- [ ] **Step 1: Add the `ToolResult` constant**

In `internal/genai/keys.go`, add to the `const` block (after `RequestModel`):

```go
	RequestModel = "gen_ai.request.model"
	ToolResult   = "gen_ai.tool.call.result"
```

- [ ] **Step 2: Write the failing test**

Create `internal/comparator/result_span_test.go`:

```go
package comparator

import (
	"context"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// toolSpan builds an execute_tool span carrying a tool name + result attribute.
func toolSpan(id, tool, result string, start time.Time) *trace.Span {
	return &trace.Span{
		ID:    id,
		Name:  "execute_tool " + tool,
		Start: start,
		Attrs: map[string]string{
			genai.Op:         genai.OpExecuteTool,
			genai.ToolName:   tool,
			genai.ToolResult: result,
		},
	}
}

// resultTrace: two "search" calls (distinct results + start times) and one
// "summarize" — enough for one-match, ambiguity, ordinals, and quantifiers.
func resultTrace() *trace.Trace {
	t0 := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	s1 := toolSpan("s1", "search", "first-result", t0)
	s2 := toolSpan("s2", "search", "second-result", t0.Add(time.Second))
	s3 := toolSpan("s3", "summarize", "the summary", t0.Add(2*time.Second))
	return &trace.Trace{Roots: []*trace.Span{s1, s2, s3}, Spans: []*trace.Span{s1, s2, s3}}
}

// toolSource builds a tool-convenience SpanSource for tests.
func toolSource(tool string, q Quant, idx int) *SpanSource {
	return &SpanSource{
		Selector: Selector{{Key: genai.ToolName, Value: tool}},
		Attr:     genai.ToolResult,
		Quant:    q,
		Index:    idx,
	}
}

func TestResultSpanSourceOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "one match, contains passes",
			ev:       core.Evidence{Trace: resultTrace()},
			exp:      ResultExpectation{Matcher: "contains", Want: "summary", Source: toolSource("summarize", QuantOne, 0)},
			wantPass: true,
		},
		{
			name:     "one match, contains fails",
			ev:       core.Evidence{Trace: resultTrace()},
			exp:      ResultExpectation{Matcher: "contains", Want: "nope", Source: toolSource("summarize", QuantOne, 0)},
			wantPass: false,
		},
		{
			name:     "one match, json-subset on attr body",
			ev:       core.Evidence{Trace: jsonResultTrace(`{"ok":true,"n":3}`)},
			exp:      ResultExpectation{Matcher: "json-subset", Want: `{"ok":true}`, Source: toolSource("lookup", QuantOne, 0)},
			wantPass: true,
		},
		{
			name:    "zero matches is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("delete", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "ambiguous bare (2 matches) is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("search", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "missing result attribute is error",
			ev:      core.Evidence{Trace: noResultAttrTrace()},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("search", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "nil trace is error",
			ev:      core.Evidence{},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("search", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "status matcher with span source is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "status", Want: "200", Source: toolSource("summarize", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "unknown matcher with span source is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "telepathy", Want: "x", Source: toolSource("summarize", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:     "Source nil unchanged (boundary path still reads Answer)",
			ev:       core.Evidence{Output: core.Output{Answer: "boundary"}},
			exp:      ResultExpectation{Matcher: "contains", Want: "boundary"},
			wantPass: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewResult().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
			if !got.Pass && len(got.Reasons) == 0 {
				t.Error("failing verdict must carry at least one Reason")
			}
		})
	}
}

// jsonResultTrace: one "lookup" tool span whose result is the given JSON string.
func jsonResultTrace(jsonResult string) *trace.Trace {
	s := toolSpan("j1", "lookup", jsonResult, time.Time{})
	return &trace.Trace{Roots: []*trace.Span{s}, Spans: []*trace.Span{s}}
}

// noResultAttrTrace: one "search" span with NO gen_ai.tool.call.result attribute.
func noResultAttrTrace() *trace.Trace {
	s := &trace.Span{ID: "n1", Name: "execute_tool search", Attrs: map[string]string{
		genai.Op: genai.OpExecuteTool, genai.ToolName: "search",
	}}
	return &trace.Trace{Roots: []*trace.Span{s}, Spans: []*trace.Span{s}}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run TestResultSpanSourceOne -v`
Expected: FAIL — compile error `undefined: Quant` / `unknown field Source in struct literal of type ResultExpectation`.

- [ ] **Step 4: Add the `Source` field + dispatch in `result.go`**

In `internal/comparator/result.go`, add the field to `ResultExpectation` (after `Target`):

```go
type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status | schema
	Want    string
	Target  string      // boundary only: "answer" (default) | "status"; ignored when Source != nil
	Source  *SpanSource // nil => driver Output (default); set => span-attribute source
}
```

Then branch in `Compare` (immediately after the type assertion, before the matcher lookup):

```go
func (result) Compare(ctx context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ResultExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: expectation must be ResultExpectation, got %T", e)
	}
	if exp.Source != nil {
		return resolveSpanSource(ctx, ev, exp)
	}
	m, ok := registry.Matcher(exp.Matcher)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	return m.Match(ctx, ev, exp.Want, exp.Target)
}
```

- [ ] **Step 5: Create `result_span.go` with `QuantOne` resolution**

Create `internal/comparator/result_span.go`:

```go
package comparator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Quant resolves which of N spans matching a SpanSource selector supplies the value.
type Quant int

const (
	QuantOne   Quant = iota // bare: exactly one match (else hard error)
	QuantFirst              // first by start order
	QuantLast               // last by start order
	QuantNth                // Index-th by start order (1-based)
	QuantEvery              // all matches must satisfy (AND)
	QuantAny                // >=1 match satisfies (OR)
)

// SpanSource selects a span-attribute result value for the result comparator.
// The tool convenience form sets Selector = {gen_ai.tool.name = X} and
// Attr = genai.ToolResult; the general form sets a parsed selector + named attr.
type SpanSource struct {
	Selector Selector
	Attr     string
	Quant    Quant
	Index    int // QuantNth only, 1-based
}

// resolveSpanSource evaluates a result expectation against a span-attribute source.
// It selects spans, extracts the attribute, synthesizes a derived Evidence whose
// Output carries the value, and dispatches to the unchanged matcher; quantifiers
// combine per-span verdicts. Every author/trace defect is a hard error (invariant #4).
func resolveSpanSource(ctx context.Context, ev core.Evidence, exp ResultExpectation) (core.Verdict, error) {
	src := exp.Source
	if exp.Matcher == "status" {
		return core.Verdict{}, fmt.Errorf("result: status matcher is boundary-only; not valid with a span source")
	}
	m, ok := registry.Matcher(exp.Matcher)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("result: Evidence.Trace is nil (span source %s)", src.Selector)
	}
	spans := matchingSpans(ev.Trace, src.Selector)
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	if len(spans) == 0 {
		return core.Verdict{}, fmt.Errorf("result: selector %s matched no spans", src.Selector)
	}
	targets, err := src.selectSpans(spans)
	if err != nil {
		return core.Verdict{}, err
	}
	return src.evaluate(ctx, m, ev, exp.Want, targets)
}

// selectSpans applies the Quant to the start-ordered, non-empty match list.
func (s SpanSource) selectSpans(spans []*trace.Span) ([]*trace.Span, error) {
	switch s.Quant {
	case QuantOne:
		if len(spans) != 1 {
			return nil, fmt.Errorf("result: selector %s matched %d spans; use first/last/Nth, or every/any", s.Selector, len(spans))
		}
		return spans, nil
	case QuantFirst:
		return spans[:1], nil
	case QuantLast:
		return spans[len(spans)-1:], nil
	case QuantNth:
		if s.Index < 1 || s.Index > len(spans) {
			return nil, fmt.Errorf("result: span #%d of selector %s out of range (%d matched)", s.Index, s.Selector, len(spans))
		}
		return spans[s.Index-1 : s.Index], nil
	case QuantEvery, QuantAny:
		return spans, nil
	default:
		return nil, fmt.Errorf("result: unknown quant %d", s.Quant)
	}
}

// extract reads the source attribute from sp. Reserved span.* keys read intrinsics
// (always present); any other key is an attribute lookup whose ABSENCE is a hard
// error (extraction semantics, unlike the selector's filter semantics).
func (s SpanSource) extract(sp *trace.Span) (string, error) {
	if reservedKey(s.Attr) {
		return spanValue(sp, s.Attr), nil
	}
	val, ok := sp.Attrs[s.Attr]
	if !ok {
		return "", fmt.Errorf("result: span %q (selector %s) has no attribute %q", sp.Name, s.Selector, s.Attr)
	}
	return val, nil
}

// evaluate matches each target span's attribute (via a synthesized Evidence) and
// combines per the Quant: One/First/Last/Nth → the single verdict; Every → AND;
// Any → OR.
func (s SpanSource) evaluate(ctx context.Context, m core.Matcher, ev core.Evidence, want string, targets []*trace.Span) (core.Verdict, error) {
	type spanVerdict struct {
		v  core.Verdict
		sp *trace.Span
	}
	results := make([]spanVerdict, 0, len(targets))
	for _, sp := range targets {
		val, err := s.extract(sp)
		if err != nil {
			return core.Verdict{}, err
		}
		derived := ev
		derived.Output = core.Output{Answer: val, Body: []byte(val)}
		v, err := m.Match(ctx, derived, want, "answer")
		if err != nil {
			return core.Verdict{}, err
		}
		results = append(results, spanVerdict{v, sp})
	}
	switch s.Quant {
	case QuantEvery:
		var reasons []string
		for _, r := range results {
			if !r.v.Pass {
				reasons = append(reasons, s.reason(r.sp, r.v))
			}
		}
		if len(reasons) > 0 {
			return core.Verdict{Pass: false, Reasons: reasons}, nil
		}
		return core.Verdict{Pass: true}, nil
	case QuantAny:
		for _, r := range results {
			if r.v.Pass {
				return core.Verdict{Pass: true}, nil
			}
		}
		reasons := make([]string, 0, len(results))
		for _, r := range results {
			reasons = append(reasons, s.reason(r.sp, r.v))
		}
		return core.Verdict{Pass: false, Reasons: reasons}, nil
	default: // One / First / Last / Nth — exactly one target
		r := results[0]
		if r.v.Pass {
			return core.Verdict{Pass: true}, nil
		}
		return core.Verdict{Pass: false, Reasons: []string{s.reason(r.sp, r.v)}}, nil
	}
}

// reason renders a failing per-span verdict, prefixed with the span identity so a
// multi-span (every/any) failure names which span tripped.
func (s SpanSource) reason(sp *trace.Span, v core.Verdict) string {
	return fmt.Sprintf("span %q attr %q: %s", sp.Name, s.Attr, strings.Join(v.Reasons, "; "))
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run TestResultSpanSourceOne -v`
Expected: PASS (all subtests). Then run the whole package to confirm no regression:
Run: `go test ./internal/comparator/`
Expected: PASS (existing `TestResultCompare`, `TestFixtureResult`, etc. unaffected — the boundary path is unchanged).

- [ ] **Step 7: Format, vet, commit**

```bash
gofmt -w internal/genai/keys.go internal/comparator/result.go internal/comparator/result_span.go internal/comparator/result_span_test.go
go vet ./internal/comparator/ ./internal/genai/
git add internal/genai/keys.go internal/comparator/result.go internal/comparator/result_span.go internal/comparator/result_span_test.go
git commit -m "feat(result): span-attribute source — single-span (QuantOne) resolution"
```

---

### Task 2: Ordinals — `QuantFirst`, `QuantLast`, `QuantNth`

The selection logic already exists (`selectSpans`); this task proves it against a multi-match trace with distinct start times and locks the start-order + out-of-range behaviour with tests.

**Files:**
- Modify: `internal/comparator/result_span_test.go`

**Interfaces:**
- Consumes: `toolSource`, `resultTrace` (Task 1).

- [ ] **Step 1: Write the failing test**

Add to `internal/comparator/result_span_test.go`:

```go
func TestResultSpanSourceOrdinals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "first call picks earliest start (first-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "first-result", Source: toolSource("search", QuantFirst, 0)},
			wantPass: true,
		},
		{
			name:     "last call picks latest start (second-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "second-result", Source: toolSource("search", QuantLast, 0)},
			wantPass: true,
		},
		{
			name:     "2nd call picks index 2 (second-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "second-result", Source: toolSource("search", QuantNth, 2)},
			wantPass: true,
		},
		{
			name:     "1st call picks index 1 (first-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "first-result", Source: toolSource("search", QuantNth, 1)},
			wantPass: true,
		},
		{
			name:     "first call mismatch fails (not an error)",
			exp:      ResultExpectation{Matcher: "exact", Want: "second-result", Source: toolSource("search", QuantFirst, 0)},
			wantPass: false,
		},
		{
			name:    "3rd call out of range is error (only 2 matched)",
			exp:     ResultExpectation{Matcher: "exact", Want: "x", Source: toolSource("search", QuantNth, 3)},
			wantErr: true,
		},
		{
			name:    "0th call out of range is error",
			exp:     ResultExpectation{Matcher: "exact", Want: "x", Source: toolSource("search", QuantNth, 0)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewResult().Compare(context.Background(), core.Evidence{Trace: resultTrace()}, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it passes (logic already present)**

Run: `go test ./internal/comparator/ -run TestResultSpanSourceOrdinals -v`
Expected: PASS. (Task 1's `selectSpans` already implements `First`/`Last`/`Nth` + out-of-range; this test is the red→green confirmation that the start-order is by `Start` and `Nth` is 1-based. If any subtest fails, fix `selectSpans` before committing.)

- [ ] **Step 3: Commit**

```bash
gofmt -w internal/comparator/result_span_test.go
go vet ./internal/comparator/
git add internal/comparator/result_span_test.go
git commit -m "test(result): ordinal (first/last/Nth) span-source addressing"
```

---

### Task 3: Quantifiers — `QuantEvery` (AND) and `QuantAny` (OR)

**Files:**
- Modify: `internal/comparator/result_span_test.go`

**Interfaces:**
- Consumes: `toolSource`, `resultTrace` (Task 1).

- [ ] **Step 1: Write the failing test**

Add to `internal/comparator/result_span_test.go`:

```go
func TestResultSpanSourceQuantifiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "every: all search results contain 'result' passes",
			exp:      ResultExpectation{Matcher: "contains", Want: "result", Source: toolSource("search", QuantEvery, 0)},
			wantPass: true,
		},
		{
			name:     "every: one search result lacks 'first' fails",
			exp:      ResultExpectation{Matcher: "contains", Want: "first", Source: toolSource("search", QuantEvery, 0)},
			wantPass: false,
		},
		{
			name:     "any: at least one search result contains 'second' passes",
			exp:      ResultExpectation{Matcher: "contains", Want: "second", Source: toolSource("search", QuantAny, 0)},
			wantPass: true,
		},
		{
			name:     "any: no search result contains 'zzz' fails",
			exp:      ResultExpectation{Matcher: "contains", Want: "zzz", Source: toolSource("search", QuantAny, 0)},
			wantPass: false,
		},
		{
			name:    "every: zero matches is error (tool never called)",
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("delete", QuantEvery, 0)},
			wantErr: true,
		},
		{
			name:    "any: zero matches is error (tool never called)",
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("delete", QuantAny, 0)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewResult().Compare(context.Background(), core.Evidence{Trace: resultTrace()}, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
			if !got.Pass && len(got.Reasons) == 0 {
				t.Error("failing verdict must carry at least one Reason naming the span")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it passes (logic already present)**

Run: `go test ./internal/comparator/ -run TestResultSpanSourceQuantifiers -v`
Expected: PASS. (Task 1's `evaluate` already implements the `Every` AND / `Any` OR combination and the zero-match guard fires in `resolveSpanSource` before selection. If a subtest fails, fix `evaluate`/`resolveSpanSource` before committing.)

- [ ] **Step 3: Confirm full-package coverage ≥ 80%**

Run: `go test ./internal/comparator/ -coverprofile=cover.out && go tool cover -func=cover.out | grep -E "result_span.go|total:"`
Expected: every `result_span.go` function ≥ 80% (the three test funcs cover all branches); total ≥ 80%. Remove `cover.out` after: `rm cover.out`.

- [ ] **Step 4: Commit**

```bash
gofmt -w internal/comparator/result_span_test.go
go vet ./internal/comparator/
git add internal/comparator/result_span_test.go
git commit -m "test(result): every/any quantifier span-source addressing"
```

---

### Task 4: Gherkin grammar — tool convenience form

Adds the shared span-spec parser, the verb→matcher map, and the two tool-form step families (string-arg + docstring).

**Files:**
- Modify: `internal/steps/steps.go`
- Modify: `internal/steps/steps_test.go`

**Interfaces:**
- Produces (used by Task 5):
  - `parseSpanSpec(slot string) (comparator.Quant, int, error)` — `""` ⇒ `QuantOne`; recognizes `the first`/`the last`/`the Nth`/`every`/`any` (with an optional trailing `call`/`span` word).
  - `verbToMatcher(verb string) (string, error)` — `contains`→`contains`, `equals`→`exact`, `matches regex`→`regex`, `json-contains`→`json-subset`, `matches schema`→`schema`.
- Consumes: `comparator.SpanSource`, `comparator.Quant` (Task 1); `genai.ToolName`, `genai.ToolResult` (Task 1).

- [ ] **Step 1: Write the failing test**

Add to `internal/steps/steps_test.go` (the `import` block already has `bytes`, `strings`, `testing`, `godog`, `engine`, `trace`, `genai`, `config`, `correlate`, `mocks`, `core`, `time`; no new imports needed):

```go
// spanResultTrace: two "search" calls with distinct results + start times and one
// "summarize" — drives the tool-form grammar through a godog suite.
func spanResultTrace() *trace.Trace {
	t0 := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	mk := func(id, tool, res string, start time.Time) *trace.Span {
		return &trace.Span{ID: id, Name: "execute_tool " + tool, Start: start,
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tool, genai.ToolResult: res}}
	}
	root := &trace.Span{Name: "invoke_agent", Attrs: map[string]string{
		genai.Op: genai.OpInvokeAgent, genai.InTokens: "100", genai.OutTokens: "50"}}
	s1 := mk("s1", "search", "first-result", t0)
	s2 := mk("s2", "search", "second-result", t0.Add(time.Second))
	s3 := mk("s3", "summarize", "the summary", t0.Add(2*time.Second))
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: []*trace.Span{root, s1, s2, s3}}
}

func TestResultToolStep(t *testing.T) {
	tests := []struct {
		name     string
		feature  string
		wantPass bool
	}{
		{
			name: "bare tool form (single match) passes",
			feature: `Feature: result-tool
  Scenario: summarize result
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of tool "summarize" contains "summary"
`,
			wantPass: true,
		},
		{
			name: "first call ordinal passes",
			feature: `Feature: result-tool
  Scenario: first search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of the first call to tool "search" equals "first-result"
`,
			wantPass: true,
		},
		{
			name: "last call ordinal passes",
			feature: `Feature: result-tool
  Scenario: last search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of the last call to tool "search" equals "second-result"
`,
			wantPass: true,
		},
		{
			name: "every call quantifier passes",
			feature: `Feature: result-tool
  Scenario: every search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of every call to tool "search" contains "result"
`,
			wantPass: true,
		},
		{
			name: "any call quantifier passes",
			feature: `Feature: result-tool
  Scenario: any search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of any call to tool "search" contains "second"
`,
			wantPass: true,
		},
		{
			name: "docstring json-contains passes",
			feature: `Feature: result-tool
  Scenario: summarize json
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of tool "summarize" json-contains:
      """
      "the summary"
      """
`,
			wantPass: false, // "the summary" is a plain string, not JSON-subset of itself-as-string; see note
		},
		{
			name: "ambiguous bare tool form fails the suite",
			feature: `Feature: result-tool
  Scenario: ambiguous search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of tool "search" contains "result"
`,
			wantPass: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, spanResultTrace())
			var out bytes.Buffer
			suite := godog.TestSuite{
				ScenarioInitializer: Initializer(eng),
				Options: &godog.Options{
					Format:          "pretty",
					Output:          &out,
					FeatureContents: []godog.Feature{{Name: tt.name, Contents: []byte(tt.feature)}},
				},
			}
			status := suite.Run()
			if tt.wantPass && status != 0 {
				t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
			}
			if !tt.wantPass && status == 0 {
				t.Fatalf("expected failing suite, got 0\n%s", out.String())
			}
		})
	}
}
```

> **Note on the json-contains case:** a JSON string literal `"the summary"` is not an object, so `json-subset` returns `Pass:false` (the subset walk only matches into objects). The case asserts the docstring step *binds and runs* (suite fails as expected), not that strings json-subset. Keep `wantPass: false`. If you prefer a passing docstring case, point the tool at a JSON-object result — but `spanResultTrace` keeps string results to mirror researchbot, so the false case is the simplest binding proof.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/steps/ -run TestResultToolStep -v`
Expected: FAIL — godog reports the steps as undefined (e.g. `the result of tool "summarize" contains "summary"` has no matching step), so passing scenarios do not reach status 0.

- [ ] **Step 3: Add the `genai` import to `steps.go`**

In `internal/steps/steps.go`, add to the import block:

```go
	"github.com/thetonymaster/mentat/internal/genai"
```

- [ ] **Step 4: Bind the tool-form steps**

In `internal/steps/steps.go`, inside `InitializerWithCollector`'s `return func(sc *godog.ScenarioContext) {`, after the existing `sc.Step(\`^the run matches shape ...\`, w.matchesShape)` line, add:

```go
		// §4.1 span-attribute result source — tool convenience form
		sc.Step(`^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (contains|equals|matches regex) "([^"]*)"$`, w.resultToolValue)
		sc.Step(`^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (json-contains|matches schema):$`, w.resultToolDoc)
```

- [ ] **Step 5: Add the helpers + handlers**

In `internal/steps/steps.go`, add a package-level ordinal regex near the other `var re...` declarations (top of file):

```go
var reSpanOrdinal = regexp.MustCompile(`^(\d+)(?:st|nd|rd|th)$`)
```

Then add these functions (place them near the existing `resultContains`/`resultEquals` handlers):

```go
// parseSpanSpec maps the captured span-spec slot to a Quant (+1-based index for
// Nth). "" => QuantOne (bare). A trailing "call"/"span" word is ignored; leading
// words: the/first/last/<n>th/every/any.
func parseSpanSpec(slot string) (comparator.Quant, int, error) {
	f := strings.Fields(slot)
	if n := len(f); n > 0 && (f[n-1] == "call" || f[n-1] == "span") {
		f = f[:n-1]
	}
	switch {
	case len(f) == 0:
		return comparator.QuantOne, 0, nil
	case f[0] == "every":
		return comparator.QuantEvery, 0, nil
	case f[0] == "any":
		return comparator.QuantAny, 0, nil
	case len(f) == 2 && f[0] == "the" && f[1] == "first":
		return comparator.QuantFirst, 0, nil
	case len(f) == 2 && f[0] == "the" && f[1] == "last":
		return comparator.QuantLast, 0, nil
	case len(f) == 2 && f[0] == "the":
		if mm := reSpanOrdinal.FindStringSubmatch(f[1]); mm != nil {
			n, _ := strconv.Atoi(mm[1])
			if n < 1 {
				return 0, 0, fmt.Errorf("span ordinal must be >= 1, got %q", f[1])
			}
			return comparator.QuantNth, n, nil
		}
	}
	return 0, 0, fmt.Errorf("unrecognized span selector %q", slot)
}

// verbToMatcher maps a Gherkin matcher verb to a registered matcher name.
func verbToMatcher(verb string) (string, error) {
	switch verb {
	case "contains":
		return "contains", nil
	case "equals":
		return "exact", nil
	case "matches regex":
		return "regex", nil
	case "json-contains":
		return "json-subset", nil
	case "matches schema":
		return "schema", nil
	default:
		return "", fmt.Errorf("unknown result matcher verb %q", verb)
	}
}

// toolSpanSource builds a tool-convenience SpanSource (gen_ai.tool.name selector,
// gen_ai.tool.call.result attribute) from a parsed span-spec slot.
func toolSpanSource(slot, tool string) (*comparator.SpanSource, error) {
	q, idx, err := parseSpanSpec(slot)
	if err != nil {
		return nil, fmt.Errorf("result of tool %q: %w", tool, err)
	}
	return &comparator.SpanSource{
		Selector: comparator.Selector{{Key: genai.ToolName, Value: tool}},
		Attr:     genai.ToolResult,
		Quant:    q,
		Index:    idx,
	}, nil
}

func (w *world) resultToolValue(slot, tool, verb, want string) error {
	src, err := toolSpanSource(slot, tool)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: want, Source: src})
}

func (w *world) resultToolDoc(slot, tool, verb string, doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("result of tool %q %s: expected a docstring, got none", tool, verb)
	}
	src, err := toolSpanSource(slot, tool)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: doc.Content, Source: src})
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/steps/ -run TestResultToolStep -v`
Expected: PASS (all subtests — passing scenarios reach status 0; the ambiguous + json-string cases fail the suite as designed).

- [ ] **Step 7: Add a unit test for `parseSpanSpec` (cover the error + ordinal branches)**

Add to `internal/steps/steps_test.go`:

```go
func TestParseSpanSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		slot    string
		wantQ   comparator.Quant
		wantIdx int
		wantErr bool
	}{
		{"", comparator.QuantOne, 0, false},
		{"the first call", comparator.QuantFirst, 0, false},
		{"the last call", comparator.QuantLast, 0, false},
		{"the 2nd call", comparator.QuantNth, 2, false},
		{"the 1st", comparator.QuantNth, 1, false},
		{"every call", comparator.QuantEvery, 0, false},
		{"any span", comparator.QuantAny, 0, false},
		{"the 0th call", 0, 0, true},
		{"sideways call", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.slot, func(t *testing.T) {
			t.Parallel()
			q, idx, err := parseSpanSpec(tt.slot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if q != tt.wantQ || idx != tt.wantIdx {
				t.Errorf("got (q=%d idx=%d), want (q=%d idx=%d)", q, idx, tt.wantQ, tt.wantIdx)
			}
		})
	}
}
```

Run: `go test ./internal/steps/ -run TestParseSpanSpec -v`
Expected: PASS.

- [ ] **Step 8: Format, vet, commit**

```bash
gofmt -w internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/steps/
git add internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): tool-form span-attribute result grammar"
```

---

### Task 5: Gherkin grammar — general selector form

**Files:**
- Modify: `internal/steps/steps.go`
- Modify: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `parseSpanSpec`, `verbToMatcher` (Task 4); `comparator.ParseSelector`, `comparator.SpanSource` (Task 1 / existing).

- [ ] **Step 1: Write the failing test**

Add to `internal/steps/steps_test.go`:

```go
func TestResultAttrStep(t *testing.T) {
	tests := []struct {
		name     string
		feature  string
		wantPass bool
	}{
		{
			name: "selector form: last search result by attribute passes",
			feature: `Feature: result-attr
  Scenario: last search via selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "gen_ai.tool.call.result" of the last span matching "gen_ai.tool.name=search" equals "second-result"
`,
			wantPass: true,
		},
		{
			name: "selector form: every search via selector passes",
			feature: `Feature: result-attr
  Scenario: every search via selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "gen_ai.tool.call.result" of every span matching "gen_ai.tool.name=search" contains "result"
`,
			wantPass: true,
		},
		{
			name: "selector form: reserved span.* attribute (name) passes",
			feature: `Feature: result-attr
  Scenario: span name via selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "span.name" of the first span matching "gen_ai.tool.name=summarize" contains "summarize"
`,
			wantPass: true,
		},
		{
			name: "selector form: bad selector fails the suite",
			feature: `Feature: result-attr
  Scenario: bad selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "gen_ai.tool.call.result" of the span matching "noequals" contains "x"
`,
			wantPass: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, spanResultTrace())
			var out bytes.Buffer
			suite := godog.TestSuite{
				ScenarioInitializer: Initializer(eng),
				Options: &godog.Options{
					Format:          "pretty",
					Output:          &out,
					FeatureContents: []godog.Feature{{Name: tt.name, Contents: []byte(tt.feature)}},
				},
			}
			status := suite.Run()
			if tt.wantPass && status != 0 {
				t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
			}
			if !tt.wantPass && status == 0 {
				t.Fatalf("expected failing suite, got 0\n%s", out.String())
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/steps/ -run TestResultAttrStep -v`
Expected: FAIL — the `attribute "..." of ... span matching "..."` steps are undefined, so passing scenarios do not reach status 0.

- [ ] **Step 3: Bind the selector-form steps**

In `internal/steps/steps.go`, after the two tool-form `sc.Step` lines from Task 4, add:

```go
		// §4.2 span-attribute result source — general selector form
		sc.Step(`^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?span matching "([^"]+)" (contains|equals|matches regex) "([^"]*)"$`, w.resultAttrValue)
		sc.Step(`^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?span matching "([^"]+)" (json-contains|matches schema):$`, w.resultAttrDoc)
```

- [ ] **Step 4: Add the selector-form handlers**

In `internal/steps/steps.go`, add (near the Task 4 handlers):

```go
// attrSpanSource builds a general SpanSource from a named attribute, a span-spec
// slot, and a raw k=v selector.
func attrSpanSource(attr, slot, selStr string) (*comparator.SpanSource, error) {
	q, idx, err := parseSpanSpec(slot)
	if err != nil {
		return nil, fmt.Errorf("attribute %q of span matching %q: %w", attr, selStr, err)
	}
	selr, err := comparator.ParseSelector(selStr)
	if err != nil {
		return nil, fmt.Errorf("parse result span selector %q: %w", selStr, err)
	}
	return &comparator.SpanSource{Selector: selr, Attr: attr, Quant: q, Index: idx}, nil
}

func (w *world) resultAttrValue(attr, slot, selStr, verb, want string) error {
	src, err := attrSpanSource(attr, slot, selStr)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: want, Source: src})
}

func (w *world) resultAttrDoc(attr, slot, selStr, verb string, doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("attribute %q of span matching %q %s: expected a docstring, got none", attr, selStr, verb)
	}
	src, err := attrSpanSource(attr, slot, selStr)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: doc.Content, Source: src})
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/steps/ -run TestResultAttrStep -v`
Expected: PASS (selector-form passing scenarios reach 0; the bad-selector scenario fails the suite via the wrapped `ParseSelector` error).

- [ ] **Step 6: Confirm steps-package coverage ≥ 80%**

Run: `go test ./internal/steps/ -coverprofile=cover.out && go tool cover -func=cover.out | grep -E "steps.go:(parseSpanSpec|verbToMatcher|toolSpanSource|attrSpanSource|resultTool|resultAttr)|total:"`
Expected: each new handler/helper ≥ 80%; total ≥ 80%. Then `rm cover.out`.

- [ ] **Step 7: Format, vet, commit**

```bash
gofmt -w internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/steps/
git add internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): selector-form span-attribute result grammar"
```

---

### Task 6: L3 meta-test (red on bad behaviour) + e2e green path

Proves the framework goes red when a span-result assertion is violated (mandatory L3) and exercises the grammar end-to-end against a live researchbot trace.

**Files:**
- Modify: `internal/steps/steps_test.go` (hermetic L3)
- Modify: `features/research_agent.feature` (e2e green)
- Create: `features/meta/bad_result_span.feature` (e2e red)
- Modify: `e2e/meta_test.go` (e2e red sweep)

**Interfaces:**
- Consumes: `spanResultTrace`, `buildEng` (Tasks 4 / existing); the live researchbot `happy` scenario (`search`→`"doc-1, doc-2"`, `fetch_doc`→`"revenue table"`, `summarize`→`"summary"`).

- [ ] **Step 1: Write the hermetic L3 red test**

Add to `internal/steps/steps_test.go`:

```go
// TestSpanResultGoesRedOnBadResult proves the result comparator goes red when a
// tool's span result does not match — the L3 contract for the span-attribute source.
func TestSpanResultGoesRedOnBadResult(t *testing.T) {
	eng := buildEng(t, spanResultTrace())
	feature := `Feature: bad-span-result
  Scenario: last search result mismatch
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of the last call to tool "search" contains "NONEXISTENT"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "bad-span-result", Contents: []byte(feature)}},
		},
	}
	status := suite.Run()
	if status == 0 {
		t.Fatalf("expected suite to fail (non-zero), but it passed\n%s", out.String())
	}
	if !strings.Contains(out.String(), "result contains") {
		t.Fatalf("expected output to contain 'result contains', got:\n%s", out.String())
	}
}
```

- [ ] **Step 2: Run the L3 test to verify it goes red as designed**

Run: `go test ./internal/steps/ -run TestSpanResultGoesRedOnBadResult -v`
Expected: PASS (the test asserts the *suite* fails non-zero and surfaces `result contains` in the reason).

- [ ] **Step 3: Commit the hermetic L3**

```bash
gofmt -w internal/steps/steps_test.go
go vet ./internal/steps/
git add internal/steps/steps_test.go
git commit -m "test(steps): L3 — span-attribute result goes red on bad result"
```

- [ ] **Step 4: Add the e2e green step**

In `features/research_agent.feature`, append one line at the end of the scenario (the live `happy` researchbot trace has `search`→`"doc-1, doc-2"`):

```gherkin
    And the result contains "Q3 revenue"
    And the result of tool "search" contains "doc-1"
```

- [ ] **Step 5: Create the e2e red feature**

Create `features/meta/bad_result_span.feature`:

```gherkin
Feature: bad span result
  Scenario: a tool result that does not match
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then the result of tool "search" contains "NONEXISTENT-RESULT"
```

- [ ] **Step 6: Add the e2e red case to the sweep**

In `e2e/meta_test.go`, add to the `cases` slice in `TestBadScenariosAreCaught`:

```go
		{"features/meta/bad_shape.feature", "shape failed"},
		{"features/meta/bad_expectation.feature", "shape failed"},
		{"features/meta/bad_result_span.feature", "result contains"},
```

- [ ] **Step 7: Run the e2e suite (requires the harness)**

```bash
make harness-up
go test -tags e2e ./e2e/ -run 'TestBadScenariosAreCaught|TestHappyScenarioPasses' -v
make harness-down
```

Expected: `TestHappyScenarioPasses` passes (the new green `the result of tool "search" contains "doc-1"` step holds against the live trace); `TestBadScenariosAreCaught/features/meta/bad_result_span.feature` passes (mentat exits non-zero and the output contains `result contains`).

> If `make harness-up` / live Tempo is unavailable in this environment, mark Steps 7 as DID NOT RUN and note it in the handoff — do **not** mark the task complete on the e2e step without observing the result. The hermetic L3 (Steps 1–3) is the gating proof; the e2e is the integration confirmation.

- [ ] **Step 8: Format, vet, commit**

```bash
gofmt -w e2e/meta_test.go
go vet -tags e2e ./e2e/
git add features/research_agent.feature features/meta/bad_result_span.feature e2e/meta_test.go
git commit -m "test(e2e): span-attribute result green path + red meta sweep"
```

---

## Final Verification

After all tasks, confirm the whole module is clean:

```bash
gofmt -l .            # prints nothing
go vet ./...          # clean
go build ./...        # clean
go test ./...         # all hermetic packages pass
golangci-lint run     # clean (a .golangci.yml exists)
```

Coverage gate (the two touched packages):

```bash
go test ./internal/comparator/ ./internal/steps/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1 && rm cover.out
```

Expected: total ≥ 80% for each package.

## Self-Review (completed during planning)

- **Spec coverage:** §2 in-scope items map to tasks — `Source` field + resolution (Task 1); tool form (Task 4); selector form (Task 5); ordinal/quantifier addressing (Tasks 2–3 comparator, 4–5 grammar); `genai.ToolResult` (Task 1, Step 1). §2 out-of-scope (sidecar YAML, `status`+span source, `semantic`, TraceQL) is respected — `status`+span source is an explicit error (Task 1 test + Step 5 guard). §6 algorithm → `resolveSpanSource`/`selectSpans`/`extract`/`evaluate` (Task 1). §8 error table → Task 1/2/3 error cases. §9 filter-vs-extraction → `matchSpan` (selector) vs `extract` (attribute) with the missing-attr error test (Task 1). §11 testing (unit, step, L3, e2e, coverage) → Tasks 1–6.
- **Placeholder scan:** none — every step carries full code, exact commands, and expected output.
- **Type consistency:** `SpanSource{Selector, Attr, Quant, Index}`, `Quant` consts, and `ResultExpectation.Source` are defined in Task 1 and referenced verbatim in Tasks 4–5; handler signatures match the capture-group counts of their `sc.Step` regexes; `verbToMatcher` maps to the registered matcher names used by the existing matchers (`contains`/`exact`/`regex`/`json-subset`/`schema`).
