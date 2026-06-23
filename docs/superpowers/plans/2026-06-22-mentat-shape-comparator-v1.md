# Shape Comparator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `shape` comparator that asserts the *structure* of a run's trace forest — span existence/count, parent-child & descendant containment, and direct-child fan-out — via focused Gherkin steps.

**Architecture:** A new `core.Comparator` (`internal/comparator/shape.go`) that reads `Evidence.Trace` only (invariant #1) and matches in memory. Spans are selected by a **conjunction of exact-equality predicates** (`key=value`, AND-ed) over span attributes plus the reserved intrinsic-field keys `span.name`/`span.status`/`span.kind`. Registered at the composition root (`engine.Build`); driven by focused English steps in `internal/steps`.

**Tech Stack:** Go 1.25, `github.com/cucumber/godog` (BDD), `go.uber.org/mock` (only where interfaces are mocked — not needed here; the comparator is pure over `Evidence`). Design spec: `docs/superpowers/specs/2026-06-22-mentat-shape-comparator-design.md`.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat`; Go 1.25.
- **Format/vet clean:** `gofmt -l .` prints nothing; `go vet ./...` clean; `golangci-lint run` clean (a `.golangci.yml` exists).
- **Comparators consume `Evidence` only** — never a `TraceStore`/`Driver` (invariant #1).
- **`Trace` is a forest** — never assume a single root (invariant #2). Roots are spans with empty `ParentID` or whose parent is absent from the trace.
- **No silent fallbacks** — a function that cannot do its job returns a wrapped `error` (`fmt.Errorf("...: %w", err)`), never a zero-value success (invariant #4). Behavioural failure → `Verdict{Pass:false, Reasons:[...]}`; author/wiring bug → `error`.
- **Tests:** table-driven; hermetic (no network); ≥80% coverage for `internal/comparator`. `t.Parallel()` is a soft default for new table-driven tests sharing no mutable state.
- **Errors name the concrete thing + value:** `fmt.Errorf("shape: unknown count op %q", op)`, not `"invalid input"`.
- **Git:** Conventional Commits; `git add .` is forbidden (add files individually); **no AI attribution** in commits.
- **CRITICAL test-fixture note (applies to every structural test below):** `store.LoadFixture` (`internal/store/filestore.go:36-40`) sets `Name`/`Status`/`Attrs`/`ParentID` but **never sets `Span.ID`** (stays `""`) and links children by the parent's (non-unique) `Name`. Production traces come only from the **Tempo** store (`internal/engine/store.go:17`), which sets real unique `SpanID`/`ParentSpanID` — so containment/fan-out are correct in production. But **containment and fan-out unit tests MUST build `trace.Trace` literals with explicit `ID` and `ParentID`** (see `shapeTrace()` in Task 5), never `LoadFixture`. Existence/count tests don't use linkage, so any construction works.

---

## File Structure

- **Create** `internal/comparator/shape_selector.go` — the span **selector**: `Pred`, `Selector`, `ParseSelector`, `(Selector).matchSpan`, `(Selector).String`. Responsibility: "*which* spans."
- **Create** `internal/comparator/shape_selector_test.go` — selector unit tests.
- **Create** `internal/comparator/shape.go` — the comparator: `Count`, `ShapeExpectation`, `NewShape`, `Name`, `Compare` (validate + dispatch on `Kind`), and the per-kind matchers. Responsibility: "*what* structure."
- **Create** `internal/comparator/shape_test.go` — comparator unit tests (table-driven, literals).
- **Modify** `internal/engine/build.go` — register `"shape"`.
- **Modify** `internal/steps/steps.go` — eight focused steps + handlers.
- **Modify** `internal/steps/steps_test.go` — `shapeTrace()` builder + a passing feature + a red feature.
- **Create** `features/meta/bad_shape.feature` — a structurally-impossible assertion (L3).
- **Modify** `e2e/meta_test.go` — add the `bad_shape` case.

Tasks are ordered so each builds on the last and ends with an independently testable, committable deliverable.

---

### Task 1: Span selector — parse + match one span

**Files:**
- Create: `internal/comparator/shape_selector.go`
- Test: `internal/comparator/shape_selector_test.go`

**Interfaces:**
- Produces (used by Tasks 2–5):
  - `type Pred struct{ Key, Value string }`
  - `type Selector []Pred`
  - `func ParseSelector(s string) (Selector, error)`
  - `func (sel Selector) matchSpan(sp *trace.Span) bool`
  - `func (sel Selector) String() string` — canonical `{k1=v1, k2=v2}`

- [ ] **Step 1: Write the failing test**

Create `internal/comparator/shape_selector_test.go`:

```go
package comparator

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/trace"
)

