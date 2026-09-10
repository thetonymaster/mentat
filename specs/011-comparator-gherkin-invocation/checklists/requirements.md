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

### One risk carried forward from 010

FR-015's L3 obligation lands in `e2e/`, behind `//go:build e2e`, which `make ci` does not
compile. During 010 that left the e2e package unbuildable for six commits without any gate
going red. SC-010 makes `go vet -tags e2e ./...` an explicit success criterion so the same
hole cannot swallow this feature's red-on-bad proof.

### Roadmap bookkeeping

D1 renumbers CLI/`mentatctl` UX from 012 to **013**, since Option B takes the 012 slot. The
source-of-truth roadmap line (`specs/009-extension-surface-integrity/spec.md:143`) and
`CLAUDE.md` both still say 012 for the CLI work and must be updated when 012 is specified.
