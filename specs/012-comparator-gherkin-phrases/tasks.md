---

description: "Task list for 012-comparator-gherkin-phrases"
---

# Tasks: Comparator-Contributed Gherkin Phrases

**Input**: Design documents from `/specs/012-comparator-gherkin-phrases/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: This project's constitution mandates Test-First / TDD as NON-NEGOTIABLE (Principle V).
Every test task below MUST be written and observed FAILING before its implementation task. Two
tasks (T004, T038) additionally require the *pre-change* behaviour to be recorded, because they
are regression tests for a **latent defect** (R10) rather than new-feature tests.

**Organization**: Tasks are grouped by user story. US1 and US2 are both P1 — US2 is not a
lesser story, it is the property that decides whether the feature is sound (see spec).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: US1–US4, on user-story phases only
- Exact file paths are given in every task

## Path Conventions

Go module `github.com/thetonymaster/mentat`. Facade at repo root (`mentat.go`, `run.go`);
internals under `internal/`; CLI under `cmd/mentat/`; live-Tempo tests under `e2e/` behind
`//go:build e2e`.

## Routing (constitution: Development Workflow)

Behaviour changes → **go-test-writer** (owns red→green). Scaffolding, docs, regeneration,
behaviour-preserving refactors → **go-coder**. Pre-commit audit → **go-reviewer** (`gate`).
Each task names its agent.

---

## Phase 1: Setup

**Purpose**: Branch, and capture the baseline evidence SC-009 and SC-012 are measured against.

- [X] T001 Create branch `012-comparator-gherkin-phrases` from `main` at `0f9dcea` (go-coder)
- [X] T002 [P] Capture baseline evidence into `specs/012-comparator-gherkin-phrases/baseline.txt`: `go test ./...`, `go test -tags e2e -timeout 25m ./e2e/` (needs `make harness-up`), and per-package coverage from `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`. This is the before-half of SC-009/SC-012 (go-coder)
- [X] T003 [P] Confirm `godog v0.15.1` is still the pinned version in `go.mod`; if it has moved, STOP — research R0/R1 are properties of that version and must be re-established before any other task (go-coder)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Close the latent collision branch, remove package-level step state, and make
registration per-engine. Every user story depends on this phase.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

**Note on sequencing**: T004–T007 close godog's silent first-wins branch **before** Phase 3 makes
it reachable. They are behaviour-preserving for every existing user (R0 measured zero golden
churn) and are deliberately kept separate — their own commit — so the risky flag change is
reviewable apart from the new-seam work.

**Corrected 2026-09-11 (R10)**: an earlier draft called this "a defect that exists at `0f9dcea`
independently of this feature". That was wrong. The 40 built-in patterns are **pairwise
disjoint** (measured: 1530 generated sentences covering every alternation branch, 0 overlaps) and
registration is single-pathed, so the ambiguous branch is **not reachable** through the public
surface today. It is latent; contributed phrases make it live. Two consequences: T004 is a
step-registration-level test rather than an end-to-end one (T038 is the end-to-end counterpart,
after phrases exist), and this work is a prerequisite of 012, not a separable bugfix.

### The latent collision defect (R1, R10) — regression test first

- [X] T004 Write the FAILING regression test in `internal/steps/ambiguity_test.go`: build a godog suite with the same options `mentat.Run` uses (`run.go:411-419`), register two colliding patterns (a broad one first, a specific one second, mirroring built-ins-before-contributed order), and assert the scenario is reported FAILED naming both expressions. Record in the test file the measured pre-change behaviour — PASSED with a nil `stepErr`, the broad pattern run and the specific one never executed. **Not an end-to-end test**: per R10 the collision cannot be constructed through the public surface at `0f9dcea` (go-test-writer, red)
- [X] T005 Add `Strict: true` to the godog options in `run.go:411-419`; confirm T004 goes green (go-test-writer, green)
- [X] T006 [P] Verify SC-012 on the non-e2e surface: `go test ./...` including the hermetic stdout golden `mentat_golden_test.go`; diff against T002's baseline (go-coder)
- [X] T007 Verify SC-012 on the e2e surface — **`make ci` does not compile this lane, so it is not evidence**: `make harness-up && go test -tags e2e -timeout 25m ./e2e/`, including the SC-005 stdout golden `e2e/golden_test.go`. Record the before/after in `specs/012-comparator-gherkin-phrases/baseline.txt` (go-coder)

### Remove package-level step state (R6, FR-010)

