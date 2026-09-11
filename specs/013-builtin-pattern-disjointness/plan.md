# Implementation Plan: Built-in Step-Pattern Disjointness

**Branch**: `013-builtin-pattern-disjointness` | **Date**: 2026-09-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/013-builtin-pattern-disjointness/spec.md`

## Summary

Two independent guarantees, both mandatory, plus the verification that keeps the second honest.

1. **No code path resolves among multiple matching step patterns by position** (US1). `matchBuiltin`
   and `matchPhrase` each return their first match as though it were their only one; they collapse
   into one count-based deferral covering every source combination.
2. **Built-in disjointness becomes decided rather than sampled** (US2). A lazy product automaton
   over `regexp/syntax` decides emptiness-of-intersection for a pattern pair, gating the built-in
   set hard and reporting contributed overlap as a finding.
3. **The decider is falsifiable** (US3). Its negative verdicts are only as good as its code — a
   claim this feature exists to stop making — so a known-verdict corpus, differential sampling, and
   mutation rehearsals stand behind it. R3 in `research.md` is why this is not optional: the
   prototype decided 780 pairs correctly and was wrong about case folding.
4. **Records state their basis** (US4), last because it can only describe the others' outcome.

Approach comes from `research.md`: stdlib `regexp/syntax` product automaton (R1), placed in
`internal/steps` (R5), with the Validate-time check positioned where it structurally cannot reach
the run path (R6).

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: **Standard library only** for this feature — `regexp/syntax`, `unicode`,
`regexp`. Pre-existing and untouched: `cucumber/godog v0.15.1` (pinned; R1/R10–R15 from 012 are
properties of that version), `cucumber/messages/go/v21`, `go.uber.org/mock`.

**Storage**: N/A — the decider is pure; no store, driver, or transport is involved.

**Testing**: `go test` with table-driven tests; `t.Parallel()` where no mutable state is shared;
uber gomock **not needed** (no interface is introduced — see Constitution Check III); `godog` for
the BDD layer; the `//go:build e2e` lane for the stdout goldens R8 says will churn.

**Target Platform**: Go library plus the `mentat` / `mentatctl` CLIs; Linux and macOS.

**Project Type**: Library + CLI (single Go module, `internal/` layered).

**Performance Goals**: The gate decides all pairs over the built-in set — 780 at 40 patterns —
within the existing `make ci` budget. Prototype baseline: **0.36s including compilation**, max 74
product states per pair. Validate-time cost is bounded by deciding only pairs touching at least one
contributed pattern: `k(k-1)/2 + 40k` for *k* contributed phrases (210 at k=5).

**Constraints**:
- No new module dependency (FR-008 / SC-006).
- No `panic` on author input — contributed patterns reach the decider, so an unmodelled construct
  is a descriptive error, never a crash and never a quiet "disjoint" (FR-012).
- The single-match path and the run path stay byte-identical (FR-003 / FR-017, SC-005 / SC-011).

**Scale/Scope**: 40 built-in patterns; an unbounded but small number of contributed phrases per
engine. 17 functional requirements, 11 success criteria, 4 user stories.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design — result at the bottom.*

