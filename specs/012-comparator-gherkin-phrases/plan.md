# Implementation Plan: Comparator-Contributed Gherkin Phrases

**Branch**: `012-comparator-gherkin-phrases` (not yet created; work started from `main` at `0f9dcea`) | **Date**: 2026-09-10 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/012-comparator-gherkin-phrases/spec.md`

## Summary

Let a registered comparator contribute the Gherkin sentence that invokes it, so a feature file
reads in the comparator's own domain language instead of naming a registry key and handing it a
payload. The work is not "add an interface": it is making step registration **per-engine and
runtime-resolved**, then re-establishing the guarantees of all **five** `stepDefs` consumers over
a set no longer knowable at compile time.

Research settled the two risks empirically ([research.md](./research.md)). Enabling godog's
`Strict` mode churns **zero** goldens across both the non-e2e and the live-Tempo e2e surfaces
(R0, measured). And godog does **not** report ambiguity without `Strict` (R1, measured): it
silently runs the first-registered match and the scenario **passes**. Built-ins register before
any contributed phrase would, so a colliding contributed phrase would be silently shadowed and
its assertion never run — a green verdict nobody wrote. Turning `Strict` on routes the ambiguity
through the After hook Mentat already trusts, needing no new mechanism.

**The defect is latent, not live** (R10, measured at implementation time — this corrects R1's
first draft, which plan.md previously repeated). The 40 built-in patterns are **pairwise
disjoint** across 1530 generated sentences covering every alternation branch, and registration is
single-pathed, so nothing can reach godog's ambiguous branch through the public surface at
`0f9dcea`. Contributed phrases are what make it reachable. `Strict: true` is therefore a
**prerequisite of this feature**, sequenced first so the flag change is reviewable on its own —
not an independent bugfix 012 happens to carry, and not something revertable once 012 ships.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: `github.com/cucumber/godog v0.15.1` (**pinned — R0 and R1 are
properties of this version; a bump re-opens both**), `github.com/cucumber/messages/go/v21`,
`go.uber.org/mock` (gomock), `stdlib reflect` (handler synthesis, R4)

**Storage**: N/A — no persistence added. Contributed phrases are engine-scoped values.

**Testing**: `go test` table-driven + uber gomock; hermetic by default; live-Tempo behind
`//go:build e2e` (harness via `make harness-up`); L3 meta-test mandatory

**Target Platform**: Library + CLI, developer machines and CI (Linux/macOS)

**Project Type**: Go library with a thin CLI (`cmd/mentat`) that is consumer-zero over the
public facade

**Performance Goals**: No runtime budget change. Phrase resolution is per engine build; pattern
compilation is ~40 regexes plus contributed ones (R6), trivial versus a single SUT drive.

**Constraints**:
- Zero cost when unused — a build with no contributed phrases must be byte-identical to
  `0f9dcea` on every golden (SC-009)
- No package-level mutable step state may survive (FR-010, D3)
- The `stepDefs` drift gate must not weaken into an unchecked superset (FR-005, D4)
- godog accepts no `[]string`/variadic handler and silently drops surplus captures (R4)

**Scale/Scope**: ~5 packages touched (`internal/core`, `internal/steps`, `internal/engine`,
`cmd/mentat`, facade root), 40 built-in step rows preserved, 23 FRs, 13 SCs.

## Constitution Check

*GATE: evaluated before Phase 0 and re-evaluated after Phase 1 design.*

| Principle | Assessment | Verdict |
|---|---|---|
| **I. Evidence-Only Comparators** | The phrase seam carries authoring text (regex captures) into a typed expectation. It grants **no** access to a store, driver or transport — D6 deliberately keeps the parse pure, inheriting 011's D2 reasoning that a parser able to reach run context would be a second, weaker channel beside `Evidence`. | ✅ PASS |
| **II. Trace Is a Forest** | Untouched. No correlation or trace-shape code in scope. | ✅ PASS (n/a) |
| **III. Seams Are Interfaces, Wired Once** | The phrase contributor is a small consumer-defined interface in `internal/core` (R2), discovered by type assertion at the existing composition root. **No new registry** — R3 reuses `Engine.Comparators()`/`Comparator(name)`. No `wire`/`fx`. | ✅ PASS |
| **IV. No Silent Fallbacks** | The feature's centre of gravity. It closes a silent fallback (R1's first-wins shadowing) *before* this feature makes it reachable (R10), rather than adding one. Every rejection path names the contributor and the offending value (FR-006/007/007a/008). Explicitly refused: nil-guarding `w.eng` in `registerSteps` (R5) — the parameter is passed in instead. | ✅ PASS |
| **V. Test-First & Hermetic** | TDD via go-test-writer; unit tests hermetic; L3 meta-test mandatory (FR-016). SC-011 is a **regression** test observed failing against the pre-change (non-strict) configuration before the fix — at the step-registration level, since R10 shows the collision is not constructible through the public surface at `0f9dcea`. Mutation rehearsals recorded in the test files per SC-003/SC-004. | ✅ PASS |

**Gate result: PASS, no violations.** Complexity Tracking is therefore omitted.

