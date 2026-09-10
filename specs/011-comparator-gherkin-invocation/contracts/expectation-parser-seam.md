# Contract: The `ExpectationParser` seam

**Consumers**: external comparator authors; `internal/steps`' new step handler.
**Fulfils**: FR-001, FR-002, FR-005, FR-009; SC-001, SC-005, SC-006.
**Decisions**: spec D2, D5. **Research**: [R2](../research.md), [R7](../research.md),
[R8](../research.md).

## The seam

```go
// package core
type ExpectationParser interface {
	ParseExpectation(text string) (Expectation, error)
}
```

```go
// package mentat
type ExpectationParser = core.ExpectationParser
```

Declared in `internal/core` and **aliased** on the facade — never declared at the facade.
`internal/steps` consumes it and root imports `internal/steps`, so a facade declaration is an
import cycle. This is 010's D5 rule: only terminal types may be facade-declared.

## Optionality is the whole design

`ExpectationParser` is **not** part of `Comparator` and MUST NOT be folded into it (FR-002).
It is discovered by type assertion at step time.

This is what makes the feature additive:

- Every existing comparator compiles and behaves identically, untouched (SC-005).
- A comparator author opts in by adding one method, with no change to registration.
- A comparator that does not implement it is not broken — it is simply not reachable from the
  new phrase, and the step says exactly that (FR-008).

**The rejected alternative**: adding `ParseExpectation` to `Comparator` itself. It would break
every existing implementation, including `examples/kafkaecho`, for a capability most
comparators do not need — and would make "comparator" mean "comparator that is Gherkin-driven",
which is a narrower thing than the seam is for.

## Purity is the contract

The method receives the docstring body and returns a value or an error. It gets **no**
`context.Context`, **no** target name, and **no** `Evidence`.

**Why this matters beyond tidiness.** `Evidence` is the single designated channel through
which a comparator sees run data (Constitution I — the portability boundary). A parser that
could reach run context would be a second, weaker channel, and a comparator built on it would
stop being portable across agent and service SUTs. Keeping the parser pure means it cannot
become that.

It also makes the seam trivially testable: text in, value out, no I/O, no fixtures.

## What Mentat guarantees to the parser

| Guarantee | Detail |
|---|---|
| Text is verbatim | `doc.Content` is passed exactly as godog produced it. Mentat does **not** trim, dedent, or normalize — whitespace may be significant to the comparator's format. |
| Text may be empty | An empty docstring is passed through. Whether that is valid is the comparator's decision, not the step's. Mentat MUST NOT pre-validate emptiness. |
| The returned value goes straight to `Compare` | Mentat does not inspect, copy, or re-shape it. It is handed to `Engine.Compare` as the `Expectation`. |
| Errors are surfaced, never swallowed | A returned error is wrapped with `%w` and named by comparator (FR-009). It is never converted into a failing verdict, because a parse failure is not an assertion failure. |

## What the parser must not assume

- **Not called once per suite.** It is called once per step occurrence. A parser MUST be safe
  to call repeatedly, and MUST NOT cache mutable state across calls keyed on nothing.
- **Not called on a fresh instance.** Comparators are registered once per `Run` and resolved
  by name; the same instance serves every scenario. Concurrency follows the suite's own
  scenario concurrency.
- **No ordering relationship with `Compare`.** The contract is only that `ParseExpectation`
  precedes the `Compare` it feeds.

## Round-trip is the author's responsibility

If a comparator's `ParseExpectation` returns a type its own `Compare` does not accept, that is
the comparator's bug. It surfaces as that comparator's own type-assertion error at `Compare`
time. **The step does not police the round trip** — doing so would require Mentat to know the
expectation type, which is exactly the coupling this seam removes.

## Nameability obligation

`ExpectationParser` appears in a published seam's method set, so 010's widened reachable-set
rule applies: `TestFacadeNameabilitySweep` (`surface_test.go:1172`) requires the facade alias
and fails without it (SC-006).

`Expectation` — the method's result type — is already facade-named and is a terminal
(`= any`), so naming this interface pulls nothing else onto the surface. The golden effect is
exactly two lines ([research R7](../research.md)).

This is the first seam added since 010 closed stability boundary 4, and therefore the first
live test that the gate catches a new unnameable seam type without anyone remembering to check.

## Falsification

Prove the gate works by breaking it, in the test file's own record — the mutation-rehearsal
discipline 009 established and 010 repeated:

1. Remove `type ExpectationParser = core.ExpectationParser` from the facade → the sweep fails,
   naming the type **and** the seam method that reaches it.
2. Restore it → green.
3. Recorded in the test file, not only in this contract.
