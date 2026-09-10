# Data Model: Custom-Comparator Gherkin Invocation (011)

**Date**: 2026-09-10 | **Spec**: [spec.md](./spec.md) | **Research**: [research.md](./research.md)

This feature adds **one interface, one step row, and one accessor**. It introduces no new
domain concept and changes no existing type's shape. What follows is the complete inventory.

---

## 1. `ExpectationParser` — the new optional seam

```go
// package core  (internal/core/core.go, beside Comparator and Expectation)

// ExpectationParser is an OPTIONAL capability a Comparator may declare. A comparator that
// implements it can be driven directly from a .feature file: the Gherkin step hands it the
// docstring body verbatim, and it returns its own expectation type.
//
// Mentat never inspects the text. Its format is entirely the comparator's business, which is
// what keeps the seam narrow — adding a comparator never requires a Mentat change.
type ExpectationParser interface {
	ParseExpectation(text string) (Expectation, error)
}
```

```go
// package mentat  (mentat.go)
type ExpectationParser = core.ExpectationParser
```

**Declared in `core`, not at the facade.** `internal/steps` consumes it and root imports
`internal/steps`, so a facade declaration would be an import cycle — 010's D5 rule, only
terminal types may be facade-declared ([research R8](./research.md)).

**Purity is the contract** (spec D2). The method receives text and returns a value or an
error. No `context.Context`, no target, no `Evidence`. A comparator needing run data has
`Evidence` at `Compare` time, which is the designated channel; widening this signature would
open a second, weaker route to run data and blur Constitution I.

**Optional by type assertion.** Nothing about `Comparator` changes (FR-002). A comparator
that does not implement `ExpectationParser` keeps compiling and keeps working through its
existing hard-coded step; it simply cannot be named from the new phrase, and says so loudly.

**Surface effect**: exactly two golden lines ([research R7](./research.md)):

```text
method (ExpectationParser) ParseExpectation(text string) (Expectation, error)
type ExpectationParser = core.ExpectationParser
```

`Expectation` is already facade-named and is a terminal (`= any`), so 010's widened sweep
pulls in nothing further.

---

## 2. The `stepDefs` row — the only grammar change

One row appended to `internal/steps/metadata.go`, in a new sixth group:

```go
{
	group:   "Extend",
	pattern: `^the "([^"]+)" comparator is satisfied by:$`,
	summary: "Runs a registered custom comparator against the docstring as its expectation.",
	example: "Then the \"revenue-shape\" comparator is satisfied by:\n  \"\"\"\n  {\"min\": 4}\n  \"\"\"",
	handler: func(w *world) any { return w.comparatorSatisfiedByDoc },
},
```

**Group `Extend` is new** ([research R3](./research.md)). The step belongs to none of the
five existing groups — it is not about driving, sequence, budgets, results or shape, but about
reaching outside the built-in grammar. Appending one row in a new group satisfies
`docs_test.go`'s contiguity invariant trivially.

**Pattern collision: none.** Verified exhaustively over all 39 registered patterns — every
existing `^the …` pattern requires a literal keyword (`agent`, `service`, `tool`, `services`,
`result`, `response`, `run`, `runs`) where this one has a quote
([research R1](./research.md)).

**Handler signature** follows the six existing docstring handlers — captures first, docstring
last ([research R2](./research.md)):

```go
func (w *world) comparatorSatisfiedByDoc(name string, doc *godog.DocString) error
```

---

## 3. `Engine.Comparators()` — the only engine addition

```go
// Comparators returns this engine's registered comparator names, sorted.
func (e *Engine) Comparators() []string { return e.reg.Comparators() }
```

Mirrors `Engine.Reporters()` (`engine.go:218`) exactly, over the already-existing
`Registry.Comparators()` (`registry.go:125`). It exists solely so FR-007's unknown-name error
can list what *is* registered.

**Nothing else is added to `Engine`.** `Engine.Comparator(name) (core.Comparator, bool)`
already exists (`engine.go:200`) and `world.eng` is a concrete `*engine.Engine`
(`steps.go:34`), so instance resolution needs no new plumbing
([research R4](./research.md)).

---

## 4. Handler control flow — the whole feature in one place

```text
                       ┌─────────────────────────────────────────┐
  Gherkin phrase ──────▶│ comparatorSatisfiedByDoc(name, doc)     │
  + docstring           └───────────────┬─────────────────────────┘
                                        │
                        1. w.eng.Comparator(name)
                                        │
                          not found ────┴──▶ error: unknown name + w.eng.Comparators()   [FR-007]
                                        │
                        2. c.(core.ExpectationParser)
                                        │
                      not implemented ──┴──▶ error: named comparator cannot parse         [FR-008]
                                        │
                        3. p.ParseExpectation(doc.Content)
                                        │
                            parse error ┴──▶ error: %w-wrapped, named by comparator       [FR-009]
                                        │
                        4. w.checkExp(name, exp, sensitive=true)                          [FR-006, FR-010]
                                        │
                                        └──▶ existing path: multi-run guard, qualifiers,
                                             judge ledger, pass/fail — all unchanged
```

Every branch that cannot proceed returns a descriptive error naming the offending value.
There is no fallback, no zero-value expectation, and no silently skipped assertion
(Constitution IV, spec D5).

**Step 4 is the load-bearing reuse.** Routing through the existing `checkExp`
(`steps.go:254`) is what makes qualifier recording, judge-usage accounting and the
`@runs(n>1)` guard (`steps.go:256`) behave identically to every built-in comparator step,
without this feature reimplementing any of it.

---

## 5. What does *not* change

Stated explicitly, because the value of this design is in how little it touches:

| Thing | Status |
|---|---|
| `core.Comparator` interface | unchanged (FR-002, SC-005) |
| `type Expectation = any` | unchanged — verified not to be the blocker |
| The six built-in comparators and their steps | unchanged |
| `registerSteps`, `StepDocs`, the drift tests' premises | unchanged (FR-012, SC-003) |
| Registration (`WithComparator`) | unchanged — this adds invocation, not registration |
| Emitted report shape | unchanged — a custom verdict lands like any other |
| `Engine.Compare` signature | unchanged |

---

## Entity relationships

```text
internal/core     Comparator ────────────────┐
                  Expectation (= any)        │  a Comparator MAY also implement
                  ExpectationParser ◀────────┘  ExpectationParser (type assertion)
       ▲
       │ aliased
       │
mentat (root)     mentat.ExpectationParser = core.ExpectationParser

internal/steps    comparatorSatisfiedByDoc ──▶ Engine.Comparator(name)
                                           ──▶ (ExpectationParser).ParseExpectation(text)
                                           ──▶ checkExp ──▶ Engine.Compare(…)
```

No new package. No new import edge except `internal/steps` → the interface it already reaches
through `core`.