func TestParseSelector(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    Selector
		wantErr bool
	}{
		{"single", "gen_ai.tool.name=search", Selector{{"gen_ai.tool.name", "search"}}, false},
		{"conjunction", "gen_ai.operation.name=execute_tool, gen_ai.tool.name=search",
			Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "search"}}, false},
		{"trims spaces", "  service.name = payment  ", Selector{{"service.name", "payment"}}, false},
		{"value may contain =", "k=a=b", Selector{{"k", "a=b"}}, false},
		{"reserved status", "span.status=ERROR", Selector{{"span.status", "ERROR"}}, false},
		{"empty selector", "", nil, true},
		{"blank selector", "   ", nil, true},
		{"missing equals", "service.name", nil, true},
		{"empty key", "=payment", nil, true},
		{"empty value", "service.name=", nil, true},
		{"empty predicate", "a=b,,c=d", nil, true},
		{"unknown reserved key", "span.staus=ERROR", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSelector(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSelector(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSelector(%q) unexpected error: %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSelector(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("pred[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSelectorMatchSpan(t *testing.T) {
	t.Parallel()
	sp := &trace.Span{
		ID: "s1", Name: "execute_tool search", Status: "ERROR", Kind: "INTERNAL",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"},
	}
	tests := []struct {
		name string
		sel  Selector
		want bool
	}{
		{"attr match", Selector{{"gen_ai.tool.name", "search"}}, true},
		{"attr mismatch", Selector{{"gen_ai.tool.name", "delete"}}, false},
		{"missing attr is non-match", Selector{{"service.name", "payment"}}, false},
		{"conjunction all hold", Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "search"}}, true},
		{"conjunction one fails", Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "delete"}}, false},
		{"reserved span.name", Selector{{"span.name", "execute_tool search"}}, true},
		{"reserved span.status", Selector{{"span.status", "ERROR"}}, true},
		{"reserved span.kind", Selector{{"span.kind", "INTERNAL"}}, true},
		{"reserved status mismatch", Selector{{"span.status", "OK"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.sel.matchSpan(sp); got != tt.want {
				t.Errorf("matchSpan = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectorString(t *testing.T) {
	t.Parallel()
	got := Selector{{"a", "1"}, {"b", "2"}}.String()
	if got != "{a=1, b=2}" {
		t.Errorf("String() = %q, want %q", got, "{a=1, b=2}")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run 'TestParseSelector|TestSelectorMatchSpan|TestSelectorString' -v`
Expected: FAIL — `undefined: ParseSelector` / `undefined: Selector` (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/comparator/shape_selector.go`:

```go
package comparator

import (
	"fmt"
	"strings"

	"github.com/thetonymaster/mentat/internal/trace"
)

// Pred is one exact-equality predicate. Key resolves to an intrinsic span field
// (the reserved keys span.name / span.status / span.kind) or, for any other key,
// the span's attribute map.
type Pred struct{ Key, Value string }

// Selector matches a span iff every predicate holds (exact string equality, AND-ed).
type Selector []Pred

// reservedKey reports whether k is one of the three intrinsic-field keys.
func reservedKey(k string) bool {
	switch k {
	case "span.name", "span.status", "span.kind":
		return true
	default:
		return false
	}
}

// ParseSelector parses a quoted conjunction like "k1=v1, k2=v2" into a Selector.
// It is a hard error (author bug, surfaced not swallowed) for the selector to be
// empty/blank, a clause to lack '=', a key or value to be empty, or a key under the
// reserved span.* namespace to be unrecognized.
func ParseSelector(s string) (Selector, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("shape: empty selector")
	}
	var sel Selector
	for _, raw := range strings.Split(s, ",") {
		clause := strings.TrimSpace(raw)
		if clause == "" {
			return nil, fmt.Errorf("shape: empty predicate in selector %q", s)
		}
		k, v, ok := strings.Cut(clause, "=") // split on FIRST '=' — values may contain '='
		if !ok {
			return nil, fmt.Errorf("shape: predicate %q missing '=' (want key=value)", clause)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			return nil, fmt.Errorf("shape: predicate %q has empty key", clause)
		}
		if v == "" {
			return nil, fmt.Errorf("shape: predicate %q has empty value", clause)
		}
		if strings.HasPrefix(k, "span.") && !reservedKey(k) {
			return nil, fmt.Errorf("shape: unknown reserved key %q (want span.name, span.status, or span.kind)", k)
		}
		sel = append(sel, Pred{Key: k, Value: v})
	}
	return sel, nil
}

// spanValue resolves a selector key against a span: reserved span.* keys read the
// intrinsic fields; everything else is an attribute lookup (missing attr → "").
func spanValue(sp *trace.Span, key string) string {
	switch key {
	case "span.name":
		return sp.Name
	case "span.status":
		return sp.Status
	case "span.kind":
		return sp.Kind
	default:
		return sp.Attr(key)
	}
}

// matchSpan reports whether sp satisfies every predicate (exact equality). A missing
// attribute yields "" and so does not equal a non-empty predicate value — i.e. a
// missing attribute is a non-match, not an error (the selector is a filter, not an
// identity extraction; this deliberately differs from the sequence comparator).
func (sel Selector) matchSpan(sp *trace.Span) bool {
	for _, p := range sel {
		if spanValue(sp, p.Key) != p.Value {
			return false
		}
	}
	return true
}

// String renders the canonical form {k1=v1, k2=v2} in declared order, for verdict reasons.
func (sel Selector) String() string {
	parts := make([]string, len(sel))
	for i, p := range sel {
		parts[i] = p.Key + "=" + p.Value
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run 'TestParseSelector|TestSelectorMatchSpan|TestSelectorString' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/comparator/shape_selector.go internal/comparator/shape_selector_test.go
go vet ./internal/comparator/
git add internal/comparator/shape_selector.go internal/comparator/shape_selector_test.go
git commit -m "feat(comparator): shape span selector (conjunction of key=value predicates)"
```

---

### Task 2: Shape comparator — existence / count / absent

**Files:**
- Create: `internal/comparator/shape.go`
- Test: `internal/comparator/shape_test.go`

**Interfaces:**
- Consumes (Task 1): `Selector`, `(Selector).matchSpan`, `(Selector).String`.
- Produces (used by Tasks 3–5):
  - `type Count struct{ Op string; N int }` (`Op` ∈ `">="`, `"=="`)
  - `type ShapeExpectation struct{ Kind string; Subject Selector; Parent Selector; Relation string; Count *Count }`
  - `func NewShape() core.Comparator`; `Name() → "shape"`; `Compare(ctx, ev, e) (core.Verdict, error)`
  - internal helper `matchingSpans(tr *trace.Trace, sel Selector) []*trace.Span`

- [ ] **Step 1: Write the failing test**

Create `internal/comparator/shape_test.go`:

```go
package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// flatTrace: three execute_tool spans (2 search, 1 summarize) with no parentage —
// sufficient for existence/count assertions, which ignore structure.
func flatTrace() *trace.Trace {
	mk := func(id, tool string) *trace.Span {
		return &trace.Span{ID: id, Name: "execute_tool " + tool,
			Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": tool}}
	}
	s1, s2, s3 := mk("s1", "search"), mk("s2", "search"), mk("s3", "summarize")
	return &trace.Trace{Roots: []*trace.Span{s1, s2, s3}, Spans: []*trace.Span{s1, s2, s3}}
}

func sel(t *testing.T, s string) Selector {
	t.Helper()
	parsed, err := ParseSelector(s)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", s, err)
	}
	return parsed
}

func TestShapeExistence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ShapeExpectation
		wantPass bool
	}{
		{"exists default >=1 passes", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search")}, true},
		{"exists default fails when absent", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=delete")}, false},
		{"at least 2 passes (two search)", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 2}}, true},
		{"at least 3 fails", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 3}}, false},
		{"exactly 2 passes", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 2}}, true},
		{"exactly 1 fails", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 1}}, false},
		{"absent passes when none", ShapeExpectation{Kind: "absent", Subject: sel(t, "gen_ai.tool.name=delete")}, true},
		{"absent fails when present", ShapeExpectation{Kind: "absent", Subject: sel(t, "gen_ai.tool.name=search")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: flatTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestShapeCompareErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ev   core.Evidence
		exp  core.Expectation
	}{
		{"wrong expectation type", core.Evidence{Trace: flatTrace()}, SequenceExpectation{}},
		{"nil trace", core.Evidence{}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b")}},
		{"empty subject", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists"}},
		{"unknown kind", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "bogus", Subject: sel(t, "a=b")}},
		{"unknown count op", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b"), Count: &Count{Op: "<", N: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewShape().Compare(context.Background(), tt.ev, tt.exp); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run 'TestShapeExistence|TestShapeCompareErrors' -v`
Expected: FAIL — `undefined: ShapeExpectation` / `undefined: NewShape` / `undefined: Count` (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/comparator/shape.go`:

```go
package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Count is a cardinality constraint. Op is ">=" or "==".
type Count struct {
	Op string
	N  int
}

// ok reports whether n satisfies the constraint. A nil Count means "at least 1".
func (c *Count) ok(n int) bool {
	if c == nil {
		return n >= 1
	}
	switch c.Op {
	case ">=":
		return n >= c.N
	case "==":
		return n == c.N
	default:
		return false // unreachable: Compare validates Op
	}
}

// describe renders the constraint for verdict reasons.
func (c *Count) describe() string {
	if c == nil {
		return "at least 1"
	}
	if c.Op == "==" {
		return fmt.Sprintf("exactly %d", c.N)
	}
	return fmt.Sprintf("at least %d", c.N)
}

// ShapeExpectation is one structural assertion. Each Gherkin step builds exactly one.
type ShapeExpectation struct {
	Kind     string   // "exists" | "absent" | "containment" | "fanout"
	Subject  Selector // the span being asserted about (the matched span / the child)
	Parent   Selector // containment & fanout: the container span; empty otherwise
	Relation string   // containment: "child" | "descendant"
	Count    *Count   // exists & fanout cardinality; nil ⇒ "at least 1"
}

type shape struct{}

// NewShape returns the structural ("shape") comparator. It reads Evidence.Trace only.
func NewShape() core.Comparator { return shape{} }
func (shape) Name() string      { return "shape" }

func (shape) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ShapeExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("shape: expectation must be ShapeExpectation, got %T", e)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("shape: Evidence.Trace is nil")
	}
	if len(exp.Subject) == 0 {
		return core.Verdict{}, fmt.Errorf("shape: Subject selector is empty")
	}
	if exp.Count != nil && exp.Count.Op != ">=" && exp.Count.Op != "==" {
		return core.Verdict{}, fmt.Errorf("shape: unknown count op %q (want \">=\" or \"==\")", exp.Count.Op)
	}
	switch exp.Kind {
	case "exists":
		return shapeExists(ev.Trace, exp), nil
	case "absent":
		return shapeAbsent(ev.Trace, exp), nil
	default:
		return core.Verdict{}, fmt.Errorf("shape: unknown Kind %q", exp.Kind)
	}
}

// matchingSpans returns every span in the forest satisfying sel.
func matchingSpans(tr *trace.Trace, sel Selector) []*trace.Span {
	var out []*trace.Span
	for _, s := range tr.Spans {
		if sel.matchSpan(s) {
			out = append(out, s)
		}
	}
	return out
}

func shapeExists(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	n := len(matchingSpans(tr, exp.Subject))
	if exp.Count.ok(n) {
		return core.Verdict{Pass: true}
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("expected %s spans matching %s, found %d", exp.Count.describe(), exp.Subject, n),
	}}
}

func shapeAbsent(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	n := len(matchingSpans(tr, exp.Subject))
	if n == 0 {
		return core.Verdict{Pass: true}
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("forbidden span matching %s was present (%d occurrence(s))", exp.Subject, n),
	}}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run 'TestShapeExistence|TestShapeCompareErrors' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/comparator/shape.go internal/comparator/shape_test.go
go vet ./internal/comparator/
git add internal/comparator/shape.go internal/comparator/shape_test.go
git commit -m "feat(comparator): shape existence/count/absent assertions"
```

---

### Task 3: Containment — child & descendant

**Files:**
- Modify: `internal/comparator/shape.go` (add `containment` validation + case + helpers)
- Test: `internal/comparator/shape_test.go` (add `TestShapeContainment`)

**Interfaces:**
- Consumes (Tasks 1–2): `Selector`, `matchingSpans`, `ShapeExpectation`.
- Produces: extends `Compare` to handle `Kind == "containment"`; internal `byIDIndex(tr)` and `isAncestor(byID, ancestorID, child)`.

> **Build structural test traces as literals with explicit `ID`/`ParentID`** (see the Global Constraints fixture note). `LoadFixture` leaves `ID=""`, which would make every `child.ParentID == parent.ID` comparison false.

- [ ] **Step 1: Write the failing test**

Add to `internal/comparator/shape_test.go`:

```go
// treeTrace: root → mid → leaf, plus a sibling "other" under root, in a SECOND root
// "root2 → orphan". Exercises direct-child, descendant, and cross-root (forest) cases.
func treeTrace() *trace.Trace {
	a := func(op string) map[string]string { return map[string]string{"gen_ai.operation.name": op} }
	root := &trace.Span{ID: "root", Name: "invoke_agent", Attrs: a("invoke_agent")}
	mid := &trace.Span{ID: "mid", ParentID: "root", Name: "chat", Attrs: a("chat")}
	leaf := &trace.Span{ID: "leaf", ParentID: "mid", Name: "execute_tool search",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"}}
	other := &trace.Span{ID: "other", ParentID: "root", Name: "execute_tool fetch",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "fetch"}}
	root2 := &trace.Span{ID: "root2", Name: "invoke_agent", Attrs: a("invoke_agent")}
	orphan := &trace.Span{ID: "orphan", ParentID: "root2", Name: "execute_tool pay",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "pay"}}
	return &trace.Trace{
		Roots: []*trace.Span{root, root2},
		Spans: []*trace.Span{root, mid, leaf, other, root2, orphan},
	}
}

func TestShapeContainment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ShapeExpectation
		wantPass bool
	}{
		{"direct child holds", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=chat")}, true},
		{"direct child fails for grandchild", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=invoke_agent")}, false},
		{"descendant holds (grandchild)", ShapeExpectation{Kind: "containment", Relation: "descendant",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=invoke_agent")}, true},
		{"descendant fails when unrelated", ShapeExpectation{Kind: "containment", Relation: "descendant",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.tool.name=fetch")}, false},
		{"cross-root child fails (different roots)", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=pay"), Parent: sel(t, "gen_ai.operation.name=chat")}, false},
		{"no matching parent fails", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.tool.name=nonexistent")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: treeTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestShapeContainmentValidation(t *testing.T) {
	t.Parallel()
	tests := []ShapeExpectation{
		{Kind: "containment", Subject: sel(t, "a=b"), Relation: "child"},                       // empty Parent
		{Kind: "containment", Subject: sel(t, "a=b"), Parent: sel(t, "c=d"), Relation: "uncle"}, // bad Relation
	}
	for i, exp := range tests {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			t.Parallel()
			if _, err := NewShape().Compare(context.Background(), core.Evidence{Trace: treeTrace()}, exp); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
```

Add `"fmt"` to the `shape_test.go` import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run 'TestShapeContainment' -v`
Expected: FAIL — containment cases error/panic or return `unknown Kind "containment"`.

- [ ] **Step 3: Write minimal implementation**

In `internal/comparator/shape.go`, add `"containment"` validation to the `switch exp.Kind` block in `Compare` — insert this case **before** `default:`:

```go
	case "containment":
		if len(exp.Parent) == 0 {
			return core.Verdict{}, fmt.Errorf("shape: containment requires a Parent selector")
		}
		if exp.Relation != "child" && exp.Relation != "descendant" {
			return core.Verdict{}, fmt.Errorf("shape: containment Relation must be \"child\" or \"descendant\", got %q", exp.Relation)
		}
		return shapeContainment(ev.Trace, exp), nil
```

Then append the helpers to `shape.go`:

```go
// byIDIndex maps span ID → span for ancestry walks. In a Tempo-sourced trace IDs are
// unique; structural assertions are only meaningful when IDs are populated.
func byIDIndex(tr *trace.Trace) map[string]*trace.Span {
	byID := make(map[string]*trace.Span, len(tr.Spans))
	for _, s := range tr.Spans {
		byID[s.ID] = s
	}
	return byID
}

// isAncestor reports whether the span with ancestorID lies on child's parent chain.
// The walk is bounded by the span count to stay safe on malformed (cyclic) traces.
func isAncestor(byID map[string]*trace.Span, ancestorID string, child *trace.Span) bool {
	cur := child
	for steps := 0; cur != nil && cur.ParentID != "" && steps < len(byID); steps++ {
		if cur.ParentID == ancestorID {
			return true
		}
		cur = byID[cur.ParentID]
	}
	return false
}

func shapeContainment(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	byID := byIDIndex(tr)
	children := matchingSpans(tr, exp.Subject)
	parents := matchingSpans(tr, exp.Parent)
	for _, c := range children {
		for _, p := range parents {
			if exp.Relation == "child" && c.ParentID == p.ID {
				return core.Verdict{Pass: true}
			}
			if exp.Relation == "descendant" && isAncestor(byID, p.ID, c) {
				return core.Verdict{Pass: true}
			}
		}
	}
	rel := "a child"
	if exp.Relation == "descendant" {
		rel = "a descendant"
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("no span matching %s is %s of a span matching %s", exp.Subject, rel, exp.Parent),
	}}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run 'TestShapeContainment|TestShapeContainmentValidation' -v`
Expected: PASS.

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/comparator/shape.go internal/comparator/shape_test.go
go vet ./internal/comparator/
git add internal/comparator/shape.go internal/comparator/shape_test.go
git commit -m "feat(comparator): shape parent-child and descendant containment"
```

---

### Task 4: Fan-out — direct-child cardinality

**Files:**
- Modify: `internal/comparator/shape.go` (add `fanout` validation + case + helper)
- Test: `internal/comparator/shape_test.go` (add `TestShapeFanout`)

**Interfaces:**
- Consumes: `Selector`, `matchingSpans`, `(Count).ok/describe`, `ShapeExpectation`.
- Produces: extends `Compare` to handle `Kind == "fanout"` (direct children only); internal `shapeFanout`.

- [ ] **Step 1: Write the failing test**

Add to `internal/comparator/shape_test.go`:

```go
// fanoutTrace: chatA has 3 search children; chatB has 1 search child. Used to prove
// the existential-over-parent reading (any one matching parent satisfying Count passes).
func fanoutTrace() *trace.Trace {
	tool := func(id, parent, name string) *trace.Span {
		return &trace.Span{ID: id, ParentID: parent, Name: "execute_tool " + name,
			Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name}}
	}
	chat := func(id string) *trace.Span {
		return &trace.Span{ID: id, Name: "chat", Attrs: map[string]string{"gen_ai.operation.name": "chat"}}
	}
	chatA, chatB := chat("chatA"), chat("chatB")
	return &trace.Trace{
		Roots: []*trace.Span{chatA, chatB},
		Spans: []*trace.Span{
			chatA, chatB,
			tool("a1", "chatA", "search"), tool("a2", "chatA", "search"), tool("a3", "chatA", "search"),
			tool("b1", "chatB", "search"),
		},
	}
}

func TestShapeFanout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ShapeExpectation
		wantPass bool
	}{
		{"at least 3 passes (chatA)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 3}}, true},
		{"at least 4 fails (max is 3)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 4}}, false},
		{"exactly 3 passes (chatA)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 3}}, true},
		{"exactly 2 fails (no parent has exactly 2)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 2}}, false},
		{"no matching parent fails", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=nope"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 1}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: fanoutTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestShapeFanoutValidation(t *testing.T) {
	t.Parallel()
	tests := []ShapeExpectation{
		{Kind: "fanout", Subject: sel(t, "a=b"), Count: &Count{">=", 1}},     // empty Parent
		{Kind: "fanout", Subject: sel(t, "a=b"), Parent: sel(t, "c=d")},      // nil Count
	}
	for i, exp := range tests {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			t.Parallel()
			if _, err := NewShape().Compare(context.Background(), core.Evidence{Trace: fanoutTrace()}, exp); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run 'TestShapeFanout' -v`
Expected: FAIL — fanout cases return `unknown Kind "fanout"`.

- [ ] **Step 3: Write minimal implementation**

In `Compare`'s `switch exp.Kind`, insert before `default:`:

```go
	case "fanout":
		if len(exp.Parent) == 0 {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires a Parent selector")
		}
		if exp.Count == nil {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires a Count")
		}
		return shapeFanout(ev.Trace, exp), nil
```

Append the helper to `shape.go` (fan-out counts **direct children only**, per the spec):

```go
func shapeFanout(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	parents := matchingSpans(tr, exp.Parent)
	best := 0
	for _, p := range parents {
		cnt := 0
		for _, s := range tr.Spans {
			if s.ParentID == p.ID && exp.Subject.matchSpan(s) {
				cnt++
			}
		}
		if exp.Count.ok(cnt) {
			return core.Verdict{Pass: true}
		}
		if cnt > best {
			best = cnt
		}
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("expected a span matching %s with %s children matching %s; best matching parent had %d",
			exp.Parent, exp.Count.describe(), exp.Subject, best),
	}}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run 'TestShapeFanout|TestShapeFanoutValidation' -v`
Expected: PASS.

- [ ] **Step 5: Full package test + coverage, then commit**

Run: `go test ./internal/comparator/ -cover`
Expected: PASS, coverage ≥ 80%.

```bash
gofmt -w internal/comparator/shape.go internal/comparator/shape_test.go
go vet ./internal/comparator/
git add internal/comparator/shape.go internal/comparator/shape_test.go
git commit -m "feat(comparator): shape direct-child fan-out cardinality"
```

---

### Task 5: Register comparator + Gherkin steps + hermetic feature tests

**Files:**
- Modify: `internal/engine/build.go` (register `"shape"`)
- Modify: `internal/steps/steps.go` (8 step bindings + handlers)
- Modify: `internal/steps/steps_test.go` (`shapeTrace()` + passing feature + red feature)

**Interfaces:**
- Consumes (Tasks 1–4): `comparator.NewShape`, `comparator.ParseSelector`, `comparator.ShapeExpectation`, `comparator.Count`.
- Produces: the eight shape steps in the godog grammar; `world` handler methods `shapeExists`/`shapeAbsent`/`shapeAtLeast`/`shapeExactly`/`shapeChildOf`/`shapeDescendantOf`/`shapeFanoutAtLeast`/`shapeFanoutExactly`.

- [ ] **Step 1: Register the comparator**

In `internal/engine/build.go`, add after the `"result"`/`"cel"` registrations (near line 26-27):

```go
	registry.RegisterComparator("shape", comparator.NewShape())
```

- [ ] **Step 2: Write the failing feature test (pass + red)**

In `internal/steps/steps_test.go`, add a structural trace builder and two tests. Build the trace as a **literal with IDs** (the `LoadFixture` path leaves `ID=""`):

```go
// shapeTrace: invoke_agent(root) → chat → {search, search, summarize(ERROR)}. IDs are
// set explicitly so shape's containment/fan-out matching works (LoadFixture omits IDs).
func shapeTrace() *trace.Trace {
	root := &trace.Span{ID: "root", Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
	chat := &trace.Span{ID: "chat", ParentID: "root", Name: "chat", Attrs: map[string]string{genai.Op: genai.OpChat}}
	mk := func(id, tool, status string) *trace.Span {
		return &trace.Span{ID: id, ParentID: "chat", Name: "execute_tool " + tool, Status: status,
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tool}}
	}
	s1, s2, sum := mk("t1", "search", "OK"), mk("t2", "search", "OK"), mk("t3", "summarize", "ERROR")
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: []*trace.Span{root, chat, s1, s2, sum}}
}

func TestFeatureExercisesShapeGrammar(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(shapeTrace(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: shape grammar
  Scenario: structural assertions hold
    Given the agent target "bot"
    When I run scenario "happy"
    Then a span matching "gen_ai.tool.name=search" exists
    And no span matching "gen_ai.tool.name=delete" exists
    And at least 2 spans match "gen_ai.tool.name=search"
    And exactly 1 span matches "gen_ai.tool.name=summarize"
    And a span matching "gen_ai.tool.name=search" is a child of a span matching "gen_ai.operation.name=chat"
    And a span matching "gen_ai.tool.name=search" is a descendant of a span matching "gen_ai.operation.name=invoke_agent"
    And a span matching "gen_ai.operation.name=chat" has at least 2 children matching "gen_ai.tool.name=search"
    And a span matching "span.status=ERROR" exists
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "shape", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}
}

// TestFeatureGoesRedOnBadShape: invoke_agent is a root, so asserting it is a child of a
// tool span must fail — the hermetic complement to the binary L3 meta-test (Task 6).
func TestFeatureGoesRedOnBadShape(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(shapeTrace(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: bad-shape
  Scenario: impossible containment
    Given the agent target "bot"
    When I run scenario "any"
    Then a span matching "gen_ai.operation.name=invoke_agent" is a child of a span matching "gen_ai.tool.name=search"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "bad-shape", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected suite to fail (non-zero), but it passed\n%s", out.String())
	}
	if !strings.Contains(out.String(), "shape failed") {
		t.Fatalf("expected output to contain \"shape failed\", got:\n%s", out.String())
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/steps/ -run 'TestFeatureExercisesShapeGrammar|TestFeatureGoesRedOnBadShape' -v`
Expected: FAIL — godog reports the shape steps as **undefined** (the pass test fails; the red test may pass for the wrong reason). Both pass only once the steps are bound.

- [ ] **Step 4: Bind the steps + handlers**

In `internal/steps/steps.go`, add these bindings inside `InitializerWithCollector`'s `func(sc *godog.ScenarioContext)`, alongside the existing `sc.Step(...)` calls:

```go
		sc.Step(`^a span matching "([^"]*)" exists$`, w.shapeExists)
		sc.Step(`^no span matching "([^"]*)" exists$`, w.shapeAbsent)
		sc.Step(`^at least (\d+) spans? match(?:es)? "([^"]*)"$`, w.shapeAtLeast)
		sc.Step(`^exactly (\d+) spans? match(?:es)? "([^"]*)"$`, w.shapeExactly)
		sc.Step(`^a span matching "([^"]*)" is a child of a span matching "([^"]*)"$`, w.shapeChildOf)
		sc.Step(`^a span matching "([^"]*)" is a descendant of a span matching "([^"]*)"$`, w.shapeDescendantOf)
		sc.Step(`^a span matching "([^"]*)" has at least (\d+) children matching "([^"]*)"$`, w.shapeFanoutAtLeast)
		sc.Step(`^a span matching "([^"]*)" has exactly (\d+) children matching "([^"]*)"$`, w.shapeFanoutExactly)
```

Add the handler methods to `internal/steps/steps.go` (anywhere among the other `func (w *world) ...` methods):

```go
func (w *world) shapeExists(s string) error {
	sel, err := comparator.ParseSelector(s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "exists", Subject: sel})
}

func (w *world) shapeAbsent(s string) error {
	sel, err := comparator.ParseSelector(s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "absent", Subject: sel})
}

func (w *world) shapeAtLeast(n int, s string) error {
	sel, err := comparator.ParseSelector(s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "exists", Subject: sel, Count: &comparator.Count{Op: ">=", N: n}})
}

func (w *world) shapeExactly(n int, s string) error {
	sel, err := comparator.ParseSelector(s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "exists", Subject: sel, Count: &comparator.Count{Op: "==", N: n}})
}

func (w *world) shapeChildOf(child, parent string) error {
	cs, err := comparator.ParseSelector(child)
	if err != nil {
		return err
	}
	ps, err := comparator.ParseSelector(parent)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "containment", Subject: cs, Parent: ps, Relation: "child"})
}

func (w *world) shapeDescendantOf(child, parent string) error {
	cs, err := comparator.ParseSelector(child)
	if err != nil {
		return err
	}
	ps, err := comparator.ParseSelector(parent)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "containment", Subject: cs, Parent: ps, Relation: "descendant"})
}

func (w *world) shapeFanoutAtLeast(parent string, n int, child string) error {
	ps, err := comparator.ParseSelector(parent)
	if err != nil {
		return err
	}
	cs, err := comparator.ParseSelector(child)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "fanout", Subject: cs, Parent: ps, Relation: "child", Count: &comparator.Count{Op: ">=", N: n}})
}

func (w *world) shapeFanoutExactly(parent string, n int, child string) error {
	ps, err := comparator.ParseSelector(parent)
	if err != nil {
		return err
	}
	cs, err := comparator.ParseSelector(child)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "fanout", Subject: cs, Parent: ps, Relation: "child", Count: &comparator.Count{Op: "==", N: n}})
}
```

(`comparator`, `core`, `bytes`, `strings`, `time`, `gomock`, `mocks`, `correlate`, `engine`, `genai`, `trace`, `config` are already imported in these files — no new imports needed.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/steps/ -run 'TestFeatureExercisesShapeGrammar|TestFeatureGoesRedOnBadShape' -v`
Expected: PASS (both).

- [ ] **Step 6: Run the full steps + engine packages, format, vet, commit**

Run: `go test ./internal/steps/ ./internal/engine/`
Expected: PASS.

```bash
gofmt -w internal/engine/build.go internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/steps/ ./internal/engine/
git add internal/engine/build.go internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): shape comparator Gherkin grammar + registration"
```

---

### Task 6: L3 binary meta-test (prove Mentat goes red)

**Files:**
- Create: `features/meta/bad_shape.feature`
- Modify: `e2e/meta_test.go` (add the `bad_shape` case)

**Interfaces:**
- Consumes (Task 5): the `shape` steps, available to the built `mentat` binary.

> This is the mandatory L3 meta-test (CLAUDE.md). It is `//go:build e2e` and runs against live Tempo (`make harness-up`). The bad assertion is structurally impossible regardless of exact live nesting: the `invoke_agent` root can never be a *child* of a tool span.

- [ ] **Step 1: Create the meta feature**

Create `features/meta/bad_shape.feature` (mirrors `features/meta/forbidden.feature`'s target/scenario):

```gherkin
Feature: meta - bad shape must fail
  Scenario: invoke_agent root cannot be a child of a tool span
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then a span matching "gen_ai.operation.name=invoke_agent" is a child of a span matching "gen_ai.tool.name=search"
```

- [ ] **Step 2: Add the failing meta-test case**

In `e2e/meta_test.go`, add one row to the `cases` slice in `TestBadScenariosAreCaught`:

```go
		{"features/meta/bad_shape.feature", "shape failed"},
```

- [ ] **Step 3: Bring up the harness and run the e2e meta-test**

Run:
```bash
make harness-up
go test -tags e2e ./e2e/ -run 'TestBadScenariosAreCaught/features_meta_bad_shape.feature' -v
```
Expected: PASS — the subtest confirms `mentat run features/meta/bad_shape.feature` exits non-zero with `"shape failed"` in its combined output.

- [ ] **Step 4: Commit**

```bash
git add features/meta/bad_shape.feature e2e/meta_test.go
git commit -m "test(e2e): L3 meta-test — shape comparator goes red on impossible structure"
```

---

## Final verification (after Task 6)

- [ ] `gofmt -l .` → prints nothing.
- [ ] `go vet ./...` → clean.
- [ ] `golangci-lint run` → clean.
- [ ] `go test ./...` → PASS (hermetic suite).
- [ ] `go test ./internal/comparator/ -cover` → ≥ 80%.
- [ ] (optional, needs harness) `go test -tags e2e ./e2e/ -run TestBadScenariosAreCaught` → PASS.
