# Mentat CEL Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reusable, sandboxed CEL expression engine (`internal/cel`) and a standalone trace-aware `cel` comparator so spec authors can assert compound/conditional boolean predicates over a run's output and curated trace aggregates.

**Architecture:** A core-free `internal/cel.Engine` owns only the variable *schema* (names + CEL types) and compiles expressions fail-fast (rejecting non-bool results). A new `internal/comparator/cel.go` consumes full `Evidence`, binds **only the variables an expression references** (so unrelated facts are never computed or parsed), evaluates the compiled program, and formats a value-snapshot reason on failure. Trace facts (tokens/cost/errors/tools/services) are read through the **same helpers** the `budgets`/`sequence` comparators use, so the two tiers can never disagree. A godog `the run satisfies …` step compiles its expressions at scenario-init via a `Before` hook, failing a malformed expectation before any SUT runs.

**Tech Stack:** Go 1.25, `github.com/google/cel-go` (new direct dep), `github.com/cucumber/godog` (existing), uber `go.uber.org/mock` (existing, not needed here — comparator tests build `Evidence` from goldens directly).

## Global Constraints

- Module path: `github.com/thetonymaster/mentat`; Go `1.25.0`.
- `gofmt -l .` clean and `go vet ./...` clean before any commit. Run `golangci-lint run` only if a `.golangci.yml` exists.
- Coverage floor **≥80% per package** (`internal/cel`, and the `cel` contribution to `internal/comparator`). Verify with the `/coverage` skill.
- **No silent fallbacks** (invariant 4): a function that cannot do its job returns a wrapped `%w` error naming the concrete value; never a zero-value success or a guessed result.
- **Comparators consume `Evidence` only**; `Trace` is a forest (`tr.Spans` is the flat list, `tr.Roots` the roots) — never assume a single root.
- **Single source of truth for facts** (spec §5): the `cel` comparator MUST call the same extraction helpers as `budgets`/`sequence`, not re-derive tokens/cost/latency/tools/services.
- Errors wrap with `fmt.Errorf("doing X: %w", err)` and name the concrete value.
- Conventional Commits (`feat:`/`refactor:`/`test:`); add files individually (`git add .` forbidden); **no AI attribution** in commits or PRs.
- Tests are **hermetic** (in-memory; no Tempo, no network); table-driven by default; both **pass-path and red-on-bad** paths covered (spec §10).

---

## Design decisions & interpretations (read before implementing)

These resolve ambiguities the spec left open or that the existing code forces. They are baked into the tasks below; flag to Q if any is wrong.

1. **Cost-absent → hard error (Q-approved 2026-06-18).** When `cost` is referenced but no span carries `gen_ai.usage.cost_usd`, the comparator returns the *same* hard error `budgets` already returns (`cost not available …`). This honors spec §5 ("can never disagree") + invariant 4. Implemented by reusing the lifted `costSum` helper verbatim.
2. **Bind only referenced variables (generalizes spec §6).** Spec §6 mandates lazy `body` JSON parse keyed on `Program.References()`. We apply the *same* principle to **all** variables: a variable an expression does not mention is never computed. This prevents a cost-less trace, a non-JSON body, a nameless tool span, or a missing `service.name` from causing a spurious failure in a test that does not reference that fact.
3. **§7 enforced via a godog `Before` hook.** The current step architecture evaluates after `Drive`. To make a malformed expression fail "before any SUT is driven," `Initializer` registers a scenario `Before` hook that extracts every `the run satisfies` expression from the scenario's steps and pre-compiles them via the comparator's `Compile(string) error` method (cached for reuse at evaluation).
4. **`latencyMs` is 0 against every golden.** Fixtures carry no timestamps (`store.LoadFixture` builds spans with zero `Start`/`End`), so `Trace.Envelope()` returns 0. Latency is therefore unit-tested with a hand-built trace that has real timestamps, not against a golden.
5. **cel-go API surface.** References come from `cel.AstToCheckedExpr(ast).GetReferenceMap()` filtered to declared schema names. Bool-result is checked with `ast.OutputType().IsExactType(cel.BoolType)`. *If the pinned cel-go version lacks `IsExactType`, substitute `ast.OutputType().String() != "bool"`* — Task 1, Step 3 verifies which compiles.
6. **JSON numbers bind as CEL `double`.** `encoding/json` decodes numbers to Go `float64`, which cel-go treats as `double`. Authors compare numeric body fields with a double literal (`body.count == 3.0`) or `int(body.count) == 3`. Documented in the step text.
7. **Lifted helper messages keep the `budgets:` prefix verbatim** (behavior-preserving — `budgets_test.go` asserts on `"invalid"`, the attr key, and `"cost not available"`). When `cel` wraps them the message reads `cel: binding cost: budgets: cost not available …`; the double prefix is accepted as honest provenance.

---

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `go.mod`, `go.sum` | Add `github.com/google/cel-go` direct dep | 1 |
| `internal/cel/cel.go` (create) | Core-free engine: schema, `NewEngine`, `Compile`, `Program.References`, `Program.Eval` | 1 |
| `internal/cel/cel_test.go` (create) | Engine unit tests: compile errors, references, eval | 1 |
| `internal/comparator/budgets.go` (modify) | Lift inline token/cost/error aggregation into shared helpers `tokenSum`/`costSum`/`errorCount` (behavior-preserving) | 2 |
| `internal/comparator/cel.go` (create) | `CELExpectation`, `NewCEL`, `Compile` (cache), `Compare`, `bindVars`, `parseBody`, `reason` | 3,4,5 |
| `internal/comparator/cel_test.go` (create) | Comparator unit tests: output vars, trace vars, body handling, red-on-bad, cost-absent, nil-trace | 3,4,5 |
| `internal/engine/build.go` (modify) | Register `cel` comparator at the composition root | 6 |
| `internal/steps/steps.go` (modify) | `the run satisfies` inline + docstring steps; `Before`-hook scenario-init precompile (§7) | 6 |
| `internal/steps/steps_test.go` (modify) | godog pass + red-on-bad cel scenarios; `precompileScenario` unit; §7 fail-before-drive | 6 |

---

### Task 1: CEL engine (`internal/cel`)

