# CEL Aggregate Scalar (computed-vs-expected) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface a `@runs(N)` aggregate assertion's numeric `computed-vs-expected` result and a per-run value column — upgrading the failure message and producing the `core.Verdict.Detail` the reporter seam will consume.

**Architecture:** At `Compile` time, statically analyse the checked aggregate CEL AST; if it is the canonical single-comparison shape (`‹macro›(r, proj) ‹op› ‹const›`), build three cached CEL sub-programs (computed, expected, per-run map). At eval time, run them to produce a `Detail{Macro, Op, Computed, Expected, PerRun}`. The comparator maps that into `core.AggregateDetail` and into an upgraded failure message.

**Tech Stack:** Go, `github.com/google/cel-go` (`cel`, `common/ast`, `common/operators`, `common/types`), godog (BDD/L3), uber gomock (unused here — no new interfaces).

**Spec:** `docs/superpowers/specs/2026-06-20-mentat-cel-aggregate-scalar-design.md`

## Global Constraints

- Module path: `github.com/thetonymaster/mentat`. Import internal packages by full path.
- `gofmt -l .` clean and `go vet ./...` clean before every commit.
- Tests are **table-driven** with `tt := tt` capture and `t.Run(tt.name, …)`.
- **No silent fallbacks** (invariant #4): a function that cannot do its job returns a wrapped `error` (`fmt.Errorf("…: %w", err)`) naming the concrete thing that failed — never a zero-value success.
- **`internal/cel` must not import `internal/core`** (it stays a leaf engine). The cel-local `Detail` struct is mapped to `core.AggregateDetail` in the comparator.
- **Coverage floor 80% per package** (`internal/cel`, `internal/comparator`). Check with `.claude/skills/coverage` or run coverage **per package** and verify each total independently (a combined profile can mask one package dropping below the floor).
- **L3 meta-test is mandatory** — Mentat must go red on bad behaviour, asserted on the `mentat` binary's combined output.
- Git: **Conventional Commits**; `git add .` is forbidden (add files individually); **no AI attribution** in commits.
- Work on a dedicated branch off `main` (see Task 0).

---

### Task 0: Branch setup

**Files:** none (git only).

- [ ] **Step 1: Create the feature branch off `main`**

This feature is its own PR, landing **before** the reporter. The two design specs currently live on `feat/mentat-reporter-seam`; bring just this feature's spec onto a fresh branch off `main`.

```bash
git checkout main
git checkout -b feat/mentat-cel-aggregate-scalar
git checkout feat/mentat-reporter-seam -- docs/superpowers/specs/2026-06-20-mentat-cel-aggregate-scalar-design.md docs/superpowers/plans/2026-06-20-mentat-cel-aggregate-scalar-v1.md
git add docs/superpowers/specs/2026-06-20-mentat-cel-aggregate-scalar-design.md docs/superpowers/plans/2026-06-20-mentat-cel-aggregate-scalar-v1.md
git commit -m "docs: CEL aggregate scalar spec + plan"
```

- [ ] **Step 2: Verify the build is green before changing code**

Run: `go build ./... && go test ./internal/cel/... ./internal/comparator/...`
Expected: PASS (baseline green).

---

### Task 1: SPIKE — sub-program primitive + macro-call recovery

**Goal:** De-risk the two cel-go unknowns before building on them: (a) build a runnable `cel.Program` from a single sub-`ast.Expr`, and (b) recover a custom macro call (`rate`/`p95`/…) and its arguments after expansion. Deliver a committed `subProgram` helper and a passing proof test.

**Files:**
- Create: `internal/cel/aggregate_detail.go`
- Test: `internal/cel/aggregate_detail_spike_test.go`

**Interfaces:**
- Produces: `func subProgram(env *celgo.Env, src *ast.AST, expr ast.Expr) (celgo.Program, error)` — compiles a single sub-expression (in the context of `src`'s SourceInfo) into a runnable program. Used by Tasks 3.

- [ ] **Step 1: Write the spike proof test**

```go
package cel

import (
	"testing"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
)

// Spike: prove we can (a) reach the top-level comparison, (b) build a runnable
// program from the computed operand sub-expr, (c) recover the macro call + its args.
func TestSpike_SubProgramAndMacroRecovery(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	checked, iss := eng.env.Compile(`rate(r, !r.failed) >= 0.8`)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile: %v", iss.Err())
	}
	native := checked.NativeRep()

	// (a) top-level comparison
	root := native.Expr()
	if root.Kind() != ast.CallKind || root.AsCall().FunctionName() != operators.GreaterEquals {
		t.Fatalf("want top-level %s, got kind=%v fn=%q", operators.GreaterEquals, root.Kind(), callFn(root))
	}
	args := root.AsCall().Args()
	if len(args) != 2 {
		t.Fatalf("want 2 args, got %d", len(args))
	}

	// (b) which operand references `runs`
	computed, expected := args[0], args[1]
	if !refsRuns(computed) || refsRuns(expected) {
		t.Fatalf("operand classification wrong")
	}

	// (c) build a program from the computed operand and eval it
	prg, err := subProgram(eng.env, native, computed)
	if err != nil {
		t.Fatalf("subProgram: %v", err)
	}
	records := []any{
		map[string]any{"failed": false},
		map[string]any{"failed": true},
	}
	out, _, err := prg.Eval(map[string]any{VarRuns: records})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got, ok := out.Value().(float64); !ok || got != 0.5 {
		t.Fatalf("want rate 0.5, got %v (%T)", out.Value(), out.Value())
	}

	// (d) recover the macro call + its projection argument
	mc, ok := native.SourceInfo().GetMacroCall(computed.ID())
	if !ok || mc.AsCall().FunctionName() != "rate" {
		t.Fatalf("macro recovery failed: ok=%v fn=%q", ok, callFn(mc))
	}
	if got := len(mc.AsCall().Args()); got != 2 {
		t.Fatalf("want 2 macro args, got %d", got)
	}
}

func callFn(e ast.Expr) string {
	if e != nil && e.Kind() == ast.CallKind {
		return e.AsCall().FunctionName()
	}
	return ""
}
```

- [ ] **Step 2: Run the test to verify it fails (helpers undefined)**

Run: `go test ./internal/cel/ -run TestSpike_SubProgramAndMacroRecovery -v`
Expected: FAIL — `undefined: subProgram`, `undefined: refsRuns`.

- [ ] **Step 3: Implement the spike helpers (primary path first)**

Create `internal/cel/aggregate_detail.go`:

```go
package cel

import (
	"fmt"

	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
)

// refsRuns reports whether expr's subtree references the `runs` identifier.
func refsRuns(expr ast.Expr) bool {
	nav := ast.NavigateAST(ast.NewAST(expr, nil))
	for _, n := range ast.MatchDescendants(nav, func(e ast.NavigableExpr) bool {
		return e.Kind() == ast.IdentKind && e.AsIdent() == VarRuns
	}) {
		_ = n
		return true
	}
	// the root itself may be the ident
	return expr.Kind() == ast.IdentKind && expr.AsIdent() == VarRuns
}

// subProgram compiles a single sub-expression of src into a runnable program,
// re-checking it in env so typed functions resolve. Primary path: native AST ->
// proto -> cel.Ast -> Program. See Task 1 spike for the fallback if this misbehaves.
func subProgram(env *celgo.Env, src *ast.AST, expr ast.Expr) (celgo.Program, error) {
	sub := ast.NewAST(expr, src.SourceInfo())
	pexpr, err := ast.ToProto(sub)
	if err != nil {
		return nil, fmt.Errorf("cel: sub-expr to proto: %w", err)
	}
	celAst := celgo.CheckedExprToAst(pexpr)
	checked, iss := env.Check(celAst)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: re-checking sub-expr: %w", iss.Err())
	}
	prg, err := env.Program(checked)
	if err != nil {
		return nil, fmt.Errorf("cel: building sub-program: %w", err)
	}
	return prg, nil
}
```

- [ ] **Step 4: Run the spike; if the primary path fails, switch to the fallback**

Run: `go test ./internal/cel/ -run TestSpike_SubProgramAndMacroRecovery -v`
Expected: PASS.

If `ast.ToProto`/`CheckedExprToAst`/`env.Check` do not compose on this cel-go version (compile error or check error), replace the body of `subProgram` with the **unparse fallback** and re-run until green:

```go
func subProgram(env *celgo.Env, src *ast.AST, expr ast.Expr) (celgo.Program, error) {
	source, err := celgo.AstToString(celgo.ParsedExprToAst(&exprpb.ParsedExpr{
		Expr:       mustProtoExpr(expr), // ast.ExprToProto(expr)
		SourceInfo: nil,
	}))
	if err != nil {
		return nil, fmt.Errorf("cel: unparse sub-expr: %w", err)
	}
	checked, iss := env.Compile(source)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: recompiling sub-expr %q: %w", source, iss.Err())
	}
	prg, err := env.Program(checked)
	if err != nil {
		return nil, fmt.Errorf("cel: building sub-program for %q: %w", source, err)
	}
	return prg, nil
}
```

Record in the commit message which path was used.

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/cel/aggregate_detail.go internal/cel/aggregate_detail_spike_test.go
go vet ./internal/cel/
git add internal/cel/aggregate_detail.go internal/cel/aggregate_detail_spike_test.go
git commit -m "feat(cel): sub-program primitive + macro-call recovery (spike, primary path)"
```

---

### Task 2: `analyze` — canonical-shape classifier

**Goal:** Pure static analysis: given a checked aggregate AST, decide canonical-or-not and extract the pieces. No program building yet.

**Files:**
- Modify: `internal/cel/aggregate_detail.go`
- Test: `internal/cel/aggregate_detail_test.go`

**Interfaces:**
- Consumes: `refsRuns` (Task 1).
- Produces:
  ```go
  type shape struct {
      op       string   // normalized to read "computed OP expected"
      macro    string   // "rate","p95",...
      computed ast.Expr // the runs-referencing operand
      expected ast.Expr // the runs-free operand
      iterVar  string   // macro arg[0] identifier, e.g. "r"
      proj     ast.Expr // macro arg[1], the projection/predicate
  }
  func analyze(src *ast.AST) (*shape, bool)
  ```

- [ ] **Step 1: Write the failing table test**

```go
package cel

import "testing"

func mustCompile(t *testing.T, expr string) *ast.AST {
	t.Helper()
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	checked, iss := eng.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		t.Fatalf("compile %q: %v", expr, iss.Err())
	}
	return checked.NativeRep()
}

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		wantOK    bool
		wantMacro string
		wantOp    string
	}{
		{"rate ge literal", `rate(r, !r.failed) >= 0.8`, true, "rate", ">="},
		{"p95 le literal", `p95(r, r.latencyMs) <= 1500`, true, "p95", "<="},
		{"count eq zero", `count(r, r.failed) == 0`, true, "count", "=="},
		{"reversed operands", `0.8 <= rate(r, !r.failed)`, true, "rate", ">="},
		{"mean lt", `mean(r, r.cost) < 0.01`, true, "mean", "<"},
		{"compound and", `rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`, false, "", ""},
		{"no comparison", `rate(r, !r.failed) > 0.5 ? true : false`, false, "", ""},
		{"composite computed", `mean(r, r.cost) + mean(r, r.tokens) <= 1.0`, false, "", ""},
		{"both sides runs", `mean(r, r.cost) <= mean(r, r.tokens)`, false, "", ""},
		{"neither side runs", `1 <= 2`, false, "", ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			sh, ok := analyze(mustCompile(t, tt.expr))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if sh.macro != tt.wantMacro {
				t.Errorf("macro = %q, want %q", sh.macro, tt.wantMacro)
			}
			if sh.op != tt.wantOp {
				t.Errorf("op = %q, want %q", sh.op, tt.wantOp)
			}
			if !refsRuns(sh.computed) || refsRuns(sh.expected) {
				t.Errorf("operand classification wrong")
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cel/ -run TestAnalyze -v`
Expected: FAIL — `undefined: analyze`, `undefined: shape`.

- [ ] **Step 3: Implement `analyze`**

Add to `internal/cel/aggregate_detail.go`:

```go
import (
	// add to the existing import block:
	"github.com/google/cel-go/common/operators"
)

type shape struct {
	op       string
	macro    string
	computed ast.Expr
	expected ast.Expr
	iterVar  string
	proj     ast.Expr
}

// knownMacros is the aggregate macro set whose projection we can recover.
var knownMacros = map[string]bool{
	"rate": true, "count": true, "sum": true, "mean": true, "min": true,
	"max": true, "stddev": true, "p50": true, "p95": true, "p99": true,
}

// flipOp maps a comparison operator to its mirror, so an "expected OP computed"
// expression normalizes to "computed flip(OP) expected".
var flipOp = map[string]string{
	operators.Greater: "<", operators.GreaterEquals: "<=",
	operators.Less: ">", operators.LessEquals: ">=",
	operators.Equals: "==", operators.NotEquals: "!=",
}

// readOp maps the internal operator name to its display form (computed on left).
var readOp = map[string]string{
	operators.Greater: ">", operators.GreaterEquals: ">=",
	operators.Less: "<", operators.LessEquals: "<=",
	operators.Equals: "==", operators.NotEquals: "!=",
}

// analyze recognizes the canonical "‹macro›(r, proj) ‹op› ‹runs-free const›" shape
// (either operand order) and returns its pieces. (nil, false) for anything else.
func analyze(src *ast.AST) (*shape, bool) {
	root := src.Expr()
	if root.Kind() != ast.CallKind {
		return nil, false
	}
	fn := root.AsCall().FunctionName()
	if _, isCmp := readOp[fn]; !isCmp {
		return nil, false
	}
	args := root.AsCall().Args()
	if len(args) != 2 {
		return nil, false
	}
	left, right := args[0], args[1]
	lRuns, rRuns := refsRuns(left), refsRuns(right)
	var computed, expected ast.Expr
	var op string
	switch {
	case lRuns && !rRuns:
		computed, expected, op = left, right, readOp[fn]
	case rRuns && !lRuns:
		computed, expected, op = right, left, flipOp[fn]
	default:
		return nil, false // both or neither reference runs
	}
	mc, ok := src.SourceInfo().GetMacroCall(computed.ID())
	if !ok || mc.Kind() != ast.CallKind || !knownMacros[mc.AsCall().FunctionName()] {
		return nil, false // computed side is not a single recognized macro call
	}
	margs := mc.AsCall().Args()
	if len(margs) != 2 || margs[0].Kind() != ast.IdentKind {
		return nil, false
	}
	return &shape{
		op:       op,
		macro:    mc.AsCall().FunctionName(),
		computed: computed,
		expected: expected,
		iterVar:  margs[0].AsIdent(),
		proj:     margs[1],
	}, true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cel/ -run TestAnalyze -v`
Expected: PASS (all 10 cases).

- [ ] **Step 5: gofmt, vet, commit**

```bash
gofmt -w internal/cel/aggregate_detail.go internal/cel/aggregate_detail_test.go
go vet ./internal/cel/
git add internal/cel/aggregate_detail.go internal/cel/aggregate_detail_test.go
git commit -m "feat(cel): canonical aggregate-comparison classifier (analyze)"
```

---

### Task 3: Build & cache the detail plan at `Compile`

**Goal:** Enable macro-call tracking, and when `Compile` sees a canonical expression, build the three sub-programs and a constructed per-run map, caching them on `AggregateProgram`.

**Files:**
- Modify: `internal/cel/aggregate.go` (env option; `AggregateProgram`; `Compile`)
- Modify: `internal/cel/aggregate_detail.go` (the `detailPlan` builder + `mapDoubles`-based projection)
- Test: `internal/cel/aggregate_detail_test.go`

**Interfaces:**
- Consumes: `analyze` (Task 2), `subProgram` (Task 1), `mapDoubles` (`aggregate.go:232`).
- Produces:
  ```go
  type detailPlan struct {
      macro        string
      op           string
      computedPrg  celgo.Program
      expectedPrg  celgo.Program
      perRunPrg    celgo.Program // runs.map(iterVar, double(proj)) -> list<double>
  }
  func buildDetailPlan(env *celgo.Env, src *ast.AST) (*detailPlan, error) // (nil,nil) when non-canonical
  ```
  and `AggregateProgram` gains an unexported `plan *detailPlan` field.

- [ ] **Step 1: Write the failing test**

```go
func TestBuildDetailPlan(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	tests := []struct {
		name     string
		expr     string
		wantPlan bool
	}{
		{"canonical", `p95(r, r.latencyMs) <= 1500`, true},
		{"non-canonical", `rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p, err := eng.Compile(tt.expr)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if (p.plan != nil) != tt.wantPlan {
				t.Fatalf("plan present = %v, want %v", p.plan != nil, tt.wantPlan)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cel/ -run TestBuildDetailPlan -v`
Expected: FAIL — `p.plan undefined`.

- [ ] **Step 3: Enable macro tracking + add the plan field + builder**

In `internal/cel/aggregate.go`, add the env option in `NewAggregateEngine` (after `aggMacros()`):

```go
	opts = append(opts, aggMacros()...)
	opts = append(opts, celgo.EnableMacroCallTracking())
```

Add the field to `AggregateProgram`:

```go
type AggregateProgram struct {
	expr   string
	prg    celgo.Program
	fields map[string]bool
	plan   *detailPlan // non-nil only for a canonical aggregate comparison
}
```

In `Compile`, after building `prg`, build the plan from the checked native AST:

```go
	plan, err := buildDetailPlan(e.env, celAst.NativeRep())
	if err != nil {
		return nil, fmt.Errorf("cel: aggregate %q: %w", expr, err)
	}
	return &AggregateProgram{expr: expr, prg: prg, fields: referencedFields(celAst), plan: plan}, nil
```

In `internal/cel/aggregate_detail.go`, add the builder. The per-run program is constructed with the existing `mapDoubles` helper (`aggregate.go:232`) so int projections coerce to double:

```go
type detailPlan struct {
	macro       string
	op          string
	computedPrg celgo.Program
	expectedPrg celgo.Program
	perRunPrg   celgo.Program
}

// buildDetailPlan returns a plan for a canonical aggregate comparison, or (nil, nil)
// when the expression is not canonical. A genuine sub-program build failure on a
// canonical expression is a hard error (invariant 4).
func buildDetailPlan(env *celgo.Env, src *ast.AST) (*detailPlan, error) {
	sh, ok := analyze(src)
	if !ok {
		return nil, nil
	}
	computedPrg, err := subProgram(env, src, sh.computed)
	if err != nil {
		return nil, fmt.Errorf("building computed sub-program: %w", err)
	}
	expectedPrg, err := subProgram(env, src, sh.expected)
	if err != nil {
		return nil, fmt.Errorf("building expected sub-program: %w", err)
	}
	perRunExpr := buildPerRunMap(sh.iterVar, sh.proj)
	perRunPrg, err := subProgram(env, src, perRunExpr)
	if err != nil {
		return nil, fmt.Errorf("building per-run sub-program: %w", err)
	}
	return &detailPlan{macro: sh.macro, op: sh.op, computedPrg: computedPrg, expectedPrg: expectedPrg, perRunPrg: perRunPrg}, nil
}
```

Add `buildPerRunMap`, reusing the same comprehension machinery as the macros. Because `mapDoubles` requires a `MacroExprFactory`, build the `runs.map(iterVar, double(proj))` comprehension directly with an `ast.ExprFactory`:

```go
import (
	// add:
	"github.com/google/cel-go/common/types"
)

// buildPerRunMap constructs `runs.map(iterVar, double(proj))` as a comprehension
// yielding list<double> — one value per run (predicates coerce bool->double to 1/0).
func buildPerRunMap(iterVar string, proj ast.Expr) ast.Expr {
	fac := ast.NewExprFactory()
	accu := "__result__"
	init := fac.NewList(0)
	cond := fac.NewLiteral(0, types.True)
	elem := fac.NewCall(0, "double", proj)
	step := fac.NewCall(0, operators.Add, fac.NewAccuIdent(0), fac.NewList(0, elem))
	return fac.NewComprehension(0,
		fac.NewIdent(0, VarRuns),
		iterVar, accu,
		init, cond, step,
		fac.NewAccuIdent(0),
	)
}
```

> Note for the implementer: the exact `ast.ExprFactory` constructor signatures (the leading `id int64` arg, `NewList`'s arity) vary by cel-go version. The spike (Task 1) already linked `common/ast`; if a signature differs, adjust the literal IDs/args to match the version in `go.mod` — the shape (`runs.map(iterVar, double(proj))`) is fixed. Verify by evaluating `perRunPrg` in Task 4.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cel/ -run TestBuildDetailPlan -v`
Expected: PASS.

- [ ] **Step 5: Confirm no regression in existing aggregate tests**

Run: `go test ./internal/cel/...`
Expected: PASS (env change + new field do not alter existing behaviour).

- [ ] **Step 6: gofmt, vet, commit**

```bash
gofmt -w internal/cel/aggregate.go internal/cel/aggregate_detail.go internal/cel/aggregate_detail_test.go
go vet ./internal/cel/
git add internal/cel/aggregate.go internal/cel/aggregate_detail.go internal/cel/aggregate_detail_test.go
git commit -m "feat(cel): build & cache computed/expected/per-run plan at Compile"
```

---

### Task 4: `AggregateProgram.Detail` — eval the plan

**Goal:** Run the cached sub-programs against the `runs` records to produce the cel-local `Detail`.

**Files:**
- Modify: `internal/cel/aggregate_detail.go`
- Test: `internal/cel/aggregate_detail_test.go`

**Interfaces:**
- Produces:
  ```go
  type Detail struct {
      Macro    string
      Op       string
      Computed float64
      Expected float64
      PerRun   []float64
  }
  func (p *AggregateProgram) Detail(vars map[string]any) (Detail, bool, error)
  ```

- [ ] **Step 1: Write the failing test**

```go
func TestAggregateProgram_Detail(t *testing.T) {
	eng, err := NewAggregateEngine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	records := []any{
		map[string]any{"failed": false, "latencyMs": int64(100)},
		map[string]any{"failed": true, "latencyMs": int64(300)},
		map[string]any{"failed": false, "latencyMs": int64(200)},
	}
	vars := map[string]any{VarRuns: records}

	t.Run("rate", func(t *testing.T) {
		p, err := eng.Compile(`rate(r, !r.failed) >= 0.8`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		d, ok, err := p.Detail(vars)
		if err != nil || !ok {
			t.Fatalf("Detail ok=%v err=%v", ok, err)
		}
		if d.Macro != "rate" || d.Op != ">=" {
			t.Errorf("macro/op = %q/%q", d.Macro, d.Op)
		}
		if d.Computed != 2.0/3.0 || d.Expected != 0.8 {
			t.Errorf("computed/expected = %v/%v", d.Computed, d.Expected)
		}
		if want := []float64{1, 0, 1}; !equalFloats(d.PerRun, want) {
			t.Errorf("perRun = %v, want %v", d.PerRun, want)
		}
	})

	t.Run("p95 reversed", func(t *testing.T) {
		p, err := eng.Compile(`1500 >= p95(r, r.latencyMs)`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		d, ok, err := p.Detail(vars)
		if err != nil || !ok {
			t.Fatalf("Detail ok=%v err=%v", ok, err)
		}
		if d.Op != "<=" || d.Expected != 1500 {
			t.Errorf("op/expected = %q/%v", d.Op, d.Expected)
		}
		if want := []float64{100, 300, 200}; !equalFloats(d.PerRun, want) {
			t.Errorf("perRun = %v, want %v", d.PerRun, want)
		}
	})

	t.Run("non-canonical -> ok=false", func(t *testing.T) {
		p, err := eng.Compile(`rate(r, !r.failed) >= 0.8 && p95(r, r.latencyMs) <= 1500`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		_, ok, err := p.Detail(vars)
		if err != nil || ok {
			t.Fatalf("want ok=false err=nil, got ok=%v err=%v", ok, err)
		}
	})
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cel/ -run TestAggregateProgram_Detail -v`
Expected: FAIL — `p.Detail undefined`, `Detail` type undefined.

- [ ] **Step 3: Implement `Detail`**

Add to `internal/cel/aggregate_detail.go`:

```go
import (
	// add:
	"reflect"

	"github.com/google/cel-go/common/types/ref"
)

// Detail is the cel-local computed-vs-expected result for a canonical aggregate.
type Detail struct {
	Macro    string
	Op       string
	Computed float64
	Expected float64
	PerRun   []float64
}

// Detail evaluates the cached plan. (zero, false, nil) when the expression is not
// canonical; a sub-eval error is propagated (invariant 4).
func (p *AggregateProgram) Detail(vars map[string]any) (Detail, bool, error) {
	if p.plan == nil {
		return Detail{}, false, nil
	}
	computed, err := evalDouble(p.plan.computedPrg, vars)
	if err != nil {
		return Detail{}, false, fmt.Errorf("cel: aggregate %q computed: %w", p.expr, err)
	}
	expected, err := evalDouble(p.plan.expectedPrg, vars)
	if err != nil {
		return Detail{}, false, fmt.Errorf("cel: aggregate %q expected: %w", p.expr, err)
	}
	perRun, err := evalDoubleList(p.plan.perRunPrg, vars)
	if err != nil {
		return Detail{}, false, fmt.Errorf("cel: aggregate %q per-run: %w", p.expr, err)
	}
	return Detail{
		Macro: p.plan.macro, Op: p.plan.op,
		Computed: computed, Expected: expected, PerRun: perRun,
	}, true, nil
}

func evalDouble(prg celgo.Program, vars map[string]any) (float64, error) {
	out, _, err := prg.Eval(vars)
	if err != nil {
		return 0, err
	}
	f, ok := out.Value().(float64)
	if !ok {
		return 0, fmt.Errorf("sub-expr did not yield double, got %T", out.Value())
	}
	return f, nil
}

func evalDoubleList(prg celgo.Program, vars map[string]any) ([]float64, error) {
	out, _, err := prg.Eval(vars)
	if err != nil {
		return nil, err
	}
	native, err := out.ConvertToNative(reflect.TypeOf([]float64{}))
	if err != nil {
		return nil, fmt.Errorf("per-run list is not numeric: %w", err)
	}
	fs, ok := native.([]float64)
	if !ok {
		return nil, fmt.Errorf("per-run list: unexpected native type %T", native)
	}
	return fs, nil
}

var _ ref.Val = nil // ensure ref import is used if referenced indirectly; remove if unused
```

> Implementer note: drop the `ref` import + the `var _` line if `ref` ends up unused after gofmt — it is listed only because `ConvertToNative` lives on `ref.Val`. The compiler/`go vet` will flag an unused import; remove it.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cel/ -run TestAggregateProgram_Detail -v`
Expected: PASS (rate, p95-reversed, non-canonical).

- [ ] **Step 5: Coverage check on the cel package**

Run: `go test ./internal/cel/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -1`
Expected: total ≥ 80%. If below, add table rows for `count`, `mean`, `sum` to `TestAggregateProgram_Detail`.

- [ ] **Step 6: gofmt, vet, commit**

```bash
gofmt -w internal/cel/aggregate_detail.go internal/cel/aggregate_detail_test.go
go vet ./internal/cel/
git add internal/cel/aggregate_detail.go internal/cel/aggregate_detail_test.go
git commit -m "feat(cel): AggregateProgram.Detail evaluates computed/expected/per-run"
```

---

### Task 5: `core.AggregateDetail` + `Verdict.Detail`

**Goal:** Add the public report-facing type and the optional `Verdict` field. Trivial, additive; gives later tasks a compile target.

**Files:**
- Modify: `internal/core/core.go:41-44`
- Test: covered transitively by Task 6 (a bare data struct needs no isolated test).

- [ ] **Step 1: Add the type and field**

Replace the `Verdict` definition (`core.go:41-44`):

```go
type Verdict struct {
	Pass    bool
	Reasons []string
	// Detail is the structured computed-vs-expected result of a canonical aggregate
	// (@runs) comparison. Non-nil only for that case; every other comparator leaves it nil.
	Detail *AggregateDetail
}

// AggregateDetail is the structured result of a canonical aggregate comparison
// ("‹macro›(r, proj) ‹op› ‹const›"). PerRun is positionally aligned with the runs order;
// predicate macros (rate/count) contribute 1.0/0.0.
type AggregateDetail struct {
	Expr     string
	Macro    string
	Op       string
	Computed float64
	Expected float64
	PerRun   []float64
}
```

- [ ] **Step 2: Verify it compiles and mocks are unaffected**

Run: `go build ./... && go test ./internal/core/...`
Expected: PASS. (`Verdict` is a struct, not an interface — `go generate`/mocks need no change.)

- [ ] **Step 3: Commit**

```bash
gofmt -w internal/core/core.go
go vet ./internal/core/
git add internal/core/core.go
git commit -m "feat(core): AggregateDetail + optional Verdict.Detail"
```

---

### Task 6: Comparator populates `Verdict.Detail` (pass and fail)

**Goal:** `aggregate_cel.go` calls `prg.Detail`, maps the cel-local `Detail` to `core.AggregateDetail` (adding `Expr`), and sets it on the verdict for both pass and fail.

**Files:**
- Modify: `internal/comparator/aggregate_cel.go:72-98` (`Aggregate`)
- Test: `internal/comparator/aggregate_cel_test.go`

**Interfaces:**
- Consumes: `AggregateProgram.Detail` (Task 4), `core.AggregateDetail` (Task 5).

- [ ] **Step 1: Write the failing test**

```go
func TestAggregateCEL_Detail(t *testing.T) {
	c := NewAggregateCEL(nil)
	evs := []core.Evidence{
		{RunID: "a", Output: core.Output{Status: 200}},
		{RunID: "b", Failed: true, FailureKind: core.FailureKindResolve},
		{RunID: "c", Output: core.Output{Status: 200}},
	}
	// rate(r, !r.failed) = 2/3 ≈ 0.667; assertion >= 0.8 -> FAIL, but Detail still set.
	v, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, !r.failed) >= 0.8`})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if v.Pass {
		t.Fatalf("want fail")
	}
	if v.Detail == nil {
		t.Fatalf("want Detail populated on fail")
	}
	if v.Detail.Macro != "rate" || v.Detail.Op != ">=" || v.Detail.Expected != 0.8 {
		t.Errorf("detail = %+v", v.Detail)
	}
	if v.Detail.Expr != `rate(r, !r.failed) >= 0.8` {
		t.Errorf("expr = %q", v.Detail.Expr)
	}

	// Passing canonical assertion also carries Detail.
	v2, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `rate(r, !r.failed) >= 0.5`})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if !v2.Pass || v2.Detail == nil {
		t.Fatalf("want pass with Detail, got pass=%v detail=%v", v2.Pass, v2.Detail)
	}

	// Non-canonical -> Detail nil.
	v3, err := c.Aggregate(context.Background(), evs, AggregateCELExpectation{Expr: `count(r, r.failed) == 0 && count(r, !r.failed) == 3`})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if v3.Detail != nil {
		t.Errorf("want nil Detail for compound, got %+v", v3.Detail)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/comparator/ -run TestAggregateCEL_Detail -v`
Expected: FAIL — `v.Detail` always nil (not yet populated).

- [ ] **Step 3: Populate `Verdict.Detail` in `Aggregate`**

In `internal/comparator/aggregate_cel.go`, replace the tail of `Aggregate` (`aggregate_cel.go:90-98`):

```go
	pass, err := prg.Eval(map[string]any{celengine.VarRuns: records})
	if err != nil {
		return core.Verdict{}, err
	}
	detail, err := buildCoreDetail(prg, exp.Expr, records)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("aggregate-cel: %w", err)
	}
	if pass {
		return core.Verdict{Pass: true, Detail: detail}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{aggregateReason(exp.Expr, evs, detail)}, Detail: detail}, nil
```

Add the mapping helper (same file):

```go
// buildCoreDetail runs the program's canonical-aggregate analysis and maps the
// cel-local Detail to core.AggregateDetail. Returns (nil, nil) for non-canonical.
func buildCoreDetail(prg *celengine.AggregateProgram, expr string, records []any) (*core.AggregateDetail, error) {
	d, ok, err := prg.Detail(map[string]any{celengine.VarRuns: records})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return &core.AggregateDetail{
		Expr: expr, Macro: d.Macro, Op: d.Op,
		Computed: d.Computed, Expected: d.Expected, PerRun: d.PerRun,
	}, nil
}
```

Note: `aggregateReason` gains a third parameter here; Task 7 implements the new signature. To keep this task compiling on its own, temporarily update the `aggregateReason` call only — its body is rewritten in Task 7. Add the parameter to the existing signature now with the body unchanged except ignoring the new arg:

```go
func aggregateReason(expr string, evs []core.Evidence, _ *core.AggregateDetail) string {
	// (existing body unchanged in this task)
	...
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/comparator/ -run TestAggregateCEL_Detail -v`
Expected: PASS.

- [ ] **Step 5: Full comparator suite still green**

Run: `go test ./internal/comparator/...`
Expected: PASS (the `aggregateReason` signature change compiles; body unchanged).

- [ ] **Step 6: gofmt, vet, commit**

```bash
gofmt -w internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
go vet ./internal/comparator/
git add internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
git commit -m "feat(comparator): populate Verdict.Detail for canonical aggregates"
```

---

### Task 7: Upgrade the failure message (computed-vs-expected + value column)

**Goal:** When `Detail` is present, render `aggregate false: rate = 0.60, want >= 0.80  (N runs)` with a per-run `value` column; otherwise keep today's message.

**Files:**
- Modify: `internal/comparator/aggregate_cel.go:163-171` (`aggregateReason`)
- Test: `internal/comparator/aggregate_cel_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestAggregateReason(t *testing.T) {
	evs := []core.Evidence{
		{RunID: "aaa", Output: core.Output{Status: 200}},
		{RunID: "bbb", Failed: true, FailureKind: core.FailureKindResolve},
	}
	t.Run("canonical -> computed-vs-expected + value column", func(t *testing.T) {
		d := &core.AggregateDetail{
			Expr: `rate(r, !r.failed) >= 0.8`, Macro: "rate", Op: ">=",
			Computed: 0.5, Expected: 0.8, PerRun: []float64{1, 0},
		}
		got := aggregateReason(d.Expr, evs, d)
		if !strings.Contains(got, "rate = 0.50, want >= 0.80") {
			t.Errorf("missing computed-vs-expected line:\n%s", got)
		}
		if !strings.Contains(got, "value") {
			t.Errorf("missing value column header:\n%s", got)
		}
	})
	t.Run("nil detail -> legacy message", func(t *testing.T) {
		got := aggregateReason(`count(r, r.failed) == 0 && true`, evs, nil)
		if !strings.Contains(got, "aggregate false:") || strings.Contains(got, "want") {
			t.Errorf("legacy message wrong:\n%s", got)
		}
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/comparator/ -run TestAggregateReason -v`
Expected: FAIL — the canonical branch and `value` column do not exist yet.

- [ ] **Step 3: Rewrite `aggregateReason`**

```go
// aggregateReason renders the failing assertion. With a canonical Detail it shows
// computed-vs-expected and a per-run value column; otherwise it shows the raw
// expression. Both include a per-run table so a test.run.id can be pasted into /traces.
func aggregateReason(expr string, evs []core.Evidence, d *core.AggregateDetail) string {
	var b strings.Builder
	if d != nil {
		fmt.Fprintf(&b, "aggregate false: %s = %.2f, want %s %.2f  (%d runs)\n", d.Macro, d.Computed, d.Op, d.Expected, len(evs))
		fmt.Fprintf(&b, "  run  test.run.id            failed  kind     value\n")
		for i, ev := range evs {
			val := ""
			if i < len(d.PerRun) {
				val = fmt.Sprintf("%g", d.PerRun[i])
			}
			fmt.Fprintf(&b, "  %-3d  %-22s %-7t %-8s %s\n", i, ev.RunID, ev.Failed, ev.FailureKind, val)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "aggregate false: %s  (%d runs)\n", expr, len(evs))
	fmt.Fprintf(&b, "  run  test.run.id            failed  kind\n")
	for i, ev := range evs {
		fmt.Fprintf(&b, "  %-3d  %-22s %-7t %s\n", i, ev.RunID, ev.Failed, ev.FailureKind)
	}
	return strings.TrimRight(b.String(), "\n")
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/comparator/ -run TestAggregateReason -v`
Expected: PASS.

- [ ] **Step 5: Update any existing reason-string assertions and re-run the package**

Run: `go test ./internal/comparator/...`
Expected: PASS. If a pre-existing test asserted the old `aggregate false: "<expr>"` quoted form for a **canonical** expression, update its expected substring to the new `‹macro› = …, want …` form (the message changed by design — spec §4.3 churn note).

- [ ] **Step 6: gofmt, vet, commit**

```bash
gofmt -w internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
go vet ./internal/comparator/
git add internal/comparator/aggregate_cel.go internal/comparator/aggregate_cel_test.go
git commit -m "feat(comparator): computed-vs-expected aggregate failure message + value column"
```

---

### Task 8: L3 meta-test — prove the detail reaches the user

**Goal:** Drive a known-bad canonical aggregate through the real `mentat` binary and assert the computed-vs-expected line appears; confirm a non-canonical bad aggregate still goes red via the fallback. Keep existing multirun L3 substrings green.

**Files:**
- Modify: `e2e/meta_test.go` (add cases; `//go:build e2e`)
- Possibly create: `e2e/testdata/features/aggregate_scalar_bad.feature` (or reuse an existing multirun bad-feature fixture)

**Interfaces:**
- Consumes: the end-to-end `mentat` run harness already used by the existing L3 meta-test.

- [ ] **Step 1: Inspect the existing L3 harness to mirror its shape**

Run: `sed -n '1,60p' e2e/meta_test.go`
Expected: see how it invokes `go run ./cmd/mentat` and greps combined stdout/stderr. Reuse that helper verbatim for the new cases.

- [ ] **Step 2: Add a known-bad canonical feature fixture**

Create `e2e/testdata/features/aggregate_scalar_bad.feature` (adjust the target/steps to match the existing bad-behaviour fixtures in `e2e/testdata`):

```gherkin
@runs(3)
Feature: aggregate scalar goes red with computed-vs-expected
  Scenario: success rate below threshold
    Given the agent target "flaky"
    When I run the agent with prompt "go"
    Then the runs satisfy "rate(r, !r.failed) >= 0.99"
```

- [ ] **Step 3: Write the failing L3 assertion**

Add to `e2e/meta_test.go` (mirroring the existing meta-test function style):

```go
func TestL3_AggregateScalarMessage(t *testing.T) {
	out := runMentat(t, "e2e/testdata/features/aggregate_scalar_bad.feature") // reuse existing helper
	if !strings.Contains(out, "rate =") || !strings.Contains(out, "want >= 0.99") {
		t.Fatalf("expected computed-vs-expected message, got:\n%s", out)
	}
}
```

- [ ] **Step 4: Run the e2e suite (needs the harness)**

Run: `make harness-up && go test -tags e2e ./e2e/ -run TestL3_AggregateScalarMessage -v`
Expected: PASS — Mentat exits non-zero and the output contains `rate = …, want >= 0.99`.

- [ ] **Step 5: Run the full e2e suite to confirm no regression**

Run: `go test -tags e2e ./e2e/...`
Expected: PASS — existing multirun L3 cases still red-on-bad with their (possibly updated) substrings.

- [ ] **Step 6: gofmt, vet, commit**

```bash
gofmt -w e2e/meta_test.go
go vet -tags e2e ./e2e/
git add e2e/meta_test.go e2e/testdata/features/aggregate_scalar_bad.feature
git commit -m "test(e2e): L3 proves computed-vs-expected aggregate message reaches output"
```

---

### Task 9: Final gate

**Files:** none (verification only).

- [ ] **Step 1: Whole-repo format/vet/test**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt -l .` prints nothing; vet clean; all hermetic tests PASS.

- [ ] **Step 2: Coverage floor**

Run each package separately so one package cannot mask another:
`go test ./internal/cel/ -coverprofile=cover-cel.out && go tool cover -func=cover-cel.out | tail -1`
`go test ./internal/comparator/ -coverprofile=cover-comparator.out && go tool cover -func=cover-comparator.out | tail -1`
Expected: both package totals ≥ 80%.

- [ ] **Step 3: Open the PR**

```bash
git push -u origin feat/mentat-cel-aggregate-scalar
gh pr create --title "feat(cel): aggregate computed-vs-expected detail + per-run value column" --body "Implements docs/superpowers/specs/2026-06-20-mentat-cel-aggregate-scalar-design.md. Unblocks the reporter seam's Verdict.Detail."
```

---

## Self-Review

- **Spec coverage:** §3 canonical rule → Task 2 (`analyze`); §4 deliverables → scalar (Task 4/6), per-run column (Task 3/4), message (Task 7), `Verdict.Detail` pass+fail (Task 6); §5 mechanism + macro tracking → Tasks 1, 3; §5.1 sub-program + spike → Task 1; §6 module split → cel (1–4), core (5), comparator (6–7); §7 error handling → Task 3 (compile-time hard error), Task 4 (eval propagation), non-canonical nil (2,4,6); §8 testing → Tasks 2,4,6,7 (L1) + Task 8 (L3) + Task 9 (coverage). No gaps.
- **Type consistency:** `shape`, `detailPlan`, cel-local `Detail{Macro,Op,Computed,Expected,PerRun}`, `core.AggregateDetail{Expr,Macro,Op,Computed,Expected,PerRun}`, `Verdict.Detail`, `subProgram(env,src,expr)`, `analyze(src)`, `buildDetailPlan(env,src)`, `Detail(vars)`, `buildCoreDetail`, `aggregateReason(expr,evs,d)` — consistent across tasks.
- **Known version-sensitivity (flagged inline, not placeholders):** the exact cel-go `ast.ExprFactory` / proto-round-trip API surface is version-dependent; Task 1 is a deliberate spike with a stated fallback, and Task 3 notes the factory signature caveat. These are de-risking steps, not deferred work.
