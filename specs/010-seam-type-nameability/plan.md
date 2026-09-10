# Implementation Plan: Seam-Type Nameability

**Feature**: `010-seam-type-nameability` | **Date**: 2026-09-09 |
**Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/010-seam-type-nameability/spec.md`

## Summary

Close stability boundary 4: four types are frozen on Mentat's public surface but cannot be
written from outside the module, which makes `Reporter` the one published seam an external
module cannot implement at all.

Three of the four (`ExtractPolicy`, `HTTPSpec`, `AggregateDetail`) are closed types and are
resolved by facade aliases. The fourth, `RunReport`, is resolved by **collapse**: the seam is
re-shaped to render `Results`, and since `Results` must carry everything `RunReport` carries
(D3), the two become one type — moved to a new leaf package `internal/result` and aliased on
the facade (D5). Reporters also move from a package-global map onto the sealed per-engine
registry, so a per-run `WithReporter` cannot reintroduce the reentrancy defect 007 closed
(D4).

Finally the nameability sweep itself widens to walk seam signatures, not just data
reachability — the root-cause fix that makes the defect class impossible rather than these
four instances absent.

**D5 supersedes the mechanism of D2/D3, not their substance.** The original design specified
facade-**declared** mirror structs; that is an import cycle, because internal packages must
consume the seam and root already imports them. Only *terminal* types can be facade-declared.
Collapsing to one aliased type fixes the cycle and, as a side effect, removes the feature's
largest risk: with no second struct there is nothing to keep in tag-and-field-order parity,
and the marshalled struct is the same struct relocated — so the emitted report bytes are
unchanged by construction. The format golden remains as a regression guard against a careless
relocation, and still lands first.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: `godog` (BDD), `go.uber.org/mock` (gomock), `encoding/json` +
`html/template` (reporters). No new dependency.

**Storage**: N/A — this feature touches no store. Hermetic throughout.

**Testing**: `go test`; table-driven by default; gomock for seam interfaces; golden files
under the `MENTAT_UPDATE_GOLDEN` convention; `-race` required for the concurrency proof.

**Target Platform**: Library + CLI, platform-agnostic.

**Project Type**: Single Go module — library with a facade package at the repository root
and a `cmd/mentat` CLI as consumer zero.

**Performance Goals**: N/A. No hot path is touched; reporting is post-run.

**Constraints**:
- Emitted report bytes MUST NOT change (FR-013) — the binding constraint on the whole
  feature.
- `examples/kafkaecho` MUST keep compiling untouched (SC-006).
- Every touched package stays at or above the 80% coverage floor.
- Public-surface changes carry the three acts: golden, changelog, migration note.

**Scale/Scope**: ~4 new facade declarations, 2 widened facade structs, 1 re-shaped seam,
3 reporters re-expressed, 1 registry seam relocated, 1 sweep definition widened. Six files
of docs to correct.

## Constitution Check

*GATE: evaluated before Phase 0 and re-evaluated after Phase 1 design.*

| Principle | Assessment |
|---|---|
| **I. Evidence-Only Comparators** | **Not engaged.** No comparator changes. `AggregateDetail` becomes nameable so a comparator author can populate `Verdict.Detail` — that is a return value, not a new input, and reaches past `Evidence` for nothing. |
| **II. Trace Is a Forest** | **Not engaged.** No correlation or trace-shape code is touched. |
| **III. Seams Are Interfaces, Wired Once** | **Strengthened.** Reporters are today the one seam wired *outside* `engine.Build`, in a package-global map. D4 brings them to the single composition root with the other five. This moves the codebase toward the principle, not away. |
| **IV. No Silent Fallbacks** | **Strengthened, with one obligation.** `RegisterReporter` currently overwrites silently (`registry.go:196-200`); the replacement fails loudly on collision. A `WithReports` entry naming an unregistered reporter must be a named error, never a skipped file. |
| **V. Test-First & Hermetic** | **Applies in full, and is load-bearing.** TDD throughout; FR-013 goes further than the default by requiring its golden green *before* the code it protects moves. Entirely hermetic — no Tempo, no network. L3 meta-test: see below. |

**L3 meta-test**: this feature adds no comparator and no new `Then` phrase, so it introduces
no new way for Mentat to render a verdict — the standing L3 suite covers the behaviour
surface unchanged. The falsification duty is discharged instead by the two deliberate-break
rehearsals this feature *does* own: the nameability gate must be shown to go red on an
unnameable seam parameter (SC-004), and the format golden red on a renamed tag and on a
field reorder (report-format-golden §Falsification). Both are recorded in their test files,
matching the mutation-rehearsal precedent set by 009 SC-001.

**Gate result: PASS.** No violations. Complexity Tracking is empty.

**Post-Phase-1 re-evaluation: PASS.** The design added no seam, no framework, no global,
and no dependency. It removes a global. Principle III's position improves; the rest are
unchanged or unengaged.

## Project Structure

### Documentation (this feature)

```text
specs/010-seam-type-nameability/
├── spec.md                             # decisions D1–D4
├── plan.md                             # this file
├── research.md                         # R1–R7, incl. 3 corrections to the spec
├── data-model.md                       # target field layouts
├── quickstart.md                       # 6 runnable validation checks
├── checklists/requirements.md          # spec-quality gate (all green)
├── contracts/
│   ├── facade-nameability-v2.md        # widened reachable set (supersedes 009's)
│   ├── reporter-seam.md                # the re-shaped seam + registration
│   └── report-format-golden.md         # byte-stability of emitted reports
└── tasks.md                            # NOT created by /speckit-plan
```

### Source Code (repository root)

```text
mentat.go                          # aliases: +ExtractPolicy, +HTTPSpec, +AggregateDetail,
                                   #   +RunRecord; Results/ScenarioResult/Reporter become
                                   #   aliases to internal/result; rationale comment rewritten
