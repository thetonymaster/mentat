# Mentat Multi-Run Aggregate Assertions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a scenario tagged `@runs(N)` execute N times and assert statistical/aggregate behaviour over the sample via a new `the runs satisfy "<CEL>"` grammar.

**Architecture:** Additive. A new `core.AggregateComparator` seam consumes `[]Evidence`; a `cel`-backed aggregate comparator builds one per-run record per run and evaluates a CEL expression over a `runs` list binding plus aggregate macros/functions. `Engine.DriveN` loops the existing per-run `Drive` (serial or parallel under the existing per-target semaphore) and converts harness failures into typed, visible samples. The single-run path (`the run satisfies`, the existing comparators, `Comparator.Compare`) is untouched.

**Tech Stack:** Go, `github.com/google/cel-go v0.28.1` (already a dependency), `github.com/cucumber/godog`, `go.uber.org/mock` (gomock). OpenTelemetry/Tempo unchanged.

**Design spec:** `docs/superpowers/specs/2026-06-19-mentat-multirun-runs-design.md`.

## Global Constraints

- Module path: `github.com/thetonymaster/mentat`.
- `gofmt -l .` clean and `go vet ./...` clean before every commit.
- No silent fallbacks: a function that cannot do its job returns a wrapped `error` (`fmt.Errorf("doing X: %w", err)`) naming the concrete value; never a zero-value success or guess.
- Comparators consume `Evidence` only — never a `TraceStore`/`Driver`.
- `Trace` is a forest — never assume one root.
- Table-driven tests; uber gomock for `core` interfaces (regen with `go generate ./...`, commit generated mocks).
- Coverage floor: 80% per package (check with `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`).
- Conventional Commits (`feat:`/`test:`/`refactor:`/`chore:`); `git add .` is forbidden — add files individually; **no AI attribution** in commits.
- The aggregate comparator registers under the name **`aggregate-cel`**.
- The per-run record `r` exposes: `runId`(string), `status`(int), `exitCode`(int), `bodyText`(string), `answer`(string), `tokens`(int), `cost`(double), `errors`(int), `latencyMs`(int), `tools`(list<string>), `services`(list<string>), `failed`(bool), `failureKind`(string `""`/`"driver"`/`"resolve"`). Trace-derived keys (`tokens`,`cost`,`errors`,`latencyMs`,`tools`,`services`) are omitted for a failed run.

---

## File Structure

**Create:**
- `internal/cel/aggregate.go` — `AggregateEngine`: declares the `runs` variable, registers aggregate macros (`rate`/`count`/`mean`/`sum`/`min`/`max`/`p50`/`p95`/`p99`/`stddev`) and their backing functions (`__sum__`/`__mean__`/`__min__`/`__max__`/`__stddev__`/`__percentile__`); `Compile` → `AggregateProgram` → `Eval`.
- `internal/cel/aggregate_test.go` — function math, macro expansion, compile/eval, bool-result enforcement.
- `internal/comparator/aggregate_cel.go` — `aggregateCEL` implementing `core.AggregateComparator`: builds per-run records from `[]Evidence` (reusing `tokenSum`/`costSum`/`errorCount`/`toolSequence`/`serviceSequence`), binds `runs`, evaluates, and renders the per-run failure table.
- `internal/comparator/aggregate_cel_test.go` — table-driven comparator tests (rates, percentiles, failed-sample handling, missing-key errors, reason rendering).

**Modify:**
- `internal/core/core.go` — add `Failed`/`FailureKind` to `Evidence`; add `AggregateComparator` interface.
- `internal/core/mocks/mock_core.go` — regenerate (adds `MockAggregateComparator`).
- `internal/registry/registry.go` — add the aggregate-comparator map + `RegisterAggregateComparator`/`AggregateComparator` accessors.
- `internal/engine/engine.go` — extract `driveOnce`; add `DriveN` and the `AggregateComparator` accessor.
- `internal/engine/build.go` — register `aggregate-cel`.
- `internal/steps/steps.go` — `world.evs`/`n`/`parallel`; parse `@runs(N[,parallel])`; route `drive` through `DriveN`; `the runs satisfy` step + precompile.
- `internal/steps/steps_test.go` — hermetic multi-run L3 meta-test (goes red on a bad distribution, green on a good one).

---

## Task 1: Core types — Evidence failure fields + AggregateComparator interface

**Files:**
- Modify: `internal/core/core.go:22-27` (Evidence), `:37-40` (after Comparator)
- Modify (generated): `internal/core/mocks/mock_core.go`

**Interfaces:**
- Produces: `core.Evidence{RunID string; Trace *trace.Trace; Output Output; Failed bool; FailureKind string}`; `core.AggregateComparator interface { Name() string; Aggregate(ctx context.Context, evs []Evidence, e Expectation) (Verdict, error) }`.

- [ ] **Step 1: Write the failing test**

Append to `internal/core/core_test.go` (create the file if absent — `package core`):

```go
package core

import "testing"

func TestEvidenceFailureFieldsDefaultZero(t *testing.T) {
	var ev Evidence
	if ev.Failed {
		t.Fatalf("zero Evidence must not be Failed")
	}
	if ev.FailureKind != "" {
		t.Fatalf("zero Evidence FailureKind = %q, want empty", ev.FailureKind)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestEvidenceFailureFieldsDefaultZero`
Expected: FAIL — compile error `ev.Failed undefined` / `ev.FailureKind undefined`.

- [ ] **Step 3: Add the fields and the interface**

In `internal/core/core.go`, change `Evidence` (lines 22-27) to:

```go
// Evidence is everything a comparator may inspect about a single run.
type Evidence struct {
	RunID  string
	Trace  *trace.Trace
	Output Output
	// Failed marks a harness-level failure for this run (driver invocation or trace
	// resolution). A failed run carries no Trace. FailureKind is "" when not failed,
	// else "driver" or "resolve" (classified by which engine call failed, §6).
	Failed      bool
	FailureKind string
}
```

Immediately after the `Comparator` interface (line 40) add:

```go
// AggregateComparator asserts a property across the N Evidence values of a
// multi-run (@runs) scenario. It is a sibling of Comparator, not a replacement:
// the single-Evidence Comparator and every existing comparator are unchanged.
type AggregateComparator interface {
	Name() string
	Aggregate(ctx context.Context, evs []Evidence, e Expectation) (Verdict, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestEvidenceFailureFieldsDefaultZero`
Expected: PASS.

- [ ] **Step 5: Regenerate mocks**

Run: `go install go.uber.org/mock/mockgen@latest && go generate ./internal/core/...`
Then verify the new mock exists:
Run: `grep -c "MockAggregateComparator" internal/core/mocks/mock_core.go`
Expected: a non-zero count (mockgen generated the struct + `Aggregate`/`Name` methods).

- [ ] **Step 6: Vet + commit**

```bash
gofmt -w internal/core/core.go internal/core/core_test.go
go vet ./internal/core/...
git add internal/core/core.go internal/core/core_test.go internal/core/mocks/mock_core.go
git commit -m "feat(core): add Evidence failure fields and AggregateComparator seam"
```

---

## Task 2: Aggregate CEL engine skeleton + backing math functions

**Files:**
- Create: `internal/cel/aggregate.go`
- Test: `internal/cel/aggregate_test.go`

