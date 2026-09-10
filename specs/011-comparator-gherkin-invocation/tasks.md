---
description: "Task list for 011 — custom-comparator Gherkin invocation"
---

# Tasks: Custom-Comparator Gherkin Invocation

**Input**: Design documents from `specs/011-comparator-gherkin-invocation/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md)

**Tests**: The constitution mandates Test-First / TDD as NON-NEGOTIABLE (Principle V). Every
test task below MUST be written and observed FAILING before its implementation. Test tasks are
not optional.

**Organization**: Grouped by user story so each is independently implementable and testable.

**Revised 2026-09-10** after `/speckit-analyze`. Four HIGH findings folded in: the `stepDefs`
group count was wrong (`Extend` is the **seventh** group, not the sixth), the nil-docstring
guard was unspecified (now T010/T015), SC-001 had **zero** coverage (now T030), and FR-010's
soundness literal was never asserted (now T013). Two Principle-V process defects also fixed:
a test row that could never have been observed failing, and one mutation asked to falsify two
independent guards.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4, mapping to the spec's user stories
- Exact file paths in every description

---

## Phase 1: Setup (Baseline)

**Purpose**: Establish that every gate is green *before* anything moves, so a later failure is
attributable to this feature rather than inherited. This is the discipline 010 used and the
reason its central claim was measured rather than argued.

- [X] T001 Record a clean baseline at the branch point: run `gofmt -l .`, `go vet ./...`, `go vet -tags e2e ./...`, and `go test . -run 'TestFacadeNameabilitySweep|TestPublicSurfaceGolden'`, capturing output in the task notes
- [X] T002 [P] Confirm `examples/kafkaecho` builds untouched at the branch point (`cd examples/kafkaecho && go build ./...`) — the SC-008 baseline

**Checkpoint**: every gate green and recorded; any later red is this feature's.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The seam and its gate. Plan Phase 2 step **5a**.

**⚠️ CRITICAL**: No user story work begins until this phase is complete. It lands before any
handler exists so the two-line golden diff is attributable to nothing else.

- [X] T003 Add the `ExpectationParser` interface to `internal/core/core.go`, beside `Comparator` and `Expectation`, with a doc comment stating it is OPTIONAL, discovered by type assertion, and that Mentat never inspects the text
- [X] T004 Add `type ExpectationParser = core.ExpectationParser` to `mentat.go`, alongside the existing seam aliases
- [X] T005 Run `go test . -run TestPublicSurfaceGolden` and observe it FAIL on surface drift, then regenerate with `MENTAT_UPDATE_GOLDEN=1` and verify the diff to `specs/007-public-extension-api/contracts/public-surface.golden` is EXACTLY two lines: the `method (ExpectationParser) ParseExpectation(text string) (Expectation, error)` line and the `type ExpectationParser = core.ExpectationParser` line
- [X] T006 Perform the mutation rehearsal per [contracts/expectation-parser-seam.md](./contracts/expectation-parser-seam.md#falsification): temporarily add `type XProbeSpec struct{ Note string }` and an `XProbe() XProbeSpec` method to `internal/core/core.go`, run `go test . -run TestFacadeNameabilitySweep`, and confirm it fails with `internal/core.XProbeSpec — reached by method (ExpectationParser) XProbe`, then revert both edits
- [X] T007 Record the T006 transcript as a comment in `surface_test.go` alongside the existing rehearsals (`:66`, `:78`, `:96`, `:116`, `:826`, `:1141`), including the note that deleting the facade alias does NOT fail the sweep because it seeds only from alias targets (`surface_test.go:1191-1198`)
- [X] T008 Verify `go test . -run 'TestFacadeNameabilitySweep|TestPublicSurfaceGolden'` is green after the T006 revert

**Checkpoint**: the seam is published, nameable, frozen in the golden, and the gate is proven
to catch a regression in it.

---

## Phase 3: User Story 1 — Invoke a registered custom comparator (Priority: P1) 🎯 MVP

**Goal**: A comparator registered with `WithComparator` and implementing `ExpectationParser`
can be named from a `.feature` file, receives its own typed expectation, and its verdict lands
like any built-in's.

**Independent Test**: register a comparator in a test binary, run an inline feature naming it,
assert the suite passes and the verdict is recorded. Hermetic — gomock store, no network.

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

- [X] T009 [US1] Write `TestCustomComparatorFromGherkin` in `internal/steps/steps_test.go` following the shape of `TestFeatureExercisesGrammarAgainstFakeEngine` (`:58`): build the engine with `engine.Build(cfg, st, cor, engine.WithExtraComparator("revenue-shape", …))`, drive an inline feature using the new phrase with a docstring, and assert `suite.Run() == 0`; observe it FAIL with an undefined-step error

### Implementation for User Story 1

- [X] T010 [US1] Add the `comparatorSatisfiedByDoc(name string, doc *godog.DocString) error` handler to `internal/steps/steps.go`, following the captures-first/docstring-last convention of `resultMeansDoc` (`:510`). It MUST open with a `doc == nil` guard returning a descriptive error naming the step (FR-017), as seven of the eight existing docstring handlers do (`:169, 434, 475, 511, 548, 559, 570`); then resolve via `w.eng.Comparator(name)`, type-assert `core.ExpectationParser`, call `ParseExpectation(doc.Content)` unmodified, and route the result through `w.checkExp(name, exp, true)` (`:254`)
- [X] T011 [US1] Add exactly ONE row to `stepDefs` in `internal/steps/metadata.go` in a new **seventh** group `Extend` — appended after the existing six (`Drive`, `Sequence`, `Budgets`, `Result`, `Aggregate / CEL`, `Shape`) — with pattern `^the "([^"]+)" comparator is satisfied by:$`, a non-blank summary and a valid example, bound to the T010 handler (depends on T010)
- [X] T012 [US1] Verify T009 passes, and assert within it that the comparator received the expectation its own `ParseExpectation` produced, and that qualifiers and judge usage were recorded on the world exactly as for a built-in step
- [X] T013 [US1] Assert FR-010's sensitivity actually reaches the engine (SC-011), modelled on `internal/steps/qualifier_test.go`: against a **bounded** (request-scoped, non-strict) target the custom comparator's verdict carries the completeness qualifier; against a **strict** target it does not. Without this, `sensitive=true` is an untested literal that could be flipped to `false` with every gate staying green

