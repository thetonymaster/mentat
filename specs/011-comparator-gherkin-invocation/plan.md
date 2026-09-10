# Implementation Plan: Custom-Comparator Gherkin Invocation

**Branch**: `011-comparator-gherkin-invocation` | **Date**: 2026-09-10 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/011-comparator-gherkin-invocation/spec.md`

---

## Summary

Let a user-registered comparator be named directly from a `.feature` file. One optional
interface (`ExpectationParser`) lets a comparator turn a docstring into its own expectation
type; one generic `stepDefs` row carries the comparator's name and that docstring to it.

The research phase shrank this feature twice. Name resolution, instance resolution and
text-to-typed-expectation **all already exist** — six comparators build typed expectations
from feature-file text today, and `Engine.Comparator(name)` already returns the instance. The
only genuinely missing pieces are a phrase that carries a name and a defined way for the named
comparator to parse the accompanying text. Net addition: **one interface, one alias, one step
row, one three-line accessor, one handler.**

The load-bearing constraint is not `type Expectation = any` — verified not to be the blocker —
but that `stepDefs` is a compile-time table serving three drift-tested consumers. Option A
adds a row to it; Option B (deferred to 012) would invert it.

---

## Technical Context

**Language/Version**: Go 1.25, module `github.com/thetonymaster/mentat`

**Primary Dependencies**: `github.com/cucumber/godog` (BDD layer, docstring binding),
`go.uber.org/mock` (gomock — `MockTraceStore` for hermetic engine construction). No new
dependency.

**Storage**: N/A. Tests use the gomock `TraceStore` and inline `FeatureContents`; nothing
touches disk or network.

**Testing**: `go test`, table-driven; uber gomock for the store seam; **in-process godog
suites** for the grammar and red-on-bad proofs, following
`internal/steps/steps_test.go:58,103` and its three siblings.

**Target Platform**: Go library (the framework) plus its `cmd/mentat` CLI consumer;
darwin/linux.

**Project Type**: Library with a CLI consumer-zero. No frontend, no service.

**Performance Goals**: N/A — no hot path is touched. The added work per step occurrence is one
map lookup and one type assertion.

**Constraints**:
- No breaking change to the published `Comparator` interface (FR-002, SC-005).
- The `stepDefs` drift invariant is preserved, never relaxed — drift-test assertions stay
  unmodified (FR-012, SC-003).
- Exactly one `stepDefs` row added (SC-002).
- 80% per-package coverage floor.
- No new e2e test, but `e2e/` must still compile: `go vet -tags e2e ./...` (SC-010).
- `examples/kafkaecho` must build untouched (SC-008).

**Scale/Scope**: ~5 source files in `internal/` plus the facade, 2 documentation files, and
one regenerated golden. Two golden lines added, exactly.

---

## Constitution Check

*GATE: passed before Phase 0; re-checked after Phase 1 design. No violations.*

| Principle | Assessment |
|---|---|
| **I. Evidence-Only Comparators** | **Upheld, and reinforced.** D2 makes `ParseExpectation(text string)` pure — no `context.Context`, no target, no `Evidence`. This is deliberate: a parser able to reach run context would be a *second* channel to run data alongside `Evidence`, and a comparator built on it would lose portability across agent and service SUTs. The narrow signature makes that impossible rather than discouraged. |
| **II. Trace Is a Forest** | **Not engaged.** No correlation, resolution or trace-shape code is touched. |
| **III. Seams Are Interfaces, Wired Once** | **Upheld.** The new seam is a small, consumer-defined interface. It introduces **no new registry and no new wiring path** — resolution goes through the existing per-engine `*Registry` via `Engine.Comparator(name)`, sealed at the existing composition root `engine.Build`. Registration is unchanged (`WithComparator`). No `wire`/`fx`. |
| **IV. No Silent Fallbacks** | **Upheld.** Three failure modes, three descriptive errors naming the offending value (D5, FR-007/008/009). No nil expectation, no zero-value success, no skipped assertion. Parse errors are `%w`-wrapped and never converted into a failing verdict — a parse failure is not an assertion failure. |
| **V. Test-First & Hermetic (NON-NEGOTIABLE)** | **Upheld.** TDD throughout, routed to **go-test-writer**. The L3 red obligation is satisfied *and improved*: [research R6](./research.md) moved it from `e2e/` (which drives a prebuilt `cmd/mentat` binary that structurally cannot contain a Go-registered comparator, behind a build tag `make ci` never compiles) to an in-process hermetic suite that actually gates. 80% floor enforced. |

**Post-design re-check**: no principle moved to "at risk" during Phase 1. Complexity Tracking
is omitted because there are no violations to justify.

---

## Project Structure

### Documentation (this feature)

```text
specs/011-comparator-gherkin-invocation/
├── spec.md                             # D1–D5, FR-001..016, SC-001..010
├── plan.md                             # This file
├── research.md                         # R1–R8 (Phase 0)
├── data-model.md                       # Phase 1
├── quickstart.md                       # Phase 1 — validation guide
├── contracts/
│   ├── expectation-parser-seam.md      # the interface, purity, optionality, nameability
│   └── step-grammar.md                 # the phrase, drift invariant, failure behaviour
├── checklists/
│   └── requirements.md
└── tasks.md                            # Phase 2 — /speckit-tasks, NOT created here
```

### Source Code (repository root)

```text
internal/core/
└── core.go                    # + ExpectationParser interface (beside Comparator, Expectation)