**Interfaces:**
- Consumes: `github.com/google/cel-go/cel`, `.../common/types`, `.../common/types/ref`.
- Produces: `cel.NewAggregateEngine() (*AggregateEngine, error)`; `(*AggregateEngine).Compile(expr string) (*AggregateProgram, error)`; `(*AggregateProgram).Eval(vars map[string]any) (bool, error)`. The env declares variable `runs` (`list(dyn)`) and the functions `__sum__`/`__mean__`/`__min__`/`__max__`/`__stddev__` (`list(double)->double`) and `__percentile__` (`list(double),double->double`).

- [ ] **Step 1: Write the failing test**

Create `internal/cel/aggregate_test.go`:

```go
package cel

import (
	"testing"
)

func evalAgg(t *testing.T, expr string, vars map[string]any) bool {
	t.Helper()
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("NewAggregateEngine: %v", err)
	}
	prg, err := eng.Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	got, err := prg.Eval(vars)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	return got
}

func TestAggregateBackingFunctions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"sum", "__sum__([1.0, 2.0, 3.0]) == 6.0", true},
		{"mean", "__mean__([2.0, 4.0]) == 3.0", true},
		{"min", "__min__([5.0, 1.0, 3.0]) == 1.0", true},
		{"max", "__max__([5.0, 1.0, 3.0]) == 5.0", true},
		{"percentile nearest-rank p95 of 1..10 is 10", "__percentile__([1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0,10.0], 0.95) == 10.0", true},
		{"percentile p50 of 1..10 is 5 (ceil(0.5*10)=5)", "__percentile__([1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0,10.0], 0.5) == 5.0", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := evalAgg(t, tt.expr, map[string]any{"runs": []any{}}); got != tt.want {
				t.Fatalf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestAggregateStddev(t *testing.T) {
	// population stddev of [2,4,4,4,5,5,7,9] is 2.0
	expr := "__stddev__([2.0,4.0,4.0,4.0,5.0,5.0,7.0,9.0]) == 2.0"
	if !evalAgg(t, expr, map[string]any{"runs": []any{}}) {
		t.Fatalf("stddev mismatch")
	}
}

func TestAggregateNonBoolRejected(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("NewAggregateEngine: %v", err)
	}
	if _, err := eng.Compile("__sum__([1.0])"); err == nil {
		t.Fatalf("expected non-bool expression to be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cel/ -run TestAggregate`
Expected: FAIL — `undefined: NewAggregateEngine`.

- [ ] **Step 3: Write the engine + functions**

Create `internal/cel/aggregate.go`:

```go
package cel

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// VarRuns is the list binding the aggregate grammar evaluates over: one record
// per run of an @runs(N) scenario.
const VarRuns = "runs"

// AggregateEngine is a CEL environment for cross-run assertions. It declares the
// `runs` list plus the aggregate macros and their backing functions.
type AggregateEngine struct{ env *celgo.Env }

// AggregateProgram is a type-checked aggregate expression whose result is bool.
type AggregateProgram struct {
	expr string
	prg  celgo.Program
}

// NewAggregateEngine builds the aggregate CEL environment.
func NewAggregateEngine() (*AggregateEngine, error) {
	opts := []celgo.EnvOption{
		celgo.Variable(VarRuns, celgo.ListType(celgo.DynType)),
	}
	opts = append(opts, aggFuncs()...)
	opts = append(opts, aggMacros()...)
	env, err := celgo.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("cel: building aggregate environment: %w", err)
	}
	return &AggregateEngine{env: env}, nil
}

// Compile type-checks expr; the result type must be bool (no silent fallback).
func (e *AggregateEngine) Compile(expr string) (*AggregateProgram, error) {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: compiling aggregate %q: %w", expr, iss.Err())
	}
	if !ast.OutputType().IsExactType(celgo.BoolType) {
		return nil, fmt.Errorf("cel: aggregate %q must evaluate to bool, got %s", expr, ast.OutputType())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel: building aggregate program for %q: %w", expr, err)
	}
	return &AggregateProgram{expr: expr, prg: prg}, nil
}

// Eval runs the program against bound vars (must include "runs").
func (p *AggregateProgram) Eval(vars map[string]any) (bool, error) {
	out, _, err := p.prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("cel: evaluating aggregate %q: %w", p.expr, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("cel: aggregate %q did not return bool, got %T", p.expr, out.Value())
	}
	return b, nil
}

// toFloats converts a CEL list value into a []float64.
func toFloats(v ref.Val) ([]float64, error) {
	out, err := v.ConvertToNative(reflect.TypeOf([]float64{}))
	if err != nil {
		return nil, fmt.Errorf("cel: aggregate list is not numeric: %w", err)
	}
	return out.([]float64), nil
}

// aggFuncs registers the list-reducing functions the macros expand into.
func aggFuncs() []celgo.EnvOption {
	listDouble := celgo.ListType(celgo.DoubleType)
	unary := func(name string, fn func([]float64) float64) celgo.EnvOption {
		return celgo.Function(name, celgo.Overload(name+"_list",
			[]*celgo.Type{listDouble}, celgo.DoubleType,
			celgo.UnaryBinding(func(v ref.Val) ref.Val {
				xs, err := toFloats(v)
				if err != nil {
					return types.WrapErr(err)
				}
				if len(xs) == 0 {
					return types.NewErr("cel: %s over an empty sample", name)
				}
				return types.Double(fn(xs))
			})))
	}
	return []celgo.EnvOption{
		unary("__sum__", sum),
		unary("__mean__", mean),
		unary("__min__", minOf),
		unary("__max__", maxOf),
		unary("__stddev__", stddev),
		celgo.Function("__percentile__", celgo.Overload("__percentile___list_double",
			[]*celgo.Type{listDouble, celgo.DoubleType}, celgo.DoubleType,
			celgo.BinaryBinding(func(l, r ref.Val) ref.Val {
				xs, err := toFloats(l)
				if err != nil {
					return types.WrapErr(err)
				}
				if len(xs) == 0 {
					return types.NewErr("cel: percentile over an empty sample")
				}
				q, ok := r.Value().(float64)
				if !ok {
					return types.NewErr("cel: percentile quantile must be double, got %T", r.Value())
				}
				return types.Double(percentile(xs, q))
			}))),
	}
}

func sum(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

func mean(xs []float64) float64 { return sum(xs) / float64(len(xs)) }

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x < m {
			m = x
		}
	}
	return m
}

func maxOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

func stddev(xs []float64) float64 {
	m := mean(xs)
	var ss float64
	for _, x := range xs {
		d := x - m
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)))
}

// percentile is nearest-rank (no interpolation): rank = ceil(q*N), 1-indexed.
func percentile(xs []float64, q float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	switch {
	case q <= 0:
		return s[0]
	case q >= 1:
		return s[len(s)-1]
	}
	rank := int(math.Ceil(q * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}

// aggMacros is implemented in Task 3 and Task 4; start with an empty set so the
// engine compiles. Replace this stub when adding the macros.
func aggMacros() []celgo.EnvOption { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cel/ -run TestAggregate`
Expected: PASS (all backing-function subtests + non-bool rejection).

- [ ] **Step 5: Vet + commit**

```bash
gofmt -w internal/cel/aggregate.go internal/cel/aggregate_test.go
go vet ./internal/cel/...
git add internal/cel/aggregate.go internal/cel/aggregate_test.go
git commit -m "feat(cel): aggregate CEL engine skeleton and list-reducing functions"
```

