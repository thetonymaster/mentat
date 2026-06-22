# Mentat CEL Aggregate Scalar (computed-vs-expected) — Design

**Date:** 2026-06-20
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-19-mentat-multirun-runs-design.md` (this builds the refinement
its §287 explicitly deferred)
**Unblocks:** `2026-06-20-mentat-reporter-seam-design.md` (the reporter consumes the
`Verdict.Detail` this feature produces)

## 1. Purpose

The multi-run aggregate comparator (`@runs(N)` + `the runs satisfy "<cel>"`) evaluates an
**arbitrary boolean** CEL expression and, on failure, renders the *expression text* plus a
per-run table (`aggregate_cel.go:163 aggregateReason`). The multirun design (§287)
deferred two refinements as a unit:

> *the failing assertion as **computed-vs-expected** (e.g. `rate = 0.60, want >= 0.80`)
> and a per-run **value column** for the asserted quantity. Both require surfacing the
> aggregate's numeric result and evaluating the macro's inner per-run sub-expression,
> neither of which the v1 bool-returning program exposes.*

This feature builds exactly that. It is **independently valuable** — it upgrades the
console/JUnit failure message for every user, with no reporter involved — and it
**produces the `Verdict.Detail` the reporter seam consumes** (so it is sequenced first).

## 2. Scope

**In scope:**

- A compile-time `analyze` over the checked aggregate AST that recognises the **canonical
  comparison shape** (§3) and builds three cached CEL sub-programs.
- `EnableMacroCallTracking()` on the aggregate env so the macro's projection is
  recoverable after expansion.
- A new `AggregateProgram.Detail(vars) (Detail, bool, error)` that yields
  `{Macro, Op, Computed, Expected, PerRun []float64}` for canonical expressions.
- `core.AggregateDetail` + `core.Verdict.Detail *AggregateDetail`.
- `aggregate_cel.go` populates `Verdict.Detail` (pass **and** fail) and upgrades
  `aggregateReason` to a computed-vs-expected message with a per-run `value` column.

**Out of scope (deferred):**

- Compound (`&&`/`||`) decomposition into multiple details — canonical single comparison
  only (§3). A compound expression yields `Detail = nil`.
- A composite computed side (`mean(...) + mean(...) <= x`) — `Detail = nil`.
- Non-numeric comparisons (string/bool operands) — `Detail = nil`.
- Any reporter/JSON/HTML work — that is the companion reporter spec.
- New CEL macros or scalar variables — this feature reads the existing grammar only.

**New dependency:** none. Reuses `github.com/google/cel-go` (already direct) — specifically
its `common/ast`, `common/operators`, and macro-call-tracking surface.

## 3. The canonical rule

`Detail` is populated **iff** the expression is, at the top level:

```
‹aggregate-macro-call›  ‹comparison-op›  ‹runs-free numeric constant›
```

in **either operand order**, where:

- the **comparison-op** is one of `>` `>=` `<` `<=` `==` `!=`;
- exactly **one** operand subtree references the `runs` identifier — the **computed**
  side — and its root maps (via macro-call tracking) to a *single* aggregate macro call
  (`rate` `count` `sum` `mean` `min` `max` `stddev` `p50` `p95` `p99`);
- the **other** operand is `runs`-free and evaluates to a `float64` — the **expected**
  side.

Everything else → `Detail = nil` (no error): compound boolean expressions, a non-comparison
root, a composite/non-macro computed side, non-numeric operands, or both/neither operand
referencing `runs`. The report and failure message then fall back to today's
expression + per-run-table string.

This rule is a **pure static property of the AST**, decided entirely at `Compile` time.

## 4. Deliverables (when canonical)

1. **Scalar** — `{Macro, Op, Computed, Expected}`. `Op` is **normalized** so it always
   reads `computed ‹op› expected`; an expression written `0.8 <= rate(...)` stores
   `Op: ">="`, `Computed: rate`, `Expected: 0.8`.
2. **Per-run value column** — `[]float64` aligned positionally with the runs order; each
   run's projection/predicate (`r.latencyMs`; a predicate macro contributes `1.0`/`0.0`).
3. **Upgraded failure message** — `aggregate false: rate = 0.60, want >= 0.80  (10 runs)`
   and the per-run table gains a `value` column. Falls back to today's message when
   `Detail` is nil.
4. **`core.Verdict.Detail`** — populated on **both pass and fail** (the reporter shows the
   computed value regardless of outcome). `Verdict.Reasons` stays fail-only and unchanged
   in the non-canonical case.

## 5. Mechanism

All static analysis and sub-program construction happen at **`Compile`** (scenario-init),
so an unsupported shape and any cel-go round-trip problem surface before the SUT is driven,
matching the existing precompile gate (`steps.go:250 precompileScenario`).

`NewAggregateEngine` (`aggregate.go:45`) gains `celgo.EnableMacroCallTracking()`. `Compile`
additionally runs `analyze(checkedAST)`:

1. **Root is a comparison call** — `operators.{Greater,GreaterEquals,Less,LessEquals,Equals,NotEquals}`,
   2 args. Else → no plan.
2. **Classify operands** — walk each operand's descendants for an `IdentKind` named `runs`
   (same `ast.NavigateAST` / `ast.MatchDescendants` precedent as `referencedFields`,
   `aggregate.go:90`). Exactly one operand references `runs` → **computed**; the other →
   **expected**. Else → no plan.
3. **Recover the macro** — the computed operand's root ID maps via
   `SourceInfo.GetMacroCall(id)` to a call whose function ∈ the macro set →
   record `Macro`, `iterVar = args[0]` (ident), `proj = args[1]`. Else (composite /
   non-macro) → no plan.
4. **Build & cache three sub-programs** (same env):
   - `computedPrg` ← the computed operand sub-AST
     (e.g. `__percentile__(runs.map(r, double(r.latencyMs)), 0.95)`).
   - `expectedPrg` ← the expected operand sub-AST (`runs`-free).
   - `perRunPrg` ← a constructed `runs.map(‹iterVar›, double(‹proj›))` (predicate macros
     coerce bool→double, giving `1.0`/`0.0`).
   - the normalized `Op`.

### 5.1 The sub-program primitive

`subProgram(env, expr ast.Expr) (celgo.Program, error)` builds a runnable program from a
single `ast.Expr`:

- **Primary path:** `ast.ToProto(ast.NewAST(expr, sourceInfo))` →
  `cel.CheckedExprToAst(proto)` → `env.Program(...)`.
- **Validated fallback** (if the proto round-trip misbehaves across cel-go versions):
  unparse the sub-AST to source (`cel.AstToString`) and `env.Compile`/`env.Program` it
  normally.

The public surface (`Detail`, the cel-local `Detail` struct) is identical under either
path. **Plan A Task 1 is a spike** that proves the primary path (and macro-call recovery)
on a real `rate(r, !r.failed) >= 0.8`; the rest of the plan is gated behind it.

### 5.2 Eval

`AggregateProgram.Detail(vars map[string]any) (Detail, bool, error)`:

- `plan == nil` → `(Detail{}, false, nil)` (non-canonical).
- else run the three cached programs against the same `runs` records →
  `Detail{Macro, Op, Computed, Expected, PerRun}`, `true`, `nil`.
- sub-eval errors propagate (`%w`, invariant 4). They cannot introduce **new** data errors:
  the computed side already evaluated successfully inside the main bool `Eval`, and
  empty-sample cases error in that main `Eval` first (so `Detail` is never reached).

## 6. Module split (no new cycles)

- **`internal/cel`** (leaf; does **not** import `core`) — `analyze`, `subProgram`, the
  cached plan on `AggregateProgram`, and `AggregateProgram.Detail` returning a
  **cel-local** `Detail` struct (`Macro string; Op string; Computed, Expected float64;
  PerRun []float64`).
- **`internal/core`** — new `AggregateDetail` struct + `Verdict.Detail *AggregateDetail`.
  `Verdict` stays a struct (no interface), so mockgen is unaffected.
- **`internal/comparator/aggregate_cel.go`** (imports both) — calls `prg.Detail(...)`, maps
  the cel-local `Detail` → `core.AggregateDetail`, sets `Verdict.Detail` on pass and fail,
  and upgrades `aggregateReason` to use it.

```go
// internal/core/core.go
type AggregateDetail struct {
    Expr     string    // original surface expression
    Macro    string    // "rate","p95",... (the computed-side macro)
    Op       string    // normalized: reads computed OP expected (">=","<=","==",...)
    Computed float64
    Expected float64
    PerRun   []float64 // aligned with the runs order; predicates are 1.0/0.0
}
type Verdict struct {
    Pass    bool
    Reasons []string
    Detail  *AggregateDetail // non-nil only for a canonical aggregate comparison
}
```

## 7. Error handling

Per invariant #4 (no silent fallbacks):

- **Non-canonical shape → `Detail` nil, no error** — a legitimate "not structured" outcome;
  today's expression + per-run-table string stands.
- **Compile-time** sub-program build failure on a *canonical* expression → hard error at
  scenario-init (before the SUT runs), wrapped naming the expression.
- **Eval-time** sub-eval error → propagated, never swallowed; in practice unreachable for
  canonical expressions on data the main `Eval` already accepted.
- **Empty sample** (`p95`/`mean` over 0 runs) — unchanged: the main `Eval` errors first.

## 8. Testing (TDD, table-driven, ≥80% per package)

- **`analyze` / `Compile` (L1):** table of expressions →
  `(hasPlan, macro, normalizedOp, computed-side)`. Canonical: both operand orders, every
  macro, `==`/`!=`. Non-canonical: compound `&&`, no comparison, composite computed
  (`mean(...)+mean(...)`), non-numeric operand, both/neither side references `runs` → no
  plan.
- **`Detail(vars)` (L1):** fixed records → assert `Computed`/`Expected`/`PerRun` per macro
  (`rate` → per-run 0/1; `p95` → latency list; `count` → predicate 1/0; `mean`/`sum`/…).
- **`aggregate_cel` comparator (L1):** `Verdict.Detail` set on **pass and fail** for
  canonical, **nil** for non-canonical; upgraded `aggregateReason` format on fail.
  *(Existing tests asserting the old reason string are updated — expected churn.)*
- **L3 meta-test (mandatory — prove Mentat goes red):** drive a known-bad **canonical**
  aggregate (e.g. `rate(...) >= 0.9` that fails) and assert the user-visible output carries
  `computed = …, want >= 0.90` — the structured detail reaches stdout. A known-bad
  **non-canonical** aggregate still goes red via the fallback message. Existing multirun L3
  substrings updated where the canonical message changed.
- **Coverage:** `internal/cel` and `internal/comparator` stay ≥80%.

## 9. Decisions made (with rationale)

- **Canonical single comparison only** — covers the real assertions (`rate(...) >= 0.8`,
  `p95(...) <= 1500`, `count(...) == 0`); compound/composite shapes have fuzzy
  computed-vs-expected semantics and are deliberately left as `Detail = nil`. (Approved.)
- **Include the per-run value column** — the multirun §288 deferral bundled it with the
  scalar; recovering the macro projection makes it a natural by-product. (Approved.)
- **All analysis + sub-program build at `Compile`** — fail fast at scenario-init, before
  the SUT, like the existing CEL precompile gate. (Approved.)
- **`runs`-referencing side = computed; constant side = expected; `Op` normalized to read
  `computed OP expected`** — handles both operand orders and constant-expression
  thresholds without string parsing. (Approved.)
- **cel-local `Detail` struct; `core.AggregateDetail` is the mapped public type** — keeps
  `internal/cel` a `core`-free leaf. (Approved.)
- **Spike-first plan (Task 1)** — the sub-AST→`Program` round-trip and macro-call recovery
  are the only real unknowns; prove them before building on them, with the
  unparse-and-recompile fallback already identified. (Approved.)