**Checkpoint**: US1 is fully functional. This is the MVP — the capability exists end to end.

---

## Phase 4: User Story 2 — Every failure names what went wrong (Priority: P2)

**Goal**: Every failure mode produces a loud, specific error. No nil expectation, no
zero-value success, no silently skipped assertion, no panic.

**Independent Test**: table-driven tests over the failure modes, asserting on error text.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

> All four table rows are written **before** T018 implements them. An earlier revision added
> the embedded-quote row after implementation, where it could never have been observed
> failing — a Principle V violation caught by `/speckit-analyze`.

- [X] T014 [US2] Write `TestCustomComparatorErrors` in `internal/steps/steps_test.go` as a table with FOUR rows written up front: (a) unregistered name → error contains the name **and** at least one registered name; (b) registered but not an `ExpectationParser` → error names the comparator and states it cannot be driven from Gherkin; (c) `ParseExpectation` returns an error → `%w`-wrapped and named by comparator; (d) a name containing an embedded quote → the error echoes the **truncated capture verbatim** so the truncation is visible to the author (spec Edge Cases). Observe all four FAIL
- [X] T015 [P] [US2] Write `TestCustomComparatorDocNil` in `internal/steps/steps_test.go` as a direct handler call mirroring `TestResultMeansDocNil` (`w := &world{}; w.comparatorSatisfiedByDoc("x", nil)`), asserting a descriptive error rather than a panic (FR-017); observe it FAIL
- [X] T016 [P] [US2] Write `TestEngineComparatorsListsRegisteredNames` in `internal/engine/engine_test.go` asserting `Engine.Comparators()` returns the sorted registered names including one added via `WithExtraComparator`; observe it FAIL (method does not exist)

### Implementation for User Story 2

- [X] T017 [US2] Add `func (e *Engine) Comparators() []string { return e.reg.Comparators() }` to `internal/engine/engine.go`, mirroring `Reporters()` (`:218`) over the existing `Registry.Comparators()` (`registry.go:125`)
- [X] T018 [US2] Implement the nil guard and the three error branches in `comparatorSatisfiedByDoc` in `internal/steps/steps.go` so T014, T015 and T016 all pass
- [X] T019 [US2] Verify T014–T016 pass and confirm by inspection that no branch returns a nil or zero-value expectation, a skipped assertion, or a passing step (Constitution IV)
- [X] T020 [US2] Extend `TestSingleRunStepRejectedInMultirunScenario` (`internal/steps/steps_test.go:912`) with the new phrase under `@runs(2)`, proving the step inherits the single-run guard (`steps.go:256`) rather than bypassing it — the spec Edge Case that had no coverage