Build the reusable, core-free engine and prove the cel-go API end-to-end. This front-loads the riskiest unknown (the cel-go surface) into the first task.

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/cel/cel.go`
- Test: `internal/cel/cel_test.go`

**Interfaces:**
- Consumes: nothing from this repo (engine imports neither `core` nor `trace`).
- Produces:
  - Exported var-name constants: `cel.VarStatus`, `VarExitCode`, `VarBody`, `VarBodyText`, `VarAnswer`, `VarTokens`, `VarCost`, `VarLatencyMs`, `VarErrors`, `VarTools`, `VarServices` (all `string`).
  - `func NewEngine() (*Engine, error)`
  - `func (e *Engine) Compile(expr string) (*Program, error)`
  - `func (p *Program) References() []string` (sorted, deduped, declared-vars only)
  - `func (p *Program) Eval(vars map[string]any) (bool, error)`

- [ ] **Step 1: Add the cel-go dependency**

Run:
```bash
go get github.com/google/cel-go@latest
```
Expected: `go.mod` gains a `require github.com/google/cel-go vX.Y.Z` line; `go.sum` updated. (Pulls the antlr runtime + a few transitive packages; `protobuf`/`genproto` are already present transitively — spec §2.)

- [ ] **Step 2: Write the failing compile test**

Create `internal/cel/cel_test.go`:
```go
package cel

import (
	"reflect"
	"strings"
	"testing"
)

func TestEngineCompile(t *testing.T) {
	eng, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tests := []struct {
		name    string
		expr    string
		wantErr bool
		errSub  string
	}{
		{name: "valid bool eq", expr: `status == 201`},
		{name: "valid list macro", expr: `"search" in tools`},
		{name: "valid conditional", expr: `status == 201 ? tokens < 5000 : tokens < 2000`},
		{name: "syntax error", expr: `status ==`, wantErr: true},
		{name: "unknown variable", expr: `nope == 1`, wantErr: true, errSub: "nope"},
		{name: "type error int vs string", expr: `status == "x"`, wantErr: true},
		{name: "non-bool result", expr: `tokens + 1`, wantErr: true, errSub: "bool"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := eng.Compile(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Compile(%q) err=%v wantErr=%v", tt.expr, err, tt.wantErr)
			}
			if tt.errSub != "" && (err == nil || !strings.Contains(err.Error(), tt.errSub)) {
				t.Fatalf("Compile(%q): want error containing %q, got %v", tt.expr, tt.errSub, err)
			}
		})
	}
}
```

- [ ] **Step 3: Run the compile test — verify it fails**

Run: `go test ./internal/cel/ -run TestEngineCompile`
Expected: FAIL — `undefined: NewEngine` (package has no non-test file yet).

- [ ] **Step 4: Implement the engine (compile side, no Eval yet)**

Create `internal/cel/cel.go`:
```go
package cel

import (
	"fmt"
	"sort"

	celgo "github.com/google/cel-go/cel"
)

// Variable names declared in the CEL environment. They are the contract between
// the engine (which declares their CEL types) and the comparator (which binds
// their values from Evidence). Keep the two in sync via these constants.
const (
	VarStatus    = "status"
	VarExitCode  = "exitCode"
	VarBody      = "body"
	VarBodyText  = "bodyText"
	VarAnswer    = "answer"
	VarTokens    = "tokens"
	VarCost      = "cost"
	VarLatencyMs = "latencyMs"
	VarErrors    = "errors"
	VarTools     = "tools"
	VarServices  = "services"
)

// Engine is a compiled CEL environment carrying the Mentat variable schema (§5).
// It imports neither core nor trace: it owns only variable names + CEL types,
// which keeps it independently testable and reusable.
type Engine struct {
	env      *celgo.Env
	declared map[string]bool
}

