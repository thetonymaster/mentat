# Specification Quality Checklist: Custom-Comparator Gherkin Invocation

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-10
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

### On the "no implementation details" items — the interpretation used

Checked, with the same reading 001–010 used: for a Go test framework, the *stakeholder* is
a developer writing an extension, and the *product* is a public API surface. A spec that
refused to name `ExpectationParser` or `stepDefs` would be describing a different feature
than the one being built. What these items are read to forbid here is **unjustified**
implementation choice — algorithms, internal data structures, task sequencing — and the
spec contains none of that. Every Go-level reference in it is either an existing published
symbol or a decision recorded with its rationale and its rejected alternative.

Stated explicitly so a future reader knows this was a deliberate reading and not an
oversight.

### No clarification markers — every open question was closed with evidence

The feature description carried three explicitly deferred decisions. All three were closed
by reading the code at `1206a56` rather than by guessing, and each is recorded as a numbered
decision with its rejected alternative:

- **Aggregate comparators — in or out? → D4: out.** Settled by a capability check, not a
  taste call: `run.go` publishes `WithDriver`, `WithStore`, `WithComparator`, `WithJudge`
  and `WithReporter` and nothing else, so no external module can register a custom aggregate
  comparator in the first place. A Gherkin phrase naming one would reach only built-ins.
- **Completeness-sensitive or not? → D3: always sensitive.** The two errors are asymmetric —
  over-qualifying adds a visible caveat, under-qualifying produces an unsound green. The
  "correct" `CompletenessSensitive` opt-out interface is deliberately *not* added, because it
  would ship with zero implementations, which the composition rule forbids.
- **`ParseExpectation` signature? → D2: text only.** Keeps the seam pure and testable off the
  I/O path, and avoids giving comparators a second, weaker route to run data alongside
  `Evidence` (Constitution I).

### Two findings that shrank the feature

Recorded here because they contradict the framing the feature was deferred under in 009.

1. **`type Expectation = any` is not the blocker.** Six comparators already build typed
   expectations from feature-file text (`steps.go:320`, `:333`, `:337`, `:498`, `:555`,
   `:562`). The type assertion works. What is missing is only that the *choice* of concrete
   type is made at compile time by the step author.
2. **No new engine plumbing is required.** `Engine.Comparator(name)` already exists
   (`engine.go:200`) and `world.eng` is a concrete `*engine.Engine` (`steps.go:34`), so the
   handler can resolve the instance and type-assert it directly. The only accessor this
   feature adds is `Engine.Comparators() []string` (FR-011), mirroring the existing
   `Reporters()`.

### One risk carried forward from 010 — ~~and where the L3 proof lands~~ *(superseded)*

> **Superseded 2026-09-10 by [research R6](../research.md).** This note originally said
> FR-015's L3 obligation lands in `e2e/`, behind `//go:build e2e`. It does not, and it cannot:
> `e2e/main_test.go:29` builds `mentatBin` from `./cmd/mentat` and drives that prebuilt
> binary, so a comparator registered in Go via `WithComparator` is structurally unreachable
> there. The proof moved to `internal/steps` as an in-process godog suite. FR-015 was amended
> in place with the correction recorded. Struck through rather than deleted so the checklist
> and the spec cannot silently disagree about which one was right.

The underlying risk still stands and still needs SC-010: `make ci` has no e2e target, so the
e2e package can stop compiling without a single gate going red — which is what left it
unbuildable for six commits during 010. This feature adds no e2e test, but it adds a `core`
interface, and `e2e/` imports `core`. Hence `go vet -tags e2e ./...` as an explicit criterion.

### Post-analyze amendments — 2026-09-10

`/speckit-analyze` found four HIGH issues after this checklist was first written. All are
resolved in the artifacts; recorded here because three of them were **my errors**, not
ambiguities:

- **`stepDefs` row/group count was wrong.** Research R3 claimed "35 rows across five groups".
  Reality is **39 rows across six groups** — `Aggregate / CEL` was missed by a grep's literal
  spacing, and written down without reconciling it against the 39 patterns counted in R1 *in
  the same session*. Two numbers that had to agree didn't, and nobody looked. `Extend` is the
  **seventh** group; corrected in R3, `data-model.md`, `contracts/step-grammar.md` and
  `tasks.md`.
- **No nil-docstring guard was specified.** Seven of the eight existing docstring handlers
  guard `doc == nil`; the planned handler would have dereferenced it and panicked, which
  Constitution IV forbids. Now **FR-017**.
- **SC-001 had zero task coverage.** Every planned test built the engine through
  `engine.WithExtraComparator` — an internal package no external module can reach — so the
  feature's headline claim was unverified. Now **FR-018**, requiring the facade path
  (`mentat.WithComparator`, `run.go:352`).
- **FR-010's soundness decision was never asserted.** `sensitive=true` was passed as a literal
  no test pinned, so it could be flipped with every gate staying green. Now **SC-011**.

One pre-existing repo defect was found and deliberately **not** fixed here:
`responseBodyJSONContains` (`steps.go:543`) is the one docstring handler with no nil guard and
will panic where its seven siblings return an error. It belongs to the `Result` grammar, not
this feature; widening 011 to fix it would blur what this branch's diff is accountable for.

### Roadmap bookkeeping

D1 renumbers CLI/`mentatctl` UX from 012 to **013**, since Option B takes the 012 slot. The
source-of-truth roadmap line (`specs/009-extension-surface-integrity/spec.md:143`) and
`CLAUDE.md` both still say 012 for the CLI work and must be updated when 012 is specified.

### Implementation-phase corrections — 2026-09-10

Four things this specification asserted turned out not to be true when the code was
written. Recorded here so the checklist and the artifacts cannot silently disagree, in
the same spirit as the struck-through R6 note above.

- **The embedded-quote edge case describes an impossible behaviour.** The spec and
  `contracts/step-grammar.md` both said a comparator name containing a quote is
  *truncated*. The pattern is anchored at both ends and `([^"]+)` cannot cross a quote,
  so such a line matches nothing at all and the step is UNDEFINED. Both documents are
  corrected in place; `TestCustomComparatorPatternRejectsEmbeddedQuote` pins it.
- **Undefined steps and the non-strict default.** godog is non-strict by default, so an
  undefined step leaves the SUITE STATUS at 0. The first conclusion drawn from that —
  that a mistyped `Then` step passes silently in a real run — was **wrong**, and is
  corrected here rather than quietly dropped. `mentat.Run` discards the suite status and
  derives Results from the collector; godog passes the After hook
  `step is undefined: <text>` as stepErr, so the scenario is recorded as FAILED
  (`TestUndefinedStepFailsTheRun`). The gap was real in exactly one place,
  `ctl.ReplayFeature`, which read the suite status directly — fixed with `Strict: true`
  and covered by `TestReplayFeatureFailsOnUndefinedStep`. It remains the reason the new
  in-process suites set `Strict` in their own harness: there, the status IS the verdict.
- **`Registry.Comparators()` did not sort** while its documented-to-sort `Reporters()`
  sibling did. It had zero non-test callers, so the nondeterminism had never mattered;
  FR-007 renders it into an error message, which makes it matter. Fixed at the source.
- **Two task orderings could not have produced a real red.** T009 asked for
  `suite.Run() == 0` as the failing assertion (impossible — see the non-strict point),
  and T015 tested a nil guard that T010 mandated implementing first. Both were
  reordered so every test was observed failing before its implementation.

None of these changed the feature's scope, its decisions, or its success criteria.