---

## Task 3: `count` and `rate` macros (filter-based)

**Files:**
- Modify: `internal/cel/aggregate.go` (replace `aggMacros`, add helpers + imports)
- Test: `internal/cel/aggregate_test.go`

**Interfaces:**
- Produces: global macros `count(r, P) -> int` (= `size(runs.filter(r, P))`) and `rate(r, P) -> double` (= `double(count)/double(size(runs))`), where `P` is a boolean per-run predicate.

- [ ] **Step 1: Write the failing test**

Append to `internal/cel/aggregate_test.go`:

```go
func runsFixture() map[string]any {
	// 4 runs; "search" present in 3; latencyMs 100,200,300,400.
	mk := func(tools []any, lat int64, failed bool) map[string]any {
		return map[string]any{"tools": tools, "latencyMs": lat, "failed": failed}
	}
	return map[string]any{"runs": []any{
		mk([]any{"search", "summarize"}, 100, false),
		mk([]any{"summarize"}, 200, false),
		mk([]any{"search"}, 300, false),
		mk([]any{"search", "summarize"}, 400, false),
	}}
}

func TestRateCountMacros(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"count present", `count(r, "search" in r.tools) == 3`, true},
		{"count absent predicate", `count(r, r.failed) == 0`, true},
		{"rate threshold met", `rate(r, "search" in r.tools) >= 0.75`, true},
		{"rate threshold missed", `rate(r, "search" in r.tools) >= 0.8`, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := evalAgg(t, tt.expr, runsFixture()); got != tt.want {
				t.Fatalf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cel/ -run TestRateCountMacros`
Expected: FAIL — compile error `undeclared reference to 'count'` (macro not registered).

- [ ] **Step 3: Implement the macros**

In `internal/cel/aggregate.go`, extend the import block with:

```go
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
```

Replace the `aggMacros` stub with:

```go
// aggMacros registers the aggregate readability macros. Each is a global macro
// over the implicit `runs` binding; they expand into ordinary comprehensions (the
// same machinery CEL's own filter/map use) plus the backing __*__ functions.
func aggMacros() []celgo.EnvOption {
	return []celgo.EnvOption{
		celgo.Macros(
			celgo.GlobalMacro("count", 2, countExpander),
			celgo.GlobalMacro("rate", 2, rateExpander),
		),
	}
}

// iterVar extracts the iteration-variable name from a macro's first argument.
func iterVar(eh celgo.MacroExprFactory, arg ast.Expr) (string, *common.Error) {
	if arg.Kind() != ast.IdentKind {
		return "", eh.NewError(arg.ID(), "first argument must be an identifier (e.g. r)")
	}
	return arg.AsIdent(), nil
}

// filterList builds `runs.filter(<v>, <pred>)` as a comprehension yielding a list.
func filterList(eh celgo.MacroExprFactory, v string, pred ast.Expr) ast.Expr {
	accu := eh.AccuIdentName()
	init := eh.NewList()
	cond := eh.NewLiteral(types.True)
	step := eh.NewCall(operators.Add, eh.NewAccuIdent(), eh.NewList(eh.NewIdent(v)))
	step = eh.NewCall(operators.Conditional, pred, step, eh.NewAccuIdent())
	return eh.NewComprehension(eh.NewIdent(VarRuns), v, accu, init, cond, step, eh.NewAccuIdent())
}

func countExpander(eh celgo.MacroExprFactory, _ ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	v, err := iterVar(eh, args[0])
	if err != nil {
		return nil, err
	}
	return eh.NewCall("size", filterList(eh, v, args[1])), nil
}

func rateExpander(eh celgo.MacroExprFactory, _ ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	v, err := iterVar(eh, args[0])
	if err != nil {
		return nil, err
	}
	num := eh.NewCall("double", eh.NewCall("size", filterList(eh, v, args[1])))
	den := eh.NewCall("double", eh.NewCall("size", eh.NewIdent(VarRuns)))
	return eh.NewCall(operators.Divide, num, den), nil
}
```