run.go                             # Results/ScenarioResult/RunRecord/ExitCode MOVE OUT to
                                   #   internal/result; toResults deleted; +WithReporter,
                                   #   +ReporterFactory; emission updated
internal/result/result.go          # NEW leaf pkg — Results (was core.RunReport),
                                   #   ScenarioResult (+RunIDs `json:"-"`), RunRecord, Reporter
surface_test.go                    # golden regeneration + widened nameability sweep
mentat_external_test.go            # facade-only witness: 6th seam + new literals
internal/core/core.go              # RunReport/ScenarioResult/RunRecord/Reporter removed
internal/core/mocks/mock_core.go   # regenerated — see the MockReporter risk below
internal/registry/registry.go      # reporters onto *Registry; package-global deleted
internal/engine/build.go           # reporter wiring at the composition root
internal/report/
├── collector.go, derive.go        # produce result.ScenarioResult / result.Results
├── ledger.go                      # Price/Budget take result types
├── json.go, html.go, junit.go     # parameter type renamed; field reads unchanged
├── register.go                    # per-engine registration
├── emit.go                        # EmitReports takes Results + the registry
├── format_golden_test.go          # NEW — FR-013, lands first
└── testdata/*.golden              # NEW — committed before anything moves
examples/kafkaecho/                # witness: must compile UNTOUCHED
docs/extending/stability.md        # boundary 4 deleted
docs/extending/new-seam.md         # reporter registration model updated
CHANGELOG                          # breaking-change entry + migration note
specs/007-.../contracts/public-surface.golden   # regenerated, reviewed by hand
```

**Structure Decision**: Single Go module. The facade package at the repository root is the
public surface and aliases everything; `internal/` holds every declaration; `cmd/mentat` is
consumer zero; `examples/kafkaecho` is the external-module witness in its own module via a
`replace` directive.

This feature adds **one package**, `internal/result`, and one testdata directory. The new
package exists because of a hard constraint rather than a preference: root imports
`internal/report`, `internal/engine` and `internal/registry`, so any type those packages
consume must be declared beneath them and aliased upward. Every other public type on the
facade already follows this shape. Putting the result types in `core` instead was rejected —
`core.ScenarioResult` already exists there and would collide with the public one.

## Phase 0 — Research

Complete. See [research.md](./research.md). Eight unknowns resolved (R1–R8), no open
questions.

**R8 is the one that mattered.** The design as originally planned was an import cycle:
`internal/report` must name `Results`, `internal/registry` must hold `Reporter`, and
`internal/engine` must take a `ReporterFactory` — while root imports all three
(`run.go:11-17`). Every public type on the facade is an alias for exactly this reason;
`Results` and `ScenarioResult` get away with being declared only because they are *terminal*
and nothing internal consumes them. A seam is not terminal. This invalidated the whole Phase
5 task list and forced D5. It was not caught by research, the data model, three contracts or
this plan's own risk table — it surfaced under adversarial review.

Three further findings **contradicted the initial model** and are folded back into the spec:

1. Instance-vs-factory is not what sets reporters apart — global-vs-per-engine is.
   `RegisterDriver`/`RegisterComparator` also take instances.
2. `public-surface.golden` is not the only golden; three exist, with an established
   normalization convention that the format golden should reuse.
3. Facade-*declared* structs render as one inline golden line; only *aliases* expand
   per-field. Added fields extend a line rather than adding lines.

A fourth finding shaped the data model: preserving report bytes constrains **field
declaration order**, not just tags, and `RunIDs` needs `json:"-"` so that adding `Runs`
does not introduce a new key.

## Phase 1 — Design & Contracts

Complete. [data-model.md](./data-model.md), three contracts, and
[quickstart.md](./quickstart.md).

Agent context (`CLAUDE.md`) updated to point at this plan.

## Phase 2 — Task sequencing (input to `/speckit-tasks`)

Execution order is **not** the spec's P1–P4 priority order. P1 is the most important story
and the most dependent one.

```text
┌─ 1. Report-format golden (FR-013)          ── blocks everything
│     internal/report/format_golden_test.go + testdata
│     green against TODAY's reporters, before anything moves
│
├─ 2. US2 + US3: three aliases (P2/P3)       ── independent, shippable alone
│     mentat.go aliases; external compile-test literals; golden lines
│     ExtractPolicy error-path coverage (capture-group validation)
│
├─ 3. US1: Reporter (P1)                     ── the large slice, needs 1
│     3a. Create internal/result; MOVE RunReport→Results, ScenarioResult,
│         RunRecord, Reporter out of core; verbatim, tags and order intact
│     3b. Update internal/report + registry to the moved types; re-shape the
│         seam to Report(res Results, w); rename parameter types in the reporters
│     3c. Facade: alias the four; delete toResults; ScenarioResult gains
│         RunIDs `json:"-"` derived from Runs
│     3d. Reporters onto *Registry; package-global deleted; WithReporter;
│         EmitReports takes the registry
│     3e. Concurrency proof under -race (SC-010)
│
└─ 4. US4: reachability gate (P4)            ── LAST; cannot be green before 3b
      sweep widened to seam signatures; failure names type + reaching position
      falsification rehearsal recorded in the test file
```

**3a is a pure move.** No field is added, removed, reordered or re-tagged in that step — the
format golden from step 1 must stay green across it, and that is exactly what it is for.
Widening (`RunIDs`) happens in 3c, after the move has been proven inert.

**Why 4 is last**: its acceptance scenario requires the widened check to report zero
unnameable types. While `Reporter.Report` still names `RunReport`, that is unreachable. The
root-cause fix can only land green after the instances it polices are gone.

**Why 1 is first**: a golden regenerated after the types move records the new bytes and proves
nothing. This is FR-013's ordering clause, and it is the difference between a real guard and a
rubber stamp. D5 reduced what it guards — the bytes are now unchanged by construction — but a
tag dropped or a field reordered during transcription is an ordinary mistake, and every other
check in the repo is blind to it.

Steps 2 and 3 are logically independent and could be parallelized, but both touch
`public-surface.golden`. Serialize them — a merge conflict in a hand-reviewed artifact costs
more than the overlap saves.

**Routing** (per the constitution's Development Workflow): steps 1, 2, 3a–3d and 4 are all
behaviour changes and go to **go-test-writer** under TDD. The documentation corrections
(FR-010, FR-015: `stability.md`, `new-seam.md`, `CHANGELOG`, the three stale comments) are
docs work for **go-coder**. **go-reviewer** in `gate` mode audits the staged diff before
commit — with particular attention to the surface golden diff and the tag/order parity the
format golden protects.

## Complexity Tracking

No constitutional violations. Table intentionally empty.

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| **Import cycle from facade-declared seam types** | **Materialized.** The original D2/D3 design was undeliverable | **Resolved by D5** — types moved to `internal/result` and aliased. Recorded as a spec edge case ("a seam cannot be a facade-declared type") so the next seam does not repeat it |
| Report JSON changes during the move | Medium — D5 makes the bytes identical *by construction*, but a tag dropped or a field reordered in transcription is an ordinary mistake, invisible to every other check | FR-013 golden landed first; step 3a is a **pure move** with the golden green across it; falsification rehearsal on a tag rename *and* a field reorder |
| `MockReporter` disappears rather than regenerating | **High** — the only `//go:generate` is `core.go:3` (`-source=core.go`), and D5 removes `Reporter` from that file, so regeneration *deletes* the mock while `registry_test.go:265` still uses it | Decide the mock's new home explicitly: a `//go:generate` directive on `internal/result`, or drop the mock in favour of a value stub. Must be an explicit task, not a side effect of `go generate` |
| Field reorder on the public `Results`/`ScenarioResult` breaks an unkeyed composite literal | Low — this repo uses keyed literals throughout | Migration note states both the reorder and the additive fields; `make ci`'s `example` target is the external witness, and must be run at the Phase 5 checkpoint, not only at the end |
| Deleting the package-global reporter map breaks a caller | Low — the call sites are enumerable | Verified at `f2afdda`: production callers are `internal/report/register.go:7-9` and `internal/report/emit.go:36` only; two tests register directly (`registry/registry_test.go:265`, `report/lifecycle_test.go:181`) and move with the seam |
| Widening the surface is a one-way door | Certain, and accepted | It is the deliberate act D1–D5 record. D5 makes the exposure *direct* rather than mirrored, so each newly public type needs its own SC-007 justification written rather than inherited |
| `RunReport` removal breaks an external reporter | None known — no external module can currently implement `Reporter`, which is the defect | Note it in the migration entry anyway |