internal/engine/
└── engine.go                  # + Engine.Comparators() — 3 lines, mirrors Reporters()

internal/steps/
├── metadata.go                # + exactly ONE stepDefs row, new "Extend" group
├── steps.go                   # + comparatorSatisfiedByDoc handler
└── steps_test.go              # + green, red, parse-error and error-table tests (in-process godog)

mentat.go                      # + type ExpectationParser = core.ExpectationParser
surface_test.go                # falsification rehearsal recorded (alias removed → sweep fails)

specs/007-public-extension-api/contracts/
└── public-surface.golden      # + exactly 2 lines

docs/
├── steps.md                   # regenerated from stepDefs
└── extending/comparator.md    # + the seam, a worked comparator, the feature-file snippet

CHANGELOG.md                   # additive entries; NO breaking-change entry
```

**Structure Decision**: no new package, and no new import edge. `ExpectationParser` joins
`Comparator` and `Expectation` in `internal/core` because `internal/steps` consumes it and root
imports `internal/steps` — 010's D5 rule forbids declaring it at the facade
([research R8](./research.md)). Everything else lands in files that already own the
responsibility: grammar rows in `metadata.go`, handlers in `steps.go`, accessors in
`engine.go`.

---

## Phase 2 — execution order

Recorded because ordering carries real information here, exactly as it did in 010.

**5a — the seam and its gate, first.** `core.ExpectationParser`, the facade alias, the
regenerated golden, and the **recorded falsification rehearsal** (remove the alias → the sweep
fails naming the type and the reaching method → restore → green). This lands before any
handler exists, so the two-line golden diff is attributable to nothing else.

**5b — `Engine.Comparators()`** belongs with the error paths, *not* with the seam. It exists
only to serve FR-007's "list the registered names", so it lands when that error does.

**5c — the handler and the row, green path first** (US1), driven by an in-process godog suite
over a test comparator registered with the existing `engine.WithExtraComparator`.

**5d — the three failure modes** (US2), table-driven, one row per D5 mode. These are the paths
most likely to be left uncovered; uncovered rejection paths were a `BLOCK` finding twice during
010, so they get their own assertions rather than riding the package coverage total.

**5e — documentation and drift** (US3): regenerate `docs/steps.md`, extend
`docs/extending/comparator.md`, and confirm the drift tests pass **with their assertions
unmodified**.

**5f — the red proofs last** (US4), because they need 5c and 5d in place.

### Two invariants to check across the whole sequence

1. **The golden changes exactly once, in 5a, by exactly two lines.** If it moves again later,
   something reached the public surface that nobody decided to put there — stop and find out
   what.
2. **The drift tests never need editing.** Needing to touch `metadata_test.go` or
   `docs_test.go` means the design drifted toward Option B, and the plan is wrong rather than
   the tests. This is the operational test of D1's central claim.

### Routing

Behaviour change → **go-test-writer** owns the TDD loop for 5b–5f. 5a's alias and golden
regeneration are mechanical → **go-coder**. Pre-commit audit → **go-reviewer** in `gate` mode.

---

## Artifacts generated

| Phase | Artifact | Status |
|---|---|---|
| 0 | `research.md` — R1–R8, all `NEEDS CLARIFICATION` resolved | Complete |
| 1 | `data-model.md` | Complete |
| 1 | `contracts/expectation-parser-seam.md` | Complete |
| 1 | `contracts/step-grammar.md` | Complete |
| 1 | `quickstart.md` | Complete |
| 1 | Agent context (`CLAUDE.md` SPECKIT block) | Updated |
| 2 | `tasks.md` | **Not created here** — run `/speckit-tasks` |