| Principle | Verdict | Basis |
|---|---|---|
| **I. Evidence-Only Comparators** | **PASS (not engaged)** | No comparator is added or changed. The decider consumes pattern *strings*; it never sees `Evidence`, a `TraceStore`, or a `Driver`. |
| **II. Trace Is a Forest** | **PASS (not engaged)** | No trace, correlation, or span code is touched. |
| **III. Seams Are Interfaces, Wired Once** | **PASS** | No new seam. The decider is a pure function, not an injected dependency — it has one implementation and no second one in a committed plan, so `/composition`'s second-implementation test says a function, not an interface. No package-level mutable state: the decider holds none, and `StepPatterns` is already a value derived from an engine rather than a cache (007's fix, which 012's `sync.Once` removal reinforced). |
| **IV. No Silent Fallbacks** | **PASS — and this is the feature** | FR-001/FR-002 remove positional resolution; FR-012 forbids reporting "disjoint" for a pattern the decider cannot model; every decider error wraps with `%w` and names the pattern and construct. The one place the feature *declines* to hard-error — contributed overlap at composition (D5/FR-014) — is not a silent fallback: it is reported as a finding with a witness, and the genuinely-failing case still fails loudly at run time through godog's `Strict`. |
| **V. Test-First & Hermetic (NON-NEGOTIABLE)** | **PASS** | All work is behaviour change → **go-test-writer** owns the TDD loop (red → green → refactor, one test at a time). Every test is hermetic: the decider is pure regex work, no network, no Tempo. Coverage floor 80% per touched package (FR-009). The mutation rehearsals required by FR-007 / SC-002 / SC-008 are the L3-equivalent proof that the guards go red — and R3 is a worked example of one finding a real defect. |

**Gate result: PASS, no violations.** *Complexity Tracking* is therefore empty.

One standing rule this feature must not break: **only terminal types may be facade-declared**
(010 D5). The decider is `internal/`-only and publishes nothing on the facade, so the
public-surface golden should not change. If it does, that is a signal to stop, not a golden to
update.

## Project Structure

### Documentation (this feature)

```text
specs/013-builtin-pattern-disjointness/
├── plan.md              # This file
├── baseline.txt         # Setup output (T001–T003); SC-005/SC-006 diff against it
├── spec.md              # Feature specification (amended by /speckit-clarify and by R3)
├── research.md          # Phase 0 output — R1..R8
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   ├── decider.md              # The intersection decider's contract
│   └── validate-surface.md     # Delta to 012's validate surface: the pattern-overlap class
├── checklists/
│   └── requirements.md  # Spec quality checklist (re-validated after clarification)
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
internal/steps/
├── disjoint.go             # NEW — the intersection decider (R5: no new package, single consumer)
├── disjoint_test.go        # NEW — known-verdict corpus, differential sampling, fuzz target (R7)
├── metadata.go             # stepDefs table; unchanged by this feature
├── metadata_test.go        # TestBuiltinStepPatternsArePairwiseDisjoint stays (FR-005), reframed
│                           #   as an independent cross-check; NEW pairwise decider gate (FR-013)
├── stepargs.go             # matchBuiltin/matchPhrase → one count-based deferral (US1, FR-001/002)
├── stepargs_test.go        # both source combinations + the two mutation rehearsals (SC-002)
├── phrase.go               # stepPatternsFor gains labelled patterns for the Validate-time check
└── precheck.go             # Finding class list gains `pattern-overlap` (R8)

run.go                      # mentat.Validate folds in pattern-overlap findings after
                            #   EngineStepChecks (R6 — structurally cannot reach the run path)

docs/extending/
├── phrases.md              # authoring guide: what overlap now reports (US4)
└── stability.md            # if any boundary statement changes (expected: none)

specs/012-comparator-gherkin-phrases/
└── contracts/validate-surface.md   # superseded pointer to 013's contract (US4, T048)

CHANGELOG.md                # the disjointness claim restated as decided (US4, FR-004)
CLAUDE.md                   # feature history + the disjointness claim (US4, FR-004)
```

**Structure Decision**: Everything lands in the existing `internal/steps` package plus one call
site in `run.go`. No new package, no new module, no new seam. `research.md` R5 records why a leaf
package for the decider was considered and declined (one consumer, and `/composition` requires a
second that exists now), and R6 records why the `run.go` call site is load-bearing rather than
incidental: the pattern half of the check set has no scenario-init counterpart, so FR-017's
"Validate-only" guarantee comes from the location and not from anyone remembering it.

The one file outside `internal/` is `run.go`, and it gains a call, not logic.

## Complexity Tracking

> No Constitution Check violations. This section is intentionally empty.