- [X] T008 Write FAILING tests in `internal/steps/precheck_test.go` proving two different pattern sets yield different `unbound-step` findings **in both evaluation orders** — the assertion the `sync.Once` cache cannot satisfy (go-test-writer, red)
- [X] T009 Delete `stepPatternsOnce` and `stepPatterns` (`internal/steps/precheck.go:76-91`) and re-shape `StepBindingFindings` (`precheck.go:95`) to take a compiled pattern set instead of reading package state; confirm T008 green (go-test-writer, green)
- [X] T010 Update the `StepBindingFindings` call site for the new signature. **Corrected 2026-09-11**: there is exactly **one** non-test caller, `cmd/mentat/validate.go:185` — not two. The scenario-init path (`steps.go:117-122`) runs `CELFindings` and `precheckShapePatterns` and never calls `StepBindingFindings`; godog reports an undefined step at runtime instead. This is also why the `sync.Once` has never bitten: its only consumer is a single-shot CLI process. It becomes a live hazard when D7's library validate entry point (T054) can be called repeatedly in-process against different engines (go-coder)

### Per-engine registration (R5, FR-002, FR-005)

- [X] T011 Write the FAILING partition test in `internal/steps/metadata_test.go`: drive `registerSteps` with the spy and a known non-empty phrase set, and assert every registered pattern is **either** a `stepDefs` row **or** a member of that set, with counts adding up. Keep the existing bidirectional equality for the built-in half (go-test-writer, red)
- [X] T012 Change `registerSteps` (`internal/steps/metadata.go:73`) to `registerSteps(reg stepRegistrar, w *world, phrases []ContributedPhrase)` and update the call site at `internal/steps/steps.go:100`. **Do not** reach through `w.eng` — the drift test passes a zero `world` whose `eng` is nil (`metadata_test.go:40`), and nil-guarding it would be the silent fallback Constitution IV forbids (go-test-writer, green)
- [X] T013 Confirm `TestNoDirectStepRegistration` (`metadata_test.go:129`) and `TestStepMetadataFieldsPresent` (`metadata_test.go:95`) still pass unchanged — registration must stay single-pathed (go-coder)

### The seams and the engine accessor (R2, R3)

- [X] T014 [P] Write FAILING tests in `internal/core/core_test.go` for the two new seams: a comparator implementing the phrase-contributor seam returns its phrases; one implementing the capture-parser seam returns a typed expectation. Use gomock where call verification matters (go-test-writer, red)
- [X] T015 Declare the contributed-phrase seam, the capture-parser seam, and the `ContributedPhrase` struct in `internal/core/core.go`, next to `ExpectationParser` (`:114-132`). **`ExpectationParser` itself must not change** — D6. See [contracts/phrase-seam.md](./contracts/phrase-seam.md) (go-test-writer, green)
- [X] T016 Add the facade aliases in `mentat.go` for both seams and `ContributedPhrase`. 010's D5 forbids declaring them at the facade; they must be aliased from `internal/core` (go-coder)
- [X] T017 Write a FAILING test in `internal/engine/engine_test.go` asserting the new accessor returns contributed phrases resolved from registered comparators, in **sorted comparator-name order** (go-test-writer, red)
- [X] T018 Add the accessor to `internal/engine/engine.go`, mirroring `Comparators()` (`:207`) — enumerate via the sorted `Registry.Comparators()` (`registry.go:125-137`), resolve each with `Comparator(name)`, type-assert for the phrase seam. **No new registry** (go-test-writer, green)

**Checkpoint**: The live defect is fixed, no package-level step state remains, registration is
per-engine, and the seams exist. User stories can now begin.

---

## Phase 3: User Story 1 - Write a scenario in the comparator's own language (Priority: P1) 🎯 MVP

**Goal**: A comparator contributes its own Gherkin sentence; a feature file written in that
sentence runs and the comparator receives its own typed expectation.

**Independent Test**: Register one comparator contributing one phrase, run a feature file
written only in that phrase, assert the verdict equals the 011-style generic step's verdict for
the same expectation.

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