// NewEngine builds the CEL environment with the declared variable schema.
func NewEngine() (*Engine, error) {
	env, err := celgo.NewEnv(
		celgo.Variable(VarStatus, celgo.IntType),
		celgo.Variable(VarExitCode, celgo.IntType),
		celgo.Variable(VarBody, celgo.DynType),
		celgo.Variable(VarBodyText, celgo.StringType),
		celgo.Variable(VarAnswer, celgo.StringType),
		celgo.Variable(VarTokens, celgo.IntType),
		celgo.Variable(VarCost, celgo.DoubleType),
		celgo.Variable(VarLatencyMs, celgo.IntType),
		celgo.Variable(VarErrors, celgo.IntType),
		celgo.Variable(VarTools, celgo.ListType(celgo.StringType)),
		celgo.Variable(VarServices, celgo.ListType(celgo.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("cel: building environment: %w", err)
	}
	declared := map[string]bool{
		VarStatus: true, VarExitCode: true, VarBody: true, VarBodyText: true,
		VarAnswer: true, VarTokens: true, VarCost: true, VarLatencyMs: true,
		VarErrors: true, VarTools: true, VarServices: true,
	}
	return &Engine{env: env, declared: declared}, nil
}

// Program is a type-checked, compiled CEL expression whose result type is bool.
type Program struct {
	expr string
	prg  celgo.Program
	refs []string
}

// Compile type-checks expr against the schema. It returns a descriptive error on
// a syntax error, an unknown variable, a type error, or a non-bool result type.
func (e *Engine) Compile(expr string) (*Program, error) {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: compiling %q: %w", expr, iss.Err())
	}
	// Result type must be bool (no silent fallback — invariant 4). If the pinned
	// cel-go lacks IsExactType, use: ast.OutputType().String() != "bool".
	if !ast.OutputType().IsExactType(celgo.BoolType) {
		return nil, fmt.Errorf("cel: expression %q must evaluate to bool, got %s", expr, ast.OutputType())
	}
	refs, err := references(ast, e.declared)
	if err != nil {
		return nil, fmt.Errorf("cel: extracting references from %q: %w", expr, err)
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel: building program for %q: %w", expr, err)
	}
	return &Program{expr: expr, prg: prg, refs: refs}, nil
}

// references returns the declared schema variables the expression uses, sorted.
// It filters the checked AST's reference map to names present in the schema, so
// operators, macros, and function names are excluded.
func references(ast *celgo.Ast, declared map[string]bool) ([]string, error) {
	checked, err := celgo.AstToCheckedExpr(ast)
	if err != nil {
		return nil, fmt.Errorf("converting to checked expr: %w", err)
	}
	seen := map[string]bool{}
	for _, ref := range checked.GetReferenceMap() {
		if name := ref.GetName(); declared[name] {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// References returns the schema variables the expression actually uses (sorted),
// so the caller binds and parses only what is needed (§6).
func (p *Program) References() []string {
	out := make([]string, len(p.refs))
	copy(out, p.refs)
	return out
}
```

- [ ] **Step 5: Run the compile test — verify it passes**

Run: `go test ./internal/cel/ -run TestEngineCompile -v`
Expected: PASS (all 7 sub-cases).
*If it fails on `IsExactType`*: replace that condition with `ast.OutputType().String() != "bool"` and re-run. *If it fails on `AstToCheckedExpr`/`GetReferenceMap`*: STOP and report — the pinned cel-go reference API differs; report the exact compile error to Q before adapting.

- [ ] **Step 6: Write the failing references + eval tests**

Append to `internal/cel/cel_test.go`:
```go
func TestEngineReferences(t *testing.T) {
	eng, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	prg, err := eng.Compile(`tokens < 5000 && status == 201`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got := prg.References()
	want := []string{"status", "tokens"} // sorted, deduped
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("References() = %v, want %v", got, want)
	}
}

func TestEngineEval(t *testing.T) {
	eng, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tests := []struct {
		name string
		expr string
		vars map[string]any
		want bool
	}{
		{"int eq true", `status == 201`, map[string]any{"status": int64(201)}, true},
		{"int eq false", `status == 201`, map[string]any{"status": int64(200)}, false},
		{"list in true", `"search" in tools`, map[string]any{"tools": []string{"search", "summarize"}}, true},
		{"list in false", `"x" in tools`, map[string]any{"tools": []string{"search"}}, false},
		{"body dyn field", `body.ok == true`, map[string]any{"body": map[string]any{"ok": true}}, true},
		{"body null", `body == null`, map[string]any{"body": nil}, true},
		{"conditional true branch", `status == 201 ? tokens < 5000 : tokens < 2000`, map[string]any{"status": int64(201), "tokens": int64(3000)}, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			prg, err := eng.Compile(tt.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.expr, err)
			}
			got, err := prg.Eval(tt.vars)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 7: Run — verify the eval test fails to compile**

Run: `go test ./internal/cel/ -run TestEngineEval`
Expected: FAIL — `prg.Eval undefined (type *Program has no field or method Eval)`.

- [ ] **Step 8: Implement `Program.Eval`**

Append to `internal/cel/cel.go`:
```go
// Eval runs the program against a bound variable map and returns the bool result.
func (p *Program) Eval(vars map[string]any) (bool, error) {
	out, _, err := p.prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("cel: evaluating %q: %w", p.expr, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("cel: expression %q did not return bool, got %T", p.expr, out.Value())
	}
	return b, nil
}
```

- [ ] **Step 9: Run all engine tests — verify they pass**

Run: `go test ./internal/cel/ -v`
Expected: PASS (`TestEngineCompile`, `TestEngineReferences`, `TestEngineEval`).

- [ ] **Step 10: Format, vet, commit**

Run:
```bash
gofmt -w internal/cel/cel.go internal/cel/cel_test.go
go vet ./internal/cel/
git add go.mod go.sum internal/cel/cel.go internal/cel/cel_test.go
git commit -m "feat(cel): core-free CEL engine with schema, fail-fast compile, references, eval"
```
Expected: clean `gofmt`/`vet`; commit created.

---

### Task 2: Lift fact-extraction helpers in `budgets.go`

Behavior-preserving refactor: extract the inline token/cost/error aggregation from `budgets.Compare` into package-private helpers so the `cel` comparator (same package) can reuse the same logic (spec §5, single source of truth). `toolSequence`/`serviceSequence` already exist as functions in `sequence.go` and need no change.

**Files:**
- Modify: `internal/comparator/budgets.go`
- Test (regression guard, unchanged): `internal/comparator/budgets_test.go`

**Interfaces:**
- Consumes: `trace.Trace`, `genai.{InTokens,OutTokens,CostUSD}` (already imported in the package).
- Produces (package-private, for Tasks 4–5):
  - `func tokenSum(t *trace.Trace) (int, error)`
  - `func costSum(t *trace.Trace) (float64, error)`
  - `func errorCount(t *trace.Trace) int`

- [ ] **Step 1: Establish the green baseline**

Run: `go test ./internal/comparator/ -run TestBudgets`
Expected: PASS (this is the regression guard — the refactor must keep it green, byte-identical error messages included).

- [ ] **Step 2: Add the `trace` import to budgets.go**

In `internal/comparator/budgets.go`, add the import (the helpers take `*trace.Trace`):
```go
	"github.com/thetonymaster/mentat/internal/trace"
```
(Place it in the existing import block alongside `core` and `genai`.)

- [ ] **Step 3: Append the three helpers (verbatim logic from the current inline blocks)**

Append to the end of `internal/comparator/budgets.go`:
```go
// tokenSum returns the total gen_ai input+output tokens across all spans. A
// non-integer or negative token attribute is a hard error (no silent fallback).
// Shared by the budgets and cel comparators so they never disagree (§5).
func tokenSum(t *trace.Trace) (int, error) {
	total := 0
	for i, s := range t.Spans {
		for _, key := range []string{genai.InTokens, genai.OutTokens} {
			raw := s.Attr(key)
			if raw == "" {
				continue
			}
			n, err := strconv.Atoi(raw)
			if err != nil {
				return 0, fmt.Errorf("budgets: span[%d] (%q) invalid %s=%q: %w", i, s.Name, key, raw, err)
			}
			if n < 0 {
				return 0, fmt.Errorf("budgets: span[%d] (%q) %s=%q out of range: must be a value >= 0", i, s.Name, key, raw)
			}
			total += n
		}
	}
	return total, nil
}

// costSum returns the total gen_ai cost in USD across all spans. Absent cost (no
// span carries the attribute) is a hard error — the behavior the cel comparator
// inherits (§5, cost-absent decision). A malformed or out-of-range value is also
// a hard error.
func costSum(t *trace.Trace) (float64, error) {
	cost := 0.0
	seen := false
	for i, s := range t.Spans {
		raw := s.Attr(genai.CostUSD)
		if raw == "" {
			continue
		}
		c, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("budgets: span[%d] (%q) invalid %s=%q: %w", i, s.Name, genai.CostUSD, raw, err)
		}
		if c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
			return 0, fmt.Errorf("budgets: span[%d] (%q) %s=%q out of range: must be a finite value >= 0", i, s.Name, genai.CostUSD, raw)
		}
		cost += c
		seen = true
	}
	if !seen {
		return 0, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
	}
	return cost, nil
}

// errorCount returns the number of spans whose Status is "Error".
func errorCount(t *trace.Trace) int {
	errs := 0
	for _, s := range t.Spans {
		if s.Status == "Error" {
			errs++
		}
	}
	return errs
}
```

- [ ] **Step 4: Replace the inline blocks in `budgets.Compare` with calls**

In `internal/comparator/budgets.go`, replace the `MaxTokens` block (currently the loop summing tokens) with:
```go
	if exp.MaxTokens != nil {
		total, err := tokenSum(ev.Trace)
		if err != nil {
			return core.Verdict{}, err
		}
		if total > *exp.MaxTokens {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total tokens %d exceed budget %d", total, *exp.MaxTokens))
		}
	}
```
Replace the `MaxCostUSD` block with:
```go
	if exp.MaxCostUSD != nil {
		cost, err := costSum(ev.Trace)
		if err != nil {
			return core.Verdict{}, err
		}
		if cost > *exp.MaxCostUSD {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total cost $%.4f exceeds budget $%.4f", cost, *exp.MaxCostUSD))
		}
	}
```
Replace the `MaxErrors` block with:
```go
	if exp.MaxErrors != nil {
		if errs := errorCount(ev.Trace); errs > *exp.MaxErrors {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%d error spans exceed budget %d", errs, *exp.MaxErrors))
		}
	}
```
Leave the `MaxLatency` block (it already calls `ev.Trace.Envelope()` directly) and the nil-`Trace` guard unchanged. Keep the `math`, `strconv`, `time` imports — they are now used by the helpers / the latency block.

- [ ] **Step 5: Run the full comparator suite — verify still green**

Run: `go test ./internal/comparator/ -v`
Expected: PASS — every existing `TestBudgets*`, `TestFixture*`, `TestSequence*`, `TestResult*` case unchanged. The `cost not available` / `invalid` substring assertions in `budgets_test.go` still hold because the helper messages are byte-identical.

- [ ] **Step 6: Format, vet, commit**

Run:
```bash
gofmt -w internal/comparator/budgets.go
go vet ./internal/comparator/
git add internal/comparator/budgets.go
git commit -m "refactor(comparator): lift token/cost/error aggregation into shared helpers"
```
Expected: clean; commit created.

---

### Task 3: `cel` comparator skeleton + output-side scalar variables

Create the comparator with its compile cache and the output-derived scalar bindings (`status`, `exitCode`, `bodyText`, `answer`), plus the failure-reason formatting (§9). Trace aggregates (Task 4) and `body` JSON (Task 5) come next.

**Files:**
- Create: `internal/comparator/cel.go`
- Test: `internal/comparator/cel_test.go`

**Interfaces:**
- Consumes: `cel.{Engine,Program}`, `cel.Var*` constants (Task 1); `core.{Comparator,Evidence,Output,Verdict,Expectation}`.
- Produces:
  - `type CELExpectation struct { Expr string }`
  - `func NewCEL() core.Comparator` (`Name() == "cel"`)
  - `func (c *celComparator) Compile(expr string) error` (concrete method beyond the interface; used by §7 precompile in Task 6)
  - package-private `bindVars(refs []string, ev core.Evidence) (map[string]any, error)` and `reason(expr string, refs []string, vars map[string]any) string` (extended in Tasks 4–5)

- [ ] **Step 1: Write the failing output-vars test**

Create `internal/comparator/cel_test.go`:
```go
package comparator

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestCELName(t *testing.T) {
	if got := NewCEL().Name(); got != "cel" {
		t.Fatalf("Name() = %q, want %q", got, "cel")
	}
}

func TestCELWrongExpectationType(t *testing.T) {
	_, err := NewCEL().Compare(context.Background(), core.Evidence{}, "not a CELExpectation")
	if err == nil {
		t.Fatal("want error for wrong expectation type, got nil")
	}
}

func TestCELOutputVars(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		out       core.Output
		wantPass  bool
		reasonSub string // substring required in the failure reason (when !wantPass)
	}{
		{name: "status pass", expr: `status == 201`, out: core.Output{Status: 201}, wantPass: true},
		{name: "status fail shows value", expr: `status == 200`, out: core.Output{Status: 201}, wantPass: false, reasonSub: "status=201"},
		{name: "exitCode pass", expr: `exitCode == 0`, out: core.Output{ExitCode: 0}, wantPass: true},
		{name: "answer contains macro", expr: `answer.contains("revenue")`, out: core.Output{Answer: "Q3 revenue up"}, wantPass: true},
		{name: "bodyText startsWith", expr: `bodyText.startsWith("{")`, out: core.Output{Body: []byte(`{"a":1}`)}, wantPass: true},
		{name: "compound fail shows offending value", expr: `status == 201 && exitCode == 0`, out: core.Output{Status: 500, ExitCode: 0}, wantPass: false, reasonSub: "status=500"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: tt.out}
			v, err := NewCEL().Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Compare(%q): %v", tt.expr, err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if tt.reasonSub != "" {
				if len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], tt.reasonSub) {
					t.Fatalf("want reason containing %q, got %v", tt.reasonSub, v.Reasons)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run — verify it fails**

Run: `go test ./internal/comparator/ -run TestCEL`
Expected: FAIL — `undefined: NewCEL` / `undefined: CELExpectation`.

- [ ] **Step 3: Implement the comparator (output-side bindings only)**

Create `internal/comparator/cel.go`:
```go
package comparator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	celengine "github.com/thetonymaster/mentat/internal/cel"
	"github.com/thetonymaster/mentat/internal/core"
)

// CELExpectation carries a single boolean CEL expression over a run's Evidence.
type CELExpectation struct {
	Expr string
}

type celComparator struct {
	engine   *celengine.Engine
	mu       sync.RWMutex
	programs map[string]*celengine.Program
}

// NewCEL returns the standalone, trace-aware CEL comparator (Name() == "cel").
// It consumes full Evidence — unlike result, which is contractually output-only.
func NewCEL() core.Comparator {
	engine, err := celengine.NewEngine()
	if err != nil {
		// The schema is a compile-time constant; a build failure is a true,
		// caller-unreachable invariant violation, not a runtime condition.
		panic(fmt.Sprintf("cel: static schema failed to build: %v", err))
	}
	return &celComparator{engine: engine, programs: map[string]*celengine.Program{}}
}

func (c *celComparator) Name() string { return "cel" }

// Compile type-checks and caches expr's program. It is called at scenario-init
// (§7) so a malformed expression fails before any SUT is driven. Safe for
// concurrent scenarios.
func (c *celComparator) Compile(expr string) error {
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

func (c *celComparator) program(expr string) (*celengine.Program, error) {
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

// Compare binds only the schema variables the expression references (§6),
// evaluates the program, and on a false result reports the expression plus a
// snapshot of the referenced bound values (§9).
func (c *celComparator) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(CELExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("cel: expectation must be CELExpectation, got %T", e)
	}
	prg, err := c.program(exp.Expr)
	if err != nil {
		return core.Verdict{}, err
	}
	refs := prg.References()
	vars, err := bindVars(refs, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	pass, err := prg.Eval(vars)
	if err != nil {
		return core.Verdict{}, err
	}
	if pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{reason(exp.Expr, refs, vars)}}, nil
}

// bindVars binds ONLY the referenced variables, so a variable an expression does
// not mention is never computed. Trace aggregates and body JSON are added in
// later tasks.
func bindVars(refs []string, ev core.Evidence) (map[string]any, error) {
	vars := make(map[string]any, len(refs))
	for _, name := range refs {
		switch name {
		case celengine.VarStatus:
			vars[name] = int64(ev.Output.Status)
		case celengine.VarExitCode:
			vars[name] = int64(ev.Output.ExitCode)
		case celengine.VarBodyText:
			vars[name] = string(ev.Output.Body)
		case celengine.VarAnswer:
			vars[name] = ev.Output.Answer
		default:
			return nil, fmt.Errorf("cel: unknown variable %q in references", name)
		}
	}
	return vars, nil
}

// reason renders the §9 failure message: the expression plus a snapshot of the
// referenced bound values, in the deterministic (sorted) reference order.
func reason(expr string, refs []string, vars map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cel false: %q", expr)
	if len(refs) > 0 {
		b.WriteString("  [")
		for i, name := range refs {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s=%v", name, vars[name])
		}
		b.WriteString("]")
	}
	return b.String()
}
```

- [ ] **Step 4: Run — verify it passes**

Run: `go test ./internal/comparator/ -run TestCEL -v`
Expected: PASS (`TestCELName`, `TestCELWrongExpectationType`, `TestCELOutputVars`).

- [ ] **Step 5: Format, vet, commit**

Run:
```bash
gofmt -w internal/comparator/cel.go internal/comparator/cel_test.go
go vet ./internal/comparator/
git add internal/comparator/cel.go internal/comparator/cel_test.go
git commit -m "feat(comparator): cel comparator skeleton with output-side variable bindings"
```
Expected: clean; commit created.

---

### Task 4: `cel` comparator — trace aggregate variables

Add the trace-derived bindings (`tokens`, `cost`, `errors`, `latencyMs`, `tools`, `services`) by calling the shared helpers (Task 2 + existing `toolSequence`/`serviceSequence`), with a nil-`Trace` guard for referenced trace vars. This is where the cost-absent hard error (decision 1) and bind-only-referenced (decision 2) become observable.

**Files:**
- Modify: `internal/comparator/cel.go`
- Test: `internal/comparator/cel_test.go`

**Interfaces:**
- Consumes: `tokenSum`/`costSum`/`errorCount` (Task 2), `toolSequence`/`serviceSequence` (existing in `sequence.go`), `trace.Trace.Envelope()`.
- Produces: extended `bindVars` (six new cases + a `traceVars` guard map). No new exported symbols.

- [ ] **Step 1: Write the failing trace-vars tests**

Append to `internal/comparator/cel_test.go` (add imports `os`, `time` to the existing block, plus `"github.com/thetonymaster/mentat/internal/genai"`, `"github.com/thetonymaster/mentat/internal/store"`, `"github.com/thetonymaster/mentat/internal/trace"`):
```go
func TestCELTraceVars(t *testing.T) {
	tests := []struct {
		name      string
		dir       string
		fixture   string
		expr      string
		wantPass  bool
		reasonSub string
	}{
		{name: "tokens under cap (researchbot happy 1800)", dir: "researchbot", fixture: "happy.json", expr: `tokens < 5000`, wantPass: true},
		{name: "tokens over cap (over_budget) shows value", dir: "researchbot", fixture: "over_budget.json", expr: `tokens < 5000`, wantPass: false, reasonSub: "tokens="},
		{name: "cost under cap (researchbot happy 0.018)", dir: "researchbot", fixture: "happy.json", expr: `cost < 1.0`, wantPass: true},
		{name: "tools present via macro", dir: "researchbot", fixture: "happy.json", expr: `"search" in tools && "summarize" in tools`, wantPass: true},
		{name: "errors zero (researchbot happy)", dir: "researchbot", fixture: "happy.json", expr: `errors == 0`, wantPass: true},
		{name: "services exclude legacy (orderflow happy)", dir: "orderflow", fixture: "happy.json", expr: `!("legacy-pricing" in services)`, wantPass: true},
		{name: "services include legacy (orderflow legacy_path) red", dir: "orderflow", fixture: "legacy_path.json", expr: `!("legacy-pricing" in services)`, wantPass: false, reasonSub: "services="},
		{name: "errors present (orderflow payment_decline)", dir: "orderflow", fixture: "payment_decline.json", expr: `errors > 0`, wantPass: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile("../../testdata/traces/" + tt.dir + "/" + tt.fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			tr, err := store.LoadFixture(data)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			ev := core.Evidence{Trace: tr}
			v, err := NewCEL().Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Compare(%q): %v", tt.expr, err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if tt.reasonSub != "" && (len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], tt.reasonSub)) {
				t.Fatalf("want reason containing %q, got %v", tt.reasonSub, v.Reasons)
			}
		})
	}
}

// TestCELCostAbsentHardError pins decision 1: referencing cost on a trace with
// no cost_usd is a hard error (reuses budgets' costSum), never a 0.0 fallback.
func TestCELCostAbsentHardError(t *testing.T) {
	tr := &trace.Trace{Spans: []*trace.Span{{
		Name:  "invoke_agent",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "100"},
	}}}
	_, err := NewCEL().Compare(context.Background(), core.Evidence{Trace: tr}, CELExpectation{Expr: `cost < 1.0`})
	if err == nil {
		t.Fatal("want hard error for cost-absent trace, got nil")
	}
	if !strings.Contains(err.Error(), "cost not available") {
		t.Fatalf("want 'cost not available' error, got %v", err)
	}
}

// TestCELLatencyCraftedTrace: goldens carry no timestamps (latencyMs==0), so
// latency is exercised with a hand-built trace that has real Start/End.
func TestCELLatencyCraftedTrace(t *testing.T) {
	now := time.Now()
	tr := &trace.Trace{Spans: []*trace.Span{{
		Name:  "invoke_agent",
		Start: now,
		End:   now.Add(50 * time.Millisecond),
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent},
	}}}
	v, err := NewCEL().Compare(context.Background(), core.Evidence{Trace: tr}, CELExpectation{Expr: `latencyMs >= 50`})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !v.Pass {
		t.Fatalf("want pass for latencyMs>=50; reasons=%v", v.Reasons)
	}
}

// TestCELNilTraceReferencedError: a referenced trace var with a nil Trace is a
// descriptive error, not a panic (no silent fallback).
func TestCELNilTraceReferencedError(t *testing.T) {
	_, err := NewCEL().Compare(context.Background(), core.Evidence{}, CELExpectation{Expr: `tokens < 5000`})
	if err == nil {
		t.Fatal("want error when a trace var is referenced but Trace is nil, got nil")
	}
}
```

- [ ] **Step 2: Run — verify it fails**

Run: `go test ./internal/comparator/ -run TestCELTraceVars`
Expected: FAIL — `bindVars` returns `unknown variable "tokens" in references` (trace cases not implemented yet).

- [ ] **Step 3: Extend `bindVars` with the trace cases + nil guard**

In `internal/comparator/cel.go`, add this package-level var above `bindVars`:
```go
// traceVars are the schema variables that require ev.Trace.
var traceVars = map[string]bool{
	celengine.VarTokens:    true,
	celengine.VarCost:      true,
	celengine.VarErrors:    true,
	celengine.VarLatencyMs: true,
	celengine.VarTools:     true,
	celengine.VarServices:  true,
}
```
Insert the nil-`Trace` guard at the top of `bindVars` (before the binding loop):
```go
	for _, name := range refs {
		if traceVars[name] && ev.Trace == nil {
			return nil, fmt.Errorf("cel: binding %q: evidence has no trace", name)
		}
	}
```
Add these six cases to the `switch` in `bindVars`, before the `default`:
```go
		case celengine.VarTokens:
			n, err := tokenSum(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding tokens: %w", err)
			}
			vars[name] = int64(n)
		case celengine.VarCost:
			v, err := costSum(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding cost: %w", err)
			}
			vars[name] = v
		case celengine.VarErrors:
			vars[name] = int64(errorCount(ev.Trace))
		case celengine.VarLatencyMs:
			vars[name] = ev.Trace.Envelope().Milliseconds() // already int64
		case celengine.VarTools:
			seq, err := toolSequence(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding tools: %w", err)
			}
			vars[name] = seq
		case celengine.VarServices:
			seq, err := serviceSequence(ev.Trace)
			if err != nil {
				return nil, fmt.Errorf("cel: binding services: %w", err)
			}
			vars[name] = seq
```

- [ ] **Step 4: Run — verify all cel trace tests pass**

Run: `go test ./internal/comparator/ -run TestCEL -v`
Expected: PASS (`TestCELTraceVars` all 8 cases, `TestCELCostAbsentHardError`, `TestCELLatencyCraftedTrace`, `TestCELNilTraceReferencedError`, plus Task 3's tests).

- [ ] **Step 5: Format, vet, commit**

Run:
```bash
gofmt -w internal/comparator/cel.go internal/comparator/cel_test.go
go vet ./internal/comparator/
git add internal/comparator/cel.go internal/comparator/cel_test.go
git commit -m "feat(comparator): cel trace aggregate bindings via shared helpers (tokens/cost/errors/latency/tools/services)"
```
Expected: clean; commit created.

---

### Task 5: `cel` comparator — `body` dyn lazy JSON parse

Implement the §6 body rules: parse JSON **only when `body` is referenced**; empty → `null`; valid → parsed `dyn`; invalid-when-referenced → hard error; not referenced → never parsed (so a non-JSON body in an unrelated test never fails spuriously).

**Files:**
- Modify: `internal/comparator/cel.go`
- Test: `internal/comparator/cel_test.go`

**Interfaces:**
- Consumes: `encoding/json`, `strings` (add to cel.go imports).
- Produces: `parseBody(body []byte) (any, error)` + a `case celengine.VarBody` in `bindVars`.

- [ ] **Step 1: Write the failing body-handling test**

Append to `internal/comparator/cel_test.go`:
```go
func TestCELBodyHandling(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		body     []byte
		wantPass bool
		wantErr  bool
		errSub   string
	}{
		{name: "valid json field match", expr: `body.status == "confirmed"`, body: []byte(`{"status":"confirmed"}`), wantPass: true},
		{name: "valid json field mismatch", expr: `body.status == "confirmed"`, body: []byte(`{"status":"declined"}`), wantPass: false},
		{name: "empty body binds null", expr: `body == null`, body: []byte(``), wantPass: true},
		{name: "invalid json referenced is hard error", expr: `body.x == 1`, body: []byte(`not json`), wantErr: true, errSub: "not valid JSON"},
		{name: "invalid json unreferenced is no error", expr: `status == 201`, body: []byte(`not json`), wantPass: true},
		{name: "numeric body field as double", expr: `body.count == 3.0`, body: []byte(`{"count":3}`), wantPass: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Status: 201, Body: tt.body}}
			v, err := NewCEL().Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.errSub != "" && (err == nil || !strings.Contains(err.Error(), tt.errSub)) {
				t.Fatalf("want error containing %q, got %v", tt.errSub, err)
			}
			if !tt.wantErr && v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}
```

- [ ] **Step 2: Run — verify it fails**

Run: `go test ./internal/comparator/ -run TestCELBodyHandling`
Expected: FAIL — `bindVars` returns `unknown variable "body" in references` for the body cases.

- [ ] **Step 3: Add `encoding/json` import, the `body` case, and `parseBody`**

In `internal/comparator/cel.go`, add `"encoding/json"` to the import block. Add this case to the `switch` in `bindVars` (before `default`):
```go
		case celengine.VarBody:
			v, err := parseBody(ev.Output.Body)
			if err != nil {
				return nil, err
			}
			vars[name] = v
```
Append the helper to `internal/comparator/cel.go`:
```go
// parseBody implements §6: body is parsed as JSON only when referenced.
//   empty   → null (binds to nil)
//   valid   → parsed value (dyn)
//   invalid → hard, descriptive error (never a guessed empty object)
// JSON numbers decode to float64 (CEL double); compare with a double literal
// (body.count == 3.0) or int(body.count) == 3.
func parseBody(body []byte) (any, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("cel: response body is not valid JSON: %w", err)
	}
	return v, nil
}
```

- [ ] **Step 4: Run — verify all cel comparator tests pass**

Run: `go test ./internal/comparator/ -run TestCEL -v`
Expected: PASS (`TestCELBodyHandling` all 6 cases + every prior cel test).

- [ ] **Step 5: Confirm package coverage ≥80%**

Run: `go test ./internal/comparator/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -n 1`
Expected: total ≥ 80.0%. (The `NewCEL` panic line is unreachable and acceptable.) If below, add table rows to `TestCELOutputVars`/`TestCELTraceVars` for any uncovered `bindVars` branch.

- [ ] **Step 6: Format, vet, commit**

Run:
```bash
gofmt -w internal/comparator/cel.go internal/comparator/cel_test.go
go vet ./internal/comparator/
git add internal/comparator/cel.go internal/comparator/cel_test.go
git commit -m "feat(comparator): cel lazy body JSON binding with hard error on invalid-when-referenced"
```
Expected: clean; commit created.

---

### Task 6: Wire `cel` into the engine + godog `the run satisfies` step + §7 scenario-init compile

Register the comparator at the composition root, add the inline and docstring grammar steps, and enforce §7 by pre-compiling every scenario's CEL expressions in a `Before` hook so a malformed expectation fails before any SUT runs. Includes the L1 red-on-bad path (spec §10).

**Files:**
- Modify: `internal/engine/build.go`
- Modify: `internal/steps/steps.go`
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `comparator.NewCEL`, `comparator.CELExpectation`; `messages.PickleStep` (from `github.com/cucumber/messages/go/v21`, already a test dep — now also a non-test dep of `steps`); the comparator's `Compile(string) error` via type assertion.
- Produces: registration of `"cel"`; steps `^the run satisfies "([^"]*)"$` and `^the run satisfies:$`; `precompileScenario([]*messages.PickleStep) error` and `satisfiesExpr(*messages.PickleStep) (string, bool)`.