> Note: register macros with `celgo.GlobalMacro` (the modern constructor) — NOT the deprecated `celgo.NewGlobalMacro`, which takes a legacy `exprpb`-based expander and will not match this signature. The expander type is `celgo.MacroFactory` (= `parser.MacroExpander`): `func(eh celgo.MacroExprFactory, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error)`, where `celgo.MacroExprFactory` aliases `parser.ExprHelper`. `eh.AccuIdentName()`/`eh.NewAccuIdent()` provide the comprehension accumulator. This mirrors `parser.MakeFilter` in cel-go v0.28.1. Do not import `parser` directly — everything is reachable through the `celgo`, `ast`, `operators`, `types`, and `common` packages.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cel/ -run 'TestRateCountMacros|TestAggregate'`
Expected: PASS.

- [ ] **Step 5: Vet + commit**

```bash
gofmt -w internal/cel/aggregate.go internal/cel/aggregate_test.go
go vet ./internal/cel/...
git add internal/cel/aggregate.go internal/cel/aggregate_test.go
git commit -m "feat(cel): rate and count aggregate macros"
```

---

## Task 4: metric macros (`mean`/`sum`/`min`/`max`/`stddev`) + percentiles (`p50`/`p95`/`p99`)

**Files:**
- Modify: `internal/cel/aggregate.go` (extend `aggMacros`, add map/projection helpers)
- Test: `internal/cel/aggregate_test.go`

**Interfaces:**
- Produces: global macros `mean|sum|min|max|stddev(r, X) -> double` (= `__fn__(runs.map(r, double(X)))`) and `p50|p95|p99(r, X) -> double` (= `__percentile__(runs.map(r, double(X)), q)`), where `X` is a numeric per-run projection.

- [ ] **Step 1: Write the failing test**

Append to `internal/cel/aggregate_test.go`:

```go
func TestMetricMacros(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"mean latency", "mean(r, r.latencyMs) == 250.0", true},      // (100+200+300+400)/4
		{"max latency", "max(r, r.latencyMs) == 400.0", true},
		{"min latency", "min(r, r.latencyMs) == 100.0", true},
		{"sum latency", "sum(r, r.latencyMs) == 1000.0", true},
		{"p95 latency nearest-rank", "p95(r, r.latencyMs) == 400.0", true},
		{"p50 latency", "p50(r, r.latencyMs) == 200.0", true}, // ceil(0.5*4)=2 -> 2nd of [100,200,300,400]
		{"int field coerced to double", "mean(r, r.latencyMs) < 300.0", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := evalAgg(t, tt.expr, runsFixture()); got != tt.want {
				t.Fatalf("%q = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cel/ -run TestMetricMacros`
Expected: FAIL — `undeclared reference to 'mean'`.

- [ ] **Step 3: Implement the metric macros**

In `internal/cel/aggregate.go`, add the `mapDoubles` helper and the metric/percentile expanders, and register them in `aggMacros`:

```go
// mapDoubles builds `runs.map(<v>, double(<proj>))` as a comprehension yielding a
// list<double>, coercing the projection so int fields (latencyMs, tokens) work.
func mapDoubles(eh celgo.MacroExprFactory, v string, proj ast.Expr) ast.Expr {
	accu := eh.AccuIdentName()
	init := eh.NewList()
	cond := eh.NewLiteral(types.True)
	elem := eh.NewCall("double", proj)
	step := eh.NewCall(operators.Add, eh.NewAccuIdent(), eh.NewList(elem))
	return eh.NewComprehension(eh.NewIdent(VarRuns), v, accu, init, cond, step, eh.NewAccuIdent())
}

// metricExpander expands fn(r, X) -> __fn__(runs.map(r, double(X))).
func metricExpander(fn string) celgo.MacroFactory {
	return func(eh celgo.MacroExprFactory, _ ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
		v, err := iterVar(eh, args[0])
		if err != nil {
			return nil, err
		}
		return eh.NewCall(fn, mapDoubles(eh, v, args[1])), nil
	}
}

// percentileExpander expands pNN(r, X) -> __percentile__(runs.map(r, double(X)), q).
func percentileExpander(q float64) celgo.MacroFactory {
	return func(eh celgo.MacroExprFactory, _ ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
		v, err := iterVar(eh, args[0])
		if err != nil {
			return nil, err
		}
		return eh.NewCall("__percentile__", mapDoubles(eh, v, args[1]), eh.NewLiteral(types.Double(q))), nil
	}
}
```

Then change `aggMacros` to include them:

```go
func aggMacros() []celgo.EnvOption {
	return []celgo.EnvOption{
		celgo.Macros(
			celgo.GlobalMacro("count", 2, countExpander),
			celgo.GlobalMacro("rate", 2, rateExpander),
			celgo.GlobalMacro("sum", 2, metricExpander("__sum__")),
			celgo.GlobalMacro("mean", 2, metricExpander("__mean__")),
			celgo.GlobalMacro("min", 2, metricExpander("__min__")),
			celgo.GlobalMacro("max", 2, metricExpander("__max__")),
			celgo.GlobalMacro("stddev", 2, metricExpander("__stddev__")),
			celgo.GlobalMacro("p50", 2, percentileExpander(0.50)),
			celgo.GlobalMacro("p95", 2, percentileExpander(0.95)),
			celgo.GlobalMacro("p99", 2, percentileExpander(0.99)),
		),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cel/`
Expected: PASS (all aggregate tests).

- [ ] **Step 5: Coverage check + vet + commit**

```bash
go test ./internal/cel/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
gofmt -w internal/cel/aggregate.go internal/cel/aggregate_test.go
go vet ./internal/cel/...
git add internal/cel/aggregate.go internal/cel/aggregate_test.go
git commit -m "feat(cel): metric and percentile aggregate macros"
```

Expected coverage: ≥80% for `internal/cel`.

---

## Task 5: aggregate-cel comparator — per-run records + happy-path Aggregate

**Files:**
- Create: `internal/comparator/aggregate_cel.go`
- Test: `internal/comparator/aggregate_cel_test.go`

**Interfaces:**
- Consumes: `cel.NewAggregateEngine`/`AggregateProgram` (Task 2-4); package-local `tokenSum`/`costSum`/`errorCount`/`toolSequence`/`serviceSequence` (`budgets.go`, `sequence.go`).
- Produces: `comparator.AggregateCELExpectation{Expr string}`; `comparator.NewAggregateCEL(pricing core.Pricing) core.AggregateComparator` (Name == `"aggregate-cel"`), with a `Compile(expr string) error` method for scenario-init precompilation.

- [ ] **Step 1: Write the failing test**

Create `internal/comparator/aggregate_cel_test.go`:

```go
package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// traceWithTools builds a minimal agent trace whose tool sequence is `tools`.
func traceWithTools(tools ...string) *trace.Trace {
	root := &trace.Span{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
	spans := []*trace.Span{root}
	for _, tl := range tools {
		spans = append(spans, &trace.Span{Name: "tool " + tl, Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tl}})
	}
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: spans}
}

func evidence(tools ...string) core.Evidence {
	return core.Evidence{RunID: "r", Trace: traceWithTools(tools...), Output: core.Output{}}
}

func TestAggregateRateHappyPath(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{
		evidence("search", "summarize"),
		evidence("summarize"),
		evidence("search"),
		evidence("search", "summarize"),
	}
	tests := []struct {
		name     string
		expr     string
		wantPass bool
	}{
		{"rate met", `rate(r, "search" in r.tools) >= 0.75`, true},
		{"rate missed", `rate(r, "search" in r.tools) >= 0.8`, false},
		{"count failed zero", `count(r, r.failed) == 0`, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v (reasons %v)", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestAggregateWrongExpectationType(t *testing.T) {
	c := NewAggregateCEL(nil)
	if _, err := c.Aggregate(context.Background(), nil, "nope"); err == nil {
		t.Fatalf("expected error for wrong expectation type")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run TestAggregate`
Expected: FAIL — `undefined: NewAggregateCEL`.

- [ ] **Step 3: Implement the comparator (happy path)**

Create `internal/comparator/aggregate_cel.go`:

```go
package comparator

import (
	"context"
	"fmt"
	"sync"

	celengine "github.com/thetonymaster/mentat/internal/cel"
	"github.com/thetonymaster/mentat/internal/core"
)

// AggregateCELExpectation carries one boolean CEL expression over the `runs` sample.
type AggregateCELExpectation struct {
	Expr string
}

type aggregateCEL struct {
	engine   *celengine.AggregateEngine
	pricing  core.Pricing
	mu       sync.RWMutex
	programs map[string]*celengine.AggregateProgram
}

// NewAggregateCEL returns the cross-run CEL comparator (Name() == "aggregate-cel").
func NewAggregateCEL(pricing core.Pricing) core.AggregateComparator {
	eng, err := celengine.NewAggregateEngine()
	if err != nil {
		panic(fmt.Sprintf("aggregate-cel: static env failed to build: %v", err))
	}
	return &aggregateCEL{engine: eng, pricing: pricing, programs: map[string]*celengine.AggregateProgram{}}
}

func (c *aggregateCEL) Name() string { return "aggregate-cel" }

// Compile type-checks and caches expr at scenario-init (so a malformed expression
// fails before any SUT is driven). Safe for concurrent scenarios.
func (c *aggregateCEL) Compile(expr string) error {
	c.mu.RLock()
	_, ok := c.programs[expr]
	c.mu.RUnlock()
	if ok {
		return nil
	}
	prg, err := c.engine.Compile(expr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.programs[expr] = prg
	c.mu.Unlock()
	return nil
}

func (c *aggregateCEL) program(expr string) (*celengine.AggregateProgram, error) {
	c.mu.RLock()
	prg, ok := c.programs[expr]
	c.mu.RUnlock()
	if ok {
		return prg, nil
	}
	if err := c.Compile(expr); err != nil {
		return nil, err
	}
	c.mu.RLock()
	prg = c.programs[expr]
	c.mu.RUnlock()
	return prg, nil
}

// Aggregate builds one record per run, binds `runs`, and evaluates expr.
func (c *aggregateCEL) Aggregate(_ context.Context, evs []core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(AggregateCELExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("aggregate-cel: expectation must be AggregateCELExpectation, got %T", e)
	}
	prg, err := c.program(exp.Expr)
	if err != nil {
		return core.Verdict{}, err
	}
	records := make([]any, 0, len(evs))
	for i, ev := range evs {
		rec, err := c.record(ev)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("aggregate-cel: run %d (%s): %w", i, ev.RunID, err)
		}
		records = append(records, rec)
	}
	pass, err := prg.Eval(map[string]any{celengine.VarRuns: records})
	if err != nil {
		return core.Verdict{}, err
	}
	if pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{aggregateReason(exp.Expr, evs)}}, nil
}

// record builds the per-run CEL map. A successful run gets every field; a failed
// run omits the six trace-derived keys (so referencing one is a hard CEL error).
func (c *aggregateCEL) record(ev core.Evidence) (map[string]any, error) {
	rec := map[string]any{
		"runId":       ev.RunID,
		"failed":      ev.Failed,
		"failureKind": ev.FailureKind,
		"status":      int64(ev.Output.Status),
		"exitCode":    int64(ev.Output.ExitCode),
		"bodyText":    string(ev.Output.Body),
		"answer":      ev.Output.Answer,
	}
	if ev.Failed || ev.Trace == nil {
		return rec, nil
	}
	tokens, err := tokenSum(ev.Trace)
	if err != nil {
		return nil, fmt.Errorf("binding tokens: %w", err)
	}
	cost, err := costSum(ev.Trace, c.pricing)
	if err != nil {
		return nil, fmt.Errorf("binding cost: %w", err)
	}
	tools, err := toolSequence(ev.Trace)
	if err != nil {
		return nil, fmt.Errorf("binding tools: %w", err)
	}
	svcs, err := serviceSequence(ev.Trace)
	if err != nil {
		return nil, fmt.Errorf("binding services: %w", err)
	}
	rec["tokens"] = int64(tokens)
	rec["cost"] = cost
	rec["errors"] = int64(errorCount(ev.Trace))
	rec["latencyMs"] = ev.Trace.Envelope().Milliseconds()
	rec["tools"] = toAnyList(tools)
	rec["services"] = toAnyList(svcs)
	return rec, nil
}

func toAnyList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// aggregateReason is replaced with the per-run table in Task 6.
func aggregateReason(expr string, _ []core.Evidence) string {
	return fmt.Sprintf("aggregate false: %q", expr)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run TestAggregate`
Expected: PASS.

- [ ] **Step 5: Vet + commit**

```bash
gofmt -w internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
go vet ./internal/comparator/...
git add internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
git commit -m "feat(comparator): aggregate-cel comparator with per-run records"
```

---

## Task 6: failed-run records + per-run failure table reason

**Files:**
- Modify: `internal/comparator/aggregate_cel.go` (`aggregateReason`, add imports `sort`/`strings`)
- Test: `internal/comparator/aggregate_cel_test.go`

**Interfaces:**
- Produces: `aggregateReason(expr string, evs []Evidence) string` rendering the expression plus a compact per-run table (index, `runId`, `failed`, `failureKind`).

- [ ] **Step 1: Write the failing test**

Append to `internal/comparator/aggregate_cel_test.go`:

```go
func failedEvidence(runID, kind string) core.Evidence {
	return core.Evidence{RunID: runID, Failed: true, FailureKind: kind}
}

func TestAggregateFailedSamples(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{
		evidence("search"),
		failedEvidence("r-bad", "resolve"),
		evidence("search"),
	}

	// rate over r.failed works even though a run has no trace.
	v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, r.failed) < 0.5`})
	if err != nil {
		t.Fatalf("Aggregate(rate failed): %v", err)
	}
	if !v.Pass {
		t.Fatalf("rate(r, r.failed) < 0.5 should pass with 1/3 failed")
	}

	// scoped metric skips the failed run via short-circuit &&.
	v, err = c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, !r.failed && "search" in r.tools) >= 0.66`})
	if err != nil {
		t.Fatalf("Aggregate(scoped): %v", err)
	}
	if !v.Pass {
		t.Fatalf("scoped rate should pass: 2/3 runs have search")
	}

	// UNscoped metric over a failed run is a hard error (missing key), not a guess.
	if _, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `mean(r, r.latencyMs) < 9999.0`}); err == nil {
		t.Fatalf("expected hard error for metric over a failed run")
	}
}

func TestAggregateReasonHasPerRunTable(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{evidence("summarize"), failedEvidence("r-2", "driver")}
	v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, "search" in r.tools) >= 0.9`})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if v.Pass {
		t.Fatalf("expected fail")
	}
	reason := v.Reasons[0]
	for _, sub := range []string{"r-2", "driver", "run", `rate(r, "search" in r.tools) >= 0.9`} {
		if !contains(reason, sub) {
			t.Fatalf("reason %q missing %q", reason, sub)
		}
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run 'TestAggregateFailedSamples|TestAggregateReasonHasPerRunTable'`
Expected: FAIL — the failed-samples test passes already, but `TestAggregateReasonHasPerRunTable` FAILs (reason lacks `r-2`/`driver`).

> If `TestAggregateFailedSamples` fails instead, the record-building from Task 5 is wrong — fix that before proceeding.

- [ ] **Step 3: Implement the per-run table**

In `internal/comparator/aggregate_cel.go`, add `"sort"` and `"strings"` to the imports and replace `aggregateReason`:

```go
// aggregateReason renders the failing expression plus a compact per-run table so a
// reader can copy a test.run.id into /traces to inspect the offending run (§8).
func aggregateReason(expr string, evs []core.Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "aggregate false: %q  (%d runs)\n", expr, len(evs))
	fmt.Fprintf(&b, "  run  test.run.id            failed  kind\n")
	for i, ev := range evs {
		fmt.Fprintf(&b, "  %-3d  %-22s %-7t %s\n", i, ev.RunID, ev.Failed, ev.FailureKind)
	}
	_ = sort.Strings // table order is iteration order (stable)
	return strings.TrimRight(b.String(), "\n")
}
```

> `sort` is referenced to document that ordering is intentional iteration order; if `go vet` flags the unused-via-blank pattern, drop the `sort` import and the `_ = sort.Strings` line.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comparator/ -run TestAggregate`
Expected: PASS.