- [ ] T019 [P] [US1] Write FAILING table-driven tests in `internal/steps/phrase_test.go` for the godog handler bridge across capture counts 0, 1, and 3 — asserting every capture reaches the parser and **none is discarded** (SC-013; godog silently drops surplus args, `internal/models/stepdef.go:58`) (go-test-writer, red)
- [ ] T020 [P] [US1] Write FAILING tests in `internal/steps/phrase_test.go` for seam routing per [data-model.md](./data-model.md) §3: captures-only → capture-parser; docstring-only, no captures → 011's `ExpectationParser`; captures + docstring → capture-parser with the docstring appended (go-test-writer, red)
- [ ] T021 [P] [US1] Write FAILING error-path tests in `internal/steps/phrase_test.go`: parse returns an error (wrapped `%w`, names the comparator); parse returns a nil expectation with no error (**refused**, mirroring `steps.go:615-650`); comparator contributes a phrase but implements no capture parser (hard error at build) (go-test-writer, red)
- [ ] T022 [US1] Write the FAILING equivalence test in `custom_phrase_facade_test.go` (repo root, alongside `custom_comparator_facade_test.go`): a contributed phrase and the equivalent 011 generic step produce identical pass/fail **and reason text** for the same expectation — SC-001 (go-test-writer, red)

### Implementation for User Story 1

- [ ] T023 [US1] Create `internal/steps/phrase.go` with the `reflect.MakeFunc` bridge: `reflect.FuncOf(N × string, error)` with N from the compiled pattern's `NumSubexp()`, plus `*messages.PickleDocString` appended for docstring-carrying phrases. Confirm T019 green. Keep godog's signature rules contained to this file (D9, R4) (go-test-writer, green)
- [ ] T024 [US1] Implement seam routing and expectation construction in `internal/steps/phrase.go`; confirm T020 green (go-test-writer, green)
- [ ] T025 [US1] Implement the error paths in `internal/steps/phrase.go`; confirm T021 green. Every message names the comparator and the offending value (go-test-writer, green)
- [ ] T026 [US1] Wire resolved phrases into the registration closure at `internal/steps/steps.go:100`, passing them to `registerSteps` (T012's parameter). Route the step through `checkSensitive` — 011's D3 stands, a custom comparator's verdict is completeness-sensitive (go-test-writer, green)
- [ ] T027 [US1] Confirm T022 green; confirm 011's generic `Extend` row (`metadata.go:367`) and `comparatorSatisfiedByDoc` (`steps.go:615`) are **untouched** and their tests unchanged — the operational test of D1 (go-test-writer, green)
- [ ] T028 [US1] Write and pass the L3 meta-test (FR-016, SC-007, Constitution V): a contributed phrase whose assertion is **false** makes the run RED with the comparator's own reason. Place it in `internal/steps` as an in-process godog suite — **not** in `e2e/`, which drives a prebuilt binary that cannot contain a consumer-registered comparator (011's R6) (go-test-writer, red→green)
- [ ] T029 [US1] Record the mutation rehearsals for T019–T021 in the test files: state **what was mutated**, not merely that red was observed. 011 hit a rehearsal that failed to go red because the mutation had not applied, and "the mutation didn't fire" is indistinguishable from "the guard is real" from output alone (go-test-writer)

**Checkpoint**: US1 fully functional — a comparator's own sentence drives it, proven equivalent
to the 011 path and proven to go RED on a false claim.

---

## Phase 4: User Story 2 - Two engines in one process stay isolated (Priority: P1)

**Goal**: Contributed phrases are scoped to the engine that resolved them; two engines in one
process cannot observe each other's phrases.

**Independent Test**: Build two engines with disjoint phrase sets, run both in one process in
**both orders**, assert each binds only its own.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [ ] T030 [P] [US2] Write the FAILING isolation test in `custom_phrase_isolation_test.go` (repo root, mirroring `mentat_run_reentrancy_test.go`): engines A and B with disjoint contributed phrases, run A→B **and** B→A, asserting each binds only its own. Both orders are required — a first-writer-wins cache passes one and fails the other (go-test-writer, red)
- [ ] T031 [P] [US2] Write the FAILING test asserting a phrase belonging to engine A is reported unbound under engine B, with the same wording any unknown step gets (go-test-writer, red)

### Implementation for User Story 2

- [ ] T032 [US2] Confirm T030/T031 go green on the Phase 2 foundation (per-engine resolution T018, no package cache T009). If either fails, a package-level cache survives somewhere — find it rather than adding a guard (go-test-writer, green)
- [ ] T033 [US2] Add a `t.Parallel()` concurrent-run variant of T030 and run the package under `-race`, so concurrent engines are covered and not only sequential ones (go-test-writer)
- [ ] T034 [US2] Grep `internal/steps` and `internal/engine` for any remaining package-level mutable state (`sync.Once`, package `var` caches) and record the result in `internal/steps/phrase.go`'s package doc — FR-010 is "none survives", not "the one we knew about is gone" (go-reviewer, `pair`)