- [ ] **Step 1: Register the comparator at the composition root**

In `internal/engine/build.go`, add after the `result` registration (line ~24):
```go
	registry.RegisterComparator("cel", comparator.NewCEL())
```

- [ ] **Step 2: Write the failing godog cel scenarios + §7 tests**

Append to `internal/steps/steps_test.go` (the file already imports `bytes`, `strings`, `testing`, `time`, `gomock`, `godog`, `messages`, `config`, `core`, `mocks`, `correlate`, `engine`, `genai`, `trace`):
```go
// TestCELStepPasses exercises the inline + docstring "the run satisfies" grammar
// against the fake engine. happyTrace has tools search/summarize and 1800 tokens;
// the shell target echoes "hi" so answer == 'hi'. Inline CEL uses single-quoted
// strings (the step regex forbids embedded double quotes); the docstring form
// may use double quotes freely.
func TestCELStepPasses(t *testing.T) {
	eng := buildEng(t, happyTrace())
	feature := `Feature: cel
  Scenario: satisfies inline and docstring
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "answer == 'hi' && tokens < 5000"
    And the run satisfies:
      """
      "search" in tools && "summarize" in tools
      """
`
	// buildEng's target runs `sh -c echo done`; override answer expectation:
	// the buildEng shell prints "done", so assert on that instead of "hi".
	feature = strings.Replace(feature, "answer == 'hi'", "answer == 'done'", 1)

	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "cel", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}
}

// TestCELStepGoesRedOnFalse proves the godog layer reports non-zero when a cel
// expression is false, and surfaces the §9 value snapshot.
func TestCELStepGoesRedOnFalse(t *testing.T) {
	eng := buildEng(t, happyTrace())
	feature := `Feature: cel-red
  Scenario: false expression fails
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "tokens < 1"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "cel-red", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected failing suite, got 0\n%s", out.String())
	}
	if s := out.String(); !strings.Contains(s, "cel false") || !strings.Contains(s, "tokens=1800") {
		t.Fatalf("expected 'cel false' + 'tokens=1800' in output, got:\n%s", s)
	}
}