- [ ] **Step 5: Coverage + vet + commit**

```bash
go test ./internal/comparator/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
gofmt -w internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
go vet ./internal/comparator/...
git add internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
git commit -m "feat(comparator): typed failed-run records and per-run failure table"
```

Expected coverage: ≥80% for `internal/comparator`.

---

## Task 7: registry + engine wiring for the aggregate comparator

**Files:**
- Modify: `internal/registry/registry.go`
- Modify: `internal/engine/build.go:26`, `internal/engine/engine.go` (add accessor)
- Test: `internal/registry/registry_test.go`, `internal/engine/engine_test.go`

**Interfaces:**
- Produces: `registry.RegisterAggregateComparator(name string, c core.AggregateComparator)`; `registry.AggregateComparator(name string) (core.AggregateComparator, bool)`; `(*engine.Engine).AggregateComparator(name string) (core.AggregateComparator, bool)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/registry/registry_test.go` (`package registry`):

```go
func TestAggregateComparatorRegistry(t *testing.T) {
	if _, ok := AggregateComparator("missing-agg"); ok {
		t.Fatalf("unexpected aggregate comparator before registration")
	}
}
```

Append to `internal/engine/engine_test.go`:

```go
func TestAggregateComparatorLookup(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := eng.AggregateComparator("aggregate-cel"); !ok {
		t.Fatalf("aggregate-cel must be registered")
	}
	if _, ok := eng.AggregateComparator("nope"); ok {
		t.Fatalf("unknown aggregate comparator must not be found")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/registry/ ./internal/engine/ -run 'TestAggregateComparatorRegistry|TestAggregateComparatorLookup'`