**Checkpoint**: US1 and US2 both work; the reentrancy property 007 established is not re-opened.

---

## Phase 5: User Story 3 - A colliding phrase fails loudly at composition (Priority: P2)

**Goal**: Collisions and malformed phrases fail with an error naming the contributor, before any
scenario runs; genuine overlap fails at match time naming every matching expression.

**Independent Test**: Register two comparators contributing the same pattern; assert the engine
build fails naming both, and that no scenario executes.

### Tests for User Story 3 (REQUIRED — Test-First) ⚠️

- [ ] T035 [P] [US3] Write FAILING table-driven validation tests in `internal/steps/phrase_test.go` for rules V1–V5 ([data-model.md](./data-model.md) §1): uncompilable pattern; unanchored pattern; identical contributed patterns; pattern identical to a built-in's; blank `Group`/`Summary`/`Example`. One row per rule, each asserting the error names the contributor and the offending value (go-test-writer, red)
- [ ] T036 [P] [US3] Write the FAILING test asserting **no scenario executes** when validation fails — the failure is at engine build, not mid-suite (go-test-writer, red)
- [ ] T037 [P] [US3] Write the FAILING test for the anchoring rule's edge case: a pattern ending in an **escaped** `\$` is not anchored and must be rejected (R8) (go-test-writer, red)
- [ ] T038 [US3] Write the FAILING end-to-end collision test: two contributed phrases whose anchored patterns both match one sentence produce a FAILED scenario naming every matching expression (FR-007b). Confirm it fails against the pre-T005 non-strict configuration first — this is the phrase-level counterpart of T004 (go-test-writer, red)

### Implementation for User Story 3

- [ ] T039 [P] [US3] Implement V1 (regex compiles) and V2 (anchored `^…$`, unescaped terminal `$`) in `internal/steps/phrase.go`; confirm the T035/T037 rows green (go-test-writer, green)
- [ ] T040 [US3] Implement V3 (duplicate contributed patterns, naming **both** contributors) and V4 (collision with a `stepDefs` row, naming the built-in step) in `internal/steps/phrase.go` (go-test-writer, green)
- [ ] T041 [US3] Implement V5 (non-blank `Group`/`Summary`/`Example`) in `internal/steps/phrase.go`, matching the bar `TestStepMetadataFieldsPresent` sets for built-in rows (D5) (go-test-writer, green)
- [ ] T042 [US3] Wire validation into the engine build path so failures surface before any scenario runs; confirm T036 green (go-test-writer, green)
- [ ] T043 [US3] Confirm T038 green on T005's `Strict: true`; assert the reason text names every matching expression (go-test-writer, green)
- [ ] T044 [US3] Assert built-ins register **before** contributed phrases, and contributed phrases in sorted comparator-name order — godog returns the first match, so ordering decides collision resolution and map order would make it vary between runs of an unchanged suite (R3) (go-test-writer)
- [ ] T045 [US3] Record a mutation rehearsal per rejection path (SC-003), naming what was mutated (go-test-writer)
- [ ] T046 [P] [US3] Add the edge-case tests the spec lists and Phase 5 has not yet covered: the same comparator registered under two names contributing the same phrase; a phrase contributed after the registry is sealed (`registry.go:71-79`); a contributed pattern matching a step the engine's tag expression never selects (go-test-writer)

- [ ] T047 [P] [US3] Pin the assumption V4 rests on: assert the 40 built-in `stepDefs` patterns are **pairwise disjoint** — no sentence matches two of them — in `internal/steps/metadata_test.go`. Generate sentences from each pattern's parsed syntax tree expanding every alternation branch. Nothing asserted this before R10 measured it, and V4 (a contributed pattern identical to a built-in's) is only meaningful if the built-in set is itself unambiguous (go-test-writer)

**Checkpoint**: Every collision and malformed-phrase path is loud and named.

---

## Phase 6: User Story 4 - The step reference and the validator tell the truth (Priority: P2)

**Goal**: A consumer can render the full reference for their own engine, and no path reports a
valid feature file as broken.

**Independent Test**: Render an engine's reference and assert it contains built-ins plus the
contributed phrases with their documentation; run the engine-aware validate path over a suite
written in contributed phrases and assert zero `unbound-step` findings.

### Tests for User Story 4 (REQUIRED — Test-First) ⚠️