// TestPrecompileScenario unit-tests §7: a malformed expression fails at
// scenario-init; good inline and docstring forms compile cleanly.
func TestPrecompileScenario(t *testing.T) {
	eng := buildEng(t, happyTrace())
	w := &world{eng: eng}

	good := []*messages.PickleStep{{Text: `the run satisfies "tokens < 5000"`}}
	if err := w.precompileScenario(good); err != nil {
		t.Fatalf("good inline precompile: %v", err)
	}
	doc := []*messages.PickleStep{{
		Text:     `the run satisfies:`,
		Argument: &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: `"search" in tools`}},
	}}
	if err := w.precompileScenario(doc); err != nil {
		t.Fatalf("good docstring precompile: %v", err)
	}
	bad := []*messages.PickleStep{{Text: `the run satisfies "tokens <"`}}
	if err := w.precompileScenario(bad); err == nil {
		t.Fatal("want error for malformed expr at scenario-init, got nil")
	}
}

// TestCELScenarioInitFailsBeforeDrive proves §7: a malformed expression fails the
// scenario before any SUT is driven — the store is never queried (Times(0)).
func TestCELScenarioInitFailsBeforeDrive(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"svc": {Adapter: "shell", Command: []string{"sh", "-c", "echo done"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Times(0)
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Times(0)
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: cel-bad
  Scenario: malformed expression fails at scenario-init
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "nope == "
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "cel-bad", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected failing suite for malformed expr, got 0\n%s", out.String())
	}
	if s := out.String(); !strings.Contains(s, "scenario-init") {
		t.Fatalf("expected 'scenario-init' in output, got:\n%s", s)
	}
	// ctrl's t.Cleanup asserts Query/GetByID were never called → no SUT resolved.
}
```

- [ ] **Step 3: Run — verify the cel-step tests fail**

Run: `go test ./internal/steps/ -run TestCEL`
Expected: FAIL — the suites pass nothing / steps are undefined (`the run satisfies` has no matching step), and `precompileScenario`/`satisfiesExpr` are undefined.

- [ ] **Step 4: Add the steps, the precompile hook, and the helpers**

In `internal/steps/steps.go`, add `"regexp"` and `messages "github.com/cucumber/messages/go/v21"` to the import block. Add these package-level vars (after the imports):
```go
var (
	reSatisfiesInline = regexp.MustCompile(`^the run satisfies "([^"]*)"$`)
	reSatisfiesDoc    = regexp.MustCompile(`^the run satisfies:$`)
)
```
In the `Initializer` returned func, after the existing `sc.Step(...)` registrations (after the `responseBodyJSONContains` line), add:
```go
		sc.Step(`^the run satisfies "([^"]*)"$`, w.runSatisfies)
		sc.Step(`^the run satisfies:$`, w.runSatisfiesDoc)

		// §7: compile every CEL expression in the scenario before any step runs,
		// so a malformed expectation fails before an expensive SUT is driven.
		sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
			if err := w.precompileScenario(scenario.Steps); err != nil {
				return ctx, err
			}
			return ctx, nil
		})