Expected: FAIL — `undefined: AggregateComparator` / `eng.AggregateComparator undefined`.

- [ ] **Step 3: Implement registry + engine accessor + Build registration**

In `internal/registry/registry.go`, add to the `var (...)` block:

```go
	aggregateComparators = map[string]core.AggregateComparator{}
```

and add accessors:

```go
// RegisterAggregateComparator registers an AggregateComparator under the given name.
func RegisterAggregateComparator(name string, c core.AggregateComparator) {
	aggregateComparators[name] = c
}

// AggregateComparator resolves a registered AggregateComparator by name.
func AggregateComparator(name string) (core.AggregateComparator, bool) {
	c, ok := aggregateComparators[name]
	return c, ok
}
```

In `internal/engine/build.go`, after line 26 (`registry.RegisterComparator("cel", ...)`) add:

```go
	registry.RegisterAggregateComparator("aggregate-cel", comparator.NewAggregateCEL(pricing))
```

In `internal/engine/engine.go`, after the `Comparator` method (line 34) add:

```go
// AggregateComparator resolves a named aggregate comparator from the registry.
func (e *Engine) AggregateComparator(name string) (core.AggregateComparator, bool) {
	return registry.AggregateComparator(name)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/registry/ ./internal/engine/ -run 'TestAggregateComparatorRegistry|TestAggregateComparatorLookup'`
Expected: PASS.

- [ ] **Step 5: Vet + commit**

```bash
gofmt -w internal/registry/registry.go internal/registry/registry_test.go internal/engine/build.go internal/engine/engine.go internal/engine/engine_test.go
go vet ./internal/registry/... ./internal/engine/...
git add internal/registry/registry.go internal/registry/registry_test.go internal/engine/build.go internal/engine/engine.go internal/engine/engine_test.go
git commit -m "feat(engine): register and resolve the aggregate-cel comparator"
```

---

## Task 8: `Engine.DriveN` — repeat loop, failure-as-sample, serial + parallel

**Files:**
- Modify: `internal/engine/engine.go` (extract `driveOnce`, add `DriveN`, imports `sync`)
- Test: `internal/engine/engine_test.go`

**Interfaces:**
- Produces: `(*engine.Engine).DriveN(ctx context.Context, target string, args []string, n int, parallel bool) ([]core.Evidence, error)`. A harness failure becomes a sample `Evidence{RunID, Failed:true, FailureKind:"driver"|"resolve"}`; a structural error (unknown target/adapter) or pinned+`n>1` returns an error.

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/engine_test.go`:

```go
func TestDriveNCollectsSamples(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()

	var n int
	cor := correlate.New(func() string { n++; return "run-" + itoa(n) }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "echo", nil, 3, false)
	if err != nil {
		t.Fatalf("DriveN: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("got %d samples, want 3", len(evs))
	}
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Failed {
			t.Fatalf("unexpected failed sample: %+v", ev)
		}
		seen[ev.RunID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("run ids not distinct: %v", seen)
	}
}

func TestDriveNResolveFailureBecomesSample(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-x").Times(2)
	// first resolve OK, second fails -> failed sample, not an aborted batch.
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}}
	gomock.InOrder(
		cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), "run-x").Return(tr, nil),
		cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), "run-x").Return(nil, errors.New("store down")),
	)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "echo", nil, 2, false)
	if err != nil {
		t.Fatalf("DriveN must not error on a per-run resolve failure: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d samples, want 2", len(evs))
	}
	if evs[0].Failed {
		t.Fatalf("first run should have succeeded")
	}
	if !evs[1].Failed || evs[1].FailureKind != "resolve" {
		t.Fatalf("second run want failed/resolve, got %+v", evs[1])
	}
}

func TestDriveNPinnedRejectsMulti(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	eng.PinRun("pinned-1")
	if _, err := eng.DriveN(context.Background(), "x", nil, 2, false); err == nil {
		t.Fatalf("pinned + n>1 must error")
	}
}

func itoa(n int) string { return string(rune('0' + n)) }
```

> `itoa` here only handles n<10, which is all these tests use; do not reuse it for larger N.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run TestDriveN`
Expected: FAIL — `eng.DriveN undefined`.

- [ ] **Step 3: Refactor `Drive` into `driveOnce` and add `DriveN`**

In `internal/engine/engine.go`, add `"sync"` to imports. Replace the body of `Drive` (lines 40-89) so the live path delegates to `driveOnce`, and add `driveOnce` + `DriveN`:

```go
// Drive injects the run tag, runs the SUT, then resolves and merges its trace.
// When PinRun was called, it resolves the pinned run id without driving.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	if e.pinned != "" {
		tr, err := e.cor.Resolve(ctx, e.st, e.pinned)
		if err != nil {
			return core.Evidence{}, fmt.Errorf("engine: resolve pinned run %q: %w", e.pinned, err)
		}
		return core.Evidence{RunID: e.pinned, Trace: tr}, nil
	}
	ev, err := e.driveOnce(ctx, target, args)
	if err != nil {
		return core.Evidence{}, err
	}
	return ev, nil
}

// driveOnce performs one live drive. On a harness failure it returns an Evidence
// flagged Failed (with RunID + FailureKind) AND the wrapped error, so single-run
// Drive can surface the error while multi-run DriveN can record the sample.
// Structural errors (unknown target/adapter) carry an empty RunID.
func (e *Engine) driveOnce(ctx context.Context, target string, args []string) (core.Evidence, error) {
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
		HTTP: core.HTTPSpec{
			URL:     t.HTTP.URL,
			Method:  t.HTTP.Method,
			Headers: t.HTTP.Headers,
		},
		Env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": e.cfg.OTLPEndpoint},
	}
	runID := e.cor.Inject(ctx, &spec)

	sem := e.sems[target]
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return core.Evidence{RunID: runID, Failed: true, FailureKind: "driver"}, fmt.Errorf("engine: drive %q: %w", target, ctx.Err())
	}
	defer func() { <-sem }()

	res, err := drv.Run(ctx, spec)
	if err != nil {
		return core.Evidence{RunID: runID, Failed: true, FailureKind: "driver"}, fmt.Errorf("engine: drive %q: %w", target, err)
	}
	tr, err := e.cor.Resolve(ctx, e.st, runID)
	if err != nil {
		return core.Evidence{RunID: runID, Failed: true, FailureKind: "resolve"}, fmt.Errorf("engine: resolve run %q: %w", runID, err)
	}
	return core.Evidence{RunID: runID, Trace: tr, Output: res.Output}, nil
}

// DriveN runs the scenario n times and returns one Evidence per run. A harness
// failure on an iteration becomes a typed failed sample (not an aborted batch);
// a structural error aborts. Serial by default; parallel iterations each acquire
// the existing per-target semaphore, with results collected by index.
func (e *Engine) DriveN(ctx context.Context, target string, args []string, n int, parallel bool) ([]core.Evidence, error) {
	if n < 1 {
		return nil, fmt.Errorf("engine: DriveN needs n>=1, got %d", n)
	}
	if e.pinned != "" && n > 1 {
		return nil, fmt.Errorf("engine: cannot multi-run a pinned scenario (n=%d); replay is deterministic", n)
	}
	evs := make([]core.Evidence, n)
	collect := func(i int) error {
		ev, err := e.driveOnce(ctx, target, args)
		if err != nil && ev.RunID == "" {
			return err // structural error: abort
		}
		evs[i] = ev // success, or a typed failed sample (ev.Failed)
		return nil
	}
	if !parallel {
		for i := 0; i < n; i++ {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
			}
			if err := collect(i); err != nil {
				return nil, err
			}
		}
		return evs, nil
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var structErr error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := collect(i); err != nil {
				mu.Lock()
				if structErr == nil {
					structErr = err
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if structErr != nil {
		return nil, structErr
	}
	return evs, nil
}
```