- [ ] T048 [P] [US4] Write the FAILING renderer test in `internal/steps/docs_test.go`: the engine-scoped reference contains every built-in row plus each contributed phrase with its group, summary and example (FR-012) (go-test-writer, red)
- [ ] T049 [P] [US4] Write the FAILING contiguity test: contributed phrases render in contiguous group blocks **after** the built-in groups even when a phrase declares an existing group name (e.g. `"Shape"`), which would otherwise duplicate a heading (FR-014, R9, `docs_test.go:43`) (go-test-writer, red)
- [ ] T050 [P] [US4] Write the FAILING test in `cmd/mentat/steps_cmd_test.go` asserting `mentat steps` output and `docs/steps.md` render the built-in rows **byte-identically** to baseline (FR-013, SC-006) (go-test-writer, red)
- [ ] T051 [US4] Write the FAILING test for the library validate entry point: a suite written in contributed phrases yields **zero** `unbound-step` findings when validated against an engine that has them (FR-011, SC-005) (go-test-writer, red)

### Implementation for User Story 4

- [ ] T052 [US4] Implement the engine-scoped reference renderer, reusing the `StepDoc` shape so both paths render from one view; confirm T048/T049 green (go-test-writer, green)
- [ ] T053 [US4] Confirm `TestStepDocsMirrorsTable` (`docs_test.go:12`) still proves the built-in view lossless (go-coder)
- [ ] T054 [US4] Add the library validate entry point to `run.go` accepting the same `Option`s `mentat.Run` accepts and returning `[]Finding`; alias `steps.Finding` (`precheck.go:23-28`) on the facade in `mentat.go`. See [contracts/validate-surface.md](./contracts/validate-surface.md). Confirm T051 green (go-test-writer, green)
- [ ] T055 [US4] Keep `cmd/mentat/validate.go`'s current strictness for built-in steps; add **no** manifest flag and no second source of phrase truth (FR-011a, D7) (go-coder)
- [ ] T056 [P] [US4] Add the sentence to `cmd/mentat/steps_cmd.go`'s generated intro stating contributed phrases are engine-scoped and not listed there; regenerate with `go generate ./...` and confirm the built-in rows in `docs/steps.md` are byte-identical (FR-013) (go-coder)
- [ ] T057 [P] [US4] Document in `cmd/mentat/validate.go`'s help text and `docs/` that a compiled binary cannot see contributed phrases, pointing at the library entry point (FR-011a) (go-coder)
- [ ] T058 [US4] Confirm T050 green (go-coder)

**Checkpoint**: All four user stories independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T059 [P] Add the contributed-phrase authoring path to `docs/extending/` — declaring a phrase, the two seams and when each applies, the anchoring rule, and the collision policy (FR-017) (go-coder)
- [ ] T060 [P] Reconcile the seam taxonomy at `specs/009-extension-surface-integrity/contracts/seam-taxonomy.md` and `docs/extending/new-seam.md` if this feature changed any seam's shape (FR-017) (go-coder)
- [ ] T061 Regenerate the public-surface golden (`specs/007-public-extension-api/contracts/public-surface.golden`) with `MENTAT_UPDATE_GOLDEN=1 go test -run TestPublicSurfaceGolden` and **hand-review the diff**: expect the two new seams with full method sets, `ContributedPhrase`'s exported fields, the validate entry point and the `Finding` alias — and **nothing else**. 011's `ExpectationParser` line must be unchanged (SC-008, FR-015) (go-coder)
- [ ] T062 Confirm `TestFacadeNameabilitySweep` (`surface_test.go`) demands the new aliases automatically, without anyone adding them by hand — 010 paying for itself (SC-008) (go-coder)
- [ ] T063 [P] Verify SC-009: build an engine with **zero** contributed phrases and confirm byte-identical output to T002's baseline across every existing golden. The overwhelmingly common case must cost nothing (go-test-writer)
- [ ] T064 Verify the 80% per-package coverage floor for every touched package with the `/coverage` skill or `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out` (SC-010, Constitution V) (go-coder)
- [ ] T065 [P] Run `gofmt -l .`, `go vet ./...` and `golangci-lint run ./...` clean (go-coder)
- [ ] T066 Run the full [quickstart.md](./quickstart.md) validation end to end, including the e2e lane with the harness up (go-coder)
- [ ] T067 Re-run `make ci` **and** `go test -tags e2e -timeout 25m ./e2e/`; record final before/after against T002's baseline. A green `make ci` alone does not discharge SC-012 (go-coder)
- [ ] T068 Update the `<!-- SPECKIT -->` block in `CLAUDE.md`: 012 shipped, what landed, and any correction this feature made to its own artifacts — following the pattern 011 set (go-coder)
- [ ] T069 **go-reviewer `gate`** audit of the staged diff: PASS/BLOCK. Conventional Commits, no `git add .`, no AI attribution (go-reviewer)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies
- **Foundational (Phase 2)**: depends on Setup — **BLOCKS all user stories**
- **US1 (Phase 3)**, **US2 (Phase 4)**, **US3 (Phase 5)**, **US4 (Phase 6)**: all depend on Phase 2
- **Polish (Phase 7)**: depends on all desired stories