One judgement call worth stating rather than burying: **D9 introduces `reflect.MakeFunc` into the
registration path.** Reflection is not itself a constitution concern, and it is confined to a
single bridge function that no other code sees. It is load-bearing rather than clever — R4 shows
godog physically cannot accept a `[]string` or variadic handler, and deriving arity from the same
regex godog matches against is what makes surplus-capture loss impossible instead of merely
unlikely.

## Project Structure

### Documentation (this feature)

```text
specs/012-comparator-gherkin-phrases/
├── spec.md              # Feature spec (D1–D9, 23 FRs, 13 SCs)
├── plan.md              # This file
├── research.md          # Phase 0 — R0–R9, two measured experiments
├── data-model.md        # Phase 1 — entities, validation rules, resolution order
├── quickstart.md        # Phase 1 — runnable validation guide
├── checklists/
│   └── requirements.md  # Spec quality checklist (all passing)
├── contracts/
│   ├── phrase-seam.md         # The contributed-phrase + capture-parser seams
│   ├── step-registration.md   # Per-engine registration, partition invariant, collisions
│   └── validate-surface.md    # D7 library validate entry point + binary's documented limit
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
mentat.go, run.go, surface_test.go     # facade: aliases, Option(s), validate entry point (D7),
                                       #   Strict:true (D8/R0), nameability sweep pays for SC-008

internal/core/
└── core.go                            # NEW: phrase-contributor + capture-parser seams (R2).
                                       #   ExpectationParser (:130) untouched — D6

internal/engine/
└── engine.go                          # NEW accessor mirroring Comparators() (:207) — R3

internal/steps/
├── metadata.go                        # stepDefs (:82) unchanged; registerSteps (:73) gains a
│                                      #   phrases parameter — R5
├── metadata_test.go                   # drift gate becomes a partition assertion — FR-005/R5
├── docs_test.go                       # contiguity extended to the engine-scoped path — FR-014
├── steps.go                           # registration call site (:100); 011's
│                                      #   comparatorSatisfiedByDoc (:615) untouched — D1
├── precheck.go                        # DELETE sync.Once cache (:76-91); StepBindingFindings
│                                      #   takes a pattern set — FR-009/FR-010/R6
└── phrase.go                          # NEW: pattern validation, handler synthesis (R4/R8)

cmd/mentat/
├── steps_cmd.go                       # built-ins render byte-identically; generated page gains
│                                      #   the engine-scoped sentence — FR-013/R9
└── validate.go                        # checker (:117) updated for R6's signature; documents
                                       #   what a binary cannot see — FR-011a

docs/
├── steps.md                           # regenerated; built-in rows byte-identical
└── extending/                         # contributed-phrase authoring path — FR-017
```

**Structure Decision**: No new package. The feature lands in the existing layering — seams in
`internal/core` (the only placement legal under 010's D5 that avoids a cycle, R2), resolution in
`internal/engine`, registration and validation in `internal/steps`, rendering in `cmd/mentat`,
publication on the facade. One new file (`internal/steps/phrase.go`) isolates pattern validation
and the `reflect.MakeFunc` bridge so nothing else sees godog's signature rules.

## Phase 0 — Research

**Status: complete.** See [research.md](./research.md). R0–R9 resolved, no `NEEDS
CLARIFICATION` remaining. Two items (R0, R1) were settled by experiment; R1 **refuted** an
assumption the spec inherited from 011's D1 and is recorded as a live defect.

## Phase 1 — Design & Contracts

**Status: complete.** [data-model.md](./data-model.md), [contracts/](./contracts/),
[quickstart.md](./quickstart.md).

**Post-design constitution re-check: PASS.** The design added no seam beyond the two in
`internal/core`, no registry, and no package-level state — it removes one (R6). Principle IV
strengthened on net: the design deletes a silent fallback that exists today (R1) and adds four
loud rejection paths.

## Implementation sequencing (informs `/speckit-tasks`, not a substitute for it)

Ordered so the two risk-carrying items land first and the seam work builds on settled ground.

1. **`Strict: true` + the ambiguity regression test.** Independent of everything else, and R0
   already shows the goldens hold. SC-011's test must be seen failing against the pre-change
   (non-strict) configuration first. Closes the silent first-wins branch **before** step 4 makes
   it reachable (R10) — the ordering is the point, not an accident of convenience.
2. **Delete the `sync.Once` cache; thread the pattern set through** (R6). Pure refactor of
   existing behaviour, unlocks per-engine anything.
3. **`registerSteps` gains its phrases parameter; drift gate becomes a partition** (R5, FR-005).
   Still zero contributed phrases in existence — the partition is provable with an empty set.
4. **The two seams in `internal/core` + the engine accessor** (R2, R3), then pattern validation
   and the `reflect.MakeFunc` bridge (R4, R8).
5. **Rendering and validation surfaces** (R9, R7) — the engine-scoped reference and D7's entry
   point.
6. **L3 meta-test, docs, taxonomy reconciliation** (FR-016, FR-017).

Steps 1–3 are behaviour-preserving for every existing user and each is independently
verifiable, which keeps the risky flag change away from the new-seam work.

## Complexity Tracking

Not applicable — the Constitution Check passed with no violations, before and after design.