- [ ] **Step 4: Run the new tests AND the existing engine tests**

Run: `go test ./internal/engine/`
Expected: PASS — the new `TestDriveN*` tests AND every pre-existing `TestDrive*` test (the refactor preserves Drive's contract).

- [ ] **Step 5: Coverage + vet + commit**

```bash
go test ./internal/engine/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
gofmt -w internal/engine/engine.go internal/engine/engine_test.go
go vet ./internal/engine/...
git add internal/engine/engine.go internal/engine/engine_test.go
git commit -m "feat(engine): DriveN repeat loop with failure-as-sample semantics"
```

Expected coverage: ≥80% for `internal/engine`.

---

## Task 9: steps grammar — `@runs(N[,parallel])` tag + `the runs satisfy`

**Files:**
- Modify: `internal/steps/steps.go`
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `engine.DriveN` (Task 8); `engine.AggregateComparator` (Task 7); `comparator.AggregateCELExpectation` (Task 5).
- Produces: a `world` holding `evs []core.Evidence`, `n int`, `parallel bool`; the `the runs satisfy "<expr>"` and `the runs satisfy:` steps; `@runs(N)` / `@runs(N,parallel)` tag parsing in `sc.Before`.

- [ ] **Step 1: Write the failing test**

Append to `internal/steps/steps_test.go`:

```go
// runsEngine builds an engine whose store returns happyTrace for every run, with
// distinct run ids, for hermetic @runs scenarios.
func runsEngine(t *testing.T, tr *trace.Trace) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 4}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
	var n int
	cor := correlate.New(func() string { n++; return "run-" + string(rune('a'+n)) }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

func TestRunsSatisfiesStep(t *testing.T) {
	feature := `Feature: multirun
  @runs(3)
  Scenario: search always present
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, \"search\" in r.tools) >= 0.9"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(runsEngine(t, happyTrace())),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "multirun", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected pass (happyTrace has search), status=%d\n%s", status, out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/steps/ -run TestRunsSatisfiesStep`
Expected: FAIL — the step `the runs satisfy "..."` is undefined, so godog reports the scenario undefined/failed.

- [ ] **Step 3: Implement the grammar**

In `internal/steps/steps.go`:

(a) Add the regexes next to the existing ones (lines 17-20):

```go
	reRunsSatisfiesInline = regexp.MustCompile(`^the runs satisfy "([^"]*)"$`)
	reRunsSatisfiesDoc    = regexp.MustCompile(`^the runs satisfy:$`)
	reRunsTag             = regexp.MustCompile(`^@runs\((\d+)(?:,(parallel))?\)$`)
```

(b) Extend `world` (lines 22-26):

```go
type world struct {
	eng      *engine.Engine
	target   string
	ev       core.Evidence
	evs      []core.Evidence
	n        int
	parallel bool
}
```

(c) Register the new steps inside `Initializer` (after line 51):

```go
		sc.Step(`^the runs satisfy "([^"]*)"$`, w.runsSatisfies)
		sc.Step(`^the runs satisfy:$`, w.runsSatisfiesDoc)
```

(d) In the `sc.Before` hook, parse the tag before precompiling. Replace the hook body (lines 55-60) with:

```go
		sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
			n, parallel, err := parseRunsTag(scenario.Tags)
			if err != nil {
				return ctx, err
			}
			w.n, w.parallel = n, parallel
			if err := w.precompileScenario(scenario.Steps); err != nil {
				return ctx, err
			}
			return ctx, nil
		})
```

(e) Change `drive` (lines 69-79) to use `DriveN` and store the slice (keeping `w.ev` as the first sample so existing single-run steps are unchanged at N=1):

```go
func (w *world) drive(args []string) error {
	if w.target == "" {
		return fmt.Errorf("no target set; use a Given ... target step first")
	}
	n := w.n
	if n < 1 {
		n = 1
	}
	evs, err := w.eng.DriveN(context.Background(), w.target, args, n, w.parallel)
	if err != nil {
		return err
	}
	w.evs = evs
	w.ev = evs[0] // single-run comparators evaluate the first run
	return nil
}
```

(f) Add the aggregate step handlers and the check, near `runSatisfies` (after line 194):

```go
func (w *world) runsSatisfies(expr string) error {
	return w.checkRuns(expr)
}

func (w *world) runsSatisfiesDoc(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("the runs satisfy: expected a docstring expression, got none")
	}
	return w.checkRuns(doc.Content)
}

func (w *world) checkRuns(expr string) error {
	if len(w.evs) == 0 {
		return fmt.Errorf("the runs satisfy: no runs driven; use a When ... step first")
	}
	c, ok := w.eng.AggregateComparator("aggregate-cel")
	if !ok {
		return fmt.Errorf("no aggregate comparator %q", "aggregate-cel")
	}
	v, err := c.Aggregate(context.Background(), w.evs, comparator.AggregateCELExpectation{Expr: expr})
	if err != nil {
		return fmt.Errorf("aggregate-cel: %w", err)
	}
	if !v.Pass {
		return fmt.Errorf("aggregate-cel failed: %s", strings.Join(v.Reasons, "; "))
	}
	return nil
}
```

(g) Extend `precompileScenario` to also compile `the runs satisfy` expressions against the aggregate comparator. Replace the loop body in `precompileScenario` (lines 200-216) with:

```go
	for _, st := range steps {
		if expr, ok := satisfiesExpr(st); ok {
			c, ok := w.eng.Comparator("cel")
			if !ok {
				return fmt.Errorf("scenario-init: 'the run satisfies' requires the cel comparator, which is not registered")
			}
			pc, ok := c.(interface{ Compile(string) error })
			if !ok {
				return fmt.Errorf("scenario-init: cel comparator %T does not support pre-compilation", c)
			}
			if err := pc.Compile(expr); err != nil {
				return fmt.Errorf("scenario-init: %w", err)
			}
			continue
		}
		if expr, ok := runsSatisfiesExpr(st); ok {
			c, ok := w.eng.AggregateComparator("aggregate-cel")
			if !ok {
				return fmt.Errorf("scenario-init: 'the runs satisfy' requires the aggregate-cel comparator, which is not registered")
			}
			pc, ok := c.(interface{ Compile(string) error })
			if !ok {
				return fmt.Errorf("scenario-init: aggregate comparator %T does not support pre-compilation", c)
			}
			if err := pc.Compile(expr); err != nil {
				return fmt.Errorf("scenario-init: %w", err)
			}
		}
	}
	return nil
```

(h) Add the aggregate-expression extractor and the tag parser near `satisfiesExpr` (after line 230):

```go
// runsSatisfiesExpr extracts a CEL expression from a "the runs satisfy" step.
func runsSatisfiesExpr(st *messages.PickleStep) (string, bool) {
	if m := reRunsSatisfiesInline.FindStringSubmatch(st.Text); m != nil {
		return m[1], true
	}
	if reRunsSatisfiesDoc.MatchString(st.Text) && st.Argument != nil && st.Argument.DocString != nil {
		return st.Argument.DocString.Content, true
	}
	return "", false
}