## Phase sequencing

Priority order is `US1 → US2 → US3 → US4`, and the dependencies are real rather than
organisational:

- **US1** is independent — it can land and ship alone, and closes the Constitution IV exposure.
- **US2** depends on nothing in US1, but shares its test fixtures (a deliberately overlapping
  pattern pair).
- **US3** depends on US2 existing, because it verifies it. It must not be deferred past US2 in the
  same branch: R3 is a defect US3's method found in US2's prototype, and shipping US2 unverified
  would reproduce this feature's own failure mode.
- **US4** depends on US1 and US2 having landed, because it documents their outcome. Writing it
  earlier produces a third round of wording corrections to a claim whose status is still moving.

Two cross-cutting items to schedule, both from `research.md`:

1. **Run the e2e lane** (`go test -tags e2e`, harness up). R8: the new finding class touches
   documented surface and the stdout goldens are `//go:build e2e`, invisible to `make ci`. This
   repo has been bitten by that gap before.
2. **No rebase needed.** An earlier draft of this item required one, on the belief that the branch
   carried 012's unsquashed history against a squashed `main`. This branch is cut directly from
   `f6bb402` (the 012 squash), so its history is already clean and the item is struck. Kept as a
   struck item rather than deleted, because "rebase first" appears in the spec's Assumptions
   history too and a reader meeting it there deserves to find out here that it was discharged.

## Constitution Check — re-evaluated after Phase 1 design

**Still PASS, no violations, Complexity Tracking still empty.** Three points the design changed or
sharpened:

- **III (seams / no package state)** — reconfirmed after R5. The design adds a pure function in an
  existing package, no interface, no registry entry, no `init()`, no package-level variable. The
  alternative considered — a leaf package for the decider — was declined precisely *because*
  wrapping a single-consumer function in package ceremony is the over-abstraction half of
  `/composition`'s warning, not the under-abstraction half.
- **IV (no silent fallbacks)** — the design found a **new** instance during Phase 0 rather than
  merely honouring the principle. R3: the prototype reported `^(?i)abc$` and `^abc$` as disjoint
  because `syntax` leaves `FoldCase` in `Inst.Arg` for single-rune instructions instead of
  materializing it into ranges. A decider that answers "disjoint" for a construct it misreads is
  the exact silent fallback this feature exists to remove, and it was inside the removal mechanism.
  Fixed and pinned; `data-model.md` §3 adds "refuse any unrecognised `EmptyOp` **by default**" so
  the next assertion Go adds is refused rather than silently joining the modelled set.
- **V (test-first, hermetic, 80%)** — unchanged and reinforced. Every artifact this feature adds is
  testable without network or Tempo. R7 changed *how* the differential check is gated: the
  deterministic table is the gate and the fuzz target is the extension, because `go test` runs a
  fuzz target's seed corpus only. A differential check that depended on fuzzing having run would
  be green in CI while never having sampled anything.

  **On the L3 meta-test: not engaged, rather than satisfied by equivalence.** An earlier draft of
  this section called the mutation rehearsals "the L3-equivalent proof". That overstates it. 013
  adds no Gherkin behaviour, so the L3 mandate (drive bad scenarios, assert Mentat goes RED) has
  nothing new to drive — and the run-path half is *already* covered by
  `TestGenuinelyOverlappingPhrasesFailLoudly` (`custom_phrase_isolation_test.go:302`, 012's
  T038/SC-011). Saying "not engaged, and here is the existing test that covers the adjacent
  behaviour" is stronger and more checkable than claiming an equivalence.

One thing the design deliberately leaves asymmetric, recorded so review does not read it as an
oversight: built-in overlap fails the build, contributed overlap is reported (D5, FR-014). The
justification is ownership, and it is argued in `contracts/decider.md`. This is not a Constitution
IV deviation — the case that genuinely fails still fails loudly at run time, and the reported case
is a *potential* failure that has not happened.