**Checkpoint**: US1 and US2 both work independently; the seam is usable rather than a guessing
game, and cannot panic.

---

## Phase 5: User Story 3 — A first-class documented step (Priority: P3)

**Goal**: The new phrase appears in `mentat steps` and `docs/steps.md` with the built-ins,
and the `stepDefs` drift invariant is preserved rather than relaxed.

**Independent Test**: run the five drift tests and `mentat steps`; confirm the committed
`docs/steps.md` matches.

- [X] T021 [US3] Regenerate `docs/steps.md` from `stepDefs` so the committed reference carries the new row
- [X] T022 [US3] Run `go test ./internal/steps/ -run 'TestStepDocs|TestStepMetadata|TestNoDirectStepRegistration' -v` and confirm all five drift tests pass with their assertions **UNMODIFIED** — needing to edit one means the design drifted into Option B and the plan is wrong, not the test (FR-012, SC-003)
- [X] T023 [P] [US3] Run `go run ./cmd/mentat steps` and confirm the `Extend` group lists the new phrase with a non-blank summary and a valid example
- [X] T024 [US3] Document the seam in `docs/extending/comparator.md`: the `ExpectationParser` interface, a complete worked comparator implementing it, and the feature-file snippet that drives it (FR-014)

**Checkpoint**: all three stories functional; the step is discoverable and documented.

---

## Phase 6: User Story 4 — Mentat proves it goes red (Priority: P4)

**Goal**: The framework is shown to FAIL correctly on a bad custom comparator. Mandatory by
Constitution V.

**Placement note**: `internal/steps`, NOT `e2e/` — per [research R6](./research.md), the e2e
suite drives a prebuilt `cmd/mentat` binary that structurally cannot contain a Go-registered
comparator, and sits behind `//go:build e2e` which `make ci` never compiles.

- [X] T025 [US4] Write `TestCustomComparatorGoesRed` in `internal/steps/steps_test.go` following `TestFeatureGoesRedOnBadScenario` (`:103`): register a comparator that returns a failing verdict, run an inline feature naming it, assert `suite.Run() != 0` AND that the output contains the comparator's own reasons — status alone is not sufficient
- [X] T026 [P] [US4] Write `TestCustomComparatorParseError` in `internal/steps/steps_test.go`: register a comparator whose `ParseExpectation` returns an error, assert the suite fails loudly rather than skipping the assertion, and that the output names the comparator and carries the wrapped cause
- [X] T027 [US4] Falsification A — temporarily make the handler ignore a failing verdict (treat `!v.Pass` as success) and confirm **T025 goes red while T026 stays green**, then revert. The cross-check is the point: it proves T025 tests the verdict guard specifically
- [X] T028 [US4] Falsification B — temporarily make the handler swallow the `ParseExpectation` error (return nil) and confirm **T026 goes red while T025 stays green**, then revert. Two guards need two mutations; one mutation covering both would leave the other test red for its original reason and prove nothing
- [X] T029 [US4] Record both falsification transcripts as comments in `internal/steps/steps_test.go`

**Checkpoint**: all four stories complete, and each red-proof is itself proven to fail when its
guard is removed.

---

## Phase 7: Cross-Cutting Proof & Polish