### Within Phase 2 (the critical path)

```
T004 → T005 → {T006, T007}          # live defect: regression test, Strict, both golden surfaces
T008 → T009 → T010                  # sync.Once removal, then call sites
T011 → T012 → T013                  # registerSteps parameter, then drift partition
T014 → T015 → T016                  # seams, then facade aliases
T017 → T018                         # engine accessor (needs T015)
```

The three chains `T004→T007`, `T008→T010` and `T011→T013` touch disjoint files and can run in
parallel. `T014→T018` may start immediately; only T018 depends on T015.

### User Story Dependencies

- **US1 (P1)**: needs Phase 2 complete. No dependency on other stories.
- **US2 (P1)**: needs Phase 2 (T009, T018) and US1's registration wiring (T026) to have phrases to isolate.
- **US3 (P2)**: needs Phase 2 and T005 (`Strict`) for T038/T043. Independent of US1's parse paths.
- **US4 (P2)**: needs Phase 2 and US1's phrase resolution for a non-empty reference.

### Within Each Story

Tests written and observed FAILING → implementation → green → mutation rehearsal recorded.

### Parallel Opportunities

- T002, T003 (Setup)
- The three Phase 2 chains above
- T019, T020, T021 — different concerns, same new file: **coordinate or serialize**, see Notes
- T030, T031 (US2 tests); T035, T036, T037 (US3 validation tests); T048, T049, T050 (US4 tests)
- T039 and T046 within US3; T056, T057 within US4
- T059, T060, T063, T065 in Polish

---

## Parallel Example: Phase 2

```bash
# Three disjoint chains, one agent each:
Agent A: T004 → T005 → T006 → T007     # run.go, internal/steps/ambiguity_test.go, e2e/
Agent B: T008 → T009 → T010            # internal/steps/precheck.go, cmd/mentat/validate.go
Agent C: T011 → T012 → T013            # internal/steps/metadata.go, metadata_test.go

# T014 → T015 → T016 (internal/core, mentat.go) may run alongside all three.
```

---

## Implementation Strategy

### Land Phase 2's `Strict` change first, in its own commit

T004–T007 close godog's silent first-wins branch and are behaviour-preserving for every existing
user (R0 measured zero golden churn on both surfaces). They land as a **separate first commit on
this branch** — not a separate PR: per R10 the branch is latent at `0f9dcea`, so there is no
user-facing fix to ship independently. The split buys reviewability of a flag that changes
behaviour for *every* engine, and it puts the guard in place before Phase 3 makes collisions
constructible.

### MVP (US1)

Phase 1 → Phase 2 → Phase 3. **Stop and validate**: a comparator's own sentence drives it,
proven equivalent to the 011 path (T022) and proven to go RED on a false claim (T028).

### Incremental delivery

Phase 2 → US1 (MVP) → US2 (soundness) → US3 (guardrails) → US4 (surfaces) → Polish. US2 is P1
alongside US1: shipping US1 without it means shipping a latent cross-run contamination bug.

---

## Notes

- **T019–T021 all create `internal/steps/phrase_test.go`.** They are marked [P] because the
  concerns are independent, but three agents writing one new file will collide. Either serialize
  them or have one agent create the file with the three table stubs first. Parallelize by
  package, not by story.
- **A TDD red poisons sibling agents' signal.** An agent running the package suite while another
  holds a deliberate red cannot tell whose failure it is. Keep concurrent agents on disjoint
  packages.
- **Subagents never run git.** Branch, staging and commits are the top-level session's job.
- Verify every test fails before implementing; `VERIFY: Ran <exact name> — Result: PASS/FAIL/DID NOT RUN`.
- Commit per task or logical group, Conventional Commits, files staged individually.
- If a godog behaviour surprises you, re-read [research.md](./research.md) before changing code —
  two of this feature's premises came from 011's D1 and **one was false**.
