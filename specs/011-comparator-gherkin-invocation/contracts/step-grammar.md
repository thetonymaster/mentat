# Contract: The invocation phrase and its failure behaviour

**Consumers**: test authors writing `.feature` files; `mentat steps`; `docs/steps.md`.
**Fulfils**: FR-003, FR-004, FR-006, FR-007, FR-008, FR-010, FR-012, FR-013; SC-002, SC-003,
SC-004.
**Decisions**: spec D1, D3, D5. **Research**: [R1](../research.md), [R3](../research.md),
[R6](../research.md).

## The phrase

```gherkin
Then the "revenue-shape" comparator is satisfied by:
  """
  { "min": 4, "currency": "USD" }
  """
```

```go
pattern: `^the "([^"]+)" comparator is satisfied by:$`
```

**One row, one group.** `stepDefs` gains exactly one entry (SC-002), in a new **seventh** group
`Extend`. The grammar is deliberately plain rather than natural: Option B (012) is where
ergonomics get solved, and this phrase should read as obviously generic so nobody mistakes it
for the final syntax.

**Unambiguous by construction.** Verified exhaustively over all 39 registered patterns: every
existing `^the …` pattern requires a literal keyword (`agent`, `service`, `tool`, `services`,
`result`, `response`, `run`, `runs`) in the position where this one has a quote. Godog treats
ambiguous matches as errors, so this was checked rather than assumed
([research R1](../research.md)).

## The drift invariant — a hard constraint, not a goal

`internal/steps/metadata.go`'s `stepDefs` is the single source of truth for three consumers:
godog registration (`registerSteps`), the `mentat steps` CLI, and `docs/steps.md`.
`metadata_test.go` and `docs_test.go` fail loudly if any diverge.

> **This feature MUST NOT modify those tests' assertions** (FR-012, SC-003). It adds a row and
> regenerates `docs/steps.md` (FR-013); the invariant is preserved, never relaxed.

This is the operational test of decision D1's central claim. If landing this feature requires
touching a drift test, the design has drifted into Option B and the plan is wrong.

The row must carry a non-blank `group`, `summary` and `example` — enforced by
`metadata_test.go` — and rows sharing a group must be contiguous, enforced by `docs_test.go`.
A single appended row in a new group satisfies both trivially.

## Resolution path

The handler introduces **no new resolution mechanism** (FR-004). It uses what exists:

```text
Engine.Comparator(name)     already exists, engine.go:200
world.eng                   concrete *engine.Engine, steps.go:34
Engine.Comparators()        added, mirroring Reporters() at engine.go:218
checkExp(name, exp, true)   already exists, steps.go:254
```

Routing through `checkExp` is mandatory (FR-006), and is what makes qualifier recording,
judge-usage accounting and the `@runs(n>1)` guard (`steps.go:256`) behave identically to every
built-in comparator step. A handler that called `Engine.Compare` directly would silently drop
all three.

## Completeness sensitivity

The step is **always** completeness-sensitive (FR-010, spec D3): it routes through
`checkSensitive` semantics, and the engine attaches the completeness qualifier when — and only
when — the target's contract is bounded.

The step cannot know an arbitrary comparator's sensitivity, and the two errors are not
symmetric: over-qualifying adds a visible, conservative caveat; under-qualifying produces an
**unsound green**, the exact property 008 exists to protect. Defaulting to the sound side is
the only defensible choice.

## Failure behaviour — three modes, three errors, no fallbacks

| Situation | Required error | Verified by |
|---|---|---|
| Name not registered | Names the captured name **verbatim** and lists the registered comparator names | SC-004 |
| Registered, but does not implement `ExpectationParser` | Names the comparator; states it cannot be driven from Gherkin | SC-004 |
| `ParseExpectation` returns an error | `%w`-wrapped, named by comparator | SC-004 |

**Verbatim matters.** The captured name is echoed with `%q`, so an otherwise invisible
difference — a trailing space, a homoglyph — is visible to the author instead of presenting as
a mysterious unknown-name error.

> **Corrected 2026-09-10 during implementation (T014).** This paragraph previously claimed a
> name containing an embedded quote is *truncated* at the quote, and that verbatim echo is what
> makes the truncation visible. That is wrong: the pattern is anchored at both ends and
> `([^"]+)` cannot cross a quote, so such a line matches **nothing** and the step is UNDEFINED
> rather than mis-captured. Pinned by `TestCustomComparatorPatternRejectsEmbeddedQuote`.
>
> Verbatim echo remains required — it just earns its keep on the cases that actually reach the
> handler, not on a truncation that cannot occur.
>
> A follow-up claim in this note — that godog's non-strict default therefore lets an undefined
> step pass silently in a real run — was investigated and found **largely false**.
> `mentat.Run` discards the suite status and derives Results from the collector, whose After
> hook receives `step is undefined: <text>` as stepErr and records the scenario as FAILED. The
> gap was real only in `ctl.ReplayFeature`, which read the suite status directly; fixed with
> `Strict: true`. See the spec's Edge Cases.

**Listing the registered names** mirrors 010's `WithReports` unknown-name behaviour and is the
difference between a usable seam and a guessing game. It is why `Engine.Comparators()` exists
at all.

None of the three may produce a nil expectation, a zero-value expectation, a skipped
assertion, or a passing step (Constitution IV).

## Documentation obligations

- `docs/steps.md` regenerated so the committed reference carries the new row (FR-013).
- `docs/extending/comparator.md` gains the `ExpectationParser` seam, a complete worked
  comparator implementing it, and the feature-file snippet that drives it (FR-014). A seam
  documented only by its interface declaration is not documented.

## Falsification

The step's value is that it goes **red** correctly, so that is what gets proven
(FR-015, SC-007), in `internal/steps` as an in-process godog suite following
`TestFeatureGoesRedOnBadScenario` (`steps_test.go:103`):

1. A registered custom comparator that returns a failing verdict → suite exits non-zero, and
   the output carries the comparator's own reasons.
2. A registered custom comparator whose `ParseExpectation` errors → the run fails loudly
   rather than skipping the assertion.
3. The green case alone is **not** sufficient evidence and does not satisfy this contract.

Not in `e2e/`: that suite drives a prebuilt `cmd/mentat` binary which cannot contain a
Go-registered comparator, and it sits behind `//go:build e2e`, which `make ci` never compiles
([research R6](../research.md)).