// parseRunsTag reads @runs(N) / @runs(N,parallel). Absent -> (1, false, nil). A tag
// that begins "@runs(" but does not match the strict form is a hard error.
func parseRunsTag(tags []*messages.PickleTag) (int, bool, error) {
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Name, "@runs(") {
			continue
		}
		m := reRunsTag.FindStringSubmatch(tag.Name)
		if m == nil {
			return 0, false, fmt.Errorf("scenario-init: malformed @runs tag %q (want @runs(N) or @runs(N,parallel))", tag.Name)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 {
			return 0, false, fmt.Errorf("scenario-init: @runs requires N>=1, got %q", tag.Name)
		}
		return n, m[2] == "parallel", nil
	}
	return 1, false, nil
}
```

(i) Add `"strconv"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/steps/ -run TestRunsSatisfiesStep`
Expected: PASS.

- [ ] **Step 5: Run the whole steps package (regression) + vet + commit**

Run: `go test ./internal/steps/`
Expected: PASS (existing grammar tests unaffected — `w.ev` still set).

```bash
gofmt -w internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/steps/...
git add internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): @runs tag and the-runs-satisfy aggregate grammar"
```

---

## Task 10: L3 meta-test — goes red on bad statistical behaviour

**Files:**
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `runsEngine` test helper (Task 9), `happyTrace`/`traceWithTools`-style fixtures, the gomock `TraceStore`.

- [ ] **Step 1: Write the meta-test**

Append to `internal/steps/steps_test.go`. This is the mandatory L3 proof: drive a scenario whose SUT exhibits the asserted behaviour only part of the time, and assert Mentat goes **red**; a clean distribution goes **green**.

```go
// badDistEngine returns an engine whose store yields a trace WITH "search" on a
// fraction of runs and WITHOUT it on the rest, deterministically by call count.
func badDistEngine(t *testing.T, withSearch, total int) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	withTrace := &trace.Trace{Roots: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}},
		Spans: []*trace.Span{
			{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}},
			{Name: "tool search", Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"}},
		}}
	withoutTrace := &trace.Trace{Roots: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}},
		Spans: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	var calls int
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string) (*trace.Trace, error) {
		calls++
		if calls <= withSearch {
			return withTrace, nil
		}
		return withoutTrace, nil
	}).Times(total)
	var n int
	cor := correlate.New(func() string { n++; return "run-" + string(rune('a'+n)) }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

func TestMultirunGoesRedOnBadDistribution(t *testing.T) {
	// 5 of 10 runs use search; rate=0.5, the assertion wants >=0.8 -> must fail.
	feature := `Feature: meta-multirun
  @runs(10)
  Scenario: search must be consulted in >= 80% of runs
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, \"search\" in r.tools) >= 0.8"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(badDistEngine(t, 5, 10)),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "meta-multirun", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected RED on a 0.5 search rate vs >=0.8, but suite passed\n%s", out.String())
	}
	if !strings.Contains(out.String(), "aggregate-cel failed") {
		t.Fatalf("expected aggregate-cel failure reason, got:\n%s", out.String())
	}
}

func TestMultirunGoesGreenOnGoodDistribution(t *testing.T) {
	// 9 of 10 runs use search; rate=0.9 >= 0.8 -> must pass.
	feature := `Feature: meta-multirun
  @runs(10)
  Scenario: search consulted in >= 80% of runs
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, \"search\" in r.tools) >= 0.8"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(badDistEngine(t, 9, 10)),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "meta-multirun", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected GREEN on a 0.9 search rate vs >=0.8, status=%d\n%s", status, out.String())
	}
}
```

- [ ] **Step 2: Run the meta-tests**

Run: `go test ./internal/steps/ -run TestMultirun`
Expected: PASS — `TestMultirunGoesRedOnBadDistribution` confirms a non-zero suite status (Mentat goes red), `TestMultirunGoesGreenOnGoodDistribution` confirms the clean distribution passes.

- [ ] **Step 3: Full suite + coverage + vet + commit**

```bash
go test ./...
go test ./internal/steps/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
gofmt -l .
go vet ./...
git add internal/steps/steps_test.go
git commit -m "test(steps): L3 meta-test — multirun goes red on bad statistical behaviour"
```

Expected: all packages PASS; `gofmt -l .` prints nothing; `internal/steps` coverage ≥80%.

---

## Self-Review

**1. Spec coverage** — every spec section maps to a task:
- §3 lifecycle (`DriveN`, `world.evs`) → Tasks 8, 9.
- §4.1 bindings / per-run record (incl. failed-run omission) → Tasks 5, 6.
- §4.2 helper macros (`rate`/`count`/`mean`/`sum`/`min`/`max`/`p50`/`p95`/`p99`/`stddev`) + double coercion → Tasks 3, 4.
- §5 `AggregateComparator` seam + registry + Build wiring + mock → Tasks 1, 5, 7.
- §6 failed-run-as-typed-sample (`Failed`/`FailureKind`, classify by call-site `driver`/`resolve`) → Tasks 1, 8.
- §7 serial default + `@runs(N,parallel)` under the per-target semaphore → Tasks 8, 9.
- §8 `Verdict.Reasons` + per-run table → Task 6.
- §9 edge cases: malformed tag (Task 9 `parseRunsTag`), pinned+`@runs` hard error (Task 8), nearest-rank percentiles (Task 2), `@runs(1)`/no-tag single path (Task 9 `drive`), fail-fast compile (Tasks 5, 9 precompile), metric-over-failed hard error (Task 6).
- §10 L1 + L3 meta-test → Tasks 2-6 (L1) and Task 10 (L3).

**2. Placeholder scan** — no "TBD"/"handle errors"/"similar to Task N". The metric macros (Task 4) are generated by parameterized factories rather than repeated; each registration line is explicit. The `aggMacros` stub in Task 2 is explicitly replaced in Tasks 3-4 (not a placeholder — a deliberate red→green staging).

**3. Type consistency** — `AggregateComparator.Aggregate(ctx, []Evidence, Expectation) (Verdict, error)` is identical in Task 1 (def), Task 5 (impl), Task 9 (call). `AggregateCELExpectation{Expr string}` consistent (Tasks 5, 9). `NewAggregateEngine`/`Compile`/`Eval` consistent (Tasks 2-5). `DriveN(ctx, target, args, n int, parallel bool) ([]Evidence, error)` consistent (Tasks 8, 9). `RegisterAggregateComparator`/`AggregateComparator` consistent (Task 7). Comparator name `"aggregate-cel"` consistent (Tasks 5, 7, 9, 10). Per-run record keys match the Global Constraints list and §4.1.

**cel-go macro API (verified against `v0.28.1`):** register macros with `celgo.GlobalMacro(name, argCount, factory)` (modern) — the deprecated `celgo.NewGlobalMacro` takes a legacy `exprpb` expander and will not match. The factory type is `celgo.MacroFactory` (= `parser.MacroExpander`): `func(eh celgo.MacroExprFactory, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error)`. Construction mirrors `parser.MakeFilter`/`MakeMap`. If anything fails to compile, confirm against `$(go env GOMODCACHE)/github.com/google/cel-go@v0.28.1/cel/macro.go` — do not guess.