- [X] T030 Prove SC-001 through the **facade** (FR-018), modelled on `TestGoldenHermeticStdout` (`mentat_golden_test.go:65`) which already runs `mentat.Run` hermetically with a facade-registered store: add a root-package test importing **only** `github.com/thetonymaster/mentat` that registers a comparator via `mentat.WithComparator` (the real external path, `run.go:352`) and drives it with the new phrase. Every other test in this feature uses `engine.WithExtraComparator`, an internal package no external module can reach — without this task the feature's headline claim is unverified
- [X] T031 [P] Add `CHANGELOG.md` entries under Added for the `ExpectationParser` seam and the new step, with NO breaking-change entry — nothing existing changes shape (FR-016)
- [X] T032 [P] Verify the coverage floor with `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`, checking `internal/steps` and `internal/engine` specifically, and confirming the five error paths from Phase 4 are covered rather than riding the package total (SC-009)
- [X] T033 Run `make ci` and confirm green
- [X] T034 Run `go vet -tags e2e ./...` and confirm the e2e package still compiles (SC-010) — `make ci` has no e2e target, and this is the hole that left e2e unbuildable for six commits during 010
- [X] T035 [P] Confirm `examples/kafkaecho` still builds untouched, comparing against the T002 baseline (SC-008)
- [X] T036 Verify the two plan invariants held: the public-surface golden changed exactly ONCE (in T005) by exactly TWO lines, and no drift-test assertion was edited anywhere in the branch — check with `git diff main -- specs/007-public-extension-api/contracts/public-surface.golden internal/steps/metadata_test.go internal/steps/docs_test.go`
- [X] T037 Walk [quickstart.md](./quickstart.md) end to end and confirm every "Done when" box

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies — start immediately
- **Foundational (Phase 2)**: depends on Phase 1 — **BLOCKS all user stories**
- **US1 (Phase 3)**: depends on Phase 2
- **US2 (Phase 4)**: depends on US1 — extends the same handler
- **US3 (Phase 5)**: depends on US1 (the row must exist before docs can render it)
- **US4 (Phase 6)**: depends on US1 and US2 — needs both the happy path and the error paths
- **Phase 7**: T030 depends on US1; the rest depend on all stories

### Why the stories are not fully independent here

Stated plainly rather than pretending otherwise. US2, US3 and US4 all act on the single
artifact US1 creates — one handler and one `stepDefs` row. US1 is the only story that can
stand alone, and it is the MVP. US2–US4 are hardening, documentation and proof layered on it,
sequential by nature. Splitting them further would create false parallelism.

### Within each story

- Tests written and observed FAILING before implementation (Constitution V) — including every
  table row, not just the first
- Handler before the metadata row (the row references the handler)
- Error paths after the happy path
- Story complete before the next priority

### Parallel Opportunities

Genuinely limited — most tasks touch `internal/steps/steps.go` or `steps_test.go`.

- T002 runs alongside T001
- T015 and T016 run alongside T014 — T016 is a different package, T015 a different test function
- T023 runs alongside T021/T022
- T026 runs alongside T025 — different test functions, no shared state
- T031, T032, T035 are independent of each other in Phase 7

---

## Parallel Example: Phase 4 tests

```bash
# Different packages / different test functions, no shared state:
Task: "T014 TestCustomComparatorErrors in internal/steps/steps_test.go"
Task: "T015 TestCustomComparatorDocNil in internal/steps/steps_test.go"
Task: "T016 TestEngineComparatorsListsRegisteredNames in internal/engine/engine_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 — baseline recorded
2. Phase 2 — seam published, golden frozen, gate rehearsed (**blocks everything**)
3. Phase 3 — US1
4. **STOP and VALIDATE**: a custom comparator runs from a feature file

At that point the capability exists. Everything after is hardening it.

### Incremental Delivery

1. Setup + Foundational → the seam is public and gated
2. US1 → the capability works (**MVP**)
3. US2 → it fails usefully and cannot panic
4. US3 → it is discoverable and documented
5. US4 → it is proven to go red, and the proofs are themselves falsified
6. Phase 7 → the facade path is proven, then gates, coverage, changelog

### Two invariants to watch throughout

1. **The golden changes exactly once, in T005, by exactly two lines.** If it moves again,
   something reached the public surface nobody decided to put there — stop and find out what.
2. **The drift tests are never edited.** Needing to touch `metadata_test.go` or `docs_test.go`
   means the design drifted toward Option B; the plan is wrong rather than the tests. This is
   the operational test of D1's central claim.

---

## Out of scope, found while planning

`responseBodyJSONContains` (`internal/steps/steps.go:543`) is the one docstring handler of
eight with **no** `doc == nil` guard — it dereferences `doc.Content` directly and will panic
where its seven siblings return a descriptive error (Constitution IV). It is a one-line fix in
the shape of `responseBodyMatchesSchema` immediately below it (`:547-550`), but it belongs to
the `Result` grammar, not this feature. Recorded, not fixed — widening 011 would blur what this
branch's diff is accountable for.

---

## Notes

- `[P]` = different files, no dependency on an incomplete task
- Verify tests fail before implementing — a test that never failed proves nothing
- Commit after each task or logical group; stage files individually (`git add .` is forbidden)
- Conventional Commits; no AI attribution
- Routing: **go-test-writer** owns T009–T030 (behaviour change, TDD); **go-coder** owns
  T003–T005 and T021 (mechanical); **go-reviewer** `gate` before commit