```
Append the handlers and helpers to `internal/steps/steps.go`:
```go
func (w *world) runSatisfies(expr string) error {
	return w.check("cel", comparator.CELExpectation{Expr: expr})
}

func (w *world) runSatisfiesDoc(doc *godog.DocString) error {
	return w.check("cel", comparator.CELExpectation{Expr: doc.Content})
}

// precompileScenario compiles every "the run satisfies" expression in the
// scenario before any step executes (§7). A syntax/type/unknown-var error fails
// the scenario at init, before the SUT is driven.
func (w *world) precompileScenario(steps []*messages.PickleStep) error {
	for _, st := range steps {
		expr, ok := satisfiesExpr(st)
		if !ok {
			continue
		}
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
	}
	return nil
}

// satisfiesExpr extracts a CEL expression from a "the run satisfies" step, in
// either the inline quoted form or the trailing docstring.
func satisfiesExpr(st *messages.PickleStep) (string, bool) {
	if m := reSatisfiesInline.FindStringSubmatch(st.Text); m != nil {
		return m[1], true
	}
	if reSatisfiesDoc.MatchString(st.Text) && st.Argument != nil && st.Argument.DocString != nil {
		return st.Argument.DocString.Content, true
	}
	return "", false
}
```

- [ ] **Step 5: Run the steps suite — verify it passes**

Run: `go test ./internal/steps/ -v`
Expected: PASS — `TestCELStepPasses`, `TestCELStepGoesRedOnFalse`, `TestPrecompileScenario`, `TestCELScenarioInitFailsBeforeDrive`, and all pre-existing step tests (`TestFeatureExercisesGrammarAgainstFakeEngine`, etc.) still green.

- [ ] **Step 6: Run the whole repo green + coverage gate**

Run:
```bash
go build ./...
go test ./...
gofmt -l .
go vet ./...
```
Expected: build clean; all packages PASS; `gofmt -l .` prints nothing; `vet` clean.
Then verify the two touched packages clear the floor:
```bash
go test ./internal/cel/ ./internal/comparator/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -n 1
```
Expected: ≥ 80.0% each (run per-package if needed via the `/coverage` skill).

- [ ] **Step 7: Format, vet, commit**

Run:
```bash
gofmt -w internal/engine/build.go internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/engine/ ./internal/steps/
git add internal/engine/build.go internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): the-run-satisfies cel grammar with scenario-init compile (§7) and red-on-bad"
```
Expected: clean; commit created.

---

## Self-Review

**1. Spec coverage** (each section → task):

| Spec section | Covered by |
| --- | --- |
| §2 `internal/cel` engine | Task 1 |
| §2 `internal/comparator/cel.go` | Tasks 3–5 |
| §2 godog grammar step | Task 6 |
| §2 L1 unit pass + red-on-bad, ≥80% | Tasks 1,3,4,5 (red-on-bad in 4/6); coverage gate in 5/6 |
| §2 new dep `cel-go` | Task 1, Step 1 |
| §3 standalone `cel` comparator, full Evidence, `NewCEL`/`CELExpectation` | Task 3 |
| §4 `Engine`/`NewEngine`/`Compile`/`Program`/`References`/`Eval`; bool-result; compile-once | Tasks 1, 3 (cache) |
| §5 variable schema (11 vars, types, sources) | Tasks 1 (decl) + 3/4/5 (bind) |
| §5 single source of truth (reuse helpers) | Task 2 (lift) + Task 4 (reuse) |
| §6 lazy body JSON, hard error on invalid-when-referenced, null on empty, no parse when unreferenced | Task 5 |
| §7 inline + docstring grammar, compiled at scenario-init | Task 6 |
| §8 determinism/safety, built-in macros only (no custom funcs) | Task 1 (stdlib env; no extensions) |
| §9 failure reason = expr + referenced value snapshot | Task 3 (`reason`) |
| §10 testing: engine compile/eval/error paths; comparator pass + red-on-bad; ≥80%; hermetic | Tasks 1,3,4,5,6 |
| §12 decisions | encoded in "Design decisions" + tasks |

No spec requirement is left without a task.

**2. Placeholder scan:** No `TBD`/`TODO`/"add error handling"/"similar to Task N". Every code step shows complete code; every run step states the exact command and expected result.

**3. Type consistency:** `NewCEL() core.Comparator`, `CELExpectation{Expr string}`, `Compile(string) error`, `bindVars(refs []string, ev core.Evidence) (map[string]any, error)`, `reason(string, []string, map[string]any) string`, `parseBody([]byte) (any, error)` — names/signatures identical across Tasks 3–6. Engine: `NewEngine() (*Engine, error)`, `Compile(string) (*Program, error)`, `References() []string`, `Eval(map[string]any) (bool, error)`, `Var*` constants — identical across Tasks 1, 3–5. Lifted helpers `tokenSum`/`costSum`/`errorCount` (Task 2) match their call sites (Task 4). The `the run satisfies` regex patterns are defined once (Task 6) and used by both registration and `satisfiesExpr`.

**Known soft spots flagged for the implementer:**
- Task 1, Step 5/9: if the pinned cel-go version renames `IsExactType`/`AstToCheckedExpr`/`GetReferenceMap`, STOP and report (the fallback for the bool check is given inline; a reference-map rename needs Q).
- Decision 7: the lifted helper error messages intentionally keep the `budgets:` prefix; wrapped cel errors read `cel: binding cost: budgets: …`. Acceptable; do not "fix" by renaming (it would break `budgets_test.go`).
