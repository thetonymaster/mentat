# Mentat CEL Engine — Design

**Date:** 2026-06-18
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`

## 1. Purpose

The v1 `result` comparator offers fixed matchers (`exact | contains | regex |
json-subset | status`). They cover common cases but cannot express compound or
conditional predicates, nor combine result facts with trace-derived facts in one
assertion. This spec adds a **CEL (Common Expression Language) engine** and a
**`cel` comparator** so spec authors can write deterministic, sandboxed boolean
expressions over a run's output and curated trace aggregates.

CEL is deterministic and side-effect-free, so it belongs in the same deterministic
comparator tier as `sequence`/`budgets`/`result` — not the Phase 4 LLM-judge tier.

This is an **independent axis** from Phase 2 portability: it benefits both the agent
and microservice paths, which is why it gets its own spec.

## 2. Scope

**In scope**

- `internal/cel` — a reusable CEL engine: environment + declared variable schema,
  compile (fail-fast type-check), evaluate against a bound variable map.
- `internal/comparator/cel.go` — a standalone `cel` comparator that binds
  `Evidence → vars` and wraps the engine.
- A godog grammar step (`the run satisfies …`) for inline and docstring expressions.
- L1 unit tests (pass-path **and** red-on-bad) against existing goldens; ≥80% coverage.

**Out of scope**

- Custom CEL functions / extension libraries — built-in macros suffice in v1 (§8).
- A raw-span-forest binding (`spans`/`roots`) — deliberately excluded to keep the
  schema stable and avoid overlap with the Phase 3 `shape` comparator. The engine can
  gain such variables later by **adding** to the schema without breaking expressions.
- CEL in sidecar expectation files — Phase 3 concern.

**New dependency**

`github.com/google/cel-go`. Heavier than the other direct deps (pulls the antlr
runtime + a few transitive packages); `protobuf`/`genproto` are already present
transitively. This footprint is accepted in exchange for a standard, well-specified,
type-checked expression language rather than a hand-rolled mini-DSL.

## 3. Exposure — a standalone `cel` comparator

The CEL environment reads **trace aggregates** (tokens, cost, latency, tool/service
names), not just the boundary output. The existing `result` comparator's own contract
is "reads only `ev.Output`; it never touches `ev.Trace`" (`internal/comparator/result.go`).
Embedding a trace-aware CEL matcher inside `result` would violate that invariant.

Therefore CEL is its **own comparator** consuming full `Evidence`:

```go
type CELExpectation struct { Expr string }

func NewCEL() core.Comparator   // Name() == "cel"
```

The existing fixed `result` matchers stay unchanged and coexist. CEL is the expressive
escape hatch for predicates the fixed matchers cannot express — not a replacement.

## 4. The engine (`internal/cel`)

```go
type Engine struct { /* *cel.Env */ }

// NewEngine builds the CEL environment with the declared variable schema (§5).
func NewEngine() (*Engine, error)

// Compile type-checks expr against the schema. Returns a descriptive error on a
// syntax error, an unknown variable, a type error, or a non-bool result type.
func (e *Engine) Compile(expr string) (*Program, error)

type Program struct { /* checked, compiled program */ }

// References returns the schema variables the expression actually uses, so the
// caller can bind/parse only what is needed (see §6, body handling).
func (p *Program) References() []string

// Eval runs the program against a bound variable map and returns the boolean result.
func (p *Program) Eval(vars map[string]any) (bool, error)
```

Design properties:

- **Core-free and generic.** The engine imports neither `core` nor `trace`; it owns
  only the variable *schema* (names + CEL types). The comparator owns the
  `Evidence → vars` binding and verdict formatting. This keeps the engine independently
  testable and reusable.
- **Result type must be `bool`.** `Compile` rejects an expression whose checked output
  type is not boolean — a malformed predicate fails at compile time, not as a runtime
  surprise (no silent fallback, invariant 4).
- **Compile once.** Expressions are compiled at scenario-init (§7); evaluation is the
  only per-run work.

## 5. Variable schema (declared in the engine)

| Variable | CEL type | Source |
|---|---|---|
| `status` | `int` | `Output.Status` (http) |
| `exitCode` | `int` | `Output.ExitCode` (shell) |
| `body` | `dyn` | `Output.Body` parsed as JSON — bound only if referenced (§6) |
| `bodyText` | `string` | `string(Output.Body)`, raw |
| `answer` | `string` | `Output.Answer` (`ExtractAnswer`) |
| `tokens` | `int` | sum of `gen_ai.usage.*_tokens` |
| `cost` | `double` | sum of `gen_ai.usage.cost_usd` |
| `latencyMs` | `int` | `Trace.Envelope()` in milliseconds |
| `errors` | `int` | error-span count |
| `tools` | `list(string)` | ordered tool names |
| `services` | `list(string)` | ordered service names |

**Single source of truth for facts.** `tools` reuses the same selection the `sequence`
comparator uses (`ByOp(execute_tool)` → `gen_ai.tool.name`); `services` reuses the
`sequence` `Kind:"service"` selection (group by `service.name`, first-seen start);
`latencyMs` reuses `Trace.Envelope()` as `budgets` does; `tokens`/`cost`/`errors` reuse
the `budgets` aggregation. These extraction helpers are factored so CEL and the
dedicated comparators can never disagree about the same fact. (Where a helper currently
lives private to a comparator, it is lifted to a shared, unexported-where-possible
location during implementation — a behavior-preserving refactor.)

## 6. JSON body handling — no silent fallback

`body` is parsed as JSON **only when the compiled expression references it**
(`Program.References()` reports this):

- Referenced, body empty → `body` binds to `null`.
- Referenced, body non-empty and valid JSON → `body` binds to the parsed value (`dyn`).
- Referenced, body non-empty and **invalid** JSON → the comparator returns a hard,
  descriptive error (`cel: response body is not valid JSON: …`). It never guesses an
  empty object.
- Not referenced → no parse is attempted, so a non-JSON body in an unrelated test is
  never a spurious failure.

Raw bytes are always reachable as `bodyText` for string/format predicates that do not
need structured access.

## 7. Grammar

Two step forms, mapping to `CELExpectation{Expr: …}`:

```gherkin
Then the run satisfies "status == 201 && tokens < 5000"

Then the run satisfies:
  """
  body.status == "confirmed" &&
  !("legacy-pricing" in services)
  """
```

Expressions are **compiled at scenario-init**: a syntax/type error or unknown-variable
reference fails the run **before any SUT is driven**, so a broken expectation surfaces
immediately rather than after an expensive run.

## 8. Determinism, safety, and functions

- CEL programs are deterministic and sandboxed: no I/O, no clock, no side effects. This
  is what qualifies CEL for the deterministic comparator tier (unlike the Phase 4
  semantic matcher).
- **No custom functions in v1.** CEL's built-in macros — `exists`, `all`, `exists_one`,
  `filter`, `map`, and the `in` operator — over the bound `tools`/`services` lists and
  `body` cover the intended predicates. Custom functions are a deliberate non-goal
  (YAGNI); add them only against ≥3 concrete needs.

## 9. Failure reasons

On a `false` result the `Verdict` is not bare. The reason includes the **expression**
and a **snapshot of the referenced bound values**, e.g.:

```
cel false: "tokens < 5000 && status == 201"  [tokens=5200 status=201]
```

Only referenced variables are shown (from `Program.References()`), keeping the message
focused. This meets the project's error-message standard: the failure shows which fact
was off, not merely that an assertion failed.

## 10. Testing

- **L1 unit (no infra):** compile/eval unit tests for the engine (good expressions,
  syntax errors, type errors, unknown vars, non-bool result, invalid-JSON body); and
  comparator tests against existing researchbot goldens (and orderflow goldens once
  Phase 2 lands), covering both the **pass path and the red-on-bad path** — a CEL
  expression that should fail must fail with the expected reason. This is the
  comparator-level analogue of the L3 principle.
- **Coverage:** ≥80% per package (`internal/cel`, and the comparator's contribution to
  `internal/comparator`).
- **Hermetic:** all CEL tests are pure in-memory; no Tempo, no network.

## 11. Relationship to other comparators

CEL can express budget/sequence-style checks in a single predicate, but the dedicated
`sequence`/`budgets`/`result` comparators remain the idiomatic, self-documenting path
for the cases they cover. CEL's purpose is **compound and conditional** assertions the
fixed comparators cannot express, for example:

```
status == 201 ? tokens < 5000 : tokens < 2000
```

It does not deprecate or replace any existing comparator.

## 12. Decisions made (with rationale)

- **Standalone `cel` comparator, not a `result` matcher** — the CEL env is trace-aware,
  and `result` is contractually output-only; a trace-aware matcher inside `result`
  would break that invariant. (Approved.)
- **Output + curated trace aggregates as the env; no raw span forest** — expressive
  enough for compound result/budget/sequence predicates while keeping the schema small
  and stable; raw spans overlap Phase 3 `shape` and can be added later additively.
  (Approved.)
- **Lazy `body` JSON parse, hard error on invalid-when-referenced** — no silent `{}`
  fallback; no spurious parse failures for tests that ignore `body`. (Approved.)
- **Inline + docstring grammar, compiled at scenario-init** — short predicates stay
  one-liners, complex ones get multi-line readability; bad expressions fail fast.
  (Approved.)
- **No custom CEL functions in v1** — built-in macros suffice; YAGNI. (Approved.)
- **Reuse the existing fact-extraction helpers** — CEL and the dedicated comparators
  share one source of truth for tokens/cost/latency/tools/services, so they can never
  disagree.
